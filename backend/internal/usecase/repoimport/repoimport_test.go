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
	openPRs       map[string][]entity.RemotePullRequest // "owner/name" -> open PRs
	prsErr        error
	checkRuns     map[string][]entity.CheckRun // head SHA -> runs
	checksErr     error
	onCheckRuns   func() // called before each check-run read, to cancel mid-backfill
}

func (f *fakeUserAPI) ListOpenPullRequests(_ context.Context, _ string, owner, name string) ([]entity.RemotePullRequest, error) {
	if f.prsErr != nil {
		return nil, f.prsErr
	}
	return f.openPRs[owner+"/"+name], nil
}

func (f *fakeUserAPI) ListCommitCheckRuns(_ context.Context, _ string, _, _, sha string) ([]entity.CheckRun, error) {
	if f.onCheckRuns != nil {
		f.onCheckRuns()
	}
	if f.checksErr != nil {
		return nil, f.checksErr
	}
	return f.checkRuns[sha], nil
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

type fakeUsers struct{}

func (fakeUsers) Upsert(context.Context, *entity.User) error { return nil }
func (fakeUsers) GetByID(_ context.Context, id uint64) (*entity.User, error) {
	if id == userID {
		return &entity.User{ID: userID, Login: "octocat"}, nil
	}
	return nil, entity.ErrNotFound
}
func (fakeUsers) GetByGitHubID(context.Context, int64) (*entity.User, error) {
	return nil, entity.ErrNotFound
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

type fakePRs struct {
	mu   sync.Mutex
	prs  map[[2]uint64]entity.PullRequest // repository id + number
	next uint64
}

func newFakePRs() *fakePRs { return &fakePRs{prs: map[[2]uint64]entity.PullRequest{}} }

func (f *fakePRs) GetByRepoAndNumber(_ context.Context, repoID uint64, number int) (*entity.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := f.prs[[2]uint64{repoID, uint64(number)}]
	if !ok {
		return nil, entity.ErrNotFound
	}
	return &pr, nil
}

func (f *fakePRs) Upsert(_ context.Context, pr *entity.PullRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := [2]uint64{pr.RepositoryID, uint64(pr.Number)}
	if existing, ok := f.prs[key]; ok {
		pr.ID = existing.ID
	} else {
		f.next++
		pr.ID = f.next
	}
	f.prs[key] = *pr
	return nil
}

type fakeChecks struct {
	mu   sync.Mutex
	runs map[int64]entity.CheckRun
}

func newFakeChecks() *fakeChecks { return &fakeChecks{runs: map[int64]entity.CheckRun{}} }

func (f *fakeChecks) UpsertCheckRun(_ context.Context, run entity.CheckRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[run.ID] = run
	return nil
}

func (f *fakeChecks) GetCheckRun(_ context.Context, id int64) (*entity.CheckRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	return &run, nil
}

func (f *fakeChecks) ListCheckRuns(_ context.Context, repoID uint64, sha string) ([]entity.CheckRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []entity.CheckRun
	for _, r := range f.runs {
		if r.RepositoryID == repoID && r.HeadSHA == sha {
			out = append(out, r)
		}
	}
	return out, nil
}

const userID = 1

func setup() (*repoimport.UseCase, *fakeUserAPI, *fakeRepos, *fakeEvents) {
	uc, api, repos, events, _, _ := setupBackfill()
	return uc, api, repos, events
}

func setupBackfill() (*repoimport.UseCase, *fakeUserAPI, *fakeRepos, *fakeEvents, *fakePRs, *fakeChecks) {
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
	prs, checks := newFakePRs(), newFakeChecks()
	uc := repoimport.NewUseCase(fakeTokens{}, api, repos, prs, checks, fakeUsers{}, events)
	return uc, api, repos, events, prs, checks
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
	if len(events.events) != 1 || events.events[0].Type != "repository.followed" || events.events[0].RepositoryName != "acme/api" ||
		events.events[0].ActorLogin != "octocat" {
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
	expired := repoimport.NewUseCase(fakeTokens{err: entity.ErrUnauthorized}, api, repos, newFakePRs(), newFakeChecks(), fakeUsers{}, events)
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

func twoOpenPRs(api *fakeUserAPI) {
	api.openPRs = map[string][]entity.RemotePullRequest{"acme/web": {
		{GitHubPRID: 7001, Number: 7, Title: "feat: cart", AuthorLogin: "bia", HTMLURL: "https://github.com/acme/web/pull/7", HeadSHA: "sha7", BaseRef: "main"},
		{GitHubPRID: 8001, Number: 8, Title: "chore(deps): bump axios from 1.7.7 to 2.0.0", AuthorLogin: "dependabot[bot]", HeadSHA: "sha8", BaseRef: "main"},
		{GitHubPRID: 9001, Number: 9, Title: "wip", AuthorLogin: "bia", IsDraft: true, HeadSHA: "sha9", BaseRef: "main"},
	}}
	api.checkRuns = map[string][]entity.CheckRun{
		"sha7": {{ID: 71, HeadSHA: "sha7", Name: "build", Status: "completed", Conclusion: "success"}},
		"sha9": {{ID: 91, HeadSHA: "sha9", Name: "build", Status: "in_progress"}},
	}
}

func TestImport_BackfillsOpenPullRequestsWithTheirCI(t *testing.T) {
	t.Parallel()
	uc, api, _, events, prs, checks := setupBackfill()
	twoOpenPRs(api)

	repo, err := uc.Import(context.Background(), userID, 20, 200)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}
	if len(prs.prs) != 3 {
		t.Fatalf("expected the 3 open PRs to be stored, got %d", len(prs.prs))
	}
	get := func(n int) entity.PullRequest { return prs.prs[[2]uint64{repo.ID, uint64(n)}] }

	web := get(7)
	if web.RepositoryID != repo.ID || web.RepositoryName != "acme/web" || web.GitHubPRID != 7001 || web.Title != "feat: cart" ||
		web.AuthorLogin != "bia" || web.State != entity.PRStateOpen || web.HeadSHA != "sha7" || web.BaseRef != "main" || web.IsDependabot || web.IsDraft {
		t.Fatalf("PR #7 stored wrongly: %+v", web)
	}
	if web.CIStatus != entity.CIStatusSuccess {
		t.Fatalf("PR #7 CI comes from GitHub's check runs, got %q", web.CIStatus)
	}
	if web.MergeableState != entity.MergeableStateUnknown {
		t.Fatalf("the PR list carries no merge state, so it must stay unknown, got %q", web.MergeableState)
	}
	if bump := get(8); !bump.IsDependabot || bump.CIStatus != entity.CIStatusNone {
		t.Fatalf("a Dependabot PR without check runs has no CI: %+v", bump)
	}
	if draft := get(9); !draft.IsDraft || draft.CIStatus != entity.CIStatusPending {
		t.Fatalf("a draft PR with a running check is pending: %+v", draft)
	}
	if run, err := checks.GetCheckRun(context.Background(), 71); err != nil || run.RepositoryID != repo.ID {
		t.Fatalf("check runs are kept for the repository so later webhooks converge: %+v, %v", run, err)
	}
	if len(events.events) != 1 || events.events[0].Type != "repository.followed" {
		t.Fatalf("backfill must not flood the timeline, only the follow is announced: %+v", events.events)
	}
}

func TestImport_BackfillRedeliverIsIdempotentAndKeepsLiveState(t *testing.T) {
	t.Parallel()
	uc, api, _, _, prs, checks := setupBackfill()
	twoOpenPRs(api)
	ctx := context.Background()

	repo, err := uc.Import(ctx, userID, 20, 200)
	if err != nil {
		t.Fatal(err)
	}
	// A webhook has since moved PR #7 on; re-importing must not rewind it to the older listing.
	live := prs.prs[[2]uint64{repo.ID, 7}]
	live.Title, live.HeadSHA, live.CIStatus = "feat: cart (renamed)", "sha7b", entity.CIStatusFailure
	prs.prs[[2]uint64{repo.ID, 7}] = live

	if _, err := uc.Import(ctx, userID, 20, 200); err != nil {
		t.Fatalf("second Import() error: %v", err)
	}
	if len(prs.prs) != 3 || len(checks.runs) != 2 {
		t.Fatalf("a repeated import changes nothing: %d PRs, %d check runs", len(prs.prs), len(checks.runs))
	}
	if got := prs.prs[[2]uint64{repo.ID, 7}]; got.Title != "feat: cart (renamed)" || got.HeadSHA != "sha7b" || got.CIStatus != entity.CIStatusFailure {
		t.Fatalf("a PR already kept live by webhooks must not be overwritten: %+v", got)
	}
}

func TestImport_BackfillNeverMovesAReportedCheckRunBackwards(t *testing.T) {
	t.Parallel()
	uc, api, repos, _, prs, checks := setupBackfill()
	twoOpenPRs(api)
	ctx := context.Background()
	// The repository exists already (e.g. unfollowed earlier) and a check_run webhook finished first.
	existing, _ := uc.Import(ctx, userID, 20, 200)
	_ = repos.SetUserFollow(ctx, userID, existing.ID, false)
	delete(prs.prs, [2]uint64{existing.ID, 7})
	checks.runs[71] = entity.CheckRun{ID: 71, RepositoryID: existing.ID, HeadSHA: "sha7", Name: "build", Status: "completed", Conclusion: "failure"}
	api.checkRuns["sha7"] = []entity.CheckRun{{ID: 71, HeadSHA: "sha7", Name: "build", Status: "in_progress"}}

	if _, err := uc.Import(ctx, userID, 20, 200); err != nil {
		t.Fatal(err)
	}
	if run := checks.runs[71]; run.Status != "completed" || run.Conclusion != "failure" {
		t.Fatalf("a run already completed must not go back to in_progress: %+v", run)
	}
	if got := prs.prs[[2]uint64{existing.ID, 7}]; got.CIStatus != entity.CIStatusFailure {
		t.Fatalf("CI is derived from the newest known runs, got %q", got.CIStatus)
	}
}

func TestImport_BackfillFailureLeavesTheRepositoryUnfollowedSoItCanBeRetried(t *testing.T) {
	t.Parallel()
	for name, fail := range map[string]func(*fakeUserAPI) error{
		"listing the PRs":      func(a *fakeUserAPI) error { a.prsErr = errors.New("github is down"); return a.prsErr },
		"reading check runs":   func(a *fakeUserAPI) error { a.checksErr = errors.New("github is down"); return a.checksErr },
		"a revoked user token": func(a *fakeUserAPI) error { a.prsErr = entity.ErrUnauthorized; return a.prsErr },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			uc, api, repos, events, _, _ := setupBackfill()
			twoOpenPRs(api)
			want := fail(api)

			if _, err := uc.Import(context.Background(), userID, 20, 200); !errors.Is(err, want) {
				t.Fatalf("expected %v, got %v", want, err)
			}
			for key, on := range repos.follows {
				if on {
					t.Fatalf("the follow is recorded only after the PRs were read, got %v", key)
				}
			}
			if len(events.events) != 0 {
				t.Fatalf("nothing is announced for a failed import: %+v", events.events)
			}

			api.prsErr, api.checksErr = nil, nil
			if _, err := uc.Import(context.Background(), userID, 20, 200); err != nil {
				t.Fatalf("retrying converges: %v", err)
			}
		})
	}
}

func TestImport_BackfillStopsWhenCanceled(t *testing.T) {
	t.Parallel()
	uc, api, repos, _, prs, _ := setupBackfill()
	twoOpenPRs(api)
	ctx, cancel := context.WithCancel(context.Background())
	api.onCheckRuns = cancel

	if _, err := uc.Import(ctx, userID, 20, 200); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(prs.prs) != 0 {
		t.Fatalf("no PR is stored after cancellation, got %d", len(prs.prs))
	}
	for key, on := range repos.follows {
		if on {
			t.Fatalf("a canceled import follows nothing, got %v", key)
		}
	}
}

func TestImport_RepositoryWithoutOpenPullRequestsStoresNone(t *testing.T) {
	t.Parallel()
	uc, _, _, _, prs, _ := setupBackfill()
	if _, err := uc.Import(context.Background(), userID, 20, 201); err != nil {
		t.Fatal(err)
	}
	if len(prs.prs) != 0 {
		t.Fatalf("nothing to backfill, got %d PRs", len(prs.prs))
	}
}
