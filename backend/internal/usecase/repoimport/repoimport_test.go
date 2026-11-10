package repoimport_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
)

type fakeTokens struct{ err error }

func (f fakeTokens) AccessToken(_ context.Context, userID uint64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "ghu_user_" + string(rune('0'+userID)), nil
}

type fakeUserAPI struct {
	installations []entity.Installation
	repos         map[int64][]entity.InstallationRepository
	files         map[string]string // "owner/name:path" -> content
	fileErr       error
	err           error
	gotToken      string
}

func (f *fakeUserAPI) ListPullRequestFiles(context.Context, string, string, string, int) ([]entity.ChangedFile, error) {
	return nil, nil
}

func (f *fakeUserAPI) GetRepositoryFile(_ context.Context, _ string, owner, name, path string) ([]byte, error) {
	if f.fileErr != nil {
		return nil, f.fileErr
	}
	content, ok := f.files[owner+"/"+name+":"+path]
	if !ok {
		return nil, entity.ErrNotFound
	}
	return []byte(content), nil
}

func (f *fakeUserAPI) ListInstallations(_ context.Context, token string) ([]entity.Installation, error) {
	f.gotToken = token
	return f.installations, f.err
}

func (f *fakeUserAPI) ListInstallationRepositories(_ context.Context, token string, id int64) ([]entity.InstallationRepository, error) {
	f.gotToken = token
	if f.err != nil {
		return nil, f.err
	}
	repos, ok := f.repos[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	return repos, nil
}

type fakeRepos struct {
	mu        sync.Mutex
	byID      map[uint64]*entity.Repository
	follows   map[[2]uint64]bool
	nextID    uint64
	upsertErr error
	listErr   error
	getErr    error
	followErr error
}

func newFakeRepos() *fakeRepos {
	return &fakeRepos{byID: map[uint64]*entity.Repository{}, follows: map[[2]uint64]bool{}}
}

func (f *fakeRepos) Upsert(_ context.Context, r *entity.Repository) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	for id, existing := range f.byID {
		if existing.GitHubRepoID == r.GitHubRepoID {
			r.ID = id
		}
	}
	if r.ID == 0 {
		f.nextID++
		r.ID = f.nextID
	}
	cp := *r
	f.byID[r.ID] = &cp
	return nil
}

func (f *fakeRepos) GetByID(_ context.Context, id uint64) (*entity.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byID[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeRepos) GetByGitHubRepoID(_ context.Context, gh int64) (*entity.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, r := range f.byID {
		if r.GitHubRepoID == gh {
			cp := *r
			return &cp, nil
		}
	}
	return nil, entity.ErrNotFound
}

func (f *fakeRepos) ListFollowedByUser(_ context.Context, userID uint64) ([]entity.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []entity.Repository
	for key, on := range f.follows {
		if on && key[0] == userID {
			out = append(out, *f.byID[key[1]])
		}
	}
	return out, nil
}

func (f *fakeRepos) SetUserFollow(_ context.Context, userID, repoID uint64, follow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.followErr != nil {
		return f.followErr
	}
	f.follows[[2]uint64{userID, repoID}] = follow
	return nil
}

type fakeEvents struct {
	mu     sync.Mutex
	events []entity.ActivityEvent
}

func (f *fakeEvents) Publish(_ context.Context, e entity.ActivityEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

const userID = 1

func setup() (*repoimport.UseCase, *fakeUserAPI, *fakeRepos, *fakeEvents) {
	api := &fakeUserAPI{
		installations: []entity.Installation{{ID: 10, AccountLogin: "octocat", AccountType: "User"}, {ID: 20, AccountLogin: "acme", AccountType: "Organization"}},
		repos: map[int64][]entity.InstallationRepository{
			10: {{GitHubRepoID: 100, InstallationID: 10, Owner: "octocat", Name: "dotfiles", FullName: "octocat/dotfiles", DefaultBranch: "main"}},
			20: {
				{GitHubRepoID: 200, InstallationID: 20, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main", Private: true},
				{GitHubRepoID: 201, InstallationID: 20, Owner: "acme", Name: "api", FullName: "acme/api", DefaultBranch: "trunk"},
			},
		},
	}
	repos := newFakeRepos()
	events := &fakeEvents{}
	uc := repoimport.NewUseCase(fakeTokens{}, api, repos, events)
	return uc, api, repos, events
}

func TestListAccounts_UsesTheUsersToken(t *testing.T) {
	t.Parallel()
	uc, api, _, _ := setup()

	accounts, err := uc.ListAccounts(context.Background(), userID)
	if err != nil || len(accounts) != 2 || accounts[1].AccountLogin != "acme" {
		t.Fatalf("ListAccounts() = %+v, %v", accounts, err)
	}
	if api.gotToken != "ghu_user_1" {
		t.Fatalf("expected the signed-in user's token, got %q", api.gotToken)
	}
}

func TestListRepositories_MarksFollowed(t *testing.T) {
	t.Parallel()
	uc, _, repos, _ := setup()
	ctx := context.Background()

	imported, err := uc.Import(ctx, userID, 20, 201)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}

	list, err := uc.ListRepositories(ctx, userID, 20)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListRepositories() = %+v, %v", list, err)
	}
	byGH := map[int64]repoimport.ImportableRepository{}
	for _, r := range list {
		byGH[r.GitHubRepoID] = r
	}
	if byGH[200].Followed || byGH[200].RepositoryID != 0 {
		t.Errorf("acme/web must not be followed: %+v", byGH[200])
	}
	if !byGH[201].Followed || byGH[201].RepositoryID != imported.ID {
		t.Errorf("acme/api must be followed with repository id %d: %+v", imported.ID, byGH[201])
	}

	// Another user's follows do not leak into this user's list.
	_ = repos.SetUserFollow(ctx, 2, imported.ID, true)
	_ = repos.SetUserFollow(ctx, userID, imported.ID, false)
	list, _ = uc.ListRepositories(ctx, userID, 20)
	for _, r := range list {
		if r.Followed {
			t.Errorf("unexpected followed repository for user 1: %+v", r)
		}
	}
}

func TestImport_PersistsFollowsAndAnnounces(t *testing.T) {
	t.Parallel()
	uc, _, repos, events := setup()
	ctx := context.Background()

	repo, err := uc.Import(ctx, userID, 20, 201)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}
	if repo.ID == 0 || repo.FullName != "acme/api" || repo.InstallationID != 20 || repo.DefaultBranch != "trunk" {
		t.Fatalf("unexpected repository: %+v", repo)
	}
	if !repos.follows[[2]uint64{userID, repo.ID}] {
		t.Fatal("expected the user to follow the imported repository")
	}
	if len(events.events) != 1 || events.events[0].Type != "repository.followed" || events.events[0].RepositoryName != "acme/api" {
		t.Fatalf("expected a repository.followed event, got %+v", events.events)
	}
}

