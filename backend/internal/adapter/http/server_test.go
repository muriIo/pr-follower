package httpadapter_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/analyzer/blastradius"
	"github.com/murilo/pr-follower/backend/internal/adapter/crypto"
	httpadapter "github.com/murilo/pr-follower/backend/internal/adapter/http"
	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/server"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
	"github.com/murilo/pr-follower/backend/internal/usecase/auth"
	"github.com/murilo/pr-follower/backend/internal/usecase/changedfiles"
	"github.com/murilo/pr-follower/backend/internal/usecase/codeowners"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
	"github.com/murilo/pr-follower/backend/internal/usecase/prfiles"
	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
	"github.com/murilo/pr-follower/backend/internal/usecase/syncjob"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

// noopGHGateway stands in for GitHub's write API: it records who wrote what and can be told to
// answer with an error.
type noopGHGateway struct {
	mu     sync.Mutex
	writes []string // "approve:<token>:<pr number>", "merge:...", "comment:..."
	err    error
}

func (n *noopGHGateway) record(kind, token string, number int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.writes = append(n.writes, fmt.Sprintf("%s:%s:%d", kind, token, number))
	return n.err
}

func (n *noopGHGateway) written() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.writes...)
}

func (n *noopGHGateway) GetFileContent(_ context.Context, _ entity.Repository, _, _ string) ([]byte, error) {
	return nil, nil
}
func (n *noopGHGateway) ListPRChangedFiles(_ context.Context, _ entity.Repository, _ int) ([]entity.ChangedFile, error) {
	return []entity.ChangedFile{{Path: "package-lock.json"}}, nil
}
func (n *noopGHGateway) StreamRepoTarball(_ context.Context, _ entity.Repository, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (n *noopGHGateway) ApprovePullRequest(_ context.Context, token string, _ entity.Repository, number int, _ string) (entity.PullRequestReview, error) {
	if err := n.record("approve", token, number); err != nil {
		return entity.PullRequestReview{}, err
	}
	return entity.PullRequestReview{GitHubReviewID: int64(9000 + number), ReviewerLogin: "octocat", State: entity.ReviewApproved,
		CommitSHA: fmt.Sprintf("live-%d", number), SubmittedAt: time.Now().UTC()}, nil
}
func (n *noopGHGateway) MergePullRequest(_ context.Context, token string, _ entity.Repository, number int, _ string) error {
	return n.record("merge", token, number)
}
func (n *noopGHGateway) CreatePRComment(_ context.Context, token string, _ entity.Repository, number int, _ string) error {
	return n.record("comment", token, number)
}

// lowRiskAnalyzer reads every package-lock.json as a harmless dev-dependency patch bump.
type lowRiskAnalyzer struct{}

func (lowRiskAnalyzer) Ecosystem() string           { return "npm" }
func (lowRiskAnalyzer) CanAnalyze(name string) bool { return name == "package-lock.json" }
func (lowRiskAnalyzer) AnalyzeLockfile(_ context.Context, in port.DependencyAnalysisInput) (*entity.DependabotAnalysis, error) {
	return &entity.DependabotAnalysis{PullRequestID: in.PullRequest.ID, HeadSHA: in.PullRequest.HeadSHA, Ecosystem: "npm",
		PackageName: in.PackageName, FromVersion: in.FromVersion, ToVersion: in.ToVersion,
		SemverBumpType: entity.SemverBumpPatch, IsDirectDep: true, DepScope: entity.DependencyScopeDev}, nil
}

// fakeGitHubOAuth stands in for github.com: any code except "bad" signs in as octocat.
type fakeGitHubOAuth struct{}

func (fakeGitHubOAuth) AuthorizeURL(state, codeChallenge string) string {
	return "https://github.com/login/oauth/authorize?state=" + url.QueryEscape(state) + "&code_challenge=" + url.QueryEscape(codeChallenge)
}

func (fakeGitHubOAuth) ExchangeCode(_ context.Context, code, _ string) (entity.OAuthToken, error) {
	if code == "bad" {
		return entity.OAuthToken{}, io.ErrUnexpectedEOF
	}
	token := "ghu_test"
	if code == "other" {
		token = "ghu_other"
	}
	return entity.OAuthToken{AccessToken: token, RefreshToken: "ghr_test", AccessTokenExpiresAt: time.Now().Add(8 * time.Hour)}, nil
}

func (fakeGitHubOAuth) RefreshToken(context.Context, string) (entity.OAuthToken, error) {
	return entity.OAuthToken{AccessToken: "ghu_refreshed"}, nil
}

// FetchProfile: code "other" signs in as hubot, anything else as octocat.
func (fakeGitHubOAuth) FetchProfile(_ context.Context, token string) (entity.GitHubProfile, error) {
	if token == "ghu_other" {
		return entity.GitHubProfile{ID: 9919, Login: "hubot", Name: "Hubot"}, nil
	}
	return entity.GitHubProfile{ID: 583231, Login: "octocat", Name: "The Octocat", Teams: []string{"@acme/web"}}, nil
}

// fakeGitHubUserAPI: octocat reaches installation 20 (acme) with two repositories.
type fakeGitHubUserAPI struct {
	// ended says which PRs (by number) are no longer open on the fake GitHub: "merged" or "closed".
	ended *fakeEndedPRs
	// files, when set, is what the repositories' CODEOWNERS files hold right now (path by repo).
	files *fakeRepoFiles
}

// fakeEndedPRs is a mutable set of PRs that ended on the fake GitHub.
type fakeEndedPRs struct {
	mu sync.Mutex
	m  map[int]string
}

func (f *fakeEndedPRs) set(number int, how string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[number] = how
}

func (f *fakeEndedPRs) get(number int) string {
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[number]
}

// fakeRepoFiles is a mutable set of repository files, keyed "owner/name:path".
type fakeRepoFiles struct {
	mu    sync.Mutex
	files map[string]string
}

func (f *fakeRepoFiles) set(key, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[key] = content
}

func (f *fakeRepoFiles) remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, key)
}

