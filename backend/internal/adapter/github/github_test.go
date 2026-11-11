package github_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

func TestVerifyWebhookSignature(t *testing.T) {
	t.Parallel()

	secret := "my-webhook-secret"
	payload := []byte(`{"action":"opened","number":42}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validHeader := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if err := github.VerifyWebhookSignature(secret, validHeader, payload); err != nil {
		t.Fatalf("expected valid signature to succeed, got %v", err)
	}

	if err := github.VerifyWebhookSignature(secret, "sha256=deadbeef", payload); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for invalid signature, got %v", err)
	}

	if err := github.VerifyWebhookSignature(secret, "", payload); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for empty signature, got %v", err)
	}
}

func TestGateway_AllEndpointsWithBackoffAndCancellation(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/contents/"):
			n := hits.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"hive-web"}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"filename":"src/app.ts","additions":10,"deletions":2,"patch":"@@ -1 +1 @@"}]`))
		case strings.Contains(r.URL.Path, "/tarball/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("tarball-bytes"))
		case strings.HasSuffix(r.URL.Path, "/reviews") || strings.HasSuffix(r.URL.Path, "/merge") || strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	gw := github.NewGateway(github.GatewayConfig{
		BaseURL:    srv.URL,
		Token:      "ghs_test_token",
		HTTPClient: srv.Client(),
		Backoff: resilience.BackoffConfig{
			BaseDelay:   5 * time.Millisecond,
			MaxDelay:    20 * time.Millisecond,
			MaxAttempts: 3,
			SleepFunc:   func(_ context.Context, _ time.Duration) error { return nil },
		},
	})

	repo := entity.Repository{ID: 1, Owner: "arvore", Name: "hive-web"}
	ctx := context.Background()

	content, err := gw.GetFileContent(ctx, repo, "sha-1", "package.json")
	if err != nil || string(content) != `{"name":"hive-web"}` {
		t.Fatalf("GetFileContent() = %q, err = %v", string(content), err)
	}

	files, err := gw.ListPRChangedFiles(ctx, repo, 42)
	if err != nil || len(files) != 1 || files[0].Path != "src/app.ts" {
		t.Fatalf("ListPRChangedFiles() = %#v, err = %v", files, err)
	}

	rc, err := gw.StreamRepoTarball(ctx, repo, "sha-1")
	if err != nil {
		t.Fatalf("StreamRepoTarball() err = %v", err)
	}
	tarBytes, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(tarBytes) != "tarball-bytes" {
		t.Fatalf("unexpected tarball bytes: %q", string(tarBytes))
	}

	if _, err := gw.ApprovePullRequest(ctx, "ghu_user", repo, 42, "LGTM"); err != nil {
		t.Fatalf("ApprovePullRequest() err = %v", err)
	}
	if err := gw.MergePullRequest(ctx, "ghu_user", repo, 42, "squash"); err != nil {
		t.Fatalf("MergePullRequest() err = %v", err)
	}
	if err := gw.CreatePRComment(ctx, "ghu_user", repo, 42, "@dependabot rebase"); err != nil {
		t.Fatalf("CreatePRComment() err = %v", err)
	}

	// Test immediate context cancellation
	canceledCtx, cancel := context.WithCancelCause(context.Background())
	cancel(entity.ErrOperationCanceled)
	_, err = gw.GetFileContent(canceledCtx, repo, "sha-2", "package.json")
	if !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected ErrOperationCanceled, got %v", err)
	}
}

func TestGateway_NonIdempotentWritesAreNotRepeatedOnAmbiguousFailures(t *testing.T) {
	t.Parallel()
	var reviews, comments, merges atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/1/reviews"):
			reviews.Add(1)
			w.WriteHeader(http.StatusBadGateway) // the review may or may not exist now
		case strings.HasSuffix(r.URL.Path, "/issues/1/comments"):
			comments.Add(1)
			w.WriteHeader(http.StatusBadGateway)
		case strings.HasSuffix(r.URL.Path, "/pulls/2/reviews"):
			if reviews.Add(1) == 2 { // after the /pulls/1 attempt
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests) // explicitly not processed
				return
			}
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/merge"):
			if merges.Add(1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK) // PUT merge is idempotent: retried
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	gw := github.NewGateway(github.GatewayConfig{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Backoff:    resilience.BackoffConfig{MaxAttempts: 3, SleepFunc: func(context.Context, time.Duration) error { return nil }},
	})
	repo := entity.Repository{ID: 1, Owner: "acme", Name: "web"}
	ctx := context.Background()

	if _, err := gw.ApprovePullRequest(ctx, "ghu_user", repo, 1, "LGTM"); err == nil || reviews.Load() != 1 {
		t.Fatalf("approve on 502: err=%v attempts=%d, want an error after exactly 1 attempt", err, reviews.Load())
	}
	if err := gw.CreatePRComment(ctx, "ghu_user", repo, 1, "@dependabot rebase"); err == nil || comments.Load() != 1 {
		t.Fatalf("comment on 502: err=%v attempts=%d, want an error after exactly 1 attempt", err, comments.Load())
	}
	if _, err := gw.ApprovePullRequest(ctx, "ghu_user", repo, 2, "LGTM"); err != nil || reviews.Load() != 3 {
		t.Fatalf("approve after Retry-After: err=%v attempts=%d, want success on the retry", err, reviews.Load())
	}
	if err := gw.MergePullRequest(ctx, "ghu_user", repo, 3, "squash"); err != nil || merges.Load() != 2 {
		t.Fatalf("merge on 502: err=%v attempts=%d, want success on the retry", err, merges.Load())
	}
}
