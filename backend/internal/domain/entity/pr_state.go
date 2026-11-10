package entity

import "strings"

// The PR state shown to users comes from GitHub: these helpers translate GitHub's
// vocabulary and never invent a value we were not told.

// MergeableStateFromGitHub maps the pull_request webhook's mergeable_state.
func MergeableStateFromGitHub(state string) MergeableState {
	switch state {
	case "clean", "has_hooks":
		return MergeableStateClean
	case "dirty":
		return MergeableStateConflicting
	case "blocked":
		return MergeableStateBlocked
	case "behind":
		return MergeableStateBehind
	case "unstable":
		return MergeableStateUnstable
	case "draft":
		return MergeableStateDraft
	default:
		return MergeableStateUnknown
	}
}

// CheckRun is one CI check GitHub reported for a commit (check_run webhook).
type CheckRun struct {
	ID           int64  `json:"id"`
	RepositoryID uint64 `json:"repository_id"`
	HeadSHA      string `json:"head_sha"`
	Name         string `json:"name"`
	Status       string `json:"status"`     // queued | in_progress | completed
	Conclusion   string `json:"conclusion"` // set when completed
}

func checkRunRank(status string) int {
	switch status {
	case "completed":
		return 2
	case "in_progress":
		return 1
	default:
		return 0
	}
}

// Supersedes reports whether r is at least as far along as prev, so a late or redelivered
// event never moves a run backwards (ADR-0011).
func (r CheckRun) Supersedes(prev CheckRun) bool {
	return checkRunRank(r.Status) >= checkRunRank(prev.Status)
}

// DeriveCIStatus aggregates the check runs of one commit. No runs means no CI reported.
func DeriveCIStatus(runs []CheckRun) CIStatus {
	counted, running := 0, false
	for _, r := range runs {
		if r.Status != "completed" {
			running = true
			counted++
			continue
		}
		switch r.Conclusion {
		case "stale":
			continue
		case "success", "neutral", "skipped":
		default: // failure, cancelled, timed_out, action_required, startup_failure
			return CIStatusFailure
		}
		counted++
	}
	switch {
	case counted == 0:
		return CIStatusNone
	case running:
		return CIStatusPending
	default:
		return CIStatusSuccess
	}
}

// CodeownersStatus records whether a repository has a CODEOWNERS file.
type CodeownersStatus string

const (
	CodeownersUnknown CodeownersStatus = ""        // not checked yet
	CodeownersPresent CodeownersStatus = "present" // rules cached in CachedCodeownersRules
	CodeownersAbsent  CodeownersStatus = "absent"  // checked: no CODEOWNERS file
)

// CodeownersKnown reports whether the repository was checked for a CODEOWNERS file.
func (r Repository) CodeownersKnown() bool {
	return r.CodeownersStatus != CodeownersUnknown
}

// RemotePullRequest is an open pull request as GitHub lists it, before PR Follower stores it.
// The list carries no merge state or CI: those come from other reads (never guessed).
type RemotePullRequest struct {
	GitHubPRID  int64
	Number      int
	Title       string
	AuthorLogin string
	HTMLURL     string
	IsDraft     bool
	HeadSHA     string
	BaseRef     string
}

// IsDependabotAuthor reports whether a PR author login is Dependabot ("dependabot[bot]").
func IsDependabotAuthor(login string) bool {
	return strings.Contains(strings.ToLower(login), "dependabot")
}
