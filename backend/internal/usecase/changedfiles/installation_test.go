package changedfiles_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/usecase/changedfiles"
)

type fakeInstallation struct {
	err   error
	calls []int64
	onGet func()
}

func (f *fakeInstallation) InstallationToken(_ context.Context, id int64) (string, error) {
	f.calls = append(f.calls, id)
	if f.onGet != nil {
		f.onGet()
	}
	if f.err != nil {
		return "", f.err
	}
	return "ghs_app", nil
}

func setupApp(inst *fakeInstallation, followers ...uint64) (*changedfiles.UseCase, *fakeGitHub, *fakeStore, *fakeTokens) {
	gh := &fakeGitHub{files: []entity.ChangedFile{{Path: "src/a.ts", Additions: 1}}, errFor: map[string]error{}}
	store := &fakeStore{saved: map[uint64]entity.PRChangedFiles{}}
	tokens := &fakeTokens{broken: map[uint64]error{}}
	var app port.InstallationTokenSource // a nil *fakeInstallation would be a non-nil interface
	if inst != nil {
		app = inst
	}
	return changedfiles.NewUseCase(tokens, gh, store, fakeFollowers{ids: followers}, app), gh, store, tokens
}

var installedRepo = entity.Repository{ID: 10, Owner: "acme", Name: "web", FullName: "acme/web", InstallationID: 555}

func TestSyncAsFollower_PrefersTheInstallationTokenSoNoFollowerHasToBeSignedIn(t *testing.T) {
	t.Parallel()
	inst := &fakeInstallation{}
	uc, gh, store, _ := setupApp(inst) // nobody follows: only the app can read

	if err := uc.SyncAsFollower(context.Background(), installedRepo, pr); err != nil {
		t.Fatalf("SyncAsFollower() error: %v", err)
	}
	if len(inst.calls) != 1 || inst.calls[0] != 555 || len(gh.calls) != 1 || gh.calls[0] != "ghs_app" {
		t.Fatalf("expected one read with the installation's token: installation=%v github=%v", inst.calls, gh.calls)
	}
	if store.saved[70].HeadSHA != "sha1" {
		t.Fatalf("files not stored: %+v", store.saved)
	}
}

func TestSyncAsFollower_FallsBackToAFollowerWhenTheAppCannotRead(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]func(*fakeInstallation, *fakeGitHub){
		"app uninstalled":    func(i *fakeInstallation, _ *fakeGitHub) { i.err = entity.ErrNotFound },
		"app key rejected":   func(i *fakeInstallation, _ *fakeGitHub) { i.err = entity.ErrUnauthorized },
		"github is down":     func(i *fakeInstallation, _ *fakeGitHub) { i.err = errors.New("boom") },
		"app token rejected": func(_ *fakeInstallation, g *fakeGitHub) { g.errFor["ghs_app"] = entity.ErrUnauthorized },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			inst := &fakeInstallation{}
			uc, gh, store, _ := setupApp(inst, 1)
			failure(inst, gh)
			if err := uc.SyncAsFollower(context.Background(), installedRepo, pr); err != nil {
				t.Fatalf("a follower can still read: %v", err)
			}
			if got := gh.calls[len(gh.calls)-1]; got != "ghu_1" {
				t.Fatalf("expected the follower's token to finish the read, got %v", gh.calls)
			}
			if store.saved[70].HeadSHA != "sha1" {
				t.Fatal("files not stored")
			}
		})
	}
}

func TestSyncAsFollower_WithoutAnInstallationOrAppItUsesFollowers(t *testing.T) {
	t.Parallel()
	inst := &fakeInstallation{}
	uc, gh, _, _ := setupApp(inst, 1)
	if err := uc.SyncAsFollower(context.Background(), repo, pr); err != nil { // repo has no installation id
		t.Fatal(err)
	}
	if len(inst.calls) != 0 || gh.calls[0] != "ghu_1" {
		t.Fatalf("no installation id: use a follower. installation=%v github=%v", inst.calls, gh.calls)
	}

	uc, gh, _, _ = setupApp(nil, 1) // the app is not configured
	if err := uc.SyncAsFollower(context.Background(), installedRepo, pr); err != nil || gh.calls[0] != "ghu_1" {
		t.Fatalf("app not configured: err=%v github=%v", err, gh.calls)
	}
}

func TestSyncAsFollower_ReportsNoReaderWhenNeitherAppNorFollowersCanRead(t *testing.T) {
	t.Parallel()
	inst := &fakeInstallation{err: entity.ErrNotFound}
	uc, _, _, _ := setupApp(inst)
	if err := uc.SyncAsFollower(context.Background(), installedRepo, pr); !errors.Is(err, changedfiles.ErrNoReader) {
		t.Fatalf("expected ErrNoReader, got %v", err)
	}
}

func TestSyncAsFollower_CancellationDuringTheAppReadDoesNotFallBack(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	inst := &fakeInstallation{err: context.Canceled, onGet: cancel}
	uc, gh, _, _ := setupApp(inst, 1)
	if err := uc.SyncAsFollower(ctx, installedRepo, pr); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if len(gh.calls) != 0 {
		t.Fatalf("a canceled request must not start another read: %v", gh.calls)
	}
}

func TestSyncAsFollower_AlreadyCurrentCommitIsNotReadAgainByTheApp(t *testing.T) {
	t.Parallel()
	inst := &fakeInstallation{}
	uc, _, _, _ := setupApp(inst)
	ctx := context.Background()
	_ = uc.SyncAsFollower(ctx, installedRepo, pr)
	if err := uc.SyncAsFollower(ctx, installedRepo, pr); err != nil || len(inst.calls) != 1 {
		t.Fatalf("redelivery: err=%v installation calls=%d", err, len(inst.calls))
	}
}
