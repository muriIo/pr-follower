package entity

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidInput      = errors.New("invalid domain input")
	ErrNotFound          = errors.New("resource not found")
	ErrOperationCanceled = errors.New("operation canceled by caller")
	ErrConflict          = errors.New("resource conflict")
	ErrUnauthorized      = errors.New("unauthorized")
	// ErrGitHubTokenExpired means the signed-in user's own GitHub access is gone (expired and not
	// refreshable, or revoked): they must sign in again, even though their session is still valid.
	// It is an ErrUnauthorized, and unlike a rejected app credential it is the user's to fix.
	ErrGitHubTokenExpired = fmt.Errorf("%w: your GitHub access expired", ErrUnauthorized)
	// ErrForbidden means GitHub refused the signed-in user's token for this action (permissions).
	ErrForbidden = errors.New("forbidden by github")
	// ErrOutcomeUnknown means a write may or may not have been applied on GitHub (a timeout, a
	// dropped connection, a 5xx): the caller must check GitHub, never assume either way.
	ErrOutcomeUnknown = errors.New("could not confirm the outcome on github")
	// ErrGitHubUnavailable means GitHub gave no usable answer to a read (a 5xx after retries, no
	// answer at all, a body that cannot be read): nothing changed, and the same request may work later.
	ErrGitHubUnavailable = errors.New("github is unavailable")
	// ErrRateLimited means GitHub refused because a rate limit was reached; the error carries when it
	// resets (see RateLimitedError). The same request works after that time.
	ErrRateLimited = errors.New("github rate limit reached")
	// ErrNotAnalysable means a Dependabot PR is one the analysis cannot read yet (several packages
	// in one PR, or a lockfile format no analyzer understands). It is not the caller's mistake.
	ErrNotAnalysable = errors.New("this pull request cannot be analysed")
	// ErrChecksRunning means a check or commit status of the PR's head commit has not finished, so it
	// cannot be approved or merged yet. It is a conflict: the same request works once they finish.
	ErrChecksRunning = fmt.Errorf("%w: checks are still running", ErrConflict)
	// ErrOwnPullRequest means the user tried to approve a pull request they authored: GitHub
	// does not allow it.
	ErrOwnPullRequest = errors.New("you cannot approve your own pull request")
	// ErrSyncInterrupted means a sync stopped reporting before it finished: the API replica running it
	// went away (a deploy, a crash). Nothing it did is lost; running it again finishes the job.
	ErrSyncInterrupted = errors.New("the sync was interrupted")
)

type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
)

type CIStatus string

const (
	CIStatusNone    CIStatus = "NONE" // no check reported for the head commit (e.g. no CI)
	CIStatusPending CIStatus = "PENDING"
	CIStatusSuccess CIStatus = "SUCCESS"
	CIStatusFailure CIStatus = "FAILURE"
)

type MergeableState string

const (
	MergeableStateClean       MergeableState = "CLEAN"
	MergeableStateConflicting MergeableState = "CONFLICTING" // GitHub "dirty"
	MergeableStateBlocked     MergeableState = "BLOCKED"     // branch protection: required reviews or checks
	MergeableStateBehind      MergeableState = "BEHIND"      // head must be updated with the base branch
	MergeableStateUnstable    MergeableState = "UNSTABLE"    // mergeable, but non-required checks fail
	MergeableStateDraft       MergeableState = "DRAFT"
	MergeableStateUnknown     MergeableState = "UNKNOWN" // GitHub has not computed it yet
)

type TurnState string

const (
	TurnStateHiddenGate       TurnState = "HIDDEN_GATE"
	TurnStateWaitingOnMe      TurnState = "WAITING_ON_ME"
	TurnStateWaitingOnAuthor  TurnState = "WAITING_ON_AUTHOR"
	TurnStateTeammateApproved TurnState = "TEAMMATE_APPROVED"
	TurnStateApprovedByMe     TurnState = "APPROVED_BY_ME"
	TurnStateSnoozed          TurnState = "SNOOZED"
	TurnStateMergedOrClosed   TurnState = "MERGED_OR_CLOSED"
)

