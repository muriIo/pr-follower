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
	"testing"

	httpadapter "github.com/murilo/pr-follower/backend/internal/adapter/http"
	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/server"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
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

func setupTestRouter(t *testing.T) (http.Handler, *operation.Registry) {
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

	userRepo := persistence.NewUserRepository(db)
	repoRepo := persistence.NewRepositoryRepository(db)
	prRepo := persistence.NewPullRequestRepository(db)
	sseHub := sse.NewHub(50)
	opReg := operation.NewRegistry()
	ghGw := &noopGHGateway{}

	triageUC := triage.NewUseCase(userRepo, repoRepo, prRepo, sseHub)
	actionUC := action.NewUseCase(prRepo, repoRepo, ghGw, sseHub)
	webhookUC := webhook.NewIngestUseCase(repoRepo, prRepo, sseHub)

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
	})

	return server.NewChiRouter(handler, "*"), opReg
}

func TestChiServer_SeedWorkspaceActionsWebhookAndCancel(t *testing.T) {
	t.Parallel()

	router, opReg := setupTestRouter(t)

	// 1. Healthz
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", rec.Code)
	}

	// 1.5 Auth Login, Me & Logout
	loginBody := bytes.NewBufferString(`{"login":"murilo","team":"@arvore/frontend"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/auth/login, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("X-User-ID", "1")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/auth/me, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/auth/logout, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Demo Seed
	req = httptest.NewRequest(http.MethodPost, "/api/v1/demo/seed", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/demo/seed, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2.5 Follow Repository
	followBody := bytes.NewBufferString(`{"owner":"arvore","name":"design-system","default_branch":"main"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/repositories/follow", followBody)
	req.Header.Set("X-User-ID", "1")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/repositories/follow, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Get Workspace Snapshot
	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspace", nil)
	req.Header.Set("X-User-ID", "1")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
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
	snoozeBody := bytes.NewBufferString(`{"condition":"UNTIL_NEXT_COMMIT"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/prs/1/snooze", snoozeBody)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on snooze, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Claim PR
	req = httptest.NewRequest(http.MethodPost, "/api/v1/prs/1/claim", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on claim, got %d", rec.Code)
	}

	// 6. Approve, Merge, Dependabot Command & Batch Merge
	req = httptest.NewRequest(http.MethodPost, "/api/v1/prs/1/approve", bytes.NewBufferString(`{"body":"LGTM"}`))
	req.Header.Set("X-Operation-ID", "op-approve-1")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on approve, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/prs/1/merge", bytes.NewBufferString(`{"method":"squash"}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on merge, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/prs/2/dependabot-command", bytes.NewBufferString(`{"command":"rebase"}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on dependabot-command, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/prs/batch-merge", bytes.NewBufferString(`{"pr_ids":[2],"method":"squash"}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
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
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on active operation cancel, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/operations/op-missing", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on missing operation cancel, got %d", rec.Code)
	}
}
