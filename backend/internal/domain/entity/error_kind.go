package entity

import (
	"context"
	"errors"
	"time"
)

// errorKinds names the errors a caller tells apart, most specific first (ErrChecksRunning is also an
// ErrConflict). The names are stored: never rename one.
var errorKinds = []struct {
	name string
	err  error
}{
	{"checks_running", ErrChecksRunning},
	{"github_token_expired", ErrGitHubTokenExpired},
	{"rate_limited", ErrRateLimited},
	{"sync_interrupted", ErrSyncInterrupted},
	{"own_pull_request", ErrOwnPullRequest},
	{"not_analysable", ErrNotAnalysable},
	{"outcome_unknown", ErrOutcomeUnknown},
	{"operation_canceled", ErrOperationCanceled},
	{"forbidden", ErrForbidden},
	{"unauthorized", ErrUnauthorized},
	{"not_found", ErrNotFound},
	{"conflict", ErrConflict},
	{"invalid_input", ErrInvalidInput},
	{"timeout", context.DeadlineExceeded},
}

// otherErrorKind is any error that is none of the above.
const otherErrorKind = "error"

// ErrorKindOf names err so it can be stored and restored with ErrorOfKind ("" for no error).
func ErrorKindOf(err error) string {
	if err == nil {
		return ""
	}
	for _, k := range errorKinds {
		if errors.Is(err, k.err) {
			return k.name
		}
	}
	return otherErrorKind
}

// ErrorOfKind restores an error stored with ErrorKindOf: the same message, and errors.Is/As answer
// as they did for the original (a rate limit keeps its reset time). An empty kind is no error.
func ErrorOfKind(kind, message string, rateLimitResetAt time.Time) error {
	if kind == "" {
		return nil
	}
	if kind == "rate_limited" {
		return &RateLimitedError{ResetAt: rateLimitResetAt}
	}
	for _, k := range errorKinds {
		if k.name == kind {
			return &restoredError{message: message, kind: k.err}
		}
	}
	return errors.New(message)
}

type restoredError struct {
	message string
	kind    error
}

func (e *restoredError) Error() string { return e.message }
func (e *restoredError) Unwrap() error { return e.kind }
