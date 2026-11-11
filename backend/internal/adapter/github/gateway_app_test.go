package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

type stubInstallationTokens struct {
	err   error
	calls []int64
}

func (s *stubInstallationTokens) InstallationToken(_ context.Context, id int64) (string, error) {
	s.calls = append(s.calls, id)
	if s.err != nil {
		return "", s.err
	}
	return "ghs_install", nil
}

// recordingGitHub notes the Authorization header of every request by "METHOD path".
type recordingGitHub struct {
	mu   sync.Mutex
	auth map[string]string
}

func (r *recordingGitHub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.auth[req.Method+" "+req.URL.Path] = req.Header.Get("Authorization")
		r.mu.Unlock()
		if req.URL.Path == "/repos/acme/web/pulls/7/files" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`ok`))
	})
}

func newAppGateway(t *testing.T, tokens *stubInstallationTokens, staticToken string) (*github.Gateway, *recordingGitHub) {
	t.Helper()
	rec := &recordingGitHub{auth: map[string]string{}}
	server := httptest.NewServer(rec.handler())
	t.Cleanup(server.Close)
	cfg := github.GatewayConfig{BaseURL: server.URL, Token: staticToken, Backoff: resilience.BackoffConfig{MaxAttempts: 1}}
	if tokens != nil {
		cfg.Installations = tokens
	}
	return github.NewGateway(cfg), rec
}

var appRepo = entity.Repository{ID: 1, Owner: "acme", Name: "web", InstallationID: 555}

func TestGateway_ReadsUseTheInstallationTokenButActionsNeverDo(t *testing.T) {
	t.Parallel()
	tokens := &stubInstallationTokens{}
	gw, rec := newAppGateway(t, tokens, "")
	ctx := context.Background()

	if _, err := gw.GetFileContent(ctx, appRepo, "main", ".github/CODEOWNERS"); err != nil {
		t.Fatalf("GetFileContent() error: %v", err)
	}
	if _, err := gw.ListPRChangedFiles(ctx, appRepo, 7); err != nil {
		t.Fatalf("ListPRChangedFiles() error: %v", err)
	}
	body, err := gw.StreamRepoTarball(ctx, appRepo, "sha1")
	if err != nil {
		t.Fatalf("StreamRepoTarball() error: %v", err)
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()

	for path, got := range map[string]string{
		"GET /repos/acme/web/contents/.github/CODEOWNERS": rec.auth["GET /repos/acme/web/contents/.github/CODEOWNERS"],
		"GET /repos/acme/web/pulls/7/files":               rec.auth["GET /repos/acme/web/pulls/7/files"],
		"GET /repos/acme/web/tarball/sha1":                rec.auth["GET /repos/acme/web/tarball/sha1"],
	} {
		if got != "Bearer ghs_install" {
			t.Errorf("%s: Authorization = %q, want the installation token", path, got)
		}
	}

	// Writes must act as the user (a later change), never as the app: they never ask for the app's token.
	before := len(tokens.calls)
	_, _ = gw.ApprovePullRequest(ctx, "ghu_user", appRepo, 7, "")
	_ = gw.MergePullRequest(ctx, "ghu_user", appRepo, 7, "squash")
	_ = gw.CreatePRComment(ctx, "ghu_user", appRepo, 7, "hi")
	if len(tokens.calls) != before {
		t.Fatalf("approve, merge and comment must not use the installation token: %v", tokens.calls)
	}
	for _, key := range []string{"POST /repos/acme/web/pulls/7/reviews", "PUT /repos/acme/web/pulls/7/merge", "POST /repos/acme/web/issues/7/comments"} {
		if got := rec.auth[key]; got != "Bearer ghu_user" {
			t.Errorf("%s sent %q: an action must carry the user's identity", key, got)
		}
	}
}

func TestGateway_ReadFallsBackToTheStaticTokenWithoutAnInstallation(t *testing.T) {
	t.Parallel()
	tokens := &stubInstallationTokens{}
	gw, rec := newAppGateway(t, tokens, "static")
	noInstall := entity.Repository{ID: 2, Owner: "acme", Name: "web"} // no installation id
	if _, err := gw.GetFileContent(context.Background(), noInstall, "main", "a.txt"); err != nil {
		t.Fatal(err)
	}
	if got := rec.auth["GET /repos/acme/web/contents/a.txt"]; got != "Bearer static" || len(tokens.calls) != 0 {
		t.Fatalf("auth = %q, installation calls = %v", got, tokens.calls)
	}

	plain, rec2 := newAppGateway(t, nil, "static") // app not configured
	if _, err := plain.GetFileContent(context.Background(), appRepo, "main", "a.txt"); err != nil {
		t.Fatal(err)
	}
	if got := rec2.auth["GET /repos/acme/web/contents/a.txt"]; got != "Bearer static" {
		t.Fatalf("app not configured: auth = %q", got)
	}
}

func TestGateway_ReadFailsWithoutSendingAnythingWhenTheAppTokenCannotBeObtained(t *testing.T) {
	t.Parallel()
	tokens := &stubInstallationTokens{err: entity.ErrNotFound}
	gw, rec := newAppGateway(t, tokens, "static")

	if _, err := gw.GetFileContent(context.Background(), appRepo, "main", "a.txt"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("expected the token error, got %v", err)
	}
	if _, err := gw.ListPRChangedFiles(context.Background(), appRepo, 7); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("expected the token error, got %v", err)
	}
	if _, err := gw.StreamRepoTarball(context.Background(), appRepo, "sha1"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("expected the token error, got %v", err)
	}
	if len(rec.auth) != 0 {
		t.Fatalf("nothing may reach GitHub without the app's token (never fall back to an unrelated identity): %v", rec.auth)
	}
}

func TestGateway_ReadWithoutAnyCredentialFailsInsteadOfGoingOutAnonymous(t *testing.T) {
	t.Parallel()
	gw, rec := newAppGateway(t, nil, "") // no app, no static token
	for name, err := range map[string]error{
		"file":    func() error { _, err := gw.GetFileContent(context.Background(), appRepo, "main", "a.txt"); return err }(),
		"files":   func() error { _, err := gw.ListPRChangedFiles(context.Background(), appRepo, 7); return err }(),
		"tarball": func() error { _, err := gw.StreamRepoTarball(context.Background(), appRepo, "sha"); return err }(),
	} {
		if !errors.Is(err, entity.ErrUnauthorized) {
			t.Errorf("%s: expected ErrUnauthorized, got %v", name, err)
		}
	}
	if len(rec.auth) != 0 {
		t.Fatalf("nothing may reach GitHub without a credential: %v", rec.auth)
	}
}

func TestGateway_ListPRChangedFilesReadsEveryPage(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		page := r.URL.Query().Get("page")
		n := 100
		if page == "2" {
			n = 5
		}
		files := make([]map[string]any, n)
		for i := range files {
			files[i] = map[string]any{"filename": fmt.Sprintf("f-%s-%d", page, i)}
		}
		_ = json.NewEncoder(w).Encode(files)
	}))
	defer srv.Close()
	gw := github.NewGateway(github.GatewayConfig{BaseURL: srv.URL, Token: "t", Backoff: resilience.BackoffConfig{MaxAttempts: 1}})

	files, err := gw.ListPRChangedFiles(context.Background(), appRepo, 7)
	if err != nil || len(files) != 105 {
		t.Fatalf("expected 105 files across two pages: %d, %v", len(files), err)
	}
	if len(queries) != 2 || queries[0] != "per_page=100&page=1" || queries[1] != "per_page=100&page=2" {
		t.Fatalf("expected two full-size page requests: %v", queries)
	}
}