func TestImport_RefreshesCachedCodeownersFromGitHub(t *testing.T) {
	t.Parallel()
	uc, _, repos, _ := setup()
	ctx := context.Background()
	rules := []entity.CodeownerRule{{Pattern: "*", Owners: []string{"@acme/web"}}}
	_ = repos.Upsert(ctx, &entity.Repository{GitHubRepoID: 200, FullName: "acme/web", CachedCodeownersRules: rules, CodeownersStatus: entity.CodeownersPresent})

	// GitHub now has no CODEOWNERS file: the stale cache must not survive the re-import.
	repo, err := uc.Import(ctx, userID, 20, 200)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}
	if repo.CodeownersStatus != entity.CodeownersAbsent || len(repo.CachedCodeownersRules) != 0 || repo.InstallationID != 20 {
		t.Fatalf("expected CODEOWNERS refreshed to absent, got %+v", repo)
	}
}

func TestImport_RejectsRepositoriesTheUserCannotReach(t *testing.T) {
	t.Parallel()
	uc, _, repos, _ := setup()
	ctx := context.Background()

	if _, err := uc.Import(ctx, userID, 20, 100); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("repo from another installation: expected ErrNotFound, got %v", err)
	}
	if _, err := uc.Import(ctx, userID, 99, 200); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("unknown installation: expected ErrNotFound, got %v", err)
	}
	if _, err := uc.Import(ctx, userID, 0, 0); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("zero ids: expected ErrInvalidInput, got %v", err)
	}
	if len(repos.byID) != 0 {
		t.Fatal("nothing may be persisted for a rejected import")
	}
}

func TestUnfollow(t *testing.T) {
	t.Parallel()
	uc, _, repos, _ := setup()
	ctx := context.Background()
	repo, _ := uc.Import(ctx, userID, 10, 100)

	if err := uc.Unfollow(ctx, userID, repo.ID); err != nil {
		t.Fatalf("Unfollow() error: %v", err)
	}
	if repos.follows[[2]uint64{userID, repo.ID}] {
		t.Fatal("expected follow removed")
	}
	if err := uc.Unfollow(ctx, userID, 0); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("zero id: expected ErrInvalidInput, got %v", err)
	}
}

