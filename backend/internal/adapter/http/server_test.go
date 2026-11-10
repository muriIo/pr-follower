package httpadapter_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	return entity.OAuthToken{AccessToken: "ghu_test", RefreshToken: "ghr_test", AccessTokenExpiresAt: time.Now().Add(8 * time.Hour)}, nil
}

func (fakeGitHubOAuth) RefreshToken(context.Context, string) (entity.OAuthToken, error) {
	return entity.OAuthToken{AccessToken: "ghu_refreshed"}, nil
}

func (fakeGitHubOAuth) FetchProfile(context.Context, string) (entity.GitHubProfile, error) {
	return entity.GitHubProfile{ID: 583231, Login: "octocat", Name: "The Octocat", Teams: []string{"@acme/web"}}, nil
}

type testServer struct {
	router http.Handler
	opReg  *operation.Registry
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?code=ok&state="+url.QueryEscape(state), nil)
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

	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{
		UserRepo:      userRepo,
		RepoRepo:      repoRepo,
		PRRepo:        prRepo,
		TriageUC:      triageUC,
		ActionUC:      actionUC,
		WebhookUC:     webhookUC,
		SSEHub:        sseHub,
		OpRegistry:    opReg,
		WebhookSecret: "test-secret",
		AuthUC:        authUC,
	})

	return testServer{router: server.NewChiRouter(handler, "*"), opReg: opReg}
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

	// 2.5 Follow Repository
	if rec := ts.do(t, http.MethodPost, "/api/v1/repositories/follow", `{"owner":"arvore","name":"design-system","default_branch":"main"}`, session); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/repositories/follow, got %d: %s", rec.Code, rec.Body.String())
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
		{http.MethodPost, "/api/v1/repositories/follow"},
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
