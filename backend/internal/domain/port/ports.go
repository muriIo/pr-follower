package port

import (
	"context"
	"io"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

type UserRepository interface {
	Upsert(ctx context.Context, user *entity.User) error
	GetByID(ctx context.Context, id uint64) (*entity.User, error)
	GetByGitHubID(ctx context.Context, ghID int64) (*entity.User, error)
}

type SessionRepository interface {
	Create(ctx context.Context, session *entity.Session) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*entity.Session, error)
	Delete(ctx context.Context, tokenHash string) error
}

// TokenCipher encrypts GitHub tokens at rest (ADR-0010).
type TokenCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// GitHubOAuth is the GitHub App user-authorization (web application flow) port (ADR-0010).
type GitHubOAuth interface {
	AuthorizeURL(state, codeChallenge string) string
	ExchangeCode(ctx context.Context, code, codeVerifier string) (entity.OAuthToken, error)
	RefreshToken(ctx context.Context, refreshToken string) (entity.OAuthToken, error)
	FetchProfile(ctx context.Context, accessToken string) (entity.GitHubProfile, error)
}

// UserTokenSource yields a usable GitHub user access token for a signed-in user (ADR-0010).
type UserTokenSource interface {
	AccessToken(ctx context.Context, userID uint64) (string, error)
}

// InstallationTokenSource authenticates as the GitHub App on one installation, for reads that have
// no signed-in user behind them (webhooks, background analysis). entity.ErrNotFound means the app is
// no longer installed there; entity.ErrUnauthorized means GitHub rejected the app's credentials.
type InstallationTokenSource interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
}

// GitHubUserAPI reads what a user can reach through the GitHub App, using their user token.
type GitHubUserAPI interface {
	ListInstallations(ctx context.Context, accessToken string) ([]entity.Installation, error)
	ListInstallationRepositories(ctx context.Context, accessToken string, installationID int64) ([]entity.InstallationRepository, error)
	// GetRepositoryFile returns a file's raw content; entity.ErrNotFound when it does not exist.
	GetRepositoryFile(ctx context.Context, accessToken, owner, name, path string) ([]byte, error)
	// ListPullRequestFiles lists a PR's changed files; entity.ErrNotFound for an unknown PR.
	ListPullRequestFiles(ctx context.Context, accessToken, owner, name string, number int) ([]entity.ChangedFile, error)
	// ListOpenPullRequests lists a repository's open PRs; entity.ErrNotFound for an unknown repository.
	ListOpenPullRequests(ctx context.Context, accessToken, owner, name string) ([]entity.RemotePullRequest, error)
	// ListCommitCheckRuns lists the CI check runs GitHub reported for a commit (none when there is no CI).
	ListCommitCheckRuns(ctx context.Context, accessToken, owner, name, sha string) ([]entity.CheckRun, error)
	// ListCommitStatuses lists a commit's legacy statuses, the latest per context (Jenkins, Vercel).
	ListCommitStatuses(ctx context.Context, accessToken, owner, name, sha string) ([]entity.CommitStatus, error)
	// ListPullRequestReviews lists the reviews recorded on a PR (drafts excluded).
	ListPullRequestReviews(ctx context.Context, accessToken, owner, name string, number int) ([]entity.PullRequestReview, error)
}

// PullRequestSnapshotStore stores the PRs read from GitHub when a repository is imported.
type PullRequestSnapshotStore interface {
	GetByRepoAndNumber(ctx context.Context, repoID uint64, number int) (*entity.PullRequest, error)
	Upsert(ctx context.Context, pr *entity.PullRequest) error
}

// CommitStatusStore keeps legacy commit statuses (Jenkins, Vercel), by GitHub's status id. Reading
// a commit's checks (ListCheckRuns) returns its statuses too, shaped like checks.
type CommitStatusStore interface {
	UpsertCommitStatus(ctx context.Context, status entity.CommitStatus) error
	GetCommitStatus(ctx context.Context, id int64) (*entity.CommitStatus, error)
}

// CheckRunRecorder keeps the check runs read at import so later check_run webhooks converge.
type CheckRunRecorder interface {
	CommitStatusStore
	UpsertCheckRun(ctx context.Context, run entity.CheckRun) error
	GetCheckRun(ctx context.Context, id int64) (*entity.CheckRun, error)
	ListCheckRuns(ctx context.Context, repositoryID uint64, headSHA string) ([]entity.CheckRun, error)
}