func (f *fakeRepoFiles) get(key string) (string, bool) {
	if f == nil {
		return "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.files[key]
	return v, ok
}

func (fakeGitHubUserAPI) ListInstallations(_ context.Context, token string) ([]entity.Installation, error) {
	if token != "ghu_test" {
		return nil, entity.ErrUnauthorized
	}
	return []entity.Installation{{ID: 20, AccountLogin: "acme", AccountType: "Organization", RepositorySelection: "selected",
		SettingsURL: "https://github.com/organizations/acme/settings/installations/20"}}, nil
}

// ListPullRequestFiles: acme/web #7 changes two files and #42 one; no other PR is known.
func (fakeGitHubUserAPI) ListPullRequestFiles(_ context.Context, _ string, owner, name string, number int) ([]entity.ChangedFile, error) {
	if owner+"/"+name == "acme/web" && number == 42 {
		return []entity.ChangedFile{{Path: "src/cart.ts", Status: "modified", Additions: 5, Deletions: 2, Patch: "@@ -1 +1 @@"}}, nil
	}
	if owner+"/"+name != "acme/web" || number != 7 {
		return nil, entity.ErrNotFound
	}
	return []entity.ChangedFile{
		{Path: "src/app.ts", Status: "modified", Additions: 3, Deletions: 1, Patch: "@@ -1 +1 @@\n-a\n+b"},
		{Path: "assets/logo.png", Status: "added", Additions: 0, Deletions: 0},
	}, nil
}

// ListOpenPullRequests / ListCommitCheckRuns: the fake GitHub has nothing open to backfill.
func (f fakeGitHubUserAPI) ListOpenPullRequests(_ context.Context, _ string, owner, name string) ([]entity.RemotePullRequest, error) {
	if owner+"/"+name != "acme/web" || f.ended.get(42) != "" {
		return nil, nil
	}
	return []entity.RemotePullRequest{{GitHubPRID: 4200, Number: 42, Title: "feat: already open", AuthorLogin: "bia",
		HTMLURL: "https://github.com/acme/web/pull/42", HeadSHA: "sha42", BaseRef: "main"}}, nil
}

// GetPullRequest: every PR is open on GitHub, written by bianca-dev, at "live-<number>".
func (f fakeGitHubUserAPI) GetPullRequest(_ context.Context, _ string, _, _ string, number int) (entity.RemotePullRequestState, error) {
	if how := f.ended.get(number); how != "" {
		return entity.RemotePullRequestState{Open: false, Merged: how == "merged", HeadSHA: fmt.Sprintf("live-%d", number)}, nil
	}
	return entity.RemotePullRequestState{Open: true, HeadSHA: fmt.Sprintf("live-%d", number), AuthorLogin: "bianca-dev"}, nil
}

func (fakeGitHubUserAPI) ListCommitStatuses(context.Context, string, string, string, string) ([]entity.CommitStatus, error) {
	return nil, nil
}

func (fakeGitHubUserAPI) ListPullRequestReviews(context.Context, string, string, string, int) ([]entity.PullRequestReview, error) {
	return nil, nil
}

func (fakeGitHubUserAPI) ListCommitCheckRuns(_ context.Context, _ string, _, _, sha string) ([]entity.CheckRun, error) {
	if sha != "sha42" {
		return nil, nil
	}
	return []entity.CheckRun{{ID: 4201, HeadSHA: sha, Name: "build", Status: "completed", Conclusion: "success"}}, nil
}

// GetRepositoryFile: acme/web has CODEOWNERS, acme/api has none.
func (f fakeGitHubUserAPI) GetRepositoryFile(_ context.Context, _ string, owner, name, path string) ([]byte, error) {
	if content, ok := f.files.get(owner + "/" + name + ":" + path); ok {
		return []byte(content), nil
	}
	return nil, entity.ErrNotFound
}

func (fakeGitHubUserAPI) ListInstallationRepositories(_ context.Context, _ string, id int64) ([]entity.InstallationRepository, error) {
	if id != 20 {
		return nil, entity.ErrNotFound
	}
	return []entity.InstallationRepository{
		{GitHubRepoID: 900, InstallationID: 20, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "main", Private: true},
		{GitHubRepoID: 901, InstallationID: 20, Owner: "acme", Name: "api", FullName: "acme/api", DefaultBranch: "main"},
	}, nil
}

type testServer struct {
	router http.Handler
	opReg  *operation.Registry
	authUC *auth.UseCase
	repos  *persistence.RepositoryRepository
	prs    *persistence.PullRequestRepository
	gh     *noopGHGateway
	files  *fakeRepoFiles
	ended  *fakeEndedPRs
	syncs  *persistence.SyncJobStore
}

// do sends a request through the router, attaching the session cookie when given.
func (ts testServer) do(t *testing.T, method, target, body string, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if session != nil {
		req.AddCookie(session)
	}
	rec := httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// signIn runs the real GitHub login + callback round trip and returns the session cookie.
func (ts testServer) signIn(t *testing.T) *http.Cookie {
	t.Helper()
	return ts.signInAs(t, "ok")
}

// signInAs completes the GitHub callback with the given code (see fakeGitHubOAuth).
func (ts testServer) signInAs(t *testing.T, code string) *http.Cookie {
	t.Helper()
	start := ts.do(t, http.MethodGet, "/api/v1/auth/github/login", "", nil)
	if start.Code != http.StatusFound {
		t.Fatalf("expected 302 from login start, got %d: %s", start.Code, start.Body.String())
	}
	location, _ := url.Parse(start.Header().Get("Location"))
	state := location.Query().Get("state")
	flow := cookieNamed(start, "pf_oauth")
	if state == "" || flow == nil {
		t.Fatalf("login start must set state and the pf_oauth cookie, got %s", start.Header())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?code="+code+"&state="+url.QueryEscape(state), nil)
	req.AddCookie(flow)
	rec := httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("expected redirect to / after callback, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	session := cookieNamed(rec, "pf_session")
	if session == nil || session.Value == "" || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected HttpOnly SameSite=Lax session cookie, got %#v", session)
	}
	return session
}

func setupTestRouter(t *testing.T) testServer {
	t.Helper()
	return setupTestRouterWithOAuth(t, fakeGitHubOAuth{})
}

func setupTestRouterWithOAuth(t *testing.T, oauth fakeGitHubOAuth) testServer {
	t.Helper()
	db, err := database.Connect(context.Background(), database.Options{
		DSN:          "sqlite://:memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("database.Connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, _ := crypto.GenerateKey()
	cipher, err := crypto.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	userRepo := persistence.NewUserRepository(db, cipher)
	repoRepo := persistence.NewRepositoryRepository(db)
	prRepo := persistence.NewPullRequestRepository(db)
	sseHub := sse.NewInMemoryHub(t.Context())
	opReg := operation.NewRegistry()
	ghGw := &noopGHGateway{}
	repoFiles := &fakeRepoFiles{files: map[string]string{"acme/web:.github/CODEOWNERS": "* @octocat\n"}}
	ended := &fakeEndedPRs{m: map[int]string{}}
	userAPI := fakeGitHubUserAPI{files: repoFiles, ended: ended}

	authUC := auth.NewUseCase(auth.Deps{OAuth: oauth, Users: userRepo, Sessions: persistence.NewSessionRepository(db)})
	actionUC := action.NewUseCase(action.Deps{PRs: prRepo, Repos: repoRepo, Users: userRepo, Tokens: authUC,
		GitHub: ghGw, State: userAPI, Reviews: prRepo, Pub: sseHub})
	changedFilesUC := changedfiles.NewUseCase(authUC, userAPI, prRepo, repoRepo, nil)
	triageUC := triage.NewUseCase(userRepo, repoRepo, prRepo, sseHub, prRepo, changedFilesUC, prRepo, triage.WithCheckRuns(prRepo))
	analyzeUC := dependabot.NewAnalyzeUseCase(prRepo, repoRepo, ghGw, []port.DependencyAnalyzer{lowRiskAnalyzer{}}, blastradius.NewScanner(), sseHub)
	runner := dependabot.NewRunner(analyzeUC, dependabot.RunnerConfig{Workers: 1})
	runCtx, stopRunner := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runner.Run(runCtx); close(done) }()
	t.Cleanup(func() { stopRunner(); <-done })
	webhookUC := webhook.NewIngestUseCase(repoRepo, prRepo, prRepo, sseHub, changedFilesUC, webhook.WithAnalysisScheduler(runner))
	reviewUC := webhook.NewReviewIngestUseCase(repoRepo, prRepo, prRepo, sseHub)
	codeownersUC := codeowners.NewUseCase(repoRepo, userAPI, authUC, repoRepo, nil, sseHub)
	pushUC := webhook.NewPushIngestUseCase(repoRepo, codeownersUC)
	importUC := repoimport.NewUseCase(authUC, userAPI, repoRepo, prRepo, prRepo, userRepo, sseHub, changedFilesUC, prRepo)
	syncCtx, stopSync := context.WithCancel(context.Background())
	t.Cleanup(stopSync)
	syncStore := persistence.NewSyncJobStore(db)
	syncJobs := syncjob.NewManager(syncCtx, importUC, syncStore)
	installationUC := webhook.NewInstallationUseCase(repoRepo, sseHub)
	filesUC := prfiles.NewUseCase(authUC, userAPI, repoRepo, prRepo)

	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{
		UserRepo:       userRepo,
		RepoRepo:       repoRepo,
		PRRepo:         prRepo,
		TriageUC:       triageUC,
		ActionUC:       actionUC,
		AnalyzeUC:      analyzeUC,
		WebhookUC:      webhookUC,
		ReviewUC:       reviewUC,
		SyncJobs:       syncJobs,
		PushUC:         pushUC,
		SSEHub:         sseHub,
		OpRegistry:     opReg,
		WebhookSecret:  "test-secret",
		AuthUC:         authUC,
		ImportUC:       importUC,
		InstallURL:     "https://github.com/apps/pr-follower/installations/new",
		InstallationUC: installationUC,
		Follows:        repoRepo,
		FilesUC:        filesUC,
	})

	return testServer{router: server.NewChiRouter(handler, "*"), opReg: opReg, authUC: authUC, repos: repoRepo, prs: prRepo, gh: ghGw, files: repoFiles, ended: ended, syncs: syncStore}
}

func TestChiServer_SeedWorkspaceActionsWebhookAndCancel(t *testing.T) {
	t.Parallel()

	ts := setupTestRouter(t)
	router, opReg := ts.router, ts.opReg

	// 1. Healthz is public
	if rec := ts.do(t, http.MethodGet, "/healthz", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", rec.Code)
	}

	// 1.5 Sign in with GitHub, then read the session user
	session := ts.signIn(t)
	rec := ts.do(t, http.MethodGet, "/api/v1/auth/me", "", session)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"login":"octocat"`) {
		t.Fatalf("expected octocat from /api/v1/auth/me, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "ghu_test") {
		t.Fatal("GitHub tokens must never be returned to the browser")
	}

	// 2. Test fixture for the signed-in user
	ts.seedWorkspaceFixture(t, session)

	// 2.5 Import a repository reachable through the GitHub App
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":901}`, session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/repositories/import, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Get Workspace Snapshot
	rec = ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/workspace, got %d", rec.Code)
	}

	var wsResp struct {
		Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		Timeline []entity.ActivityEvent   `json:"timeline"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&wsResp); err != nil {
		t.Fatalf("decode workspace response: %v", err)
	}
	if len(wsResp.Snapshot.Seats) == 0 || len(wsResp.Snapshot.DependabotPRs) == 0 || len(wsResp.Snapshot.InboxWaitingOnMe) == 0 {
		t.Fatalf("expected populated seats, dependabot PRs, and inbox items after seed, got %#v", wsResp.Snapshot)
	}

	// 4. Snooze PR
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/1/snooze", `{"condition":"UNTIL_NEXT_COMMIT"}`, session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on snooze, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Claim PR
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/1/claim", "", session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on claim, got %d", rec.Code)
	}

	// 6. Approve, Merge, Dependabot Command & Batch Merge
	req := httptest.NewRequest(http.MethodPost, "/api/v1/prs/1/approve", bytes.NewBufferString(`{"body":"LGTM"}`))
	req.Header.Set("X-Operation-ID", "op-approve-1")
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on approve, got %d", rec.Code)
	}

	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/1/merge", `{"method":"squash"}`, session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on merge, got %d", rec.Code)
	}
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/2/dependabot-command", `{"command":"rebase"}`, session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on dependabot-command, got %d", rec.Code)
	}
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/batch-merge", `{"pr_ids":[2],"method":"squash"}`, session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on batch-merge, got %d", rec.Code)
	}

	// 7. Webhook Ingestion with HMAC Signature
	whPayload := []byte(`{
		"action": "opened",
		"number": 99,
		"pull_request": {
			"id": 88899,
			"number": 99,
			"title": "chore(deps): bump tailwindcss from 4.0.0 to 4.0.1",
			"state": "open",
			"draft": false,
			"html_url": "https://github.com/arvore/hive-web/pull/99",
			"user": {"login": "dependabot[bot]"},
			"head": {"sha": "sha-99"},
			"base": {"ref": "main"}
		},
		"repository": {
			"id": 5001,
			"full_name": "arvore/hive-web"
		}
	}`)
	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write(whPayload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req = httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(whPayload))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", sig)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted on webhook, got %d: %s", rec.Code, rec.Body.String())
	}

	// 8. Cancel active operation returns 200; missing, or another user's, returns 404
	_, release := opReg.Begin(context.Background(), 1, "op-active-55") // the first user signed in
	defer release()
	_, releaseOther := opReg.Begin(context.Background(), 2, "op-someone-elses")
	defer releaseOther()
	if rec := ts.do(t, http.MethodDelete, "/api/v1/operations/op-someone-elses", "", session); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 cancelling another user's operation, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/operations/op-active-55", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on active operation cancel, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/operations/op-missing", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on missing operation cancel, got %d", rec.Code)
	}
}

// echoBus delivers every broadcast back to its subscribers, like Postgres NOTIFY to all replicas.
type echoBus struct{ sent chan string }

func (b *echoBus) Broadcast(_ context.Context, message string) error {
	b.sent <- message
	return nil
}

func (b *echoBus) Subscribe(context.Context) <-chan string { return make(chan string) }

func TestCancelOperation_AsksEveryReplicaWhenThisOneDoesNotRunIt(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	bus := &echoBus{sent: make(chan string, 1)}
	ts.opReg.Connect(t.Context(), bus)

	rec := ts.do(t, http.MethodDelete, "/api/v1/operations/op-elsewhere", "", session)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"cancel_requested":true`) {
		t.Fatalf("expected 202 with cancel_requested, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := <-bus.sent; got != "1:op-elsewhere" {
		t.Fatalf("broadcast %q, want the user's own operation", got)
	}
}

func TestAuth_ProtectedRoutesRequireSession(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/workspace"},
		{http.MethodGet, "/api/v1/events/stream"},
		{http.MethodPost, "/api/v1/repositories/import"},
		{http.MethodPost, "/api/v1/prs/1/approve"},
		{http.MethodDelete, "/api/v1/operations/x"},
	} {
		if rec := ts.do(t, route.method, route.path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without session: expected 401, got %d", route.method, route.path, rec.Code)
		}
	}

	forged := &http.Cookie{Name: "pf_session", Value: "forged"}
	if rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("forged session: expected 401, got %d", rec.Code)
	}
}

func TestAuth_LogoutEndsSession(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)

	rec := ts.do(t, http.MethodPost, "/api/v1/auth/logout", "", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on logout, got %d", rec.Code)
	}
	cleared := cookieNamed(rec, "pf_session")
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("expected logout to expire the session cookie, got %#v", cleared)
	}
	if rec := ts.do(t, http.MethodGet, "/api/v1/auth/me", "", session); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", rec.Code)
	}
	if rec := ts.do(t, http.MethodPost, "/api/v1/auth/logout", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("logout without a session must still succeed, got %d", rec.Code)
	}
}

