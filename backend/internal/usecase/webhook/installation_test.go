package webhook_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

// fakeAccessStore holds repositories keyed by GitHub repository id.
type fakeAccessStore struct {
	mu       sync.Mutex
	repos    map[int64]*entity.Repository
	listErr  error
	saveErr  error
	getErr   error
	getCalls int
}

func newFakeAccessStore(repos ...entity.Repository) *fakeAccessStore {
	s := &fakeAccessStore{repos: map[int64]*entity.Repository{}}
	for i := range repos {
		r := repos[i]
		r.ID = uint64(i + 1)
		s.repos[r.GitHubRepoID] = &r
	}
	return s
}

func (s *fakeAccessStore) ListByInstallation(_ context.Context, installationID int64) ([]entity.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []entity.Repository
	for _, r := range s.repos {
		if r.InstallationID == installationID {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return out, nil
}

func (s *fakeAccessStore) SaveAccess(_ context.Context, repo *entity.Repository) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	cp := *repo
	s.repos[repo.GitHubRepoID] = &cp
	return nil
}

func (s *fakeAccessStore) GetByGitHubRepoID(_ context.Context, id int64) (*entity.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.getErr != nil {
		return nil, s.getErr
	}
	r, ok := s.repos[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *fakeAccessStore) access(id int64) (entity.RepositoryAccess, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repos[id].Access, s.repos[id].InstallationID
}

type recordingPublisher struct {
	mu     sync.Mutex
	events []entity.ActivityEvent
}

func (p *recordingPublisher) Publish(_ context.Context, e entity.ActivityEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
	return nil
}

func (p *recordingPublisher) types() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, e := range p.events {
		out = append(out, e.Type+" "+e.RepositoryName)
	}
	return out
}

func acmeRepos() *fakeAccessStore {
	return newFakeAccessStore(
		entity.Repository{GitHubRepoID: 900, InstallationID: 20, FullName: "acme/api", Access: entity.RepositoryAccessActive},
		entity.Repository{GitHubRepoID: 901, InstallationID: 20, FullName: "acme/web", Access: entity.RepositoryAccessActive},
		entity.Repository{GitHubRepoID: 100, InstallationID: 10, FullName: "octo/x", Access: entity.RepositoryAccessActive},
	)
}

func newInstallationUC(store *fakeAccessStore, pub *recordingPublisher) *webhook.InstallationUseCase {
	return webhook.NewInstallationUseCase(store, pub)
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestInstallationEvent_DeletedMarksEveryRepositoryRemovedAndRedeliveryIsIdempotent(t *testing.T) {
	t.Parallel()
	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)

	if err := uc.IngestInstallationEvent(context.Background(), webhook.InstallationEventPayload{Action: "deleted", InstallationID: 20, AccountLogin: "acme"}); err != nil {
		t.Fatalf("IngestInstallationEvent() error: %v", err)
	}
	for _, id := range []int64{900, 901} {
		if access, _ := store.access(id); access != entity.RepositoryAccessRemoved {
			t.Errorf("repo %d: expected removed, got %q", id, access)
		}
	}
	if access, _ := store.access(100); access != entity.RepositoryAccessActive {
		t.Errorf("other installation must be untouched, got %q", access)
	}
	if want := []string{"repository.access_lost acme/api", "repository.access_lost acme/web"}; !equal(pub.types(), want) {
		t.Fatalf("events = %v, want %v", pub.types(), want)
	}

	// Redelivery is idempotent: no new events.
	_ = uc.IngestInstallationEvent(context.Background(), webhook.InstallationEventPayload{Action: "deleted", InstallationID: 20})
	if len(pub.types()) != 2 {
		t.Fatalf("redelivered webhook must not publish again: %v", pub.types())
	}
}

func TestInstallationEvent_SuspendAndUnsuspend(t *testing.T) {
	t.Parallel()
	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)
	ctx := context.Background()

	_ = uc.IngestInstallationEvent(ctx, webhook.InstallationEventPayload{Action: "suspend", InstallationID: 20})
	if access, _ := store.access(900); access != entity.RepositoryAccessSuspended {
		t.Fatalf("expected suspended, got %q", access)
	}
	_ = uc.IngestInstallationEvent(ctx, webhook.InstallationEventPayload{Action: "unsuspend", InstallationID: 20})
	if access, _ := store.access(900); access != entity.RepositoryAccessActive {
		t.Fatalf("expected active again, got %q", access)
	}
	want := []string{
		"repository.access_suspended acme/api", "repository.access_suspended acme/web",
		"repository.access_restored acme/api", "repository.access_restored acme/web",
	}
	if !equal(pub.types(), want) {
		t.Fatalf("events = %v, want %v", pub.types(), want)
	}
}

