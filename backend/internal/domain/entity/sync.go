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