func TestAuth_CallbackRejectsForgeryAndFailures(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)

	start := ts.do(t, http.MethodGet, "/api/v1/auth/github/login", "", nil)
	flow := cookieNamed(start, "pf_oauth")
	location, _ := url.Parse(start.Header().Get("Location"))
	state := location.Query().Get("state")

	callback := func(query string, withFlow bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?"+query, nil)
		if withFlow {
			req.AddCookie(flow)
		}
		rec := httptest.NewRecorder()
		ts.router.ServeHTTP(rec, req)
		return rec
	}

	cases := []struct {
		name, query string
		withFlow    bool
		wantError   string
	}{
		{"missing flow cookie", "code=ok&state=" + url.QueryEscape(state), false, "state_mismatch"},
		{"state mismatch", "code=ok&state=other", true, "state_mismatch"},
		{"user denied access", "error=access_denied&state=" + url.QueryEscape(state), true, "access_denied"},
		{"code exchange fails", "code=bad&state=" + url.QueryEscape(state), true, "login_failed"},
	}
	for _, tc := range cases {
		rec := callback(tc.query, tc.withFlow)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error="+tc.wantError {
			t.Errorf("%s: expected redirect to /?auth_error=%s, got %d %q", tc.name, tc.wantError, rec.Code, rec.Header().Get("Location"))
		}
		if cookieNamed(rec, "pf_session") != nil {
			t.Errorf("%s: no session may be issued", tc.name)
		}
	}
}

func TestAuth_LoginWithoutGitHubConfiguration(t *testing.T) {
	t.Parallel()
	router := server.NewChiRouter(httpadapter.NewHandler(httpadapter.HandlerDeps{}), "*")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/login", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error=not_configured" {
		t.Fatalf("expected redirect to /?auth_error=not_configured, got %d %q", rec.Code, rec.Header().Get("Location"))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspace", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth configured, got %d", rec.Code)
	}
}

func TestRepositoryImport_ListImportAndUnfollow(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)

	rec := ts.do(t, http.MethodGet, "/api/v1/github/installations", "", session)
	var accounts struct {
		Accounts   []entity.Installation `json:"accounts"`
		InstallURL string                `json:"install_url"`
	}
	if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&accounts) != nil {
		t.Fatalf("installations: %d %s", rec.Code, rec.Body.String())
	}
	if len(accounts.Accounts) != 1 || accounts.Accounts[0].AccountLogin != "acme" ||
		accounts.InstallURL != "https://github.com/apps/pr-follower/installations/new" {
		t.Fatalf("unexpected accounts payload: %+v", accounts)
	}

	listRepos := func() []repoimport.ImportableRepository {
		t.Helper()
		rec := ts.do(t, http.MethodGet, "/api/v1/github/installations/20/repositories", "", session)
		var body struct {
			Repositories []repoimport.ImportableRepository `json:"repositories"`
		}
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("repositories: %d %s", rec.Code, rec.Body.String())
		}
		return body.Repositories
	}
	if repos := listRepos(); len(repos) != 2 || repos[0].Followed || repos[1].Followed {
		t.Fatalf("expected two unfollowed repositories, got %+v", repos)
	}

	rec = ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session)
	var imported struct {
		Repository entity.Repository `json:"repository"`
	}
	if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&imported) != nil || imported.Repository.FullName != "acme/web" {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	repos := listRepos()
	if !repos[0].Followed || repos[0].RepositoryID != imported.Repository.ID {
		t.Fatalf("expected acme/web followed, got %+v", repos[0])
	}

	ws := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	if !strings.Contains(ws.Body.String(), "acme/web") {
		t.Fatalf("expected imported repository among workspace seats: %s", ws.Body.String())
	}

	target := "/api/v1/repositories/" + strconv.FormatUint(imported.Repository.ID, 10) + "/follow"
	if rec := ts.do(t, http.MethodDelete, target, "", session); rec.Code != http.StatusOK {
		t.Fatalf("unfollow: %d %s", rec.Code, rec.Body.String())
	}
	if repos := listRepos(); repos[0].Followed {
		t.Fatalf("expected acme/web unfollowed, got %+v", repos[0])
	}
}

func TestRepositoryImport_RejectsUnreachableAndInvalidInput(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"repository outside the installation", http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":12345}`, http.StatusNotFound},
		{"installation the user cannot reach", http.MethodPost, "/api/v1/repositories/import", `{"installation_id":99,"github_repo_id":900}`, http.StatusNotFound},
		{"malformed body", http.MethodPost, "/api/v1/repositories/import", `{`, http.StatusBadRequest},
		{"unknown installation listing", http.MethodGet, "/api/v1/github/installations/99/repositories", "", http.StatusNotFound},
		{"non-numeric installation", http.MethodGet, "/api/v1/github/installations/abc/repositories", "", http.StatusBadRequest},
		{"non-numeric repository", http.MethodDelete, "/api/v1/repositories/abc/follow", "", http.StatusBadRequest},
		{"free-form follow is gone", http.MethodPost, "/api/v1/repositories/follow", `{"owner":"x","name":"y"}`, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		rec := ts.do(t, tc.method, tc.path, tc.body, session)
		if tc.name == "free-form follow is gone" {
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s: expected 404/405, got %d", tc.name, rec.Code)
			}
			continue
		}
		if rec.Code != tc.want {
			t.Errorf("%s: expected %d, got %d: %s", tc.name, tc.want, rec.Code, rec.Body.String())
		}
	}

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/github/installations"},
		{http.MethodGet, "/api/v1/github/installations/20/repositories"},
		{http.MethodPost, "/api/v1/repositories/import"},
		{http.MethodDelete, "/api/v1/repositories/1/follow"},
	} {
		if rec := ts.do(t, route.method, route.path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without session: expected 401, got %d", route.method, route.path, rec.Code)
		}
	}
}

func TestRepositoryImport_NotConfigured(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)

	// Same signed-in user, but a handler built without the import use case.
	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{AuthUC: ts.authUC})
	router := server.NewChiRouter(handler, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/github/installations", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"unavailable"`) {
		t.Fatalf("expected 503 with code unavailable when GitHub import is not configured, got %d %s", rec.Code, rec.Body.String())
	}
}

// postWebhook sends a GitHub webhook signed with the test secret.
func (ts testServer) postWebhook(t *testing.T, event, payload string) *httptest.ResponseRecorder {
	t.Helper()
	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(payload))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewBufferString(payload))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	return rec
}