func TestInstallationEvent_CreatedRestoresKnownRepositoriesUnderNewInstallation(t *testing.T) {
	t.Parallel()
	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)
	ctx := context.Background()
	_ = uc.IngestInstallationEvent(ctx, webhook.InstallationEventPayload{Action: "deleted", InstallationID: 20})
	pub.events = nil

	// Re-installed (new installation id) on acme/api only; 555 is unknown to PR Follower.
	err := uc.IngestInstallationEvent(ctx, webhook.InstallationEventPayload{Action: "created", InstallationID: 21, RepositoryIDs: []int64{900, 555}})
	if err != nil {
		t.Fatalf("IngestInstallationEvent() error: %v", err)
	}
	if access, inst := store.access(900); access != entity.RepositoryAccessActive || inst != 21 {
		t.Fatalf("acme/api: expected active under 21, got %q / %d", access, inst)
	}
	if access, _ := store.access(901); access != entity.RepositoryAccessRemoved {
		t.Fatalf("acme/web was not reselected and stays removed, got %q", access)
	}
	if want := []string{"repository.access_restored acme/api"}; !equal(pub.types(), want) {
		t.Fatalf("events = %v, want %v", pub.types(), want)
	}
}

func TestInstallationEvent_IgnoresOtherActions(t *testing.T) {
	t.Parallel()
	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)
	if err := uc.IngestInstallationEvent(context.Background(), webhook.InstallationEventPayload{Action: "new_permissions_accepted", InstallationID: 20}); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(pub.types()) != 0 || store.getCalls != 0 {
		t.Fatalf("unexpected side effects: events=%v", pub.types())
	}
}

func TestInstallationRepositoriesEvent_RemovedAndAdded(t *testing.T) {
	t.Parallel()
	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)
	ctx := context.Background()

	err := uc.IngestInstallationRepositoriesEvent(ctx, webhook.InstallationRepositoriesEventPayload{
		Action: "removed", InstallationID: 20, RemovedIDs: []int64{901, 555},
	})
	if err != nil {
		t.Fatalf("removed: %v", err)
	}
	if access, _ := store.access(901); access != entity.RepositoryAccessRemoved {
		t.Fatalf("acme/web: expected removed, got %q", access)
	}
	if access, _ := store.access(900); access != entity.RepositoryAccessActive {
		t.Fatalf("acme/api must stay active, got %q", access)
	}

	err = uc.IngestInstallationRepositoriesEvent(ctx, webhook.InstallationRepositoriesEventPayload{
		Action: "added", InstallationID: 20, AddedIDs: []int64{901},
	})
	if err != nil {
		t.Fatalf("added: %v", err)
	}
	if access, _ := store.access(901); access != entity.RepositoryAccessActive {
		t.Fatalf("acme/web: expected active again, got %q", access)
	}
	want := []string{"repository.access_lost acme/web", "repository.access_restored acme/web"}
	if !equal(pub.types(), want) {
		t.Fatalf("events = %v, want %v", pub.types(), want)
	}
}

func TestInstallationRepositoriesEvent_IgnoresRemovalFromAnotherInstallation(t *testing.T) {
	t.Parallel()
	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)

	// octo/x belongs to installation 10; a removal reported by installation 20 must not touch it.
	_ = uc.IngestInstallationRepositoriesEvent(context.Background(), webhook.InstallationRepositoriesEventPayload{
		Action: "removed", InstallationID: 20, RemovedIDs: []int64{100},
	})
	if access, _ := store.access(100); access != entity.RepositoryAccessActive {
		t.Fatalf("expected octo/x untouched, got %q", access)
	}
	if len(pub.types()) != 0 {
		t.Fatalf("unexpected events: %v", pub.types())
	}
}

