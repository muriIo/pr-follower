package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence/sqlcgen"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

type UserRepository struct {
	q      *sqlcgen.Queries
	cipher port.TokenCipher
}

// NewUserRepository stores users with their GitHub tokens encrypted by cipher (ADR-0010).
func NewUserRepository(db *sql.DB, cipher port.TokenCipher) *UserRepository {
	return &UserRepository{q: sqlcgen.New(db), cipher: cipher}
}

func (r *UserRepository) Upsert(ctx context.Context, user *entity.User) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	teamsBytes, _ := json.Marshal(user.Teams)
	if len(teamsBytes) == 0 || string(teamsBytes) == "null" {
		teamsBytes = []byte("[]")
	}

	encAccess, err := r.cipher.Encrypt(user.GitHubToken.AccessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}
	encRefresh, err := r.cipher.Encrypt(user.GitHubToken.RefreshToken)
	if err != nil {
		return fmt.Errorf("encrypt refresh token: %w", err)
	}

	row, err := r.q.UpsertUser(ctx, sqlcgen.UpsertUserParams{
		GithubID:              user.GitHubID,
		Login:                 user.Login,
		Name:                  user.Name,
		AvatarUrl:             user.AvatarURL,
		TeamsJson:             string(teamsBytes),
		EncryptedToken:        encAccess,
		EncryptedRefreshToken: encRefresh,
		AccessTokenExpiresAt:  toUnix(user.GitHubToken.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: toUnix(user.GitHubToken.RefreshTokenExpiresAt),
	})
	if err != nil {
		return err
	}
	user.ID = uint64(row.ID)
	user.CreatedAt = row.CreatedAt
	user.UpdatedAt = row.UpdatedAt
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id uint64) (*entity.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetUserByID(ctx, int64(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return r.toDomainUser(row), nil
}

func (r *UserRepository) GetByGitHubID(ctx context.Context, ghID int64) (*entity.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetUserByGitHubID(ctx, ghID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return r.toDomainUser(row), nil
}

// toDomainUser decrypts the stored tokens. A token that no longer decrypts (for example after
// TOKEN_ENCRYPTION_KEY rotation) is dropped, which sends the user back through GitHub sign-in.
func (r *UserRepository) toDomainUser(m sqlcgen.User) *entity.User {
	var teams []string
	if m.TeamsJson != "" {
		_ = json.Unmarshal([]byte(m.TeamsJson), &teams)
	}
	token := entity.OAuthToken{
		AccessTokenExpiresAt:  fromUnix(m.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: fromUnix(m.RefreshTokenExpiresAt),
	}
	access, accessErr := r.cipher.Decrypt(m.EncryptedToken)
	refresh, refreshErr := r.cipher.Decrypt(m.EncryptedRefreshToken)
	if accessErr != nil || refreshErr != nil {
		token = entity.OAuthToken{}
	} else {
		token.AccessToken = access
		token.RefreshToken = refresh
	}
	return &entity.User{
		ID:          uint64(m.ID),
		GitHubID:    m.GithubID,
		Login:       m.Login,
		Name:        m.Name,
		AvatarURL:   m.AvatarUrl,
		Teams:       teams,
		GitHubToken: token,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

func toUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

type SessionRepository struct {
	q *sqlcgen.Queries
}

func NewSessionRepository(db *sql.DB) *SessionRepository {
	return &SessionRepository{q: sqlcgen.New(db)}
}

func (r *SessionRepository) Create(ctx context.Context, session *entity.Session) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return r.q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		TokenHash: session.TokenHash,
		UserID:    int64(session.UserID),
		ExpiresAt: toUnix(session.ExpiresAt),
	})
}

func (r *SessionRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*entity.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return &entity.Session{
		TokenHash: row.TokenHash,
		UserID:    uint64(row.UserID),
		ExpiresAt: fromUnix(row.ExpiresAt),
		CreatedAt: row.CreatedAt,
	}, nil
}

func (r *SessionRepository) Delete(ctx context.Context, tokenHash string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return r.q.DeleteSession(ctx, tokenHash)
}

type RepositoryRepository struct {
	q *sqlcgen.Queries
}

func NewRepositoryRepository(db *sql.DB) *RepositoryRepository {
	return &RepositoryRepository{q: sqlcgen.New(db)}
}

func (r *RepositoryRepository) Upsert(ctx context.Context, repo *entity.Repository) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	rulesBytes, _ := json.Marshal(repo.CachedCodeownersRules)
	if len(rulesBytes) == 0 || string(rulesBytes) == "null" {
		rulesBytes = []byte("[]")
	}

	row, err := r.q.UpsertRepository(ctx, sqlcgen.UpsertRepositoryParams{
		GithubRepoID:     repo.GitHubRepoID,
		InstallationID:   repo.InstallationID,
		Owner:            repo.Owner,
		Name:             repo.Name,
		FullName:         repo.FullName,
		DefaultBranch:    repo.DefaultBranch,
		CodeownersJson:   string(rulesBytes),
		Access:           string(accessOrActive(repo.Access)),
		CodeownersStatus: string(repo.CodeownersStatus),
	})
	if err != nil {
		return err
	}
	repo.ID = uint64(row.ID)
	repo.Access = entity.RepositoryAccess(row.Access)
	repo.CreatedAt = row.CreatedAt
	repo.UpdatedAt = row.UpdatedAt
	return nil
}

// ListByInstallation returns every repository attached to a GitHub App installation.
func (r *RepositoryRepository) ListByInstallation(ctx context.Context, installationID int64) ([]entity.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	rows, err := r.q.ListRepositoriesByInstallation(ctx, installationID)
	if err != nil {
		return nil, err
	}
	out := make([]entity.Repository, 0, len(rows))
	for _, row := range rows {
		out = append(out, *toDomainRepo(row))
	}
	return out, nil
}

// IsFollowing reports whether the user follows the repository; live events are scoped by it.
func (r *RepositoryRepository) IsFollowing(ctx context.Context, userID, repositoryID uint64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, context.Cause(ctx)
	}
	n, err := r.q.CountUserRepositoryFollow(ctx, sqlcgen.CountUserRepositoryFollowParams{
		UserID:       int64(userID),
		RepositoryID: int64(repositoryID),
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SaveAccess persists a repository's access state and installation (ADR-0010).
func (r *RepositoryRepository) SaveAccess(ctx context.Context, repo *entity.Repository) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return r.q.UpdateRepositoryAccess(ctx, sqlcgen.UpdateRepositoryAccessParams{
		ID:             int64(repo.ID),
		InstallationID: repo.InstallationID,
		Access:         string(accessOrActive(repo.Access)),
	})
}

func accessOrActive(access entity.RepositoryAccess) entity.RepositoryAccess {
	if access == "" {
		return entity.RepositoryAccessActive
	}
	return access
}

func (r *RepositoryRepository) GetByID(ctx context.Context, id uint64) (*entity.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetRepositoryByID(ctx, int64(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return toDomainRepo(row), nil
}

func (r *RepositoryRepository) GetByGitHubRepoID(ctx context.Context, ghRepoID int64) (*entity.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetRepositoryByGitHubRepoID(ctx, ghRepoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return toDomainRepo(row), nil
}

func (r *RepositoryRepository) ListFollowedByUser(ctx context.Context, userID uint64) ([]entity.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	rows, err := r.q.ListFollowedRepositoriesByUser(ctx, int64(userID))
	if err != nil {
		return nil, err
	}
	out := make([]entity.Repository, 0, len(rows))
	for _, row := range rows {
		out = append(out, *toDomainRepo(row))
	}
	return out, nil
}

// ListFollowerIDs lists the users who follow a repository, oldest account first.
func (r *RepositoryRepository) ListFollowerIDs(ctx context.Context, repositoryID uint64) ([]uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	ids, err := r.q.ListRepositoryFollowerIDs(ctx, int64(repositoryID))
	if err != nil {
		return nil, err
	}
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		out = append(out, uint64(id))
	}
	return out, nil
}

func (r *RepositoryRepository) SetUserFollow(ctx context.Context, userID, repoID uint64, follow bool) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if !follow {
		return r.q.UnfollowRepository(ctx, sqlcgen.UnfollowRepositoryParams{
			UserID:       int64(userID),
			RepositoryID: int64(repoID),
		})
	}
	return r.q.FollowRepository(ctx, sqlcgen.FollowRepositoryParams{
		UserID:       int64(userID),
		RepositoryID: int64(repoID),
	})
}

func toDomainRepo(m sqlcgen.Repository) *entity.Repository {
	var rules []entity.CodeownerRule
	if m.CodeownersJson != "" {
		_ = json.Unmarshal([]byte(m.CodeownersJson), &rules)
	}
	return &entity.Repository{
		ID:                    uint64(m.ID),
		GitHubRepoID:          m.GithubRepoID,
		InstallationID:        m.InstallationID,
		Owner:                 m.Owner,
		Name:                  m.Name,
		FullName:              m.FullName,
		DefaultBranch:         m.DefaultBranch,
		CachedCodeownersRules: rules,
		Access:                entity.RepositoryAccess(m.Access),
		CodeownersStatus:      entity.CodeownersStatus(m.CodeownersStatus),
		CreatedAt:             m.CreatedAt,
		UpdatedAt:             m.UpdatedAt,
	}
}

type PullRequestRepository struct {
	db *sql.DB
	q  *sqlcgen.Queries
}

func NewPullRequestRepository(db *sql.DB) *PullRequestRepository {
	return &PullRequestRepository{
		db: db,
		q:  sqlcgen.New(db),
	}
}

func (r *PullRequestRepository) Upsert(ctx context.Context, pr *entity.PullRequest) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	var claimedID sql.NullInt64
	if pr.ClaimedByUserID != nil {
		claimedID = sql.NullInt64{Int64: int64(*pr.ClaimedByUserID), Valid: true}
	}

	row, err := r.q.UpsertPullRequest(ctx, sqlcgen.UpsertPullRequestParams{
		RepositoryID:    int64(pr.RepositoryID),
		RepositoryName:  pr.RepositoryName,
		GithubPrID:      pr.GitHubPRID,
		Number:          int64(pr.Number),
		Title:           pr.Title,
		AuthorLogin:     pr.AuthorLogin,
		HtmlUrl:         pr.HTMLURL,
		IsDependabot:    pr.IsDependabot,
		IsDraft:         pr.IsDraft,
		State:           string(pr.State),
		MergeableState:  string(pr.MergeableState),
		CiStatus:        string(pr.CIStatus),
		HeadSha:         pr.HeadSHA,
		BaseRef:         pr.BaseRef,
		ClaimedByUserID: claimedID,
		ClaimedByLogin:  pr.ClaimedByLogin,
	})
	if err != nil {
		return err
	}
	pr.ID = uint64(row.ID)
	pr.UpdatedAt = row.UpdatedAt
	return nil
}

func (r *PullRequestRepository) GetByID(ctx context.Context, id uint64) (*entity.PullRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetPullRequestByID(ctx, int64(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return toDomainPR(row), nil
}

func (r *PullRequestRepository) GetByRepoAndNumber(ctx context.Context, repoID uint64, number int) (*entity.PullRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetPullRequestByRepoAndNumber(ctx, sqlcgen.GetPullRequestByRepoAndNumberParams{
		RepositoryID: int64(repoID),
		Number:       int64(number),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return toDomainPR(row), nil
}

// HeadSHA returns the stored head commit of a PR, if PR Follower has seen it.
func (r *PullRequestRepository) HeadSHA(ctx context.Context, repositoryID uint64, number int) (string, bool) {
	pr, err := r.GetByRepoAndNumber(ctx, repositoryID, number)
	if err != nil || pr.HeadSHA == "" {
		return "", false
	}
	return pr.HeadSHA, true
}

// ListOpenByHeadSHA returns the open PRs of a repository whose head is the given commit.
func (r *PullRequestRepository) ListOpenByHeadSHA(ctx context.Context, repositoryID uint64, headSHA string) ([]entity.PullRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	rows, err := r.q.ListOpenPullRequestsByHead(ctx, sqlcgen.ListOpenPullRequestsByHeadParams{
		RepositoryID: int64(repositoryID),
		HeadSha:      headSHA,
	})
	if err != nil {
		return nil, err
	}
	out := make([]entity.PullRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, *toDomainPR(row))
	}
	return out, nil
}

// UpsertCheckRun stores a check run by its GitHub id (idempotent, ADR-0011).
func (r *PullRequestRepository) UpsertCheckRun(ctx context.Context, run entity.CheckRun) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return r.q.UpsertCheckRun(ctx, sqlcgen.UpsertCheckRunParams{
		CheckRunID:   run.ID,
		RepositoryID: int64(run.RepositoryID),
		HeadSha:      run.HeadSHA,
		Name:         run.Name,
		Status:       run.Status,
		Conclusion:   run.Conclusion,
		Title:        run.Title,
		DetailsUrl:   run.DetailsURL,
	})
}

func (r *PullRequestRepository) GetCheckRun(ctx context.Context, id int64) (*entity.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetCheckRun(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return &entity.CheckRun{ID: row.CheckRunID, RepositoryID: uint64(row.RepositoryID), HeadSHA: row.HeadSha, Name: row.Name, Status: row.Status, Conclusion: row.Conclusion, Title: row.Title, DetailsURL: row.DetailsUrl}, nil
}

// ListCheckRuns returns everything GitHub lists as a check of one commit: its check runs and its
// legacy commit statuses (shaped like checks, marked by Source).
func (r *PullRequestRepository) ListCheckRuns(ctx context.Context, repositoryID uint64, headSHA string) ([]entity.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	rows, err := r.q.ListCheckRunsByHead(ctx, sqlcgen.ListCheckRunsByHeadParams{RepositoryID: int64(repositoryID), HeadSha: headSHA})
	if err != nil {
		return nil, err
	}
	out := make([]entity.CheckRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, entity.CheckRun{ID: row.CheckRunID, RepositoryID: uint64(row.RepositoryID), HeadSHA: row.HeadSha, Name: row.Name, Status: row.Status, Conclusion: row.Conclusion, Title: row.Title, DetailsURL: row.DetailsUrl, Source: entity.CheckSourceCheckRun})
	}
	statuses, err := r.q.ListCommitStatusesByHead(ctx, sqlcgen.ListCommitStatusesByHeadParams{RepositoryID: int64(repositoryID), HeadSha: headSHA})
	if err != nil {
		return nil, err
	}
	for _, row := range statuses {
		out = append(out, statusFromRow(row.StatusID, row.RepositoryID, row.HeadSha, row.Context, row.State, row.Description, row.TargetUrl).AsCheckRun())
	}
	return out, nil
}

func statusFromRow(id, repoID int64, sha, context, state, description, targetURL string) entity.CommitStatus {
	return entity.CommitStatus{ID: id, RepositoryID: uint64(repoID), HeadSHA: sha, Context: context, State: state, Description: description, TargetURL: targetURL}
}

// UpsertCommitStatus stores a commit status by its GitHub id (idempotent, ADR-0011).
func (r *PullRequestRepository) UpsertCommitStatus(ctx context.Context, s entity.CommitStatus) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return r.q.UpsertCommitStatus(ctx, sqlcgen.UpsertCommitStatusParams{
		StatusID: s.ID, RepositoryID: int64(s.RepositoryID), HeadSha: s.HeadSHA, Context: s.Context,
		State: s.State, Description: s.Description, TargetUrl: s.TargetURL,
	})
}

func (r *PullRequestRepository) GetCommitStatus(ctx context.Context, id int64) (*entity.CommitStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetCommitStatus(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	s := statusFromRow(row.StatusID, row.RepositoryID, row.HeadSha, row.Context, row.State, row.Description, row.TargetUrl)
	return &s, nil
}

// SaveChangedFiles stores the files a PR changes at a commit, replacing those of an older commit.
// Patches are dropped: ownership needs only the paths, and the drawer reads patches live.
func (r *PullRequestRepository) SaveChangedFiles(ctx context.Context, files entity.PRChangedFiles) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	slim := make([]entity.ChangedFile, 0, len(files.Files))
	for _, f := range files.Files {
		slim = append(slim, entity.ChangedFile{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions})
	}
	raw, err := json.Marshal(slim)
	if err != nil {
		return err
	}
	return r.q.UpsertPullRequestChangedFiles(ctx, sqlcgen.UpsertPullRequestChangedFilesParams{
		PullRequestID: int64(files.PullRequestID),
		HeadSha:       files.HeadSHA,
		FilesJson:     string(raw),
	})
}

// ListChangedFiles returns the stored changed files of the given PRs; PRs without any are absent.
func (r *PullRequestRepository) ListChangedFiles(ctx context.Context, prIDs []uint64) (map[uint64]entity.PRChangedFiles, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	out := make(map[uint64]entity.PRChangedFiles, len(prIDs))
	if len(prIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(prIDs))
	args := make([]any, 0, len(prIDs))
	for i, id := range prIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, int64(id))
	}
	query := fmt.Sprintf(`SELECT pull_request_id, head_sha, files_json FROM pr_changed_files WHERE pull_request_id IN (%s)`,
		strings.Join(placeholders, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var prID int64
		var headSHA, filesJSON string
		if err := rows.Scan(&prID, &headSHA, &filesJSON); err != nil {
			return nil, err
		}
		files := []entity.ChangedFile{}
		if err := json.Unmarshal([]byte(filesJSON), &files); err != nil {
			return nil, fmt.Errorf("decode changed files of pull request %d: %w", prID, err)
		}
		out[uint64(prID)] = entity.PRChangedFiles{PullRequestID: uint64(prID), HeadSHA: headSHA, Files: files}
	}
	return out, rows.Err()
}

func (r *PullRequestRepository) ListOpenByRepoIDs(ctx context.Context, repoIDs []uint64) ([]entity.PullRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if len(repoIDs) == 0 {
		return []entity.PullRequest{}, nil
	}

	placeholders := make([]string, len(repoIDs))
	args := make([]any, 0, len(repoIDs)+1)
	for i, id := range repoIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, int64(id))
	}
	args = append(args, string(entity.PRStateOpen))

	query := fmt.Sprintf(`
		SELECT id, repository_id, repository_name, github_pr_id, number, title, author_login, html_url,
		       is_dependabot, is_draft, state, mergeable_state, ci_status, head_sha, base_ref,
		       claimed_by_user_id, claimed_by_login, updated_at
		FROM pull_requests
		WHERE repository_id IN (%s) AND state = $%d
		ORDER BY updated_at DESC
	`, strings.Join(placeholders, ","), len(repoIDs)+1)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []entity.PullRequest
	for rows.Next() {
		var m sqlcgen.PullRequest
		if err := rows.Scan(
			&m.ID, &m.RepositoryID, &m.RepositoryName, &m.GithubPrID, &m.Number, &m.Title,
			&m.AuthorLogin, &m.HtmlUrl, &m.IsDependabot, &m.IsDraft, &m.State,
			&m.MergeableState, &m.CiStatus, &m.HeadSha, &m.BaseRef,
			&m.ClaimedByUserID, &m.ClaimedByLogin, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, *toDomainPR(m))
	}
	return out, rows.Err()
}

func (r *PullRequestRepository) SaveAnalysis(ctx context.Context, a *entity.DependabotAnalysis) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	pathBytes, _ := json.Marshal(a.DependencyPathGraph)
	blastBytes, _ := json.Marshal(a.BlastRadiusUsages)
	advBytes, _ := json.Marshal(a.AdvisoriesAndNotes)

	row, err := r.q.UpsertDependabotAnalysis(ctx, sqlcgen.UpsertDependabotAnalysisParams{
		PullRequestID:     int64(a.PullRequestID),
		HeadSha:           a.HeadSHA,
		Ecosystem:         a.Ecosystem,
		PackageName:       a.PackageName,
		FromVersion:       a.FromVersion,
		ToVersion:         a.ToVersion,
		SemverBumpType:    string(a.SemverBumpType),
		IsDirectDep:       a.IsDirectDep,
		DepScope:          string(a.DepScope),
		RiskVerdict:       string(a.RiskVerdict),
		RiskScore:         int64(a.RiskScore),
		VerdictSummaryKey: a.VerdictSummaryKey,
		PathGraphJson:     string(pathBytes),
		BlastRadiusJson:   string(blastBytes),
		AdvisoriesJson:    string(advBytes),
	})
	if err != nil {
		return err
	}
	a.ID = uint64(row.ID)
	a.UpdatedAt = row.UpdatedAt
	return nil
}