func TestUseCase_PropagatesFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	_, api, repos, events := setup()
	expired := repoimport.NewUseCase(fakeTokens{err: entity.ErrUnauthorized}, api, repos, events)
	if _, err := expired.ListAccounts(ctx, userID); !errors.Is(err, entity.ErrUnauthorized) {
		t.Errorf("ListAccounts with expired token: got %v", err)
	}
	if _, err := expired.ListRepositories(ctx, userID, 20); !errors.Is(err, entity.ErrUnauthorized) {
		t.Errorf("ListRepositories with expired token: got %v", err)
	}
	if _, err := expired.Import(ctx, userID, 20, 200); !errors.Is(err, entity.ErrUnauthorized) {
		t.Errorf("Import with expired token: got %v", err)
	}

	boom := errors.New("github down")
	uc, api2, repos2, _ := setup()
	api2.err = boom
	if _, err := uc.ListAccounts(ctx, userID); !errors.Is(err, boom) {
		t.Errorf("ListAccounts: got %v", err)
	}
	if _, err := uc.ListRepositories(ctx, userID, 20); !errors.Is(err, boom) {
		t.Errorf("ListRepositories: got %v", err)
	}
	api2.err = nil
	repos2.upsertErr = boom
	if _, err := uc.Import(ctx, userID, 20, 200); !errors.Is(err, boom) {
		t.Errorf("Import persist failure: got %v", err)
	}
	repos2.upsertErr = nil

	repos2.listErr = boom
	if _, err := uc.ListRepositories(ctx, userID, 20); !errors.Is(err, boom) {
		t.Errorf("ListRepositories follow lookup failure: got %v", err)
	}
	repos2.getErr = boom
	if _, err := uc.Import(ctx, userID, 20, 200); !errors.Is(err, boom) {
		t.Errorf("Import existing-repo lookup failure: got %v", err)
	}
	repos2.getErr = nil
	repos2.followErr = boom
	if _, err := uc.Import(ctx, userID, 20, 200); !errors.Is(err, boom) {
		t.Errorf("Import follow failure: got %v", err)
	}
}

func TestUseCase_HonorsCanceledContext(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := setup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := uc.ListAccounts(ctx, userID); !errors.Is(err, context.Canceled) {
		t.Errorf("ListAccounts: got %v", err)
	}
	if _, err := uc.ListRepositories(ctx, userID, 20); !errors.Is(err, context.Canceled) {
		t.Errorf("ListRepositories: got %v", err)
	}
	if _, err := uc.Import(ctx, userID, 20, 200); !errors.Is(err, context.Canceled) {
		t.Errorf("Import: got %v", err)
	}
	if err := uc.Unfollow(ctx, userID, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Unfollow: got %v", err)
	}
}

func TestImport_RecordsWhetherTheRepositoryHasCodeowners(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	uc, api, _, _ := setup()
	api.files = map[string]string{
		"acme/web:.github/CODEOWNERS": "# owners\n/src/ @acme/web @octocat\n",
		"acme/api:CODEOWNERS":         "* @acme/platform\n",
	}

	web, err := uc.Import(ctx, userID, 20, 200)
	if err != nil {
		t.Fatalf("Import(web) error: %v", err)
	}
	if web.CodeownersStatus != entity.CodeownersPresent || len(web.CachedCodeownersRules) != 1 || web.CachedCodeownersRules[0].Pattern != "/src/" {
		t.Fatalf("acme/web has .github/CODEOWNERS: %+v", web)
	}
	api2, _ := uc.Import(ctx, userID, 20, 201)
	if api2.CodeownersStatus != entity.CodeownersPresent || len(api2.CachedCodeownersRules) != 1 {
		t.Fatalf("acme/api has a root CODEOWNERS: %+v", api2)
	}
	dotfiles, _ := uc.Import(ctx, userID, 10, 100)
	if dotfiles.CodeownersStatus != entity.CodeownersAbsent || len(dotfiles.CachedCodeownersRules) != 0 {
		t.Fatalf("octocat/dotfiles has no CODEOWNERS: %+v", dotfiles)
	}
}

func TestImport_KeepsCodeownersUnknownWhenGitHubCannotSay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uc, api, repos, _ := setup()
	rules := []entity.CodeownerRule{{Pattern: "*", Owners: []string{"@acme/web"}}}
	_ = repos.Upsert(ctx, &entity.Repository{GitHubRepoID: 200, FullName: "acme/web", CachedCodeownersRules: rules, CodeownersStatus: entity.CodeownersPresent})

	api.fileErr = errors.New("github 502")
	web, err := uc.Import(ctx, userID, 20, 200)
	if err != nil {
		t.Fatalf("a CODEOWNERS lookup failure must not fail the import: %v", err)
	}
	if web.CodeownersStatus != entity.CodeownersPresent || len(web.CachedCodeownersRules) != 1 {
		t.Fatalf("expected the previously known CODEOWNERS kept, got %+v", web)
	}
	fresh, _ := uc.Import(ctx, userID, 20, 201)
	if fresh.CodeownersStatus != entity.CodeownersUnknown {
		t.Fatalf("never checked successfully: expected unknown, got %q", fresh.CodeownersStatus)
	}
}
