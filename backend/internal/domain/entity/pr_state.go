package entity

import (
	"sort"
	"strings"
)

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
	// Title is the short result GitHub shows beside the check ("Sem mudanças em apps/web").
	Title string `json:"title"`
	// DetailsURL is where the check reports in full (the CI provider's page).
	DetailsURL string `json:"details_url"`
	// Source says where the check came from: a check run, or a legacy commit status (Jenkins,
	// Vercel). The two have separate id spaces. Empty means a check run.
	Source string `json:"source"`
}

// Where a CheckRun came from.
const (
	CheckSourceCheckRun = "check_run"
	CheckSourceStatus   = "status"
)

// CommitStatus is a legacy commit status (the `status` webhook, the combined-status API): one state
// a CI provider reported for a commit under a context. Each update is a new status with a new id.
type CommitStatus struct {
	ID           int64
	RepositoryID uint64
	HeadSHA      string
	Context      string
	State        string // pending | success | failure | error
	Description  string
	TargetURL    string
}

// AsCheckRun shows a status the way GitHub lists it among a commit's checks: pending is running,
// success passes, and failure or error fail.
func (s CommitStatus) AsCheckRun() CheckRun {
	run := CheckRun{
		ID: s.ID, RepositoryID: s.RepositoryID, HeadSHA: s.HeadSHA, Name: s.Context,
		Title: s.Description, DetailsURL: s.TargetURL, Source: CheckSourceStatus,
	}
	switch s.State {
	case "success":
		run.Status, run.Conclusion = "completed", "success"
	case "failure", "error":
		run.Status, run.Conclusion = "completed", "failure"
	default: // pending, or a state nobody reported
		run.Status = "in_progress"
	}
	return run
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

// LatestCheckRuns keeps, for each check name, only its latest run, which is how GitHub shows a
// commit: re-running a check creates a new run with the same name and the old one no longer counts.
// Runs GitHub marked stale are dropped. Runs without a name cannot be matched and are all kept.
// A status and a check run with the same name are different checks (their ids are unrelated). The
// result is ordered by name (then id).
func LatestCheckRuns(runs []CheckRun) []CheckRun {
	latest := make(map[string]CheckRun, len(runs))
	key := func(r CheckRun) string { return r.Source + "\x00" + r.Name } // a status and a check run never replace each other
	var unnamed []CheckRun
	for _, r := range runs {
		if r.Status == "completed" && r.Conclusion == "stale" {
			continue
		}
		if r.Name == "" {
			unnamed = append(unnamed, r)
			continue
		}
		if cur, ok := latest[key(r)]; !ok || r.ID > cur.ID {
			latest[key(r)] = r
		}
	}
	out := append(unnamed, make([]CheckRun, 0, len(latest))...)
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// DeriveCIStatus aggregates the check runs of one commit, counting only the latest run of each
// check. No runs means no CI reported.
func DeriveCIStatus(runs []CheckRun) CIStatus {
	counted, running := 0, false
	for _, r := range LatestCheckRuns(runs) {
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

// PRChangedFiles is the list of files a pull request changes at one commit, as GitHub reported
// it. It is evaluated only when HeadSHA is the PR's current head.
type PRChangedFiles struct {
	PullRequestID uint64
	HeadSHA       string
	Files         []ChangedFile
}

// IsCurrentFor reports whether the files describe the PR's current head commit.
func (f PRChangedFiles) IsCurrentFor(pr PullRequest) bool {
	return f.HeadSHA != "" && f.HeadSHA == pr.HeadSHA
}

// RemotePullRequestState is a PR's live state on GitHub, read before acting on it.
type RemotePullRequestState struct {
	Open        bool
	Merged      bool
	HeadSHA     string
	AuthorLogin string
}