type SnoozeCondition string

const (
	SnoozeConditionNone       SnoozeCondition = ""
	SnoozeConditionNextCommit SnoozeCondition = "UNTIL_NEXT_COMMIT"
	SnoozeConditionCIGreen    SnoozeCondition = "UNTIL_CI_GREEN"
)

type SemverBumpType string

const (
	SemverBumpPatch   SemverBumpType = "PATCH"
	SemverBumpMinor   SemverBumpType = "MINOR"
	SemverBumpMajor   SemverBumpType = "MAJOR"
	SemverBumpUnknown SemverBumpType = "UNKNOWN"
)

type DependencyScope string

const (
	DependencyScopeProd       DependencyScope = "PROD"
	DependencyScopeDev        DependencyScope = "DEV"
	DependencyScopeTransitive DependencyScope = "TRANSITIVE"
)

type RiskVerdict string

const (
	RiskVerdictLow    RiskVerdict = "LOW"
	RiskVerdictMedium RiskVerdict = "MEDIUM"
	RiskVerdictHigh   RiskVerdict = "HIGH"
)

type User struct {
	ID          uint64     `json:"id"`
	GitHubID    int64      `json:"github_id"`
	Login       string     `json:"login"`
	Name        string     `json:"name"`
	AvatarURL   string     `json:"avatar_url"`
	Teams       []string   `json:"teams"`
	GitHubToken OAuthToken `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type CodeownerRule struct {
	Pattern string   `json:"pattern"`
	Owners  []string `json:"owners"`
}

type Repository struct {
	ID                    uint64           `json:"id"`
	GitHubRepoID          int64            `json:"github_repo_id"`
	InstallationID        int64            `json:"installation_id"`
	Owner                 string           `json:"owner"`
	Name                  string           `json:"name"`
	FullName              string           `json:"full_name"`
	DefaultBranch         string           `json:"default_branch"`
	CachedCodeownersRules []CodeownerRule  `json:"cached_codeowners_rules"`
	CodeownersStatus      CodeownersStatus `json:"codeowners_status"`
	Access                RepositoryAccess `json:"access"`
	CreatedAt             time.Time        `json:"created_at"`
	UpdatedAt             time.Time        `json:"updated_at"`
}

func (r Repository) Validate() error {
	if r.GitHubRepoID <= 0 || strings.TrimSpace(r.Owner) == "" || strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.FullName) == "" {
		return fmt.Errorf("%w: repository requires github_repo_id, owner, name, and full_name", ErrInvalidInput)
	}
	return nil
}

type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status,omitempty"` // added | modified | removed | renamed | ...
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

type PullRequest struct {
	ID              uint64         `json:"id"`
	RepositoryID    uint64         `json:"repository_id"`
	RepositoryName  string         `json:"repository_name"`
	GitHubPRID      int64          `json:"github_pr_id"`
	Number          int            `json:"number"`
	Title           string         `json:"title"`
	AuthorLogin     string         `json:"author_login"`
	HTMLURL         string         `json:"html_url"`
	IsDependabot    bool           `json:"is_dependabot"`
	IsDraft         bool           `json:"is_draft"`
	State           PRState        `json:"state"`
	MergeableState  MergeableState `json:"mergeable_state"`
	CIStatus        CIStatus       `json:"ci_status"`
	HeadSHA         string         `json:"head_sha"`
	BaseRef         string         `json:"base_ref"`
	ClaimedByUserID *uint64        `json:"claimed_by_user_id,omitempty"`
	ClaimedByLogin  string         `json:"claimed_by_login,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func (pr PullRequest) Validate() error {
	if pr.RepositoryID == 0 || pr.GitHubPRID <= 0 || pr.Number <= 0 || strings.TrimSpace(pr.Title) == "" || strings.TrimSpace(pr.HeadSHA) == "" {
		return fmt.Errorf("%w: pull request requires repository_id, github_pr_id, number, title, and head_sha", ErrInvalidInput)
	}
	return nil
}

func (pr PullRequest) IsReadyForReviewGate() bool {
	return pr.State == PRStateOpen &&
		!pr.IsDraft &&
		(pr.CIStatus == CIStatusSuccess || pr.CIStatus == CIStatusNone || pr.CIStatus == "") &&
		pr.MergeableState != MergeableStateConflicting
}

type DependencyNode struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Scope   DependencyScope `json:"scope"`
}

type BlastRadiusUsage struct {
	FilePath    string `json:"file_path"`
	LineNumber  int    `json:"line_number"`
	ImportStmt  string `json:"import_stmt"`
	CodeSnippet string `json:"code_snippet"`
}

type AdvisoryAndNote struct {
	CVEID          string `json:"cve_id,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Summary        string `json:"summary"`
	IsBreakingNote bool   `json:"is_breaking_note"`
}

type DependabotAnalysis struct {
	ID                  uint64             `json:"id"`
	PullRequestID       uint64             `json:"pull_request_id"`
	HeadSHA             string             `json:"head_sha"`
	Ecosystem           string             `json:"ecosystem"`
	PackageName         string             `json:"package_name"`
	FromVersion         string             `json:"from_version"`
	ToVersion           string             `json:"to_version"`
	SemverBumpType      SemverBumpType     `json:"semver_bump_type"`
	IsDirectDep         bool               `json:"is_direct_dep"`
	DepScope            DependencyScope    `json:"dep_scope"`
	RiskVerdict         RiskVerdict        `json:"risk_verdict"`
	RiskScore           int                `json:"risk_score"`
	VerdictSummaryKey   string             `json:"verdict_summary_key"`
	DependencyPathGraph []DependencyNode   `json:"dependency_path_graph"`
	BlastRadiusUsages   []BlastRadiusUsage `json:"blast_radius_usages"`
	AdvisoriesAndNotes  []AdvisoryAndNote  `json:"advisories_and_notes"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

type UserPRState struct {
	ID                    uint64          `json:"id"`
	PullRequestID         uint64          `json:"pull_request_id"`
	UserID                uint64          `json:"user_id"`
	TurnState             TurnState       `json:"turn_state"`
	TeamAlreadyApproved   bool            `json:"team_already_approved"`
	ApprovedByLogin       string          `json:"approved_by_login,omitempty"`
	SnoozedUntilCondition SnoozeCondition `json:"snoozed_until_condition,omitempty"`
	SnoozedAtSHA          string          `json:"snoozed_at_sha,omitempty"`
	OwnedFilesChanged     []ChangedFile   `json:"owned_files_changed"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

func (s UserPRState) IsSnoozedFor(headSHA string, ciStatus CIStatus) bool {
	switch s.SnoozedUntilCondition {
	case SnoozeConditionNextCommit:
		return s.SnoozedAtSHA != "" && s.SnoozedAtSHA == headSHA
	case SnoozeConditionCIGreen:
		return ciStatus != CIStatusSuccess
	default:
		return false
	}
}

type ActivityEvent struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	RepositoryID   uint64 `json:"repository_id"`
	RepositoryName string `json:"repository_name"`
	PullRequestID  uint64 `json:"pull_request_id,omitempty"`
	PRNumber       int    `json:"pr_number,omitempty"`
	ActorLogin     string `json:"actor_login"`
	// Params are the facts the client phrases in the user's language (title, method, package...).
	Params     map[string]string `json:"params,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
}

// RateLimitedError is a GitHub rate limit, with the time it resets. It matches ErrRateLimited.
type RateLimitedError struct{ ResetAt time.Time }

func (e *RateLimitedError) Error() string {
	return "github rate limit reached, resets at " + e.ResetAt.UTC().Format(time.RFC3339)
}

func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited }
