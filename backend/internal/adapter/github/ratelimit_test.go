package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// limitedServer answers every request with the given status and headers/body until `after` requests
// were made, then with a PR.
func limitedServer(t *testing.T, status int, headers map[string]string, body string, after int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) > after {
			_, _ = w.Write([]byte(`{"state":"open","head":{"sha":"abc"},"user":{"login":"bia"}}`))
			return
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func rateLimitClient(server *httptest.Server, slept *[]time.Duration, mu *sync.Mutex) *github.UserClient {
	return github.NewUserClient(github.UserClientConfig{
		APIBaseURL: server.URL,
		HTTPClient: server.Client(),
		Backoff: resilience.BackoffConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Second,
			SleepFunc: func(_ context.Context, d time.Duration) error {
				mu.Lock()
				defer mu.Unlock()
				*slept = append(*slept, d)
				return nil
			}},
	})
}

func epochIn(d time.Duration) string { return strconv.FormatInt(time.Now().Add(d).Unix(), 10) }

func TestRateLimit_AShortWaitUntilTheResetIsHonoured(t *testing.T) {
	t.Parallel()
	for name, status := range map[string]int{"403": http.StatusForbidden, "429": http.StatusTooManyRequests} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server, hits := limitedServer(t, status, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": epochIn(3 * time.Second)}, `{"message":"API rate limit exceeded"}`, 1)
			var slept []time.Duration
			var mu sync.Mutex
			c := rateLimitClient(server, &slept, &mu)

			if _, err := c.GetPullRequest(context.Background(), "ghu", "acme", "web", 7); err != nil {
				t.Fatalf("after the reset the call succeeds: %v", err)
			}
			if hits.Load() != 2 || len(slept) != 1 || slept[0] < time.Second || slept[0] > 6*time.Second {
				t.Fatalf("expected one wait of a few seconds (until the reset) and one retry: hits=%d slept=%v", hits.Load(), slept)
			}
		})
	}
}

func TestRateLimit_ALongWaitFailsFastAndSaysWhen(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(10 * time.Minute)
	server, hits := limitedServer(t, http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(reset.Unix(), 10)}, `{"message":"API rate limit exceeded"}`, 99)
	var slept []time.Duration
	var mu sync.Mutex
	c := rateLimitClient(server, &slept, &mu)

	_, err := c.GetPullRequest(context.Background(), "ghu", "acme", "web", 7)
	if !errors.Is(err, entity.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
	var limited *entity.RateLimitedError
	if !errors.As(err, &limited) || limited.ResetAt.Sub(reset) > time.Second || reset.Sub(limited.ResetAt) > time.Second {
		t.Fatalf("the error must carry when the limit resets: %+v", limited)
	}
	if hits.Load() != 1 || len(slept) != 0 {
		t.Fatalf("a ten minute wait is not waited for inside a request: hits=%d slept=%v", hits.Load(), slept)
	}
}

func TestRateLimit_ASecondaryLimitIsTreatedAsOne(t *testing.T) {
	t.Parallel()
	server, hits := limitedServer(t, http.StatusForbidden, nil, `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`, 99)
	var slept []time.Duration
	var mu sync.Mutex
	c := rateLimitClient(server, &slept, &mu)

	_, err := c.GetPullRequest(context.Background(), "ghu", "acme", "web", 7)
	var limited *entity.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected a rate limit error, got %v", err)
	}
	if d := time.Until(limited.ResetAt); d < 30*time.Second || d > 90*time.Second {
		t.Fatalf("GitHub asks to wait at least a minute: resets in %v", d)
	}
	if hits.Load() != 1 {
		t.Fatalf("no hammering: %d requests", hits.Load())
	}
}

func TestRateLimit_APlainForbiddenIsNotMistakenForALimit(t *testing.T) {
	t.Parallel()
	server, _ := limitedServer(t, http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "4321"}, `{"message":"Resource not accessible by integration"}`, 99)
	var slept []time.Duration
	var mu sync.Mutex
	c := rateLimitClient(server, &slept, &mu)

	_, err := c.GetPullRequest(context.Background(), "ghu", "acme", "web", 7)
	if err == nil || errors.Is(err, entity.ErrRateLimited) {
		t.Fatalf("a 403 with requests left is a refusal, not a rate limit: %v", err)
	}
}

func TestRateLimit_RetryAfterIsStillHonoured(t *testing.T) {
	t.Parallel()
	server, hits := limitedServer(t, http.StatusTooManyRequests, map[string]string{"Retry-After": "2"}, ``, 1)
	var slept []time.Duration
	var mu sync.Mutex
	c := rateLimitClient(server, &slept, &mu)
	if _, err := c.GetPullRequest(context.Background(), "ghu", "acme", "web", 7); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("hits=%d slept=%v", hits.Load(), slept)
	}
}

func TestRateLimit_ActionsReportItToo_AndNeverAsRefusedOrUnknown(t *testing.T) {
	t.Parallel()
	server, hits := limitedServer(t, http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": epochIn(20 * time.Minute)}, `{"message":"API rate limit exceeded"}`, 99)
	gw := github.NewGateway(github.GatewayConfig{BaseURL: server.URL, HTTPClient: server.Client(), Backoff: resilience.BackoffConfig{MaxAttempts: 3, SleepFunc: func(context.Context, time.Duration) error { return nil }}})
	repo := entity.Repository{ID: 1, Owner: "acme", Name: "web"}

	_, err := gw.ApprovePullRequest(context.Background(), "ghu", repo, 7, "")
	if !errors.Is(err, entity.ErrRateLimited) || errors.Is(err, entity.ErrForbidden) || errors.Is(err, entity.ErrOutcomeUnknown) {
		t.Fatalf("not processed because of the limit: expected only ErrRateLimited, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("the write is not repeated: %d requests", hits.Load())
	}
}
