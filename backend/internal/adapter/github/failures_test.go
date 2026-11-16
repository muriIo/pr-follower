package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// ADR-0013: every way GitHub can fail ends in a domain kind the user is shown, never a bare error.

func answering(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// unreachable is a server that has already gone away: every request fails in transport.
func unreachable(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	return server
}

func fastGateway(server *httptest.Server) *github.Gateway {
	return github.NewGateway(github.GatewayConfig{
		BaseURL: server.URL, Token: "ghs_static", HTTPClient: server.Client(),
		Backoff: resilience.BackoffConfig{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
	})
}

var someRepo = entity.Repository{ID: 1, Owner: "acme", Name: "web"}

func TestFailures_ReadsNameEveryGitHubAnswer(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		server  func(*testing.T) *httptest.Server
		want    error
		message string
	}{
		"5xx after retries": {func(t *testing.T) *httptest.Server { return answering(t, http.StatusBadGateway, "") }, entity.ErrGitHubUnavailable, ""},
		"no answer at all":  {unreachable, entity.ErrGitHubUnavailable, ""},
		"a 403 that is not limits": {func(t *testing.T) *httptest.Server {
			return answering(t, http.StatusForbidden, `{"message":"Resource not accessible by integration"}`)
		}, entity.ErrForbidden, "Resource not accessible by integration"},
		"a 422 refusal": {func(t *testing.T) *httptest.Server {
			return answering(t, http.StatusUnprocessableEntity, `{"message":"Validation Failed"}`)
		}, entity.ErrConflict, "Validation Failed"},
		"a 429 without headers": {func(t *testing.T) *httptest.Server { return answering(t, http.StatusTooManyRequests, "") }, entity.ErrRateLimited, ""},
	} {
		server := tc.server(t)
		for call, read := range map[string]func() error{
			"app gateway": func() error {
				_, err := fastGateway(server).ListPRChangedFiles(context.Background(), someRepo, 7)
				return err
			},
			"user client": func() error {
				_, err := newUserClient(server).GetPullRequest(context.Background(), "ghu_a", "acme", "web", 7)
				return err
			},
		} {
			err := read()
			if !errors.Is(err, tc.want) {
				t.Errorf("%s, %s: got %v, want %v", name, call, err, tc.want)
			}
			if tc.message != "" && (err == nil || !strings.Contains(err.Error(), tc.message)) {
				t.Errorf("%s, %s: GitHub's explanation is lost: %v", name, call, err)
			}
		}
	}
}

func TestFailures_ACanceledReadStaysCanceled(t *testing.T) {
	t.Parallel()
	server := unreachable(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fastGateway(server).ListPRChangedFiles(ctx, someRepo, 7); errors.Is(err, entity.ErrGitHubUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled read is the caller's doing, not GitHub's: %v", err)
	}
}

func TestFailures_AnUnexpectedRefusalOfAWriteKeepsGitHubsMessage(t *testing.T) {
	t.Parallel()
	server := answering(t, http.StatusBadRequest, `{"message":"Problems parsing JSON"}`)
	err := fastGateway(server).MergePullRequest(context.Background(), "ghu_a", someRepo, 7, "squash")
	if !errors.Is(err, entity.ErrConflict) || !strings.Contains(err.Error(), "Problems parsing JSON") {
		t.Fatalf("got %v, want a conflict carrying GitHub's message", err)
	}
}

func TestFailures_SignInExchangeNamesItsFailures(t *testing.T) {
	t.Parallel()
	down := answering(t, http.StatusServiceUnavailable, "")
	if _, err := newOAuthClient(down).RefreshToken(context.Background(), "ghr_1"); !errors.Is(err, entity.ErrGitHubUnavailable) {
		t.Errorf("token endpoint down: got %v, want ErrGitHubUnavailable", err)
	}
	refused := answering(t, http.StatusOK, `{"error":"bad_refresh_token","error_description":"The refresh token is invalid"}`)
	if _, err := newOAuthClient(refused).RefreshToken(context.Background(), "ghr_1"); !errors.Is(err, entity.ErrUnauthorized) || !strings.Contains(err.Error(), "bad_refresh_token") {
		t.Errorf("refused refresh: got %v, want ErrUnauthorized with GitHub's code", err)
	}
	empty := answering(t, http.StatusOK, `{}`)
	if _, err := newOAuthClient(empty).RefreshToken(context.Background(), "ghr_1"); !errors.Is(err, entity.ErrGitHubUnavailable) {
		t.Errorf("unreadable answer: got %v, want ErrGitHubUnavailable", err)
	}
}

func TestFailures_AnUnreadableInstallationTokenIsGitHubUnavailable(t *testing.T) {
	t.Parallel()
	key, pemKey := newKey(t)
	app := &fakeApp{appID: "42", key: key, clock: time.Now, lifetime: time.Hour, status: http.StatusCreated}
	auth := newAppAuth(t, app, pemKey, time.Now)
	if _, err := auth.InstallationToken(context.Background(), 1); !errors.Is(err, entity.ErrGitHubUnavailable) {
		t.Fatalf("got %v, want ErrGitHubUnavailable", err)
	}
}
