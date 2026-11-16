package entity

import "time"

// SyncFailure is a repository a sync could not bring in line with GitHub, and why.
type SyncFailure struct {
	Repository string
	Err        error
}

// SyncResult says what a sync of the user's followed repositories found.
type SyncResult struct {
	Repositories int
	Added        int
	Updated      int
	Closed       int
	Merged       int
	Failed       []SyncFailure
	// Skipped counts repositories not attempted because GitHub's rate limit was reached first.
	Skipped int
	// RateLimitedUntil is when that limit resets (zero when it was not reached).
	RateLimitedUntil time.Time
}

// SyncProgress says where a running sync is: Done of Total repositories finished, Current being read.
type SyncProgress struct {
	Total   int
	Done    int
	Current string
}

// SyncJobState is where a user's sync stands.
type SyncJobState string

const (
	SyncJobIdle     SyncJobState = "idle" // the user never ran one
	SyncJobRunning  SyncJobState = "running"
	SyncJobDone     SyncJobState = "done"
	SyncJobFailed   SyncJobState = "failed"
	SyncJobCanceled SyncJobState = "canceled"
)

// SyncJob is a user's latest sync as every API replica sees it. RunID tells one run from the next,
// so a run that lost its claim (its replica stalled and another started over) cannot overwrite it.
type SyncJob struct {
	UserID          uint64
	RunID           string
	State           SyncJobState
	Progress        SyncProgress
	Result          *SyncResult // set when done
	Err             error       // set when failed
	CancelRequested bool
	StartedAt       time.Time
	HeartbeatAt     time.Time // the running replica's latest sign of life
	FinishedAt      time.Time
}
