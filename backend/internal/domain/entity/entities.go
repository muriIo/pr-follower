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
)

type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
)

type CIStatus string

const (
	CIStatusPending CIStatus = "PENDING"
	CIStatusSuccess CIStatus = "SUCCESS"
	CIStatusFailure CIStatus = "FAILURE"
)

type MergeableState string

const (
	MergeableStateClean       MergeableState = "CLEAN"
	MergeableStateConflicting MergeableState = "CONFLICTING"
	MergeableStateUnknown     MergeableState = "UNKNOWN"
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
	ID             uint64
	GitHubID       int64
	Login          string
	Name           string
	AvatarURL      string
	Teams          []string
	EncryptedToken string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CodeownerRule struct {
	Pattern string   `json:"pattern"`
	Owners  []string `json:"owners"`
}

type Repository struct {
	ID                    uint64
	GitHubRepoID          int64
	InstallationID        int64
	Owner                 string
	Name                  string
	FullName              string
	DefaultBranch         string
	CachedCodeownersRules []CodeownerRule
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (r Repository) Validate() error {
	if r.GitHubRepoID <= 0 || strings.TrimSpace(r.Owner) == "" || strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.FullName) == "" {
		return fmt.Errorf("%w: repository requires github_repo_id, owner, name, and full_name", ErrInvalidInput)
	}
	return nil
}

type ChangedFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

type PullRequest struct {
	ID              uint64
	RepositoryID    uint64
	RepositoryName  string
	GitHubPRID      int64
	Number          int
	Title           string
	AuthorLogin     string
	HTMLURL         string
	IsDependabot    bool
	IsDraft         bool
	State           PRState
	MergeableState  MergeableState
	CIStatus        CIStatus
	HeadSHA         string
	BaseRef         string
	ClaimedByUserID *uint64
	ClaimedByLogin  string
	UpdatedAt       time.Time
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
		pr.CIStatus == CIStatusSuccess &&
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
	ID                  uint64
	PullRequestID       uint64
	HeadSHA             string
	Ecosystem           string
	PackageName         string
	FromVersion         string
	ToVersion           string
	SemverBumpType      SemverBumpType
	IsDirectDep         bool
	DepScope            DependencyScope
	RiskVerdict         RiskVerdict
	RiskScore           int
	VerdictSummaryKey   string
	DependencyPathGraph []DependencyNode
	BlastRadiusUsages   []BlastRadiusUsage
	AdvisoriesAndNotes  []AdvisoryAndNote
	UpdatedAt           time.Time
}

type UserPRState struct {
	ID                    uint64
	PullRequestID         uint64
	UserID                uint64
	TurnState             TurnState
	TeamAlreadyApproved   bool
	ApprovedByLogin       string
	SnoozedUntilCondition SnoozeCondition
	SnoozedAtSHA          string
	OwnedFilesChanged     []ChangedFile
	UpdatedAt             time.Time
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
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	RepositoryID   uint64    `json:"repository_id"`
	RepositoryName string    `json:"repository_name"`
	PullRequestID  uint64    `json:"pull_request_id,omitempty"`
	PRNumber       int       `json:"pr_number,omitempty"`
	ActorLogin     string    `json:"actor_login"`
	Summary        string    `json:"summary"`
	OccurredAt     time.Time `json:"occurred_at"`
}
