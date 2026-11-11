package changedfiles_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/changedfiles"
)

type fakeTokens struct{ broken map[uint64]error }

func (f fakeTokens) AccessToken(_ context.Context, userID uint64) (string, error) {
	if err := f.broken[userID]; err != nil {
		return "", err
	}
	return "ghu_" + string(rune('0'+userID)), nil
}

type fakeGitHub struct {
	files    []entity.ChangedFile
	errFor   map[string]error // token -> error
	calls    []string         // tokens used
	onCalled func()
}

func (f *fakeGitHub) ListPullRequestFiles(_ context.Context, token, owner, name string, number int) ([]entity.ChangedFile, error) {
	f.calls = append(f.calls, token)
	if f.onCalled != nil {
		f.onCalled()
	}
	if owner != "acme" || name != "web" || number != 7 {
		return nil, entity.ErrNotFound
	}
	if err := f.errFor[token]; err != nil {
		return nil, err
	}
	return f.files, nil
}

type fakeStore struct {
	saved   map[uint64]entity.PRChangedFiles
	saves   int
	saveErr error
	listErr error
}

func (f *fakeStore) SaveChangedFiles(_ context.Context, files entity.PRChangedFiles) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves++
	f.saved[files.PullRequestID] = files
	return nil
}

func (f *fakeStore) ListChangedFiles(_ context.Context, ids []uint64) (map[uint64]entity.PRChangedFiles, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := map[uint64]entity.PRChangedFiles{}
	for _, id := range ids {
		if v, ok := f.saved[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

type fakeFollowers struct {
	ids []uint64
	err error
}

func (f fakeFollowers) ListFollowerIDs(context.Context, uint64) ([]uint64, error) {
	return f.ids, f.err
}

var (
	repo = entity.Repository{ID: 10, Owner: "acme", Name: "web", FullName: "acme/web"}
	pr   = entity.PullRequest{ID: 70, RepositoryID: 10, Number: 7, HeadSHA: "sha1", State: entity.PRStateOpen}
)

func setup(followers ...uint64) (*changedfiles.UseCase, *fakeGitHub, *fakeStore, *fakeTokens) {
	gh := &fakeGitHub{files: []entity.ChangedFile{{Path: "src/a.ts", Additions: 1}}, errFor: map[string]error{}}
	store := &fakeStore{saved: map[uint64]entity.PRChangedFiles{}}
	tokens := &fakeTokens{broken: map[uint64]error{}}
	return changedfiles.NewUseCase(tokens, gh, store, fakeFollowers{ids: followers}), gh, store, tokens
}

func TestSyncWithToken_StoresTheFilesOfTheHeadCommit(t *testing.T) {
	t.Parallel()
	uc, gh, store, _ := setup()
	ctx := context.Background()

	if err := uc.SyncWithToken(ctx, "ghu_x", repo, pr); err != nil {
		t.Fatalf("SyncWithToken() error: %v", err)
	}
	got := store.saved[70]
	if got.HeadSHA != "sha1" || len(got.Files) != 1 || got.Files[0].Path != "src/a.ts" {
		t.Fatalf("unexpected stored files: %+v", got)
	}

	// Idempotent: the same commit is not read from GitHub or written again.
	if err := uc.SyncWithToken(ctx, "ghu_x", repo, pr); err != nil || len(gh.calls) != 1 || store.saves != 1 {
		t.Fatalf("repeat: err=%v github calls=%d saves=%d", err, len(gh.calls), store.saves)
	}

	// A new commit is read again and replaces the old files.
	next := pr
	next.HeadSHA = "sha2"
	gh.files = []entity.ChangedFile{{Path: "docs/b.md"}}
	if err := uc.SyncWithToken(ctx, "ghu_x", repo, next); err != nil || store.saved[70].HeadSHA != "sha2" || store.saved[70].Files[0].Path != "docs/b.md" {
		t.Fatalf("new commit: err=%v stored=%+v", err, store.saved[70])
	}
}

func TestSyncWithToken_RejectsWhatCannotBeEvaluatedAndPropagatesFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uc, gh, store, _ := setup()

	if err := uc.SyncWithToken(ctx, "ghu_x", repo, entity.PullRequest{Number: 7, HeadSHA: "sha1"}); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("unsaved PR: expected ErrInvalidInput, got %v", err)
	}
	if err := uc.SyncWithToken(ctx, "ghu_x", repo, entity.PullRequest{ID: 70, Number: 7}); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("PR without head: expected ErrInvalidInput, got %v", err)
	}

	boom := errors.New("github is down")
	gh.errFor["ghu_x"] = boom
	if err := uc.SyncWithToken(ctx, "ghu_x", repo, pr); !errors.Is(err, boom) || len(store.saved) != 0 {
		t.Fatalf("a failed read stores nothing: err=%v saved=%d", err, len(store.saved))
	}
	delete(gh.errFor, "ghu_x")

	store.saveErr = errors.New("db down")
	if err := uc.SyncWithToken(ctx, "ghu_x", repo, pr); !errors.Is(err, store.saveErr) {
		t.Fatalf("expected the store failure, got %v", err)
	}
	store.saveErr = nil
	store.listErr = errors.New("db down")
	if err := uc.SyncWithToken(ctx, "ghu_x", repo, pr); !errors.Is(err, store.listErr) {
		t.Fatalf("expected the store failure, got %v", err)
	}
	store.listErr = nil

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := uc.SyncWithToken(canceled, "ghu_x", repo, pr); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Canceled while GitHub answers: nothing is stored.
	midway, stop := context.WithCancel(ctx)
	gh.onCalled = stop
	if err := uc.SyncWithToken(midway, "ghu_x", repo, pr); !errors.Is(err, context.Canceled) || len(store.saved) != 0 {
		t.Fatalf("canceled midway: err=%v saved=%d", err, len(store.saved))
	}
}

