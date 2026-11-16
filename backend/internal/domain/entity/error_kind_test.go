package entity_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestErrorKind_RoundTripKeepsWhatCallersCheck(t *testing.T) {
	t.Parallel()
	reset := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)

	for _, original := range []error{
		entity.ErrGitHubTokenExpired,
		fmt.Errorf("read acme/web: %w", entity.ErrForbidden),
		entity.ErrNotFound,
		entity.ErrChecksRunning,
		entity.ErrOutcomeUnknown,
		entity.ErrSyncInterrupted,
		fmt.Errorf("read acme/web: %w", entity.ErrGitHubUnavailable),
		fmt.Errorf("analysis: %w", context.DeadlineExceeded),
		&entity.RateLimitedError{ResetAt: reset},
	} {
		kind := entity.ErrorKindOf(original)
		restored := entity.ErrorOfKind(kind, original.Error(), reset)

		for _, sentinel := range []error{
			entity.ErrGitHubTokenExpired, entity.ErrUnauthorized, entity.ErrForbidden, entity.ErrNotFound,
			entity.ErrConflict, entity.ErrChecksRunning, entity.ErrOutcomeUnknown, entity.ErrRateLimited,
			entity.ErrSyncInterrupted, entity.ErrGitHubUnavailable, context.DeadlineExceeded,
		} {
			if errors.Is(original, sentinel) != errors.Is(restored, sentinel) {
				t.Errorf("%q (kind %q): errors.Is(%q) = %v before, %v after", original, kind, sentinel,
					errors.Is(original, sentinel), errors.Is(restored, sentinel))
			}
		}
		if restored.Error() != original.Error() {
			t.Errorf("message changed: %q -> %q", original, restored)
		}
	}

	var limited *entity.RateLimitedError
	if !errors.As(entity.ErrorOfKind(entity.ErrorKindOf(&entity.RateLimitedError{ResetAt: reset}), "", reset), &limited) || !limited.ResetAt.Equal(reset) {
		t.Fatalf("a rate limit keeps its reset time, got %v", limited)
	}
}

func TestErrorKind_UnknownErrorsStayErrors(t *testing.T) {
	t.Parallel()
	err := entity.ErrorOfKind(entity.ErrorKindOf(errors.New("boom")), "boom", time.Time{})
	if err == nil || err.Error() != "boom" || errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("an unknown error is restored as a plain error, got %v", err)
	}
	if entity.ErrorKindOf(nil) != "" || entity.ErrorOfKind("", "", time.Time{}) != nil {
		t.Fatal("no error has no kind, and no kind is no error")
	}
}