func seatAccess(t *testing.T, ts testServer, session *http.Cookie, fullName string) string {
	t.Helper()
	rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	var body struct {
		Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	for _, seat := range body.Snapshot.Seats {
		if seat.Repository.FullName == fullName {
			return string(seat.Repository.Access)
		}
	}
	t.Fatalf("seat %s not found in workspace", fullName)
	return ""
}

func TestInstallationWebhooks_FlagAndRestoreRepositoryAccess(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)

	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if got := seatAccess(t, ts, session, "acme/web"); got != "active" {
		t.Fatalf("imported repository starts active, got %q", got)
	}

	if rec := ts.postWebhook(t, "installation", `{"action":"deleted","installation":{"id":20,"account":{"login":"acme"}}}`); rec.Code != http.StatusAccepted {
		t.Fatalf("installation webhook: %d %s", rec.Code, rec.Body.String())
	}
	if got := seatAccess(t, ts, session, "acme/web"); got != "removed" {
		t.Fatalf("after uninstall expected removed, got %q", got)
	}

	payload := `{"action":"added","installation":{"id":21},"repositories_added":[{"id":900,"full_name":"acme/web"}],"repositories_removed":[]}`
	if rec := ts.postWebhook(t, "installation_repositories", payload); rec.Code != http.StatusAccepted {
		t.Fatalf("installation_repositories webhook: %d %s", rec.Code, rec.Body.String())
	}
	if got := seatAccess(t, ts, session, "acme/web"); got != "active" {
		t.Fatalf("after re-adding expected active, got %q", got)
	}

	if rec := ts.postWebhook(t, "installation", `{"action":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed installation payload: expected 400, got %d", rec.Code)
	}
	if rec := ts.postWebhook(t, "installation_repositories", `[`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed installation_repositories payload: expected 400, got %d", rec.Code)
	}
}

// openStream connects to the live event stream with a session and returns the JSON payloads
// of its "activity" frames. It returns once the stream is open (and therefore subscribed).
func openStream(t *testing.T, baseURL string, session *http.Cookie) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/events/stream", nil)
	req.AddCookie(session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("open stream: %v %v", resp, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	reader := bufio.NewReader(resp.Body)
	if first, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(first, "retry:") {
		t.Fatalf("expected the stream to open with a retry hint, got %q, %v", first, err)
	}
	out := make(chan string, 16)
	go func() {
		defer close(out)
		named := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "event: activity":
				named = true
			case strings.HasPrefix(line, "data: ") && named:
				out <- strings.TrimPrefix(line, "data: ")
				named = false
			}
		}
	}()
	return out
}

func nextEvent(t *testing.T, events <-chan string) string {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a live event")
		return ""
	}
}

func TestLiveEvents_OnlyReachUsersWhoFollowTheRepository(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	octocat := ts.signIn(t)
	hubot := ts.signInAs(t, "other")

	for _, body := range []string{`{"installation_id":20,"github_repo_id":900}`, `{"installation_id":20,"github_repo_id":901}`} {
		if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", body, octocat); rec.Code != http.StatusOK {
			t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
		}
	}
	var me struct {
		User entity.User `json:"user"`
	}
	_ = json.NewDecoder(ts.do(t, http.MethodGet, "/api/v1/auth/me", "", hubot).Body).Decode(&me)
	api, _ := ts.repos.GetByGitHubRepoID(context.Background(), 901)
	if err := ts.repos.SetUserFollow(context.Background(), me.User.ID, api.ID, true); err != nil {
		t.Fatalf("follow: %v", err)
	}

	srv := httptest.NewServer(ts.router)
	t.Cleanup(srv.Close) // runs after the streams' own cleanups cancel them
	octocatEvents := openStream(t, srv.URL, octocat)
	hubotEvents := openStream(t, srv.URL, hubot)

	prOpened := func(repoID, number int) string {
		return fmt.Sprintf(`{"action":"opened","number":%d,"pull_request":{"id":%d,"number":%d,"title":"t","state":"open",`+
			`"user":{"login":"bianca"},"head":{"sha":"s"},"base":{"ref":"main"}},"repository":{"id":%d}}`, number, 7000+number, number, repoID)
	}
	for _, payload := range []string{prOpened(900, 1), prOpened(901, 2)} {
		if rec := ts.postWebhook(t, "pull_request", payload); rec.Code != http.StatusAccepted {
			t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
		}
	}

	first, second := nextEvent(t, octocatEvents), nextEvent(t, octocatEvents)
	if !strings.Contains(first, `"type":"pr.opened"`) || !strings.Contains(first, "acme/web") || !strings.Contains(second, "acme/api") {
		t.Fatalf("octocat follows both repositories and must see both PRs:\n%s\n%s", first, second)
	}
	if got := nextEvent(t, hubotEvents); !strings.Contains(got, "acme/api") {
		t.Fatalf("hubot follows only acme/api; acme/web activity leaked:\n%s", got)
	}
}

// seedWorkspaceFixture writes test repositories, PRs, analyses and turn state for the signed-in
// user straight through persistence (test data only; production has no demo endpoint).
func (ts testServer) seedWorkspaceFixture(t *testing.T, session *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	var me struct {
		User entity.User `json:"user"`
	}
	if err := json.NewDecoder(ts.do(t, http.MethodGet, "/api/v1/auth/me", "", session).Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	user := &me.User

	repos := []entity.Repository{
		{
			GitHubRepoID:     5001,
			Owner:            "arvore",
			Name:             "hive-web",
			FullName:         "arvore/hive-web",
			DefaultBranch:    "main",
			CodeownersStatus: entity.CodeownersPresent,
			CachedCodeownersRules: []entity.CodeownerRule{
				{Pattern: "/src/app/", Owners: []string{"@" + user.Login, "@arvore/frontend"}},
			},
		},
		{
			GitHubRepoID:     5002,
			Owner:            "arvore",
			Name:             "checkout-api",
			FullName:         "arvore/checkout-api",
			DefaultBranch:    "main",
			CodeownersStatus: entity.CodeownersPresent,
			CachedCodeownersRules: []entity.CodeownerRule{
				{Pattern: "*", Owners: []string{"@arvore/core-architects"}},
			},
		},
	}

	for i := range repos {
		if err := ts.repos.Upsert(ctx, &repos[i]); err != nil {
			t.Fatalf("seed repository: %v", err)
		}
		_ = ts.repos.SetUserFollow(ctx, user.ID, repos[i].ID, true)
	}

	pr1 := &entity.PullRequest{
		RepositoryID:   repos[0].ID,
		RepositoryName: repos[0].FullName,
		GitHubPRID:     80001,
		Number:         142,
		Title:          "feat(seats): add real-time CI status pulse to repo tiles",
		AuthorLogin:    "bianca-dev",
		HTMLURL:        "https://github.com/arvore/hive-web/pull/142",
		IsDependabot:   false,
		IsDraft:        false,
		State:          entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusSuccess,
		HeadSHA:        "7f8a9b1",
		BaseRef:        "main",
	}
	_ = ts.prs.Upsert(ctx, pr1)
	// The turn is derived: PR #142 changes a file the signed-in user owns.
	_ = ts.prs.SaveChangedFiles(ctx, entity.PRChangedFiles{PullRequestID: pr1.ID, HeadSHA: pr1.HeadSHA, Files: []entity.ChangedFile{
		{Path: "src/app/features/seats-grid/seat-card.component.ts", Additions: 24, Deletions: 3},
	}})

	pr2 := &entity.PullRequest{
		RepositoryID:   repos[0].ID,
		RepositoryName: repos[0].FullName,
		GitHubPRID:     80002,
		Number:         145,
		Title:          "chore(deps-dev): bump vitest from 2.1.1 to 2.1.4",
		AuthorLogin:    "dependabot[bot]",
		HTMLURL:        "https://github.com/arvore/hive-web/pull/145",
		IsDependabot:   true,
		State:          entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusSuccess,
		HeadSHA:        "9c0d1e2",
		BaseRef:        "main",
	}
	_ = ts.prs.Upsert(ctx, pr2)
	_ = ts.prs.SaveAnalysis(ctx, &entity.DependabotAnalysis{
		PullRequestID:     pr2.ID,
		HeadSHA:           pr2.HeadSHA,
		Ecosystem:         "npm",
		PackageName:       "vitest",
		FromVersion:       "2.1.1",
		ToVersion:         "2.1.4",
		SemverBumpType:    entity.SemverBumpPatch,
		IsDirectDep:       true,
		DepScope:          entity.DependencyScopeDev,
		RiskVerdict:       entity.RiskVerdictLow,
		RiskScore:         12,
		VerdictSummaryKey: "dependabot.verdict.low.dev_safe",
		DependencyPathGraph: []entity.DependencyNode{
			{Name: "hive-web", Version: "root", Scope: entity.DependencyScopeProd},
			{Name: "vitest", Version: "2.1.4", Scope: entity.DependencyScopeDev},
		},
	})

	pr3 := &entity.PullRequest{
		RepositoryID:   repos[1].ID,
		RepositoryName: repos[1].FullName,
		GitHubPRID:     80003,
		Number:         89,
		Title:          "chore(deps): bump axios from 1.7.7 to 2.0.0",
		AuthorLogin:    "dependabot[bot]",
		HTMLURL:        "https://github.com/arvore/checkout-api/pull/89",
		IsDependabot:   true,
		State:          entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusSuccess,
		HeadSHA:        "3a4b5c6",
		BaseRef:        "main",
	}
	_ = ts.prs.Upsert(ctx, pr3)
	_ = ts.prs.SaveAnalysis(ctx, &entity.DependabotAnalysis{
		PullRequestID:     pr3.ID,
		HeadSHA:           pr3.HeadSHA,
		Ecosystem:         "npm",
		PackageName:       "axios",
		FromVersion:       "1.7.7",
		ToVersion:         "2.0.0",
		SemverBumpType:    entity.SemverBumpMajor,
		IsDirectDep:       true,
		DepScope:          entity.DependencyScopeProd,
		RiskVerdict:       entity.RiskVerdictHigh,
		RiskScore:         88,
		VerdictSummaryKey: "dependabot.verdict.high.direct_prod_major",
		DependencyPathGraph: []entity.DependencyNode{
			{Name: "checkout-api", Version: "root", Scope: entity.DependencyScopeProd},
			{Name: "axios", Version: "2.0.0", Scope: entity.DependencyScopeProd},
		},
		BlastRadiusUsages: []entity.BlastRadiusUsage{
			{
				FilePath:    "src/infra/payment-gateway.client.ts",
				LineNumber:  4,
				ImportStmt:  "import axios, { AxiosInstance } from 'axios';",
				CodeSnippet: "import { Injectable } from '@nestjs/common';\nimport axios, { AxiosInstance } from 'axios';\n\n@Injectable()",
			},
		},
		AdvisoriesAndNotes: []entity.AdvisoryAndNote{
			{
				Severity:       "HIGH",
				Summary:        "Major v2.0.0 drops legacy CommonJS default export behavior",
				IsBreakingNote: true,
			},
		},
	})
}

func TestDemoSeedEndpointIsGone(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/demo/seed", "", session); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/v1/demo/seed must not exist in production, got %d", rec.Code)
	}
}

func seatPR(t *testing.T, ts testServer, session *http.Cookie, fullName string, number int) (entity.Repository, entity.PullRequest) {
	t.Helper()
	rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	var body struct {
		Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	for _, seat := range body.Snapshot.Seats {
		if seat.Repository.FullName != fullName {
			continue
		}
		for _, pr := range seat.PullRequests {
			if pr.Number == number {
				return seat.Repository, pr
			}
		}
	}
	t.Fatalf("PR %s#%d not found in workspace", fullName, number)
	return entity.Repository{}, entity.PullRequest{}
}

func TestPRState_ReflectsGitHubForRepositoriesWithoutCIOrCodeowners(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	for _, id := range []int{900, 901} {
		if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", fmt.Sprintf(`{"installation_id":20,"github_repo_id":%d}`, id), session); rec.Code != http.StatusOK {
			t.Fatalf("import %d: %d %s", id, rec.Code, rec.Body.String())
		}
	}

	pr := `{"action":"opened","number":11,"pull_request":{"id":1111,"number":11,"title":"chore: updating README","state":"open",` +
		`"mergeable_state":"blocked","user":{"login":"murilo"},"head":{"sha":"h-11"},"base":{"ref":"main"}},"repository":{"id":901}}`
	if rec := ts.postWebhook(t, "pull_request", pr); rec.Code != http.StatusAccepted {
		t.Fatalf("pull_request webhook: %d %s", rec.Code, rec.Body.String())
	}

	repo, got := seatPR(t, ts, session, "acme/api", 11)
	if repo.CodeownersStatus != entity.CodeownersAbsent {
		t.Errorf("acme/api has no CODEOWNERS: got %q", repo.CodeownersStatus)
	}
	if got.CIStatus != entity.CIStatusNone || got.MergeableState != entity.MergeableStateBlocked {
		t.Errorf("no CI reported and GitHub says blocked: got CI %q, merge %q", got.CIStatus, got.MergeableState)
	}
	if web, _ := ts.repos.GetByGitHubRepoID(context.Background(), 900); web.CodeownersStatus != entity.CodeownersPresent {
		t.Errorf("acme/web has CODEOWNERS: got %q", web.CodeownersStatus)
	}

	checkRun := func(status, conclusion string) string {
		return fmt.Sprintf(`{"action":"x","check_run":{"id":555,"name":"test","head_sha":"h-11","status":%q,"conclusion":%q},"repository":{"id":901}}`, status, conclusion)
	}
	if rec := ts.postWebhook(t, "check_run", checkRun("in_progress", "")); rec.Code != http.StatusAccepted {
		t.Fatalf("check_run webhook: %d %s", rec.Code, rec.Body.String())
	}
	if _, got := seatPR(t, ts, session, "acme/api", 11); got.CIStatus != entity.CIStatusPending {
		t.Fatalf("a running check: expected PENDING, got %q", got.CIStatus)
	}
	_ = ts.postWebhook(t, "check_run", checkRun("completed", "success"))
	if _, got := seatPR(t, ts, session, "acme/api", 11); got.CIStatus != entity.CIStatusSuccess {
		t.Fatalf("a passed check: expected SUCCESS, got %q", got.CIStatus)
	}
	if rec := ts.postWebhook(t, "check_run", `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed check_run: expected 400, got %d", rec.Code)
	}
}

func TestPullRequestFiles_ListsChangedFilesWithOwnership(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}

	rec := ts.do(t, http.MethodGet, "/api/v1/repositories/acme/web/pulls/7/files", "", session)
	var body struct {
		Files []prfiles.File `json:"files"`
	}
	if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
		t.Fatalf("files: %d %s", rec.Code, rec.Body.String())
	}
	if len(body.Files) != 2 || body.Files[0].Path != "src/app.ts" || body.Files[0].Patch == "" || body.Files[1].Patch != "" {
		t.Fatalf("unexpected files: %+v", body.Files)
	}
	// acme/web's CODEOWNERS ("* @octocat") makes octocat the owner of every file.
	if !body.Files[0].Owned || !body.Files[1].Owned || body.Files[1].Status != "added" {
		t.Fatalf("expected ownership and status from GitHub: %+v", body.Files)
	}

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/repositories/acme/api/pulls/7/files", http.StatusNotFound},  // not followed
		{"/api/v1/repositories/acme/web/pulls/99/files", http.StatusNotFound}, // unknown PR
		{"/api/v1/repositories/acme/web/pulls/abc/files", http.StatusBadRequest},
	} {
		if rec := ts.do(t, http.MethodGet, tc.path, "", session); rec.Code != tc.want {
			t.Errorf("%s: expected %d, got %d", tc.path, tc.want, rec.Code)
		}
	}
	if rec := ts.do(t, http.MethodGet, "/api/v1/repositories/acme/web/pulls/7/files", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without session: expected 401, got %d", rec.Code)
	}

	unconfigured := server.NewChiRouter(httpadapter.NewHandler(httpadapter.HandlerDeps{AuthUC: ts.authUC}), "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/repositories/acme/web/pulls/7/files", nil)
	req.AddCookie(session)
	rr := httptest.NewRecorder()
	unconfigured.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"code":"unavailable"`) {
		t.Errorf("without the files use case: expected 503 with code unavailable, got %d %s", rr.Code, rr.Body.String())
	}
}

func workspaceTimeline(t *testing.T, ts testServer, session *http.Cookie) []entity.ActivityEvent {
	t.Helper()
	rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	var body struct {
		Timeline []entity.ActivityEvent `json:"timeline"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	return body.Timeline
}

