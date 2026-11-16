package entity

import "time"

// AnalysisFailure is why the latest analysis of a Dependabot PR's commit did not produce a result.
// ErrorKind is an ErrorKindOf name ("forbidden", "not_analysable", "timeout", ...); Message is the
// error as it happened (often GitHub's own explanation). An analysis of the commit replaces it.
type AnalysisFailure struct {
	PullRequestID uint64    `json:"pull_request_id"`
	HeadSHA       string    `json:"head_sha"`
	ErrorKind     string    `json:"error_kind"`
	Message       string    `json:"message"`
	FailedAt      time.Time `json:"failed_at"`
}
