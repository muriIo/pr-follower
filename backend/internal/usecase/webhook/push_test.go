package webhook_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

type recordingRefresher struct {
	repos []string
	err   error
}

func (r *recordingRefresher) RefreshCodeowners(_ context.Context, repo entity.Repository) error {
	r.repos = append(r.repos, repo.FullName)
	return r.err
}

func pushPayload(mutate ...func(*webhook.PushEventPayload)) webhook.PushEventPayload {
	p := webhook.PushEventPayload{GitHubRepoID: 9001, Ref: "refs/heads/main", DefaultBranch: "main", ChangedPaths: []string{".github/CODEOWNERS"}}
	for _, m := range mutate {
		m(&p)
	}
	return p
}

func newPushIngest(refresher *recordingRefresher) *webhook.PushIngestUseCase {
	repos := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web", DefaultBranch: "main"}}
	return webhook.NewPushIngestUseCase(repos, refresher)
}

func TestIngestPushEvent_RefreshesWhenTheDefaultBranchChangesCodeowners(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*webhook.PushEventPayload){
		".github/CODEOWNERS": func(p *webhook.PushEventPayload) {},
		"the root file":      func(p *webhook.PushEventPayload) { p.ChangedPaths = []string{"src/a.ts", "CODEOWNERS"} },
		"docs/CODEOWNERS":    func(p *webhook.PushEventPayload) { p.ChangedPaths = []string{"docs/CODEOWNERS"} },
		"a truncated push (GitHub lists at most 20 commits): cannot rule it out": func(p *webhook.PushEventPayload) {
			p.ChangedPaths, p.Truncated = []string{"src/a.ts"}, true
		},
	} {
		refresher := &recordingRefresher{}
		if err := newPushIngest(refresher).IngestPushEvent(context.Background(), pushPayload(mutate)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(refresher.repos) != 1 || refresher.repos[0] != "arvore/hive-web" {
			t.Errorf("%s: expected one refresh, got %v", name, refresher.repos)
		}
	}
}

func TestIngestPushEvent_IgnoresWhatCannotChangeTheRulesInForce(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*webhook.PushEventPayload){
		"another file":                func(p *webhook.PushEventPayload) { p.ChangedPaths = []string{"src/app.ts", "README.md"} },
		"no files at all":             func(p *webhook.PushEventPayload) { p.ChangedPaths = nil },
		"a CODEOWNERS in a subfolder": func(p *webhook.PushEventPayload) { p.ChangedPaths = []string{"src/CODEOWNERS"} },
		"a feature branch":            func(p *webhook.PushEventPayload) { p.Ref = "refs/heads/feat/x" },
		"a tag":                       func(p *webhook.PushEventPayload) { p.Ref = "refs/tags/v1" },
		"a deleted branch":            func(p *webhook.PushEventPayload) { p.Deleted = true },
		"a branch named like the default one but elsewhere": func(p *webhook.PushEventPayload) { p.Ref = "refs/heads/release/main" },
	} {
		refresher := &recordingRefresher{}
		if err := newPushIngest(refresher).IngestPushEvent(context.Background(), pushPayload(mutate)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(refresher.repos) != 0 {
			t.Errorf("%s: nothing to refresh, got %v", name, refresher.repos)
		}
	}
}

func TestIngestPushEvent_UsesTheStoredDefaultBranchWhenThePayloadHasNone(t *testing.T) {
	t.Parallel()
	refresher := &recordingRefresher{}
	err := newPushIngest(refresher).IngestPushEvent(context.Background(), pushPayload(func(p *webhook.PushEventPayload) { p.DefaultBranch = "" }))
	if err != nil || len(refresher.repos) != 1 {
		t.Fatalf("err=%v refreshes=%v", err, refresher.repos)
	}
}

func TestIngestPushEvent_UnknownRepositoryFailureAndCancellation(t *testing.T) {
	t.Parallel()
	refresher := &recordingRefresher{}
	unknown := webhook.NewPushIngestUseCase(&fakeRepoRepo{err: entity.ErrNotFound}, refresher)
	if err := unknown.IngestPushEvent(context.Background(), pushPayload()); err != nil || len(refresher.repos) != 0 {
		t.Fatalf("a repository nobody follows is ignored: err=%v refreshes=%v", err, refresher.repos)
	}

	boom := errors.New("nobody can read")
	failing := &recordingRefresher{err: boom}
	if err := newPushIngest(failing).IngestPushEvent(context.Background(), pushPayload()); !errors.Is(err, boom) {
		t.Fatalf("a refresh that failed must fail the delivery (so it shows red and can be redelivered): %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newPushIngest(&recordingRefresher{}).IngestPushEvent(canceled, pushPayload()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

// convergingRefresher behaves like the real one: it writes only when the repository's rules differ
// from what GitHub holds now.
type convergingRefresher struct{ writes int }

func (c *convergingRefresher) RefreshCodeowners(_ context.Context, repo entity.Repository) error {
	if repo.CodeownersStatus != entity.CodeownersPresent {
		c.writes++
		return nil
	}
	return nil
}

func TestIngestPushEvent_RedeliverAfterTheRulesAreCurrentIsANoOp(t *testing.T) {
	t.Parallel()
	// GitHub redelivers (a manual redelivery, or a retry after a failed answer): the use case keeps
	// no state of its own, so every delivery just asks for a refresh and a current cache is left alone.
	current := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, FullName: "arvore/hive-web", DefaultBranch: "main", CodeownersStatus: entity.CodeownersPresent}}
	refresher := &convergingRefresher{}
	uc := webhook.NewPushIngestUseCase(current, refresher)
	for range 3 {
		if err := uc.IngestPushEvent(context.Background(), pushPayload()); err != nil {
			t.Fatal(err)
		}
	}
	if refresher.writes != 0 {
		t.Fatalf("a redelivered push must not change anything once the rules are current, got %d writes", refresher.writes)
	}
}
