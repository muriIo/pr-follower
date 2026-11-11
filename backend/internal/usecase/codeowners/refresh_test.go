package codeowners_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/codeowners"
)

// fakeFiles plays GitHub's contents API: each token sees a set of files, or fails.
type fakeFiles struct {
	files map[string]string // path -> content, what the repository holds now
	deny  map[string]error  // token -> error returned for every read with it
	boom  error             // returned for every read when set (GitHub failing)
	reads []string          // "token:path"
}

func (f *fakeFiles) GetRepositoryFile(_ context.Context, token, _, _, path string) ([]byte, error) {
	f.reads = append(f.reads, token+":"+path)
	if err := f.deny[token]; err != nil {
		return nil, err
	}
	if f.boom != nil {
		return nil, f.boom
	}
	if content, ok := f.files[path]; ok {
		return []byte(content), nil
	}
	return nil, entity.ErrNotFound
}

type fakeApp struct {
	err   error
	calls []int64
}

func (f *fakeApp) InstallationToken(_ context.Context, id int64) (string, error) {
	f.calls = append(f.calls, id)
	if f.err != nil {
		return "", f.err
	}
	return "ghs_app", nil
}

type fakeTokens struct{ broken map[uint64]error }

func (f fakeTokens) AccessToken(_ context.Context, id uint64) (string, error) {
	if err := f.broken[id]; err != nil {
		return "", err
	}
	return "ghu_" + string(rune('0'+id)), nil
}

type fakeFollowers struct{ ids []uint64 }

func (f fakeFollowers) ListFollowerIDs(context.Context, uint64) ([]uint64, error) { return f.ids, nil }

type fakeRepos struct {
	saved []entity.Repository
	err   error
}

func (f *fakeRepos) Upsert(_ context.Context, r *entity.Repository) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, *r)
	return nil
}
func (f *fakeRepos) GetByID(context.Context, uint64) (*entity.Repository, error) {
	return nil, entity.ErrNotFound
}
func (f *fakeRepos) GetByGitHubRepoID(context.Context, int64) (*entity.Repository, error) {
	return nil, entity.ErrNotFound
}
func (f *fakeRepos) ListFollowedByUser(context.Context, uint64) ([]entity.Repository, error) {
	return nil, nil
}
func (f *fakeRepos) SetUserFollow(context.Context, uint64, uint64, bool) error { return nil }

type fakePub struct{ events []entity.ActivityEvent }

func (f *fakePub) Publish(_ context.Context, ev entity.ActivityEvent) error {
	f.events = append(f.events, ev)
	return nil
}

var stored = entity.Repository{
	ID: 10, Owner: "acme", Name: "web", FullName: "acme/web", InstallationID: 555, DefaultBranch: "main",
	CodeownersStatus:      entity.CodeownersPresent,
	CachedCodeownersRules: []entity.CodeownerRule{{Pattern: "*", Owners: []string{"@old"}}},
}

type harness struct {
	uc    *codeowners.UseCase
	files *fakeFiles
	app   *fakeApp
	repos *fakeRepos
	pub   *fakePub
}

func newHarness(files map[string]string, followers ...uint64) harness {
	h := harness{files: &fakeFiles{files: files, deny: map[string]error{}}, app: &fakeApp{}, repos: &fakeRepos{}, pub: &fakePub{}}
	h.uc = codeowners.NewUseCase(h.repos, h.files, fakeTokens{broken: map[uint64]error{}}, fakeFollowers{ids: followers}, h.app, h.pub)
	return h
}

func TestRefresh_StoresTheRulesNowInForceAndAnnouncesTheChange(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{".github/CODEOWNERS": "* @new\n/docs/ @bia\n"})

	if err := h.uc.RefreshCodeowners(context.Background(), stored); err != nil {
		t.Fatalf("RefreshCodeowners() error: %v", err)
	}
	if len(h.repos.saved) != 1 {
		t.Fatalf("expected the repository to be saved once: %+v", h.repos.saved)
	}
	got := h.repos.saved[0]
	if got.CodeownersStatus != entity.CodeownersPresent || len(got.CachedCodeownersRules) != 2 || got.CachedCodeownersRules[0].Owners[0] != "@new" {
		t.Fatalf("unexpected saved rules: %+v", got)
	}
	if got.ID != 10 || got.InstallationID != 555 || got.DefaultBranch != "main" {
		t.Fatalf("only the CODEOWNERS fields change: %+v", got)
	}
	if len(h.pub.events) != 1 || h.pub.events[0].Type != "repository.codeowners_updated" || h.pub.events[0].RepositoryName != "acme/web" ||
		h.pub.events[0].Params["rules"] != "2" {
		t.Fatalf("expected one announcement of the change: %+v", h.pub.events)
	}
	if h.files.reads[0] != "ghs_app:.github/CODEOWNERS" || len(h.app.calls) != 1 || h.app.calls[0] != 555 {
		t.Fatalf("the app's installation token reads first: %v", h.files.reads)
	}
}

