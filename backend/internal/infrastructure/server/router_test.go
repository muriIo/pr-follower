package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	httpadapter "github.com/murilo/pr-follower/backend/internal/adapter/http"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/server"
)

func TestNewChiRouter_HealthzAndCORSPreflight(t *testing.T) {
	t.Parallel()

	h := httpadapter.NewHandler(httpadapter.HandlerDeps{})
	router := server.NewChiRouter(h, "http://localhost:4200")

	// 1. OPTIONS preflight
	optReq := httptest.NewRequest(http.MethodOptions, "/api/v1/workspace", nil)
	optRec := httptest.NewRecorder()
	router.ServeHTTP(optRec, optReq)
	if optRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on OPTIONS preflight, got %d", optRec.Code)
	}
	if got := optRec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4200" {
		t.Errorf("expected Access-Control-Allow-Origin http://localhost:4200, got %q", got)
	}

	// 2. GET /healthz
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", rec.Code)
	}
}
