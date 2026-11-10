package prfiles_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/prfiles"
)

type fakeTokens struct{ err error }

func (f fakeTokens) AccessToken(context.Context, uint64) (string, error) { return "ghu_user", f.err }

type fakeGitHub struct {
	calls atomic.Int32
	files []entity.ChangedFile
	err   error
	gate  chan struct{} // when set, calls wait on it (to observe coalescing)
}

func (f *fakeGitHub) ListInstallations(context.Context, string) ([]entity.Installation, error) {
	return nil, nil
}
func (f *fakeGitHub) ListInstallationRepositories(context.Context, string, int64) ([]entity.InstallationRepository, error) {
	return nil, nil
}
func (f *fakeGitHub) GetRepositoryFile(context.Context, string, string, string, string) ([]byte, error) {
	return nil, entity.ErrNotFound
}
func (f *fakeGitHub) ListPullRequestFiles(_ context.Context, token, owner, name string, number int) ([]entity.ChangedFile, error) {
	f.calls.Add(1)
	if f.gate != nil {
		<-f.gate
	}
	if token != "ghu_user" || owner != "acme" || name != "web" || number != 7 {
		return nil, entity.ErrNotFound
	}
	return f.files, f.err
}

type fakeRepos struct {
	followed []entity.Repository
	err      error
}

func (f fakeRepos) ListFollowedByUser(context.Context, uint64) ([]entity.Repository, error) {
	return f.followed, f.err
}

type fakePRs struct{ heads map[int]string }

func (f fakePRs) HeadSHA(_ context.Context, _ uint64, number int) (string, bool) {
	head, ok := f.heads[number]
	return head, ok
}

var user = entity.User{ID: 1, Login: "octocat", Teams: []string{"@acme/web"}}

func setup(gh *fakeGitHub, heads map[int]string) *prfiles.UseCase {
	repo := entity.Repository{ID: 10, Owner: "acme", Name: "web", FullName: "acme/web", CachedCodeownersRules: []entity.CodeownerRule{
		{Pattern: "*", Owners: []string{"@acme/platform"}},
		{Pattern: "/src/app/", Owners: []string{"@acme/web"}},
		{Pattern: "docs/", Owners: []string{"@octocat"}},
	}}
	return prfiles.NewUseCase(fakeTokens{}, gh, fakeRepos{followed: []entity.Repository{repo}}, fakePRs{heads: heads})
}

func paths(files []prfiles.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func TestList_FlagsOwnedFilesAndListsThemFirst(t *testing.T) {
	t.Parallel()
	gh := &fakeGitHub{files: []entity.ChangedFile{
		{Path: "go.mod", Additions: 1},
		{Path: "src/app/seat.ts", Additions: 24, Deletions: 3, Patch: "@@"},
		{Path: "README.md"},
		{Path: "docs/setup.md"},
	}}
	files, err := setup(gh, nil).List(context.Background(), user, "acme/web", 7)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if got := paths(files); len(got) != 4 || got[0] != "src/app/seat.ts" || got[1] != "docs/setup.md" || got[2] != "go.mod" || got[3] != "README.md" {
		t.Fatalf("owned files first, GitHub order otherwise; got %v", got)
	}
	owned := map[string]bool{}
	for _, f := range files {
		owned[f.Path] = f.Owned
	}
	if !owned["src/app/seat.ts"] || !owned["docs/setup.md"] || owned["go.mod"] || owned["README.md"] {
		t.Fatalf("unexpected ownership: %v", owned)
	}
}

func TestList_OnlyForFollowedRepositories(t *testing.T) {
	t.Parallel()
	gh := &fakeGitHub{}
	if _, err := setup(gh, nil).List(context.Background(), user, "acme/secret", 7); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("a repository the user does not follow: expected ErrNotFound, got %v", err)
	}
	if gh.calls.Load() != 0 {
		t.Fatal("GitHub must not be called for a repository the user does not follow")
	}
}

func TestList_CachesPerHeadCommitAndRefetchesOnANewCommit(t *testing.T) {
	t.Parallel()
	gh := &fakeGitHub{files: []entity.ChangedFile{{Path: "a.ts"}}}
	heads := map[int]string{7: "sha-1"}
	uc := setup(gh, heads)
	ctx := context.Background()

	for range 3 {
		if _, err := uc.List(ctx, user, "acme/web", 7); err != nil {
			t.Fatalf("List() error: %v", err)
		}
	}
	if gh.calls.Load() != 1 {
		t.Fatalf("same head commit: expected 1 GitHub call, got %d", gh.calls.Load())
	}
	heads[7] = "sha-2"
	_, _ = uc.List(ctx, user, "acme/web", 7)
	if gh.calls.Load() != 2 {
		t.Fatalf("new head commit: expected a fresh GitHub call, got %d", gh.calls.Load())
	}
}

func TestList_DoesNotCacheWithoutAKnownHead(t *testing.T) {
	t.Parallel()
	gh := &fakeGitHub{files: []entity.ChangedFile{{Path: "a.ts"}}}
	uc := setup(gh, nil)
	_, _ = uc.List(context.Background(), user, "acme/web", 7)
	_, _ = uc.List(context.Background(), user, "acme/web", 7)
	if gh.calls.Load() != 2 {
		t.Fatalf("unknown head: nothing to key a cache on, expected 2 calls, got %d", gh.calls.Load())
	}
}

func TestList_CoalescesConcurrentRequests(t *testing.T) {
	t.Parallel()
	gh := &fakeGitHub{files: []entity.ChangedFile{{Path: "a.ts"}}, gate: make(chan struct{})}
	uc := setup(gh, map[int]string{7: "sha-1"})

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = uc.List(context.Background(), user, "acme/web", 7)
		}()
	}
	for gh.calls.Load() == 0 {
	}
	close(gh.gate)
	wg.Wait()
	if gh.calls.Load() != 1 {
		t.Fatalf("concurrent requests for one PR: expected 1 GitHub call, got %d", gh.calls.Load())
	}
}

func TestList_PropagatesFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	boom := errors.New("github down")

	failing := setup(&fakeGitHub{err: boom}, map[int]string{7: "sha-1"})
	if _, err := failing.List(ctx, user, "acme/web", 7); !errors.Is(err, boom) {
		t.Errorf("GitHub failure: got %v", err)
	}
	if _, err := setup(&fakeGitHub{}, nil).List(ctx, user, "acme/web", 404); !errors.Is(err, entity.ErrNotFound) {
		t.Errorf("unknown PR: got %v", err)
	}

	expired := prfiles.NewUseCase(fakeTokens{err: entity.ErrUnauthorized}, &fakeGitHub{}, fakeRepos{followed: []entity.Repository{{ID: 10, Owner: "acme", Name: "web", FullName: "acme/web"}}}, fakePRs{})
	if _, err := expired.List(ctx, user, "acme/web", 7); !errors.Is(err, entity.ErrUnauthorized) {
		t.Errorf("expired token: got %v", err)
	}
	repoFailure := prfiles.NewUseCase(fakeTokens{}, &fakeGitHub{}, fakeRepos{err: boom}, fakePRs{})
	if _, err := repoFailure.List(ctx, user, "acme/web", 7); !errors.Is(err, boom) {
		t.Errorf("follow lookup failure: got %v", err)
	}
	if _, err := setup(&fakeGitHub{}, nil).List(ctx, user, "acme/web", 0); !errors.Is(err, entity.ErrInvalidInput) {
		t.Errorf("invalid number: got %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := setup(&fakeGitHub{}, nil).List(canceled, user, "acme/web", 7); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: got %v", err)
	}
}