func TestWorkspaceTimeline_OnlyFollowedRepositoriesNewestFirst(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	octocat := ts.signIn(t)
	hubot := ts.signInAs(t, "other")
	for _, body := range []string{`{"installation_id":20,"github_repo_id":900}`, `{"installation_id":20,"github_repo_id":901}`} {
		if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", body, octocat); rec.Code != http.StatusOK {
			t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
		}
	}
	var me struct {
		User entity.User `json:"user"`
	}
	_ = json.NewDecoder(ts.do(t, http.MethodGet, "/api/v1/auth/me", "", hubot).Body).Decode(&me)
	api, _ := ts.repos.GetByGitHubRepoID(context.Background(), 901)
	_ = ts.repos.SetUserFollow(context.Background(), me.User.ID, api.ID, true)

	opened := func(repoID, number int, sender string) string {
		return fmt.Sprintf(`{"action":"opened","number":%d,"pull_request":{"id":%d,"number":%d,"title":"t","state":"open",`+
			`"user":{"login":"bianca"},"head":{"sha":"s"},"base":{"ref":"main"}},"repository":{"id":%d},"sender":{"login":%q}}`,
			number, 8000+number, number, repoID, sender)
	}
	_ = ts.postWebhook(t, "pull_request", opened(900, 1, "bianca"))
	_ = ts.postWebhook(t, "pull_request", opened(901, 2, "octocat"))

	for _, ev := range workspaceTimeline(t, ts, hubot) {
		if ev.RepositoryName == "acme/web" {
			t.Fatalf("hubot does not follow acme/web but its activity leaked: %+v", ev)
		}
	}

	timeline := workspaceTimeline(t, ts, octocat)
	var prEvents []entity.ActivityEvent
	for _, ev := range timeline {
		if ev.Type == "pr.opened" {
			prEvents = append(prEvents, ev)
		}
	}
	if len(prEvents) != 2 || prEvents[0].PRNumber != 2 || prEvents[1].PRNumber != 1 {
		t.Fatalf("expected newest first (#2 then #1), got %+v", prEvents)
	}
	if prEvents[0].ActorLogin != "octocat" || prEvents[1].ActorLogin != "bianca" {
		t.Fatalf("the actor is the webhook sender: %+v", prEvents)
	}
}

func TestRepositoryImport_ShowsAlreadyOpenPullRequestsWithoutAWebhook(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}

	_, pr := seatPR(t, ts, session, "acme/web", 42)
	if pr.Title != "feat: already open" || pr.State != entity.PRStateOpen || pr.CIStatus != entity.CIStatusSuccess ||
		pr.MergeableState != entity.MergeableStateUnknown || pr.HeadSHA != "sha42" {
		t.Fatalf("the PR open at import time must appear with GitHub's CI and an honest unknown merge state: %+v", pr)
	}
}

func TestTurnEngine_MinhaVezHoldsOnlyPRsTouchingMyFiles(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	octocat, hubot := ts.signIn(t), ts.signInAs(t, "other")
	for _, session := range []*http.Cookie{octocat, hubot} {
		if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
			t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
		}
	}
	workspace := func(session *http.Cookie) triage.WorkspaceSnapshot {
		t.Helper()
		var body struct {
			Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		}
		rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("workspace: %d", rec.Code)
		}
		return body.Snapshot
	}
	numbers := func(items []triage.InboxItem) []int {
		out := []int{}
		for _, it := range items {
			out = append(out, it.PullRequest.Number)
		}
		sort.Ints(out)
		return out
	}

	// acme/web's CODEOWNERS is "* @octocat": the PR open at import time is octocat's turn only.
	mine := workspace(octocat)
	if got := numbers(mine.InboxWaitingOnMe); len(got) != 1 || got[0] != 42 {
		t.Fatalf("octocat's turn = %v, want [42]", got)
	}
	owned := mine.InboxWaitingOnMe[0].UserState.OwnedFilesChanged
	if len(owned) != 1 || owned[0].Path != "src/cart.ts" || owned[0].Patch != "" {
		t.Fatalf("octocat's files in the PR, without patches: %+v", owned)
	}
	theirs := workspace(hubot)
	if len(theirs.InboxWaitingOnMe) != 0 || len(theirs.InboxDimmed) != 0 || len(theirs.OwnershipPendingPRIDs) != 0 {
		t.Fatalf("hubot owns nothing there: the PR must not be in hubot's inbox: %+v", theirs)
	}
	if len(theirs.Seats) != 1 || theirs.Seats[0].OpenPRCount != 1 || theirs.Seats[0].WaitingOnMeCount != 0 {
		t.Fatalf("hubot still sees the PR in the seat: %+v", theirs.Seats)
	}

	// A PR opened later: the webhook reads its files with a follower's token.
	opened := func(number int) string {
		return fmt.Sprintf(`{"action":"opened","number":%d,"pull_request":{"id":%d,"number":%d,"title":"t","state":"open",`+
			`"user":{"login":"bianca"},"head":{"sha":"s%d"},"base":{"ref":"main"}},"repository":{"id":900},"sender":{"login":"bianca"}}`,
			number, 8000+number, number, number)
	}
	if rec := ts.postWebhook(t, "pull_request", opened(7)); rec.Code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
	}
	if got := numbers(workspace(octocat).InboxWaitingOnMe); len(got) != 2 || got[0] != 7 {
		t.Fatalf("octocat's turn = %v, want [7 42]", got)
	}

	// A PR whose files GitHub cannot give us is never shown as a turn: it stays pending.
	if rec := ts.postWebhook(t, "pull_request", opened(8)); rec.Code != http.StatusAccepted {
		t.Fatalf("a failed files read must not lose the webhook: %d", rec.Code)
	}
	after := workspace(octocat)
	if got := numbers(after.InboxWaitingOnMe); len(got) != 2 {
		t.Fatalf("an unevaluated PR is not a turn: %v", got)
	}
	if len(after.OwnershipPendingPRIDs) != 1 || after.Seats[0].OpenPRCount != 3 {
		t.Fatalf("expected one pending PR among three open: pending=%v seat=%+v", after.OwnershipPendingPRIDs, after.Seats[0])
	}
}

