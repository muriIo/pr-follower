package webhook_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

// fakeFilesSyncer records which PR commits were handed over to be read with a follower's token.
type fakeFilesSyncer struct {
	synced  []string // "number@sha"
	err     error
	pub     *fakePub
	eventsN []int // events already published when each sync ran
	onSync  func()
}

func (f *fakeFilesSyncer) SyncWithToken(context.Context, string, entity.Repository, entity.PullRequest) error {
	return nil
}
func (f *fakeFilesSyncer) SyncAsUser(context.Context, uint64, entity.Repository, entity.PullRequest) error {
	return nil
}
func (f *fakeFilesSyncer) SyncAsFollower(_ context.Context, repo entity.Repository, pr entity.PullRequest) error {
	f.synced = append(f.synced, repo.FullName+"#"+string(rune('0'+pr.Number))+"@"+pr.HeadSHA)
	if f.pub != nil {
		f.eventsN = append(f.eventsN, len(f.pub.events))
	}
	if f.onSync != nil {
		f.onSync()
	}
	return f.err
}

func filesFixture(status entity.CodeownersStatus) (*webhook.IngestUseCase, *fakeFilesSyncer, *fakePub) {
	repoRepo := &fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, Owner: "acme", Name: "web", FullName: "acme/web", CodeownersStatus: status}}
	pub := &fakePub{}
	syncer := &fakeFilesSyncer{pub: pub}
	uc := webhook.NewIngestUseCase(repoRepo, &fakePRRepo{}, newFakeChecks(), pub, syncer)
	return uc, syncer, pub
}

func prEvent(action, sha string, mutate ...func(*webhook.PullRequestEventPayload)) webhook.PullRequestEventPayload {
	p := webhook.PullRequestEventPayload{Action: action, GitHubRepoID: 9001, GitHubPRID: 1, Number: 7, Title: "feat: x",
		AuthorLogin: "bia", State: entity.PRStateOpen, HeadSHA: sha}
	for _, m := range mutate {
		m(&p)
	}
	return p
}

func TestIngestPullRequestEvent_ReadsChangedFilesBeforeAnnouncing(t *testing.T) {
	t.Parallel()
	uc, syncer, pub := filesFixture(entity.CodeownersPresent)
	ctx := context.Background()

	if err := uc.IngestPullRequestEvent(ctx, prEvent("opened", "sha1")); err != nil {
		t.Fatalf("IngestPullRequestEvent() error: %v", err)
	}
	if len(syncer.synced) != 1 || syncer.synced[0] != "acme/web#7@sha1" {
		t.Fatalf("a new PR has its files read with a follower's token: %v", syncer.synced)
	}
	if syncer.eventsN[0] != 0 || len(pub.events) != 1 {
		t.Fatalf("files are read before clients are told to reload: eventsBefore=%v events=%d", syncer.eventsN, len(pub.events))
	}

	// A new commit changes the files, so they are read again.
	_ = uc.IngestPullRequestEvent(ctx, prEvent("synchronize", "sha2"))
	if len(syncer.synced) != 2 || syncer.synced[1] != "acme/web#7@sha2" {
		t.Fatalf("a new commit is read: %v", syncer.synced)
	}
}

func TestIngestPullRequestEvent_RedeliverDoesNotReadChangedFilesAgain(t *testing.T) {
	t.Parallel()
	uc, syncer, pub := filesFixture(entity.CodeownersPresent)
	ctx := context.Background()

	_ = uc.IngestPullRequestEvent(ctx, prEvent("opened", "sha1"))
	if err := uc.IngestPullRequestEvent(ctx, prEvent("opened", "sha1")); err != nil {
		t.Fatalf("redelivery error: %v", err)
	}
	if len(syncer.synced) != 1 || len(pub.events) != 1 {
		t.Fatalf("a redelivery reads and announces nothing: syncs=%d events=%d", len(syncer.synced), len(pub.events))
	}
}

func TestIngestPullRequestEvent_SkipsChangedFilesWhenNoTurnCanComeFromThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, tc := range map[string]struct {
		status entity.CodeownersStatus
		event  webhook.PullRequestEventPayload
	}{
		"repository without CODEOWNERS": {entity.CodeownersAbsent, prEvent("opened", "sha1")},
		"CODEOWNERS not checked":        {entity.CodeownersUnknown, prEvent("opened", "sha1")},
		"Dependabot PR":                 {entity.CodeownersPresent, prEvent("opened", "sha1", func(p *webhook.PullRequestEventPayload) { p.AuthorLogin = "dependabot[bot]" })},
		"closed PR":                     {entity.CodeownersPresent, prEvent("closed", "sha1", func(p *webhook.PullRequestEventPayload) { p.State = entity.PRStateClosed })},
		"merged PR": {entity.CodeownersPresent, prEvent("closed", "sha1", func(p *webhook.PullRequestEventPayload) {
			p.State, p.Merged = entity.PRStateClosed, true
		})},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			uc, syncer, pub := filesFixture(tc.status)
			if err := uc.IngestPullRequestEvent(ctx, tc.event); err != nil {
				t.Fatal(err)
			}
			if len(syncer.synced) != 0 || len(pub.events) != 1 {
				t.Fatalf("syncs=%v events=%d", syncer.synced, len(pub.events))
			}
		})
	}
}

func TestIngestPullRequestEvent_AFailedFilesReadDoesNotLoseTheWebhook(t *testing.T) {
	t.Parallel()
	uc, syncer, pub := filesFixture(entity.CodeownersPresent)
	syncer.err = errors.New("no follower can read")

	if err := uc.IngestPullRequestEvent(context.Background(), prEvent("opened", "sha1")); err != nil {
		t.Fatalf("GitHub does not redeliver on its own, so the PR must be kept: %v", err)
	}
	if len(pub.events) != 1 {
		t.Fatalf("the PR is still announced; its files are read when a follower loads the workspace: %d events", len(pub.events))
	}

	// Without a syncer wired (login not configured) the webhook works as before.
	plain := webhook.NewIngestUseCase(&fakeRepoRepo{repo: &entity.Repository{ID: 10, GitHubRepoID: 9001, CodeownersStatus: entity.CodeownersPresent}},
		&fakePRRepo{}, newFakeChecks(), &fakePub{}, nil)
	if err := plain.IngestPullRequestEvent(context.Background(), prEvent("opened", "sha1")); err != nil {
		t.Fatal(err)
	}
}

func TestIngestPullRequestEvent_StopsWhenCanceledWhileReadingFiles(t *testing.T) {
	t.Parallel()
	uc, syncer, pub := filesFixture(entity.CodeownersPresent)
	ctx, cancel := context.WithCancel(context.Background())
	syncer.onSync = cancel // canceled while GitHub is being read
	if err := uc.IngestPullRequestEvent(ctx, prEvent("opened", "sha1")); !errors.Is(err, context.Canceled) || len(pub.events) != 0 || len(syncer.synced) != 1 {
		t.Fatalf("err=%v events=%d syncs=%d", err, len(pub.events), len(syncer.synced))
	}
}