// RepositoryAccessStore updates repository access from installation webhooks (ADR-0010).
type RepositoryAccessStore interface {
	GetByGitHubRepoID(ctx context.Context, ghRepoID int64) (*entity.Repository, error)
	ListByInstallation(ctx context.Context, installationID int64) ([]entity.Repository, error)
	SaveAccess(ctx context.Context, repo *entity.Repository) error
}

// FollowChecker scopes live events to the repositories a user follows.
type FollowChecker interface {
	IsFollowing(ctx context.Context, userID, repositoryID uint64) (bool, error)
}

// CheckRunStore keeps CI check runs and finds the PRs a commit belongs to.
type CheckRunStore interface {
	CommitStatusStore
	UpsertCheckRun(ctx context.Context, run entity.CheckRun) error
	GetCheckRun(ctx context.Context, id int64) (*entity.CheckRun, error)
	ListCheckRuns(ctx context.Context, repositoryID uint64, headSHA string) ([]entity.CheckRun, error)
	ListOpenByHeadSHA(ctx context.Context, repositoryID uint64, headSHA string) ([]entity.PullRequest, error)
}

// FollowedRepositories lists the repositories a user follows.
type FollowedRepositories interface {
	ListFollowedByUser(ctx context.Context, userID uint64) ([]entity.Repository, error)
}

// PullRequestHeads tells the stored head commit of a PR, when PR Follower knows it.
type PullRequestHeads interface {
	HeadSHA(ctx context.Context, repositoryID uint64, number int) (string, bool)
}

type RepositoryRepository interface {
	Upsert(ctx context.Context, repo *entity.Repository) error
	GetByID(ctx context.Context, id uint64) (*entity.Repository, error)
	GetByGitHubRepoID(ctx context.Context, ghRepoID int64) (*entity.Repository, error)
	ListFollowedByUser(ctx context.Context, userID uint64) ([]entity.Repository, error)
	SetUserFollow(ctx context.Context, userID, repoID uint64, follow bool) error
}

type PullRequestRepository interface {
	Upsert(ctx context.Context, pr *entity.PullRequest) error
	GetByID(ctx context.Context, id uint64) (*entity.PullRequest, error)
	GetByRepoAndNumber(ctx context.Context, repoID uint64, number int) (*entity.PullRequest, error)
	ListOpenByRepoIDs(ctx context.Context, repoIDs []uint64) ([]entity.PullRequest, error)
	SaveAnalysis(ctx context.Context, analysis *entity.DependabotAnalysis) error
	GetAnalysisByPRAndSHA(ctx context.Context, prID uint64, headSHA string) (*entity.DependabotAnalysis, error)
	UpsertUserPRState(ctx context.Context, state *entity.UserPRState) error
	GetUserPRState(ctx context.Context, prID, userID uint64) (*entity.UserPRState, error)
	ListUserPRStates(ctx context.Context, userID uint64, prIDs []uint64) (map[uint64]entity.UserPRState, error)
}

type GitHubGateway interface {
	GetFileContent(ctx context.Context, repo entity.Repository, ref, filePath string) ([]byte, error)
	ListPRChangedFiles(ctx context.Context, repo entity.Repository, prNumber int) ([]entity.ChangedFile, error)
	StreamRepoTarball(ctx context.Context, repo entity.Repository, ref string) (io.ReadCloser, error)
	// The writes act as the user whose accessToken is given (never as the app). An outcome GitHub
	// did not answer for is entity.ErrOutcomeUnknown; a refusal is ErrForbidden, ErrNotFound or
	// ErrConflict with GitHub's explanation.
	// ApprovePullRequest returns the review GitHub recorded.
	ApprovePullRequest(ctx context.Context, accessToken string, repo entity.Repository, prNumber int, body string) (entity.PullRequestReview, error)
	MergePullRequest(ctx context.Context, accessToken string, repo entity.Repository, prNumber int, method string) error
	CreatePRComment(ctx context.Context, accessToken string, repo entity.Repository, prNumber int, comment string) error
}