func (r *PullRequestRepository) GetAnalysisByPRAndSHA(ctx context.Context, prID uint64, headSHA string) (*entity.DependabotAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetDependabotAnalysisByPRAndSHA(ctx, sqlcgen.GetDependabotAnalysisByPRAndSHAParams{
		PullRequestID: int64(prID),
		HeadSha:       headSHA,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return toDomainAnalysis(row), nil
}

func (r *PullRequestRepository) UpsertUserPRState(ctx context.Context, s *entity.UserPRState) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	ownedBytes, _ := json.Marshal(s.OwnedFilesChanged)

	row, err := r.q.UpsertUserPRState(ctx, sqlcgen.UpsertUserPRStateParams{
		PullRequestID:         int64(s.PullRequestID),
		UserID:                int64(s.UserID),
		TurnState:             string(s.TurnState),
		TeamAlreadyApproved:   s.TeamAlreadyApproved,
		ApprovedByLogin:       s.ApprovedByLogin,
		SnoozedUntilCondition: string(s.SnoozedUntilCondition),
		SnoozedAtSha:          s.SnoozedAtSHA,
		OwnedFilesJson:        string(ownedBytes),
	})
	if err != nil {
		return err
	}
	s.ID = uint64(row.ID)
	s.UpdatedAt = row.UpdatedAt
	return nil
}

func (r *PullRequestRepository) GetUserPRState(ctx context.Context, prID, userID uint64) (*entity.UserPRState, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetUserPRState(ctx, sqlcgen.GetUserPRStateParams{
		PullRequestID: int64(prID),
		UserID:        int64(userID),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, entity.ErrNotFound
		}
		return nil, err
	}
	return toDomainUserPRState(row), nil
}

func (r *PullRequestRepository) ListUserPRStates(ctx context.Context, userID uint64, prIDs []uint64) (map[uint64]entity.UserPRState, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	out := make(map[uint64]entity.UserPRState, len(prIDs))
	if len(prIDs) == 0 {
		return out, nil
	}

	placeholders := make([]string, len(prIDs))
	args := make([]any, 0, len(prIDs)+1)
	args = append(args, int64(userID))
	for i, id := range prIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, int64(id))
	}

	query := fmt.Sprintf(`
		SELECT id, pull_request_id, user_id, turn_state, team_already_approved, approved_by_login,
		       snoozed_until_condition, snoozed_at_sha, owned_files_json, updated_at
		FROM pr_user_states
		WHERE user_id = $1 AND pull_request_id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var m sqlcgen.PrUserState
		if err := rows.Scan(
			&m.ID, &m.PullRequestID, &m.UserID, &m.TurnState, &m.TeamAlreadyApproved,
			&m.ApprovedByLogin, &m.SnoozedUntilCondition, &m.SnoozedAtSha,
			&m.OwnedFilesJson, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		st := toDomainUserPRState(m)
		out[st.PullRequestID] = *st
	}
	return out, rows.Err()
}

func toDomainPR(m sqlcgen.PullRequest) *entity.PullRequest {
	var claimedID *uint64
	if m.ClaimedByUserID.Valid {
		uid := uint64(m.ClaimedByUserID.Int64)
		claimedID = &uid
	}
	return &entity.PullRequest{
		ID:              uint64(m.ID),
		RepositoryID:    uint64(m.RepositoryID),
		RepositoryName:  m.RepositoryName,
		GitHubPRID:      m.GithubPrID,
		Number:          int(m.Number),
		Title:           m.Title,
		AuthorLogin:     m.AuthorLogin,
		HTMLURL:         m.HtmlUrl,
		IsDependabot:    m.IsDependabot,
		IsDraft:         m.IsDraft,
		State:           entity.PRState(m.State),
		MergeableState:  entity.MergeableState(m.MergeableState),
		CIStatus:        entity.CIStatus(m.CiStatus),
		HeadSHA:         m.HeadSha,
		BaseRef:         m.BaseRef,
		ClaimedByUserID: claimedID,
		ClaimedByLogin:  m.ClaimedByLogin,
		UpdatedAt:       m.UpdatedAt,
	}
}

func toDomainAnalysis(m sqlcgen.DependabotAnalysis) *entity.DependabotAnalysis {
	var pathGraph []entity.DependencyNode
	var blast []entity.BlastRadiusUsage
	var adv []entity.AdvisoryAndNote
	if m.PathGraphJson != "" {
		_ = json.Unmarshal([]byte(m.PathGraphJson), &pathGraph)
	}
	if m.BlastRadiusJson != "" {
		_ = json.Unmarshal([]byte(m.BlastRadiusJson), &blast)
	}
	if m.AdvisoriesJson != "" {
		_ = json.Unmarshal([]byte(m.AdvisoriesJson), &adv)
	}
	return &entity.DependabotAnalysis{
		ID:                  uint64(m.ID),
		PullRequestID:       uint64(m.PullRequestID),
		HeadSHA:             m.HeadSha,
		Ecosystem:           m.Ecosystem,
		PackageName:         m.PackageName,
		FromVersion:         m.FromVersion,
		ToVersion:           m.ToVersion,
		SemverBumpType:      entity.SemverBumpType(m.SemverBumpType),
		IsDirectDep:         m.IsDirectDep,
		DepScope:            entity.DependencyScope(m.DepScope),
		RiskVerdict:         entity.RiskVerdict(m.RiskVerdict),
		RiskScore:           int(m.RiskScore),
		VerdictSummaryKey:   m.VerdictSummaryKey,
		DependencyPathGraph: pathGraph,
		BlastRadiusUsages:   blast,
		AdvisoriesAndNotes:  adv,
		UpdatedAt:           m.UpdatedAt,
	}
}

func toDomainUserPRState(m sqlcgen.PrUserState) *entity.UserPRState {
	var owned []entity.ChangedFile
	if m.OwnedFilesJson != "" {
		_ = json.Unmarshal([]byte(m.OwnedFilesJson), &owned)
	}
	return &entity.UserPRState{
		ID:                    uint64(m.ID),
		PullRequestID:         uint64(m.PullRequestID),
		UserID:                uint64(m.UserID),
		TurnState:             entity.TurnState(m.TurnState),
		TeamAlreadyApproved:   m.TeamAlreadyApproved,
		ApprovedByLogin:       m.ApprovedByLogin,
		SnoozedUntilCondition: entity.SnoozeCondition(m.SnoozedUntilCondition),
		SnoozedAtSHA:          m.SnoozedAtSha,
		OwnedFilesChanged:     owned,
		UpdatedAt:             m.UpdatedAt,
	}
}

// UpsertReview stores a review by its GitHub id and reports whether anything changed. A
// dismissed review stays dismissed (ADR-0011: redelivery and reordering converge).
func (r *PullRequestRepository) UpsertReview(ctx context.Context, review entity.PullRequestReview) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, context.Cause(ctx)
	}
	row, err := r.q.GetPullRequestReview(ctx, review.GitHubReviewID)
	switch {
	case err == nil:
		stored := entity.PullRequestReview{State: entity.ReviewState(row.State), CommitSHA: row.CommitSha}
		if !review.Supersedes(stored) || (stored.State == review.State && stored.CommitSHA == review.CommitSHA) {
			return false, nil
		}
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	err = r.q.UpsertPullRequestReview(ctx, sqlcgen.UpsertPullRequestReviewParams{
		GithubReviewID: review.GitHubReviewID,
		PullRequestID:  int64(review.PullRequestID),
		ReviewerLogin:  review.ReviewerLogin,
		State:          string(review.State),
		CommitSha:      review.CommitSHA,
		SubmittedAt:    review.SubmittedAt,
	})
	return err == nil, err
}

// ListReviews returns the stored reviews of the given PRs, keyed by PR id.
func (r *PullRequestRepository) ListReviews(ctx context.Context, prIDs []uint64) (map[uint64][]entity.PullRequestReview, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	out := make(map[uint64][]entity.PullRequestReview, len(prIDs))
	if len(prIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(prIDs))
	args := make([]any, 0, len(prIDs))
	for i, id := range prIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, int64(id))
	}
	query := fmt.Sprintf(`SELECT github_review_id, pull_request_id, reviewer_login, state, commit_sha, submitted_at
FROM pr_reviews WHERE pull_request_id IN (%s) ORDER BY github_review_id ASC`, strings.Join(placeholders, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var review entity.PullRequestReview
		var prID int64
		var state string
		if err := rows.Scan(&review.GitHubReviewID, &prID, &review.ReviewerLogin, &state, &review.CommitSHA, &review.SubmittedAt); err != nil {
			return nil, err
		}
		review.PullRequestID, review.State = uint64(prID), entity.ReviewState(state)
		out[review.PullRequestID] = append(out[review.PullRequestID], review)
	}
	return out, rows.Err()
}

// ListCheckRunsForCommits returns the check runs of many commits in one query: every run whose
// repository and commit are both among the ones given. Callers group the result by repository and
// commit (a commit id can in principle appear in two repositories).
func (r *PullRequestRepository) ListCheckRunsForCommits(ctx context.Context, repositoryIDs []uint64, headSHAs []string) ([]entity.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if len(repositoryIDs) == 0 || len(headSHAs) == 0 {
		return []entity.CheckRun{}, nil
	}
	args := make([]any, 0, len(repositoryIDs)+len(headSHAs))
	repoMarks := make([]string, len(repositoryIDs))
	for i, id := range repositoryIDs {
		repoMarks[i] = fmt.Sprintf("$%d", len(args)+1)
		args = append(args, int64(id))
	}
	shaMarks := make([]string, len(headSHAs))
	for i, sha := range headSHAs {
		shaMarks[i] = fmt.Sprintf("$%d", len(args)+1)
		args = append(args, sha)
	}
	query := fmt.Sprintf(`SELECT check_run_id, repository_id, head_sha, name, status, conclusion, title, details_url
FROM pr_check_runs WHERE repository_id IN (%s) AND head_sha IN (%s) ORDER BY check_run_id ASC`,
		strings.Join(repoMarks, ","), strings.Join(shaMarks, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []entity.CheckRun{}
	for rows.Next() {
		var run entity.CheckRun
		var repoID int64
		if err := rows.Scan(&run.ID, &repoID, &run.HeadSHA, &run.Name, &run.Status, &run.Conclusion, &run.Title, &run.DetailsURL); err != nil {
			return nil, err
		}
		run.RepositoryID, run.Source = uint64(repoID), entity.CheckSourceCheckRun
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	statusQuery := fmt.Sprintf(`SELECT status_id, repository_id, head_sha, context, state, description, target_url
FROM pr_commit_statuses WHERE repository_id IN (%s) AND head_sha IN (%s) ORDER BY status_id ASC`,
		strings.Join(repoMarks, ","), strings.Join(shaMarks, ","))
	statusRows, err := r.db.QueryContext(ctx, statusQuery, args...)
	if err != nil {
		return nil, err
	}
	defer statusRows.Close()
	for statusRows.Next() {
		var id, repoID int64
		var sha, contextName, state, description, targetURL string
		if err := statusRows.Scan(&id, &repoID, &sha, &contextName, &state, &description, &targetURL); err != nil {
			return nil, err
		}
		out = append(out, statusFromRow(id, repoID, sha, contextName, state, description, targetURL).AsCheckRun())
	}
	return out, statusRows.Err()
}

var _ port.AnalysisFailureStore = (*PullRequestRepository)(nil)

func (r *PullRequestRepository) RecordAnalysisFailure(ctx context.Context, f entity.AnalysisFailure) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return r.q.UpsertAnalysisFailure(ctx, sqlcgen.UpsertAnalysisFailureParams{
		PullRequestID: int64(f.PullRequestID),
		HeadSha:       f.HeadSHA,
		ErrorKind:     f.ErrorKind,
		Message:       f.Message,
		FailedAt:      f.FailedAt.UTC(),
	})
}

func (r *PullRequestRepository) GetAnalysisFailure(ctx context.Context, prID uint64, headSHA string) (*entity.AnalysisFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	row, err := r.q.GetAnalysisFailure(ctx, sqlcgen.GetAnalysisFailureParams{PullRequestID: int64(prID), HeadSha: headSHA})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, entity.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entity.AnalysisFailure{
		PullRequestID: uint64(row.PullRequestID), HeadSHA: row.HeadSha, ErrorKind: row.ErrorKind,
		Message: row.Message, FailedAt: row.FailedAt.UTC(),
	}, nil
}
