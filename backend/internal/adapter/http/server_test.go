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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/crypto"
	httpadapter "github.com/murilo/pr-follower/backend/internal/adapter/http"
	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/server"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
	"github.com/murilo/pr-follower/backend/internal/usecase/auth"
	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

type noopGHGateway struct{}

func (n *noopGHGateway) GetFileContent(_ context.Context, _ entity.Repository, _, _ string) ([]byte, error) {
	return nil, nil
}
func (n *noopGHGateway) ListPRChangedFiles(_ context.Context, _ entity.Repository, _ int) ([]entity.ChangedFile, error) {
	return nil, nil
}
func (n *noopGHGateway) StreamRepoTarball(_ context.Context, _ entity.Repository, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (n *noopGHGateway) ApprovePullRequest(_ context.Context, _ entity.Repository, _ int, _ string) error {
	return nil
}
func (n *noopGHGateway) MergePullRequest(_ context.Context, _ entity.Repository, _ int, _ string) error {
	return nil
}
func (n *noopGHGateway) CreatePRComment(_ context.Context, _ entity.Repository, _ int, _ string) error {
	return nil
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
type fakeGitHubUserAPI struct{}

func (fakeGitHubUserAPI) ListInstallations(_ context.Context, token string) ([]entity.Installation, error) {
	if token != "ghu_test" {
		return nil, entity.ErrUnauthorized
	}
	return []entity.Installation{{ID: 20, AccountLogin: "acme", AccountType: "Organization", RepositorySelection: "selected",
		SettingsURL: "https://github.com/organizations/acme/settings/installations/20"}}, nil
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
	sseHub := sse.NewHub(50)
	opReg := operation.NewRegistry()
	ghGw := &noopGHGateway{}

	triageUC := triage.NewUseCase(userRepo, repoRepo, prRepo, sseHub)
	actionUC := action.NewUseCase(prRepo, repoRepo, ghGw, sseHub)
	webhookUC := webhook.NewIngestUseCase(repoRepo, prRepo, sseHub)
	authUC := auth.NewUseCase(auth.Deps{OAuth: oauth, Users: userRepo, Sessions: persistence.NewSessionRepository(db)})
	importUC := repoimport.NewUseCase(authUC, fakeGitHubUserAPI{}, repoRepo, sseHub)
	installationUC := webhook.NewInstallationUseCase(repoRepo, sseHub)

	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{
		UserRepo:       userRepo,
		RepoRepo:       repoRepo,
		PRRepo:         prRepo,
		TriageUC:       triageUC,
		ActionUC:       actionUC,
		WebhookUC:      webhookUC,
		SSEHub:         sseHub,
		OpRegistry:     opReg,
		WebhookSecret:  "test-secret",
		AuthUC:         authUC,
		ImportUC:       importUC,
		InstallURL:     "https://github.com/apps/pr-follower/installations/new",
		InstallationUC: installationUC,
		Follows:        repoRepo,
	})

	return testServer{router: server.NewChiRouter(handler, "*"), opReg: opReg, authUC: authUC, repos: repoRepo}
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

	// 2. Demo Seed (for the signed-in user)
	if rec := ts.do(t, http.MethodPost, "/api/v1/demo/seed", "", session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/demo/seed, got %d: %s", rec.Code, rec.Body.String())
	}

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

	// 8. Cancel active operation returns 200, missing returns 404
	_, release := opReg.Begin(context.Background(), "op-active-55")
	defer release()

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

func TestAuth_ProtectedRoutesRequireSession(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/workspace"},
		{http.MethodGet, "/api/v1/events/stream"},
		{http.MethodPost, "/api/v1/demo/seed"},
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
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when GitHub import is not configured, got %d", rec.Code)
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
