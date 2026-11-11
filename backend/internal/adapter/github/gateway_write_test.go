package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

var writeRepo = entity.Repository{ID: 1, Owner: "acme", Name: "web", InstallationID: 555}

func fastBackoff(attempts int) resilience.BackoffConfig {
	return resilience.BackoffConfig{MaxAttempts: attempts, SleepFunc: func(context.Context, time.Duration) error { return nil }}
}

func TestGateway_WritesActAsTheUserWhoseTokenIsGiven(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	auth := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth[r.Method+" "+r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		if r.URL.Path == "/repos/acme/web/pulls/7/reviews" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":901,"state":"APPROVED","commit_id":"sha7","submitted_at":"2026-11-10T09:00:00Z","user":{"login":"octocat"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	tokens := &stubInstallationTokens{}
	gw := github.NewGateway(github.GatewayConfig{BaseURL: srv.URL, Token: "static", Installations: tokens, Backoff: fastBackoff(1)})
	ctx := context.Background()

	review, err := gw.ApprovePullRequest(ctx, "ghu_user", writeRepo, 7, "LGTM")
	if err != nil {
		t.Fatalf("ApprovePullRequest() error: %v", err)
	}
	if review.GitHubReviewID != 901 || review.State != entity.ReviewApproved || review.CommitSHA != "sha7" || review.ReviewerLogin != "octocat" ||
		!review.SubmittedAt.Equal(time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("the review GitHub recorded must come back: %+v", review)
	}
	if err := gw.MergePullRequest(ctx, "ghu_user", writeRepo, 7, "squash"); err != nil {
		t.Fatalf("MergePullRequest() error: %v", err)
	}
	if err := gw.CreatePRComment(ctx, "ghu_user", writeRepo, 7, "@dependabot rebase"); err != nil {
		t.Fatalf("CreatePRComment() error: %v", err)
	}

	for _, key := range []string{"POST /repos/acme/web/pulls/7/reviews", "PUT /repos/acme/web/pulls/7/merge", "POST /repos/acme/web/issues/7/comments"} {
		if got := auth[key]; got != "Bearer ghu_user" {
			t.Errorf("%s: Authorization = %q, want the user's token (never the static or the app's)", key, got)
		}
	}
	if len(tokens.calls) != 0 {
		t.Errorf("a write must never ask for the app's installation token: %v", tokens.calls)
	}
}

func TestGateway_WritesRefuseToRunWithoutAUserToken(t *testing.T) {
	t.Parallel()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	gw := github.NewGateway(github.GatewayConfig{BaseURL: srv.URL, Token: "static", Backoff: fastBackoff(1)})

	if _, err := gw.ApprovePullRequest(context.Background(), "", writeRepo, 7, ""); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("approve without a token: expected ErrUnauthorized, got %v", err)
	}
	if err := gw.MergePullRequest(context.Background(), "", writeRepo, 7, "squash"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("merge without a token: expected ErrUnauthorized, got %v", err)
	}
	if err := gw.CreatePRComment(context.Background(), "", writeRepo, 7, "x"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("comment without a token: expected ErrUnauthorized, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("nothing may reach GitHub without the user's token, got %d requests", hits)
	}
}

func TestGateway_MapsWhatGitHubRefusedAndKeepsItsExplanation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status  int
		message string
		want    error
	}{
		{http.StatusUnauthorized, "Bad credentials", entity.ErrGitHubTokenExpired},
		{http.StatusForbidden, "Resource not accessible by integration", entity.ErrForbidden},
		{http.StatusNotFound, "Not Found", entity.ErrNotFound},
		{http.StatusMethodNotAllowed, "Pull Request is not mergeable", entity.ErrConflict},
		{http.StatusConflict, "Head branch was modified. Review and try the merge again.", entity.ErrConflict},
		{http.StatusUnprocessableEntity, "Can not approve your own pull request", entity.ErrConflict},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"message":%q}`, tc.message)
			}))
			defer srv.Close()
			gw := github.NewGateway(github.GatewayConfig{BaseURL: srv.URL, Backoff: fastBackoff(3)})
			ctx := context.Background()

			_, approveErr := gw.ApprovePullRequest(ctx, "ghu_user", writeRepo, 7, "")
			mergeErr := gw.MergePullRequest(ctx, "ghu_user", writeRepo, 7, "squash")
			commentErr := gw.CreatePRComment(ctx, "ghu_user", writeRepo, 7, "x")
			for name, err := range map[string]error{"approve": approveErr, "merge": mergeErr, "comment": commentErr} {
				if !errors.Is(err, tc.want) {
					t.Errorf("%s: expected %v, got %v", name, tc.want, err)
				}
				if errors.Is(err, entity.ErrOutcomeUnknown) {
					t.Errorf("%s: a refusal is a definite answer, not an unknown outcome: %v", name, err)
				}
				if tc.status != http.StatusUnauthorized && tc.status != http.StatusNotFound && !strings.Contains(err.Error(), tc.message) {
					t.Errorf("%s: GitHub's explanation %q must be kept, got %v", name, tc.message, err)
				}
			}
		})
	}
}

func TestGateway_AnAmbiguousWriteIsReportedAsOutcomeUnknown(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.Method]++
		mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
	}))
	gw := github.NewGateway(github.GatewayConfig{BaseURL: srv.URL, Backoff: fastBackoff(3)})
	ctx := context.Background()

	_, approveErr := gw.ApprovePullRequest(ctx, "ghu_user", writeRepo, 7, "")
	commentErr := gw.CreatePRComment(ctx, "ghu_user", writeRepo, 7, "x")
	mergeErr := gw.MergePullRequest(ctx, "ghu_user", writeRepo, 7, "squash")
	for name, err := range map[string]error{"approve": approveErr, "comment": commentErr, "merge": mergeErr} {
		if !errors.Is(err, entity.ErrOutcomeUnknown) {
			t.Errorf("%s on 502: expected ErrOutcomeUnknown, got %v", name, err)
		}
	}
	if hits["POST"] != 2 || hits["PUT"] != 3 {
		t.Errorf("POSTs are not repeated blindly (1 each) and the idempotent merge is retried: %v", hits)
	}

	// The connection dies: the request may or may not have been processed.
	srv.Close()
	if _, err := gw.ApprovePullRequest(ctx, "ghu_user", writeRepo, 7, ""); !errors.Is(err, entity.ErrOutcomeUnknown) {
		t.Errorf("transport failure: expected ErrOutcomeUnknown, got %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := gw.ApprovePullRequest(canceled, "ghu_user", writeRepo, 7, ""); !errors.Is(err, context.Canceled) || errors.Is(err, entity.ErrOutcomeUnknown) {
		t.Errorf("a canceled request never started: expected context.Canceled only, got %v", err)
	}
}