func TestTurnEngine_ReviewWebhooksMovePRsAwayFromMinhaVez(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	octocat := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, octocat); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	workspace := func() triage.WorkspaceSnapshot {
		t.Helper()
		var body struct {
			Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		}
		rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", octocat)
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("workspace: %d", rec.Code)
		}
		return body.Snapshot
	}
	review := func(id int, login, state, sha string) string {
		return fmt.Sprintf(`{"action":"submitted","review":{"id":%d,"user":{"login":%q},"state":%q,"commit_id":%q,"submitted_at":"2026-11-10T09:00:00Z"},`+
			`"pull_request":{"number":42},"repository":{"id":900},"sender":{"login":%q}}`, id, login, state, sha, login)
	}
	if got := workspace(); len(got.InboxWaitingOnMe) != 1 || len(got.InboxDimmed) != 0 {
		t.Fatalf("PR 42 starts as octocat's turn: %+v", got)
	}

	// A teammate approves the head commit: it is no longer octocat's move.
	if rec := ts.postWebhook(t, "pull_request_review", review(1, "bob", "approved", "sha42")); rec.Code != http.StatusAccepted {
		t.Fatalf("review webhook: %d %s", rec.Code, rec.Body.String())
	}
	got := workspace()
	if len(got.InboxWaitingOnMe) != 0 || len(got.InboxDimmed) != 1 || got.InboxDimmed[0].UserState.TurnState != entity.TurnStateTeammateApproved {
		t.Fatalf("a teammate's approval dims the PR: %+v", got)
	}
	if rec := ts.postWebhook(t, "pull_request_review", review(1, "bob", "approved", "sha42")); rec.Code != http.StatusAccepted {
		t.Fatalf("redelivery: %d", rec.Code)
	}
	if got := workspace(); len(got.InboxDimmed) != 1 {
		t.Fatalf("redelivery changes nothing: %+v", got)
	}

	// The reviewer's approval is dismissed: the PR is octocat's turn again.
	dismissed := `{"action":"dismissed","review":{"id":1,"user":{"login":"bob"},"state":"dismissed","commit_id":"sha42","submitted_at":"2026-11-10T09:00:00Z"},` +
		`"pull_request":{"number":42},"repository":{"id":900},"sender":{"login":"alice"}}`
	if rec := ts.postWebhook(t, "pull_request_review", dismissed); rec.Code != http.StatusAccepted {
		t.Fatalf("dismissal: %d", rec.Code)
	}
	if got := workspace(); len(got.InboxWaitingOnMe) != 1 {
		t.Fatalf("a dismissed approval no longer counts: %+v", got)
	}

	// octocat's own approval, made on GitHub, dims it as well.
	if rec := ts.postWebhook(t, "pull_request_review", review(2, "octocat", "approved", "sha42")); rec.Code != http.StatusAccepted {
		t.Fatalf("own review: %d", rec.Code)
	}
	got = workspace()
	if len(got.InboxWaitingOnMe) != 0 || len(got.InboxDimmed) != 1 || got.InboxDimmed[0].UserState.TurnState != entity.TurnStateApprovedByMe {
		t.Fatalf("my own approval dims the PR: %+v", got)
	}

	if rec := ts.postWebhook(t, "pull_request_review", `{not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed payload is rejected, got %d", rec.Code)
	}
}

func TestActions_ActAsTheSignedInUserAndNeverApproveTheirOwnPR(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	ts.seedWorkspaceFixture(t, session)

	first, err := ts.prs.GetByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	own := &entity.PullRequest{RepositoryID: first.RepositoryID, RepositoryName: "arvore/hive-web", GitHubPRID: 70001, Number: 501, Title: "mine",
		AuthorLogin: "octocat", State: entity.PRStateOpen, MergeableState: entity.MergeableStateClean, CIStatus: entity.CIStatusSuccess, HeadSHA: "live-501", BaseRef: "main"}
	if err := ts.prs.Upsert(context.Background(), own); err != nil {
		t.Fatal(err)
	}
	ownID := strconv.FormatUint(own.ID, 10)

	rec := ts.do(t, http.MethodPost, "/api/v1/prs/"+ownID+"/approve", `{}`, session)
	var body map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "own_pull_request" {
		t.Fatalf("approving your own PR: status %d body %v, want 422 own_pull_request", rec.Code, body)
	}
	if got := ts.gh.written(); len(got) != 0 {
		t.Fatalf("nothing may reach GitHub: %v", got)
	}

	// Someone else's PR goes through, with the user's own token, and the review is kept.
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/1/approve", `{"body":"LGTM"}`, session); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	got := ts.gh.written()
	if len(got) != 1 || !strings.HasPrefix(got[0], "approve:ghu_test:") {
		t.Fatalf("expected one approval written with the signed-in user's token: %v", got)
	}

	// Merging your own PR is allowed.
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/"+ownID+"/merge", `{}`, session); rec.Code != http.StatusOK {
		t.Fatalf("merge own PR: %d %s", rec.Code, rec.Body.String())
	}

	// A batch containing the user's own PR is refused as a whole.
	ts.gh.writes = nil
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/batch-merge", `{"pr_ids":[2,`+ownID+`]}`, session); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("batch with an own PR: %d", rec.Code)
	}
	if got := ts.gh.written(); len(got) != 0 {
		t.Fatalf("the batch must be refused before any write: %v", got)
	}
}

func TestActions_SurfaceGitHubsAnswerHonestly(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	ts.seedWorkspaceFixture(t, session)

	for name, tc := range map[string]struct {
		err     error
		status  int
		code    string
		message string
	}{
		"checks still running":             {entity.ErrChecksRunning, http.StatusConflict, "checks_running", ""},
		"no answer from GitHub":            {entity.ErrOutcomeUnknown, http.StatusBadGateway, "outcome_unknown", ""},
		"GitHub unreachable on a read":     {fmt.Errorf("%w: github server error (502)", entity.ErrGitHubUnavailable), http.StatusBadGateway, "github_unavailable", ""},
		"GitHub refused (perms)":           {fmt.Errorf("%w: Resource not accessible", entity.ErrForbidden), http.StatusForbidden, "forbidden", "Resource not accessible"},
		"the user's GitHub access is gone": {fmt.Errorf("%w: Bad credentials", entity.ErrGitHubTokenExpired), http.StatusUnauthorized, "github_token_expired", ""},
		"GitHub refused (state)":           {fmt.Errorf("%w: Pull Request is not mergeable", entity.ErrConflict), http.StatusConflict, "conflict", "Pull Request is not mergeable"},
	} {
		ts.gh.mu.Lock()
		ts.gh.err = tc.err
		ts.gh.mu.Unlock()
		rec := ts.do(t, http.MethodPost, "/api/v1/prs/1/merge", `{}`, session)
		var body map[string]string
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if rec.Code != tc.status || body["code"] != tc.code {
			t.Errorf("%s: status %d body %v, want %d %s", name, rec.Code, body, tc.status, tc.code)
		}
		if tc.message != "" && body["error"] != tc.message {
			t.Errorf("%s: the client shows GitHub's explanation alone, got %q", name, body["error"])
		}
	}
}

// dependabotPR stores an open Dependabot PR in the repository of PR 1 and returns its id.
func (ts testServer) dependabotPR(t *testing.T, number int, title string) uint64 {
	t.Helper()
	first, err := ts.prs.GetByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	pr := &entity.PullRequest{RepositoryID: first.RepositoryID, RepositoryName: first.RepositoryName, GitHubPRID: int64(71000 + number), Number: number,
		Title: title, AuthorLogin: "dependabot[bot]", IsDependabot: true, State: entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean, CIStatus: entity.CIStatusSuccess, HeadSHA: fmt.Sprintf("live-%d", number), BaseRef: "main"}
	if err := ts.prs.Upsert(context.Background(), pr); err != nil {
		t.Fatal(err)
	}
	return pr.ID
}

// radarItemOf returns a Dependabot PR's entry in the workspace the signed-in user loads.
func radarItemOf(t *testing.T, ts testServer, session *http.Cookie, prID uint64) triage.DependabotItem {
	t.Helper()
	var body struct {
		Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
	}
	rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
		t.Fatalf("workspace: %d", rec.Code)
	}
	for _, item := range body.Snapshot.DependabotPRs {
		if item.PullRequest.ID == prID {
			return item
		}
	}
	t.Fatalf("PR %d is not in the radar", prID)
	return triage.DependabotItem{}
}

func analysisOf(t *testing.T, ts testServer, session *http.Cookie, prID uint64) *entity.DependabotAnalysis {
	t.Helper()
	var body struct {
		Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
	}
	rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
		t.Fatalf("workspace: %d", rec.Code)
	}
	for _, item := range body.Snapshot.DependabotPRs {
		if item.PullRequest.ID == prID {
			return item.Analysis
		}
	}
	t.Fatalf("PR %d is not in the radar", prID)
	return nil
}

func TestAnalysis_AFollowerCanRunItOnDemandAndSeeTheResult(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	ts.seedWorkspaceFixture(t, session)
	prID := ts.dependabotPR(t, 601, "chore(deps-dev): bump vitest from 2.1.0 to 2.1.1")
	path := "/api/v1/prs/" + strconv.FormatUint(prID, 10) + "/analyze"

	if analysisOf(t, ts, session, prID) != nil {
		t.Fatal("not analysed yet")
	}
	if rec := ts.do(t, http.MethodPost, path, "", session); rec.Code != http.StatusOK {
		t.Fatalf("analyze: %d %s", rec.Code, rec.Body.String())
	}
	got := analysisOf(t, ts, session, prID)
	if got == nil || got.PackageName != "vitest" || got.RiskVerdict != entity.RiskVerdictLow {
		t.Fatalf("the radar must show the stored analysis: %+v", got)
	}
	if rec := ts.do(t, http.MethodPost, path, "", session); rec.Code != http.StatusOK {
		t.Fatalf("asking again converges: %d", rec.Code)
	}

	// A grouped update names no single package: refused, and still "not analysed".
	grouped := ts.dependabotPR(t, 602, "chore(deps): bump the npm_and_yarn group with 3 updates")
	rec := ts.do(t, http.MethodPost, "/api/v1/prs/"+strconv.FormatUint(grouped, 10)+"/analyze", "", session)
	var refusal map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&refusal)
	if rec.Code != http.StatusUnprocessableEntity || refusal["code"] != "not_analysable" {
		t.Fatalf("grouped update: %d %v, want 422 not_analysable (not a generic bad request)", rec.Code, refusal)
	}
	if analysisOf(t, ts, session, grouped) != nil {
		t.Fatal("a grouped update must stay unanalysed, not be analysed as 'unknown'")
	}
}

func TestAnalysis_SomeoneWhoDoesNotFollowTheRepositoryCannotReachIt(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	owner := ts.signIn(t)
	ts.seedWorkspaceFixture(t, owner)
	prID := ts.dependabotPR(t, 601, "chore(deps-dev): bump vitest from 2.1.0 to 2.1.1")
	stranger := ts.signInAs(t, "other")

	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/"+strconv.FormatUint(prID, 10)+"/analyze", "", stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("a stranger must get 404, got %d", rec.Code)
	}
	if analysisOf(t, ts, owner, prID) != nil {
		t.Fatal("nothing may have been analysed on the stranger's request")
	}
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/"+strconv.FormatUint(prID, 10)+"/analyze", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", rec.Code)
	}
}

func TestAnalysis_NotAvailableWithoutTheGitHubAppCredentials(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{AuthUC: ts.authUC}) // no AnalyzeUC
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/prs/1/analyze", nil)
	req.AddCookie(session)
	server.NewChiRouter(handler, "*").ServeHTTP(rec, req)
	var body map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusServiceUnavailable || body["code"] != "unavailable" {
		t.Fatalf("expected 503 unavailable, got %d %v", rec.Code, body)
	}
}

func TestAnalysis_ADependabotWebhookIsAnalysedInTheBackground(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	ts.seedWorkspaceFixture(t, session)

	payload := `{"action":"opened","number":603,"pull_request":{"id":72003,"number":603,"title":"chore(deps-dev): bump vitest from 2.1.0 to 2.1.1","state":"open",` +
		`"user":{"login":"dependabot[bot]"},"head":{"sha":"dep-603"},"base":{"ref":"main"}},"repository":{"id":5001},"sender":{"login":"dependabot[bot]"}}`
	if rec := ts.postWebhook(t, "pull_request", payload); rec.Code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		var body struct {
			Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		}
		_ = json.NewDecoder(ts.do(t, http.MethodGet, "/api/v1/workspace", "", session).Body).Decode(&body)
		for _, item := range body.Snapshot.DependabotPRs {
			if item.PullRequest.Number == 603 && item.Analysis != nil {
				if item.Analysis.RiskVerdict != entity.RiskVerdictLow {
					t.Fatalf("unexpected verdict: %+v", item.Analysis)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the webhook's Dependabot PR was never analysed in the background")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestActions_AnApprovedDependabotPRStaysApprovedWhenTheWorkspaceReloads(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	ts.seedWorkspaceFixture(t, session)
	prID := ts.dependabotPR(t, 611, "chore(deps-dev): bump vitest from 2.1.0 to 2.1.1")

	if radarItemOf(t, ts, session, prID).ApprovedByMe {
		t.Fatal("not approved yet")
	}
	if rec := ts.do(t, http.MethodPost, "/api/v1/prs/"+strconv.FormatUint(prID, 10)+"/approve", `{}`, session); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	// What the panel does after every action: it reloads the workspace. The approval GitHub
	// recorded must still be there, or the panel shows the PR as unapproved again.
	if !radarItemOf(t, ts, session, prID).ApprovedByMe {
		t.Fatal("the radar item must report my approval after a reload")
	}

	// A new commit makes the approval stale.
	pr, err := ts.prs.GetByID(context.Background(), prID)
	if err != nil {
		t.Fatal(err)
	}
	pr.HeadSHA = "live-612"
	if err := ts.prs.Upsert(context.Background(), pr); err != nil {
		t.Fatal(err)
	}
	if radarItemOf(t, ts, session, prID).ApprovedByMe {
		t.Fatal("an approval of an older commit must not count")
	}
}

func TestWorkspace_ShowsWhoReviewedAPR(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	review := func(id int, login, state, sha string) string {
		return fmt.Sprintf(`{"action":"submitted","review":{"id":%d,"user":{"login":%q},"state":%q,"commit_id":%q,"submitted_at":"2026-11-10T09:00:00Z"},`+
			`"pull_request":{"number":42},"repository":{"id":900},"sender":{"login":%q}}`, id, login, state, sha, login)
	}
	for _, payload := range []string{review(1, "bob", "approved", "sha42"), review(2, "carol", "changes_requested", "older")} {
		if rec := ts.postWebhook(t, "pull_request_review", payload); rec.Code != http.StatusAccepted {
			t.Fatalf("review webhook: %d", rec.Code)
		}
	}

	var body struct {
		Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
	}
	rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
	if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
		t.Fatalf("workspace: %d", rec.Code)
	}
	var got []entity.ReviewDecision
	for _, decisions := range body.Snapshot.Reviews {
		got = decisions
	}
	if len(body.Snapshot.Reviews) != 1 || len(got) != 2 {
		t.Fatalf("expected both reviewers on PR 42: %+v", body.Snapshot.Reviews)
	}
	if got[0].Reviewer != "bob" || got[0].State != entity.ReviewApproved || !got[0].Current ||
		got[1].Reviewer != "carol" || got[1].State != entity.ReviewChangesRequested || got[1].Current {
		t.Fatalf("expected bob's current approval, then carol's stale request: %+v", got)
	}
}

func TestPush_ACodeownersChangeOnTheDefaultBranchMovesTheTurn(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	waiting := func() int {
		t.Helper()
		var body struct {
			Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		}
		rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("workspace: %d", rec.Code)
		}
		return len(body.Snapshot.InboxWaitingOnMe)
	}
	push := func(ref string, paths ...string) *httptest.ResponseRecorder {
		list, _ := json.Marshal(paths)
		return ts.postWebhook(t, "push", fmt.Sprintf(`{"ref":%q,"deleted":false,"repository":{"id":900,"default_branch":"main"},`+
			`"commits":[{"added":[],"removed":[],"modified":%s}],"head_commit":{"added":[],"removed":[],"modified":%s}}`, ref, list, list))
	}
	if waiting() != 1 {
		t.Fatal("PR 42 starts as octocat's turn: CODEOWNERS says '* @octocat'")
	}

	// CODEOWNERS changes on GitHub, but a push that does not touch it changes nothing here.
	ts.files.set("acme/web:.github/CODEOWNERS", "* @someone-else\n")
	if rec := push("refs/heads/main", "src/cart.ts"); rec.Code != http.StatusAccepted {
		t.Fatalf("push: %d", rec.Code)
	}
	if rec := push("refs/heads/feat/x", ".github/CODEOWNERS"); rec.Code != http.StatusAccepted {
		t.Fatalf("push: %d", rec.Code)
	}
	if waiting() != 1 {
		t.Fatal("neither push can have changed the rules in force")
	}

	// A push of the new CODEOWNERS to the default branch: the cached rules follow, and so does the turn.
	if rec := push("refs/heads/main", ".github/CODEOWNERS"); rec.Code != http.StatusAccepted {
		t.Fatalf("push: %d %s", rec.Code, rec.Body.String())
	}
	if waiting() != 0 {
		t.Fatal("octocat no longer owns anything in PR 42, so it is no longer octocat's turn")
	}
	// Redelivery changes nothing.
	if rec := push("refs/heads/main", ".github/CODEOWNERS"); rec.Code != http.StatusAccepted || waiting() != 0 {
		t.Fatalf("redelivery: %d", rec.Code)
	}

	// The file is deleted: the repository has no CODEOWNERS, and nobody is assigned a review.
	ts.files.set("acme/web:.github/CODEOWNERS", "* @octocat\n")
	ts.files.remove("acme/web:.github/CODEOWNERS")
	if rec := push("refs/heads/main", ".github/CODEOWNERS"); rec.Code != http.StatusAccepted {
		t.Fatalf("push: %d", rec.Code)
	}
	if waiting() != 0 {
		t.Fatal("a repository without CODEOWNERS gives nobody a turn")
	}
	repo, err := ts.repos.GetByGitHubRepoID(context.Background(), 900)
	if err != nil || repo.CodeownersStatus != entity.CodeownersAbsent {
		t.Fatalf("the repository must be recorded as having no CODEOWNERS: %+v, %v", repo, err)
	}
}

func TestPush_ARefreshNobodyCanDoFailsTheDeliveryInsteadOfKeepingStaleRulesQuietly(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d", rec.Code)
	}
	// Unfollow: nobody can read now, and the app has no credentials in this test.
	repo, _ := ts.repos.GetByGitHubRepoID(context.Background(), 900)
	if rec := ts.do(t, http.MethodDelete, "/api/v1/repositories/"+strconv.FormatUint(repo.ID, 10)+"/follow", "", session); rec.Code >= 300 {
		t.Fatalf("unfollow: %d", rec.Code)
	}
	rec := ts.postWebhook(t, "push", `{"ref":"refs/heads/main","repository":{"id":900,"default_branch":"main"},"commits":[{"modified":[".github/CODEOWNERS"]}]}`)
	if rec.Code < 500 {
		t.Fatalf("expected the delivery to fail visibly, got %d", rec.Code)
	}
}

func TestWorkspace_ShowsTheChecksOfAPRLikeGitHubDoes(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	check := func(id int, name, status, conclusion, title string) {
		t.Helper()
		payload := fmt.Sprintf(`{"action":"completed","check_run":{"id":%d,"name":%q,"head_sha":"sha42","status":%q,"conclusion":%q,`+
			`"details_url":"https://ci.example.com/%d","output":{"title":%q}},"repository":{"id":900}}`, id, name, status, conclusion, id, title)
		if rec := ts.postWebhook(t, "check_run", payload); rec.Code != http.StatusAccepted {
			t.Fatalf("check_run webhook: %d %s", rec.Code, rec.Body.String())
		}
	}
	checksOf42 := func() (checks []entity.CheckRun, ci entity.CIStatus) {
		t.Helper()
		var body struct {
			Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		}
		rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("workspace: %d", rec.Code)
		}
		for _, seat := range body.Snapshot.Seats {
			for _, pr := range seat.PullRequests {
				if pr.Number == 42 {
					return body.Snapshot.Checks[pr.ID], pr.CIStatus
				}
			}
		}
		t.Fatal("PR 42 not found")
		return nil, ""
	}

	check(5001, "build", "completed", "failure", "Build failed")
	check(5002, "test", "completed", "success", "42 tests passed")
	check(5003, "lint", "in_progress", "", "")
	checks, ci := checksOf42()
	if len(checks) != 3 || ci != entity.CIStatusFailure {
		t.Fatalf("expected three checks and a failing PR: %+v %q", checks, ci)
	}
	if checks[0].Name != "build" || checks[0].Title != "Build failed" || checks[0].DetailsURL != "https://ci.example.com/5001" {
		t.Fatalf("each check carries its short result and its link: %+v", checks[0])
	}

	// build is re-run (a new run, same name) and passes; lint finishes: nothing failed any more.
	check(5004, "build", "completed", "success", "Build passed")
	check(5003, "lint", "completed", "success", "")
	checks, ci = checksOf42()
	if len(checks) != 3 || ci != entity.CIStatusSuccess {
		t.Fatalf("the re-run replaces the failed run: %+v %q", checks, ci)
	}
	if checks[0].Name != "build" || checks[0].ID != 5004 {
		t.Fatalf("only the latest run of build is shown: %+v", checks[0])
	}
}

func TestWorkspace_AStatusFromVercelHoldsTheChecksPanelBack(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	post := func(event, payload string) {
		t.Helper()
		if rec := ts.postWebhook(t, event, payload); rec.Code != http.StatusAccepted {
			t.Fatalf("%s webhook: %d %s", event, rec.Code, rec.Body.String())
		}
	}
	status := func(id int, state, description string) {
		t.Helper()
		post("status", fmt.Sprintf(`{"id":%d,"sha":"sha42","context":"Vercel","state":%q,"description":%q,"target_url":"https://vercel.com/d/%d","repository":{"id":900}}`, id, state, description, id))
	}
	view := func() ([]entity.CheckRun, entity.CIStatus) {
		t.Helper()
		var body struct {
			Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		}
		rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("workspace: %d", rec.Code)
		}
		for _, seat := range body.Snapshot.Seats {
			for _, pr := range seat.PullRequests {
				if pr.Number == 42 {
					return body.Snapshot.Checks[pr.ID], pr.CIStatus
				}
			}
		}
		t.Fatal("PR 42 not found")
		return nil, ""
	}

	// Import already found a passing check run for the commit: green.
	if checks, ci := view(); ci != entity.CIStatusSuccess || len(checks) != 1 {
		t.Fatalf("before Vercel reports: %+v %q", checks, ci)
	}

	// Vercel starts deploying (a commit status, not a check run): the PR is no longer green.
	status(7001, "pending", "Vercel is deploying your app")
	checks, ci := view()
	if ci != entity.CIStatusPending || len(checks) != 2 {
		t.Fatalf("a pending status must show up and hold CI back: %+v %q", checks, ci)
	}
	var vercel entity.CheckRun
	for _, c := range checks {
		if c.Name == "Vercel" {
			vercel = c
		}
	}
	if vercel.Status != "in_progress" || vercel.Title != "Vercel is deploying your app" || vercel.DetailsURL != "https://vercel.com/d/7001" || vercel.Source != entity.CheckSourceStatus {
		t.Fatalf("the status is listed like GitHub lists it: %+v", vercel)
	}

	// Redelivery changes nothing; the deployment finishing replaces the pending status.
	status(7001, "pending", "Vercel is deploying your app")
	if _, ci := view(); ci != entity.CIStatusPending {
		t.Fatalf("redelivery: %q", ci)
	}
	status(7002, "success", "Deployment has completed")
	checks, ci = view()
	if ci != entity.CIStatusSuccess || len(checks) != 2 {
		t.Fatalf("the latest status of the context decides: %+v %q", checks, ci)
	}
}

func TestSync_RunsInTheBackgroundAndBringsTheWorkspaceInLineWithGitHub(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/import", `{"installation_id":20,"github_repo_id":900}`, session); rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	openNumbers := func() []int {
		t.Helper()
		var body struct {
			Snapshot triage.WorkspaceSnapshot `json:"snapshot"`
		}
		rec := ts.do(t, http.MethodGet, "/api/v1/workspace", "", session)
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("workspace: %d", rec.Code)
		}
		out := []int{}
		for _, seat := range body.Snapshot.Seats {
			for _, pr := range seat.PullRequests {
				out = append(out, pr.Number)
			}
		}
		return out
	}
	status := func(cookie *http.Cookie) map[string]any {
		t.Helper()
		rec := ts.do(t, http.MethodGet, "/api/v1/sync", "", cookie)
		var body map[string]any
		if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&body) != nil {
			t.Fatalf("GET /sync: %d %s", rec.Code, rec.Body.String())
		}
		return body
	}
	// sync starts a sync and waits for it to finish, returning its final status.
	sync := func() map[string]any {
		t.Helper()
		rec := ts.do(t, http.MethodPost, "/api/v1/sync", "", session)
		var started map[string]any
		if rec.Code != http.StatusAccepted || json.NewDecoder(rec.Body).Decode(&started) != nil {
			t.Fatalf("POST /sync must answer at once with 202: %d %s", rec.Code, rec.Body.String())
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			st := status(session)
			if st["state"] != "running" {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatal("the sync never finished")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	if st := status(session); st["state"] != "idle" {
		t.Fatalf("before any sync the state is idle: %v", st)
	}
	if got := openNumbers(); len(got) != 1 || got[0] != 42 {
		t.Fatalf("PR 42 starts open: %v", got)
	}

	// PR 42 is merged on GitHub while no webhook reaches us: the workspace still lists it...
	ts.ended.set(42, "merged")
	if got := openNumbers(); len(got) != 1 {
		t.Fatalf("without a webhook nothing has changed here: %v", got)
	}
	// ...until the user syncs.
	st := sync()
	result, _ := st["result"].(map[string]any)
	if st["state"] != "done" || result["repositories"] != float64(1) || result["merged"] != float64(1) || result["closed"] != float64(0) {
		t.Fatalf("unexpected final status: %v", st)
	}
	if st["total"] != float64(1) || st["done"] != float64(1) || st["started_at"] == nil || st["finished_at"] == nil {
		t.Fatalf("a finished sync reports its progress and times: %v", st)
	}
	if got := openNumbers(); len(got) != 0 {
		t.Fatalf("a PR merged on GitHub must be gone after a sync: %v", got)
	}
	again := sync()
	if r, _ := again["result"].(map[string]any); r["merged"] != float64(0) || r["closed"] != float64(0) || r["updated"] != float64(0) {
		t.Fatalf("a second sync changes nothing: %v", again)
	}

	// Cancelling nothing is harmless; someone who follows nothing syncs nothing; signed out is refused.
	if rec := ts.do(t, http.MethodDelete, "/api/v1/sync", "", session); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"canceled":false`) {
		t.Fatalf("DELETE /sync with nothing running: %d %s", rec.Code, rec.Body.String())
	}
	other := ts.signInAs(t, "other")
	if st := status(other); st["state"] != "idle" {
		t.Fatalf("another user's sync is separate: %v", st)
	}
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		if rec := ts.do(t, method, "/api/v1/sync", "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s /sync signed out: %d", method, rec.Code)
		}
	}
}

func TestSync_ASyncWhoseReplicaWentAwayReadsAsInterrupted(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	gone := time.Now().Add(-time.Hour)
	if _, err := ts.syncs.ClaimSyncJob(context.Background(), 1, "dead-replica", gone, gone); err != nil {
		t.Fatal(err)
	}

	rec := ts.do(t, http.MethodGet, "/api/v1/sync", "", session)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"failed"`) ||
		!strings.Contains(rec.Body.String(), `"code":"sync_interrupted"`) {
		t.Fatalf("GET /sync of an abandoned sync: %d %s", rec.Code, rec.Body.String())
	}
	if rec := ts.do(t, http.MethodPost, "/api/v1/sync", "", session); rec.Code != http.StatusAccepted ||
		!strings.Contains(rec.Body.String(), `"started":true`) {
		t.Fatalf("an interrupted sync must not block the next one: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSync_NotAvailableWithoutGitHubSignIn(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{AuthUC: ts.authUC}) // no SyncJobs
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/sync", nil)
		req.AddCookie(session)
		server.NewChiRouter(handler, "*").ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"unavailable"`) {
			t.Fatalf("%s /sync: %d %s", method, rec.Code, rec.Body.String())
		}
	}
}