// PullRequestStateReader reads a PR's live state and reviews with a user's token, so an action
// can check what GitHub says now (and confirm an ambiguous outcome) before and after writing.
type PullRequestStateReader interface {
	GetPullRequest(ctx context.Context, accessToken, owner, name string, number int) (entity.RemotePullRequestState, error)
	ListPullRequestReviews(ctx context.Context, accessToken, owner, name string, number int) ([]entity.PullRequestReview, error)
	// ListCommitCheckRuns and ListCommitStatuses read what GitHub shows as a commit's checks right now.
	ListCommitCheckRuns(ctx context.Context, accessToken, owner, name, sha string) ([]entity.CheckRun, error)
	ListCommitStatuses(ctx context.Context, accessToken, owner, name, sha string) ([]entity.CommitStatus, error)
}

type DependencyAnalysisInput struct {
	PullRequest  entity.PullRequest
	Repository   entity.Repository
	PackageName  string
	FromVersion  string
	ToVersion    string
	PackageJSON  []byte
	LockfileName string
	LockfileRaw  []byte
	Advisories   []entity.AdvisoryAndNote
}

type DependencyAnalyzer interface {
	Ecosystem() string
	CanAnalyze(lockfileName string) bool
	AnalyzeLockfile(ctx context.Context, input DependencyAnalysisInput) (*entity.DependabotAnalysis, error)
}

type BlastRadiusScanner interface {
	ScanTarball(ctx context.Context, tarballStream io.Reader, targetPackages []string) ([]entity.BlastRadiusUsage, error)
}

type EventPublisher interface {
	Publish(ctx context.Context, event entity.ActivityEvent) error
}

// ChangedFilesStore keeps the files each PR changes at its head commit (one row per PR).
type ChangedFilesStore interface {
	SaveChangedFiles(ctx context.Context, files entity.PRChangedFiles) error
	ListChangedFiles(ctx context.Context, prIDs []uint64) (map[uint64]entity.PRChangedFiles, error)
}

// ReviewStore keeps the reviews GitHub recorded, by GitHub's review id.
type ReviewStore interface {
	// UpsertReview stores a review and reports whether anything changed (false on a redelivery).
	UpsertReview(ctx context.Context, review entity.PullRequestReview) (bool, error)
	ListReviews(ctx context.Context, prIDs []uint64) (map[uint64][]entity.PullRequestReview, error)
}

// CheckRunBatchReader reads the check runs of many commits in one query: every run whose repository
// and commit are both among those given (callers group the result by repository and commit).
type CheckRunBatchReader interface {
	ListCheckRunsForCommits(ctx context.Context, repositoryIDs []uint64, headSHAs []string) ([]entity.CheckRun, error)
}

// RepositoryFollowers lists who follows a repository.
type RepositoryFollowers interface {
	ListFollowerIDs(ctx context.Context, repositoryID uint64) ([]uint64, error)
}

// PullRequestFilesReader reads a PR's changed files from GitHub with a user token.
type PullRequestFilesReader interface {
	ListPullRequestFiles(ctx context.Context, accessToken, owner, name string, number int) ([]entity.ChangedFile, error)
}

// ChangedFilesSyncer makes sure a PR's changed files are stored for its current head commit.
type ChangedFilesSyncer interface {
	// SyncWithToken reads them with the given user token.
	SyncWithToken(ctx context.Context, accessToken string, repo entity.Repository, pr entity.PullRequest) error
	// SyncAsUser reads them with that user's token.
	SyncAsUser(ctx context.Context, userID uint64, repo entity.Repository, pr entity.PullRequest) error
	// SyncAsFollower reads them with the token of any user who follows the repository.
	SyncAsFollower(ctx context.Context, repo entity.Repository, pr entity.PullRequest) error
}

// PullRequestAnalyzer analyses a Dependabot PR at its head commit and stores the result.
type PullRequestAnalyzer interface {
	AnalyzePR(ctx context.Context, prID uint64) (*entity.DependabotAnalysis, error)
}

// AnalysisScheduler asks for a PR to be analysed in the background. It never blocks and never
// fails the caller: the work is best effort and can always be asked for again.
type AnalysisScheduler interface {
	ScheduleAnalysis(ctx context.Context, prID uint64)
}

// RepositoryFileReader reads a file from a repository's default branch with the given token (a
// user's, or an installation's): entity.ErrNotFound when it does not exist.
type RepositoryFileReader interface {
	GetRepositoryFile(ctx context.Context, accessToken, owner, name, path string) ([]byte, error)
}

// CodeownersRefresher re-reads a repository's CODEOWNERS and updates the cached rules.
type CodeownersRefresher interface {
	RefreshCodeowners(ctx context.Context, repo entity.Repository) error
}