func TestInstallationUseCase_PropagatesFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")

	store := acmeRepos()
	store.listErr = boom
	uc := newInstallationUC(store, &recordingPublisher{})
	if err := uc.IngestInstallationEvent(context.Background(), webhook.InstallationEventPayload{Action: "deleted", InstallationID: 20}); !errors.Is(err, boom) {
		t.Errorf("list failure: got %v", err)
	}

	store = acmeRepos()
	store.saveErr = boom
	uc = newInstallationUC(store, &recordingPublisher{})
	if err := uc.IngestInstallationEvent(context.Background(), webhook.InstallationEventPayload{Action: "deleted", InstallationID: 20}); !errors.Is(err, boom) {
		t.Errorf("save failure on installation event: got %v", err)
	}
	if err := uc.IngestInstallationRepositoriesEvent(context.Background(), webhook.InstallationRepositoriesEventPayload{Action: "removed", InstallationID: 20, RemovedIDs: []int64{900}}); !errors.Is(err, boom) {
		t.Errorf("save failure on repositories event: got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	uc = newInstallationUC(acmeRepos(), &recordingPublisher{})
	if err := uc.IngestInstallationEvent(ctx, webhook.InstallationEventPayload{Action: "deleted", InstallationID: 20}); !errors.Is(err, context.Canceled) {
		t.Errorf("installation event: expected context.Canceled, got %v", err)
	}
	if err := uc.IngestInstallationRepositoriesEvent(ctx, webhook.InstallationRepositoriesEventPayload{Action: "added", InstallationID: 20, AddedIDs: []int64{900}}); !errors.Is(err, context.Canceled) {
		t.Errorf("repositories event: expected context.Canceled, got %v", err)
	}
}

func TestInstallationRepositoriesEvent_EdgeCases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)
	if err := uc.IngestInstallationRepositoriesEvent(ctx, webhook.InstallationRepositoriesEventPayload{Action: "renamed", InstallationID: 20}); err != nil {
		t.Fatalf("unknown action: %v", err)
	}

	// An active repository moving to another installation is saved silently.
	if err := uc.IngestInstallationRepositoriesEvent(ctx, webhook.InstallationRepositoriesEventPayload{Action: "added", InstallationID: 99, AddedIDs: []int64{100}}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if access, inst := store.access(100); access != entity.RepositoryAccessActive || inst != 99 {
		t.Fatalf("expected octo/x active under 99, got %q / %d", access, inst)
	}
	if len(pub.types()) != 0 {
		t.Fatalf("a move without access change must not publish: %v", pub.types())
	}

	boom := errors.New("db down")
	failing := acmeRepos()
	failing.getErr = boom
	if err := newInstallationUC(failing, &recordingPublisher{}).IngestInstallationRepositoriesEvent(ctx,
		webhook.InstallationRepositoriesEventPayload{Action: "added", InstallationID: 20, AddedIDs: []int64{900}}); !errors.Is(err, boom) {
		t.Fatalf("lookup failure: got %v", err)
	}
}

type cancelingAccessStore struct {
	*fakeAccessStore
	cancel context.CancelFunc
}

func (c cancelingAccessStore) GetByGitHubRepoID(ctx context.Context, id int64) (*entity.Repository, error) {
	c.cancel()
	return c.fakeAccessStore.GetByGitHubRepoID(ctx, id)
}

func TestInstallationRepositoriesEvent_StopsWhenCanceledMidway(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	store := acmeRepos()
	uc := webhook.NewInstallationUseCase(cancelingAccessStore{fakeAccessStore: store, cancel: cancel}, &recordingPublisher{})

	err := uc.IngestInstallationRepositoriesEvent(ctx, webhook.InstallationRepositoriesEventPayload{
		Action: "removed", InstallationID: 20, RemovedIDs: []int64{900, 901},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if access, _ := store.access(901); access != entity.RepositoryAccessActive {
		t.Fatalf("second repository must not be processed after cancellation, got %q", access)
	}
}

func TestInstallationRepositoriesEvent_RedeliveryIsIdempotent(t *testing.T) {
	t.Parallel()
	store, pub := acmeRepos(), &recordingPublisher{}
	uc := newInstallationUC(store, pub)
	payload := webhook.InstallationRepositoriesEventPayload{Action: "removed", InstallationID: 20, RemovedIDs: []int64{901}}

	for range 2 {
		if err := uc.IngestInstallationRepositoriesEvent(context.Background(), payload); err != nil {
			t.Fatalf("delivery: %v", err)
		}
	}
	if access, _ := store.access(901); access != entity.RepositoryAccessRemoved {
		t.Fatalf("expected removed, got %q", access)
	}
	if want := []string{"repository.access_lost acme/web"}; !equal(pub.types(), want) {
		t.Fatalf("events = %v, want %v", pub.types(), want)
	}
}
