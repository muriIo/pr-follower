package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	httpadapter "github.com/murilo/pr-follower/backend/internal/adapter/http"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/server"
)

type fakeDatabase struct {
	err      error
	deadline bool
}

func (f *fakeDatabase) PingContext(ctx context.Context) error {
	_, f.deadline = ctx.Deadline()
	return f.err
}

func getAPIHealth(t *testing.T, db httpadapter.DatabasePinger) (int, map[string]string) {
	t.Helper()
	router := server.NewChiRouter(httpadapter.NewHandler(httpadapter.HandlerDeps{Database: db}), "*")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)) // no session cookie
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

// The panel answers /healthz itself, so /api/v1/healthz is how anyone outside checks that the API
// and its database are up. It is public and pings the database with a deadline.
func TestAPIHealth_IsPublicAndReportsTheDatabase(t *testing.T) {
	t.Parallel()

	up := &fakeDatabase{}
	code, body := getAPIHealth(t, up)
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("expected 200 ok with the database up, got %d %v", code, body)
	}
	if !up.deadline {
		t.Error("expected the database ping to carry a deadline")
	}

	code, body = getAPIHealth(t, &fakeDatabase{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")})
	if code != http.StatusServiceUnavailable || body["status"] != "unavailable" {
		t.Fatalf("expected 503 unavailable with the database down, got %d %v", code, body)
	}
	if len(body) != 1 {
		t.Errorf("a public endpoint must not explain the failure, got %v", body)
	}
}

func TestAPIHealth_WithoutADatabaseIsUnavailable(t *testing.T) {
	t.Parallel()

	if code, _ := getAPIHealth(t, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no database is wired, got %d", code)
	}
}