func TestSyncAsUser_UsesThatUsersToken(t *testing.T) {
	t.Parallel()
	uc, gh, store, tokens := setup()
	ctx := context.Background()

	if err := uc.SyncAsUser(ctx, 3, repo, pr); err != nil || len(gh.calls) != 1 || gh.calls[0] != "ghu_3" || len(store.saved) != 1 {
		t.Fatalf("SyncAsUser(): err=%v calls=%v", err, gh.calls)
	}
	tokens.broken[4] = entity.ErrUnauthorized
	next := pr
	next.HeadSHA = "sha2"
	if err := uc.SyncAsUser(ctx, 4, repo, next); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("a user without a valid token: expected ErrUnauthorized, got %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := uc.SyncAsUser(canceled, 3, repo, next); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSyncAsFollower_TriesFollowersUntilOneCanRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uc, gh, store, tokens := setup(1, 2, 3)
	tokens.broken[1] = entity.ErrUnauthorized   // signed out: no usable token
	gh.errFor["ghu_2"] = entity.ErrUnauthorized // token revoked on GitHub

	if err := uc.SyncAsFollower(ctx, repo, pr); err != nil {
		t.Fatalf("SyncAsFollower() error: %v", err)
	}
	if len(gh.calls) != 2 || gh.calls[1] != "ghu_3" || store.saved[70].HeadSHA != "sha1" {
		t.Fatalf("expected follower 3 to read after 1 and 2 failed: calls=%v saved=%+v", gh.calls, store.saved)
	}

	// Already stored for this commit: no follower is even asked.
	if err := uc.SyncAsFollower(ctx, repo, pr); err != nil || len(gh.calls) != 2 {
		t.Fatalf("repeat: err=%v calls=%v", err, gh.calls)
	}
}

func TestSyncAsFollower_ReportsWhenNobodyCanRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	uc, _, store, _ := setup() // nobody follows
	if err := uc.SyncAsFollower(ctx, repo, pr); !errors.Is(err, changedfiles.ErrNoReader) || len(store.saved) != 0 {
		t.Fatalf("no followers: expected ErrNoReader, got %v", err)
	}

	uc, gh, store, _ := setup(1, 2)
	gh.errFor["ghu_1"], gh.errFor["ghu_2"] = entity.ErrUnauthorized, entity.ErrUnauthorized
	if err := uc.SyncAsFollower(ctx, repo, pr); !errors.Is(err, changedfiles.ErrNoReader) || len(store.saved) != 0 {
		t.Fatalf("all tokens rejected: expected ErrNoReader, got %v", err)
	}

	// A GitHub outage is not a token problem: stop instead of burning every follower's token.
	uc, gh, _, _ = setup(1, 2)
	boom := errors.New("github is down")
	gh.errFor["ghu_1"] = boom
	if err := uc.SyncAsFollower(ctx, repo, pr); !errors.Is(err, boom) || len(gh.calls) != 1 {
		t.Fatalf("outage: err=%v calls=%v", err, gh.calls)
	}

	failing := changedfiles.NewUseCase(fakeTokens{}, &fakeGitHub{}, &fakeStore{saved: map[uint64]entity.PRChangedFiles{}}, fakeFollowers{err: boom})
	if err := failing.SyncAsFollower(ctx, repo, pr); !errors.Is(err, boom) {
		t.Fatalf("expected the followers failure, got %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := uc.SyncAsFollower(canceled, repo, pr); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
