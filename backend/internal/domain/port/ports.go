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

// GitHubUserAPI reads what a user can reach through the GitHub App, using their user token.
type GitHubUserAPI interface {
	ListInstallations(ctx context.Context, accessToken string) ([]entity.Installation, error)
	ListInstallationRepositories(ctx context.Context, accessToken string, installationID int64) ([]entity.InstallationRepository, error)
}

// RepositoryAccessStore updates repository access from installation webhooks (ADR-0010).
type RepositoryAccessStore interface {
	GetByGitHubRepoID(ctx context.Context, ghRepoID int64) (*entity.Repository, error)
	ListByInstallation(ctx context.Context, installationID int64) ([]entity.Repository, error)
	SaveAccess(ctx context.Context, repo *entity.Repository) error
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
	ApprovePullRequest(ctx context.Context, repo entity.Repository, prNumber int, body string) error
	MergePullRequest(ctx context.Context, repo entity.Repository, prNumber int, method string) error
	CreatePRComment(ctx context.Context, repo entity.Repository, prNumber int, comment string) error
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