func TestRefresh_UsesTheFirstCodeownersFileGitHubWouldUse(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"CODEOWNERS": "* @root\n", "docs/CODEOWNERS": "* @docs\n"})
	if err := h.uc.RefreshCodeowners(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	if got := h.repos.saved[0].CachedCodeownersRules[0].Owners[0]; got != "@root" {
		t.Fatalf("precedence is .github/, then the root, then docs/: got %q", got)
	}
}

func TestRefresh_NothingChangedMeansNothingStoredOrAnnounced(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{".github/CODEOWNERS": "* @old\n"})
	for range 3 { // a redelivered push
		if err := h.uc.RefreshCodeowners(context.Background(), stored); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.repos.saved) != 0 || len(h.pub.events) != 0 {
		t.Fatalf("convergence (ADR-0011): saved=%v events=%v", h.repos.saved, h.pub.events)
	}
}

func TestRefresh_ADeletedFileMakesTheRepositoryHaveNoCodeowners(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{})
	if err := h.uc.RefreshCodeowners(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	got := h.repos.saved[0]
	if got.CodeownersStatus != entity.CodeownersAbsent || len(got.CachedCodeownersRules) != 0 {
		t.Fatalf("expected absent with no rules: %+v", got)
	}
	if len(h.pub.events) != 1 {
		t.Fatalf("expected the change announced: %+v", h.pub.events)
	}
}

func TestRefresh_ACodeownersFileAppearingInARepositoryWithoutOne(t *testing.T) {
	t.Parallel()
	none := stored
	none.CodeownersStatus, none.CachedCodeownersRules = entity.CodeownersAbsent, nil
	h := newHarness(map[string]string{"CODEOWNERS": "* @new\n"})
	if err := h.uc.RefreshCodeowners(context.Background(), none); err != nil {
		t.Fatal(err)
	}
	if got := h.repos.saved[0]; got.CodeownersStatus != entity.CodeownersPresent || len(got.CachedCodeownersRules) != 1 {
		t.Fatalf("expected the new rules: %+v", got)
	}
}

func TestRefresh_FallsBackToAFollowerWhenTheAppCannotRead(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]func(h harness){
		"app credentials missing": func(h harness) { h.app.err = entity.ErrUnauthorized },
		"app uninstalled":         func(h harness) { h.app.err = entity.ErrNotFound },
		"app token refused":       func(h harness) { h.files.deny["ghs_app"] = entity.ErrUnauthorized },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(map[string]string{"CODEOWNERS": "* @new\n"}, 1)
			setup(h)
			if err := h.uc.RefreshCodeowners(context.Background(), stored); err != nil {
				t.Fatalf("a follower can still read: %v", err)
			}
			if len(h.repos.saved) != 1 {
				t.Fatalf("expected the rules stored: %+v", h.repos.saved)
			}
		})
	}

	// Without an installation id the app is not even asked.
	noInstall := stored
	noInstall.InstallationID = 0
	h := newHarness(map[string]string{"CODEOWNERS": "* @new\n"}, 1)
	if err := h.uc.RefreshCodeowners(context.Background(), noInstall); err != nil || len(h.app.calls) != 0 {
		t.Fatalf("err=%v app calls=%v", err, h.app.calls)
	}
}

func TestRefresh_NeverGuessesWhenNobodyCanRead(t *testing.T) {
	t.Parallel()
	// The app cannot read and nobody follows: keep what is known and say so (the delivery fails
	// visibly instead of silently leaving stale rules).
	h := newHarness(map[string]string{"CODEOWNERS": "* @new\n"})
	h.app.err = entity.ErrUnauthorized
	if err := h.uc.RefreshCodeowners(context.Background(), stored); !errors.Is(err, codeowners.ErrNoReader) {
		t.Fatalf("expected ErrNoReader, got %v", err)
	}
	if len(h.repos.saved) != 0 || len(h.pub.events) != 0 {
		t.Fatal("nothing may change when the file could not be read")
	}

	// GitHub failing is not "the file is gone": a 5xx must not clear the rules.
	g := newHarness(map[string]string{}, 1)
	g.files.boom = errors.New("github is down")
	if err := g.uc.RefreshCodeowners(context.Background(), stored); !errors.Is(err, g.files.boom) {
		t.Fatalf("expected the GitHub error, got %v", err)
	}
	if len(g.repos.saved) != 0 {
		t.Fatalf("a failed read must never become 'no CODEOWNERS': %+v", g.repos.saved)
	}
}

func TestRefresh_PropagatesStorageFailureAndCancellation(t *testing.T) {
	t.Parallel()
	h := newHarness(map[string]string{"CODEOWNERS": "* @new\n"})
	boom := errors.New("db down")
	h.repos.err = boom
	if err := h.uc.RefreshCodeowners(context.Background(), stored); !errors.Is(err, boom) {
		t.Fatalf("expected the storage error, got %v", err)
	}
	if len(h.pub.events) != 0 {
		t.Fatal("not stored, not announced")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newHarness(nil).uc.RefreshCodeowners(canceled, stored); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
