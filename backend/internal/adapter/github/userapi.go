package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

const (
	userAPIPerPage  = 100
	userAPIMaxPages = 50 // 5,000 repositories per installation
)

type UserClientConfig struct {
	APIBaseURL string // default https://api.github.com
	HTTPClient *http.Client
	Backoff    resilience.BackoffConfig
}

// UserClient implements port.GitHubUserAPI: calls made with a user's access token, so GitHub
// only returns what both the user and the app can reach (ADR-0010).
type UserClient struct {
	cfg UserClientConfig
}

func NewUserClient(cfg UserClientConfig) *UserClient {
	cfg.APIBaseURL = strings.TrimRight(cfg.APIBaseURL, "/")
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = "https://api.github.com"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Backoff.MaxAttempts <= 0 {
		cfg.Backoff = resilience.DefaultBackoffConfig()
	}
	return &UserClient{cfg: cfg}
}

func (c *UserClient) ListInstallations(ctx context.Context, accessToken string) ([]entity.Installation, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	type installation struct {
		ID      int64 `json:"id"`
		Account struct {
			Login     string `json:"login"`
			Type      string `json:"type"`
			AvatarURL string `json:"avatar_url"`
		} `json:"account"`
		RepositorySelection string `json:"repository_selection"`
		HTMLURL             string `json:"html_url"`
	}
	var out []entity.Installation
	err := c.paginate(ctx, accessToken, "/user/installations", func(raw json.RawMessage) (int, int, error) {
		var page struct {
			TotalCount    int            `json:"total_count"`
			Installations []installation `json:"installations"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, 0, err
		}
		for _, in := range page.Installations {
			out = append(out, entity.Installation{
				ID:                  in.ID,
				AccountLogin:        in.Account.Login,
				AccountType:         in.Account.Type,
				AccountAvatarURL:    in.Account.AvatarURL,
				RepositorySelection: in.RepositorySelection,
				SettingsURL:         in.HTMLURL,
			})
		}
		return page.TotalCount, len(page.Installations), nil
	})
	return out, err
}

func (c *UserClient) ListInstallationRepositories(ctx context.Context, accessToken string, installationID int64) ([]entity.InstallationRepository, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	type repository struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	var out []entity.InstallationRepository
	path := fmt.Sprintf("/user/installations/%d/repositories", installationID)
	err := c.paginate(ctx, accessToken, path, func(raw json.RawMessage) (int, int, error) {
		var page struct {
			TotalCount   int          `json:"total_count"`
			Repositories []repository `json:"repositories"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, 0, err
		}
		for _, r := range page.Repositories {
			out = append(out, entity.InstallationRepository{
				GitHubRepoID:   r.ID,
				InstallationID: installationID,
				Owner:          r.Owner.Login,
				Name:           r.Name,
				FullName:       r.FullName,
				DefaultBranch:  r.DefaultBranch,
				Private:        r.Private,
			})
		}
		return page.TotalCount, len(page.Repositories), nil
	})
	return out, err
}

// GetRepositoryFile reads a file's raw content with the user's token; a missing file is
// entity.ErrNotFound.
func (c *UserClient) GetRepositoryFile(ctx context.Context, accessToken, owner, name, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	target := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.cfg.APIBaseURL, owner, name, strings.TrimPrefix(path, "/"))
	return resilience.Retry(ctx, c.cfg.Backoff, func(reqCtx context.Context) ([]byte, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
		if err != nil {
			return nil, resilience.Permanent(err)
		}
		req.Header.Set("Accept", "application/vnd.github.raw+json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := c.cfg.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, resilience.Permanent(fmt.Errorf("%w: github rejected the user token", entity.ErrUnauthorized))
		}
		if err := classifyHTTPStatus(resp); err != nil {
			return nil, err
		}
		return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	})
}

// prFilesMaxPages is GitHub's own cap: the files endpoint lists at most 3,000 files.
const prFilesMaxPages = 30

// ListPullRequestFiles lists a PR's changed files with the user's token. GitHub omits the
// patch of large or binary files; those come back without one.
func (c *UserClient) ListPullRequestFiles(ctx context.Context, accessToken, owner, name string, number int) ([]entity.ChangedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	var out []entity.ChangedFile
	for page := 1; page <= prFilesMaxPages; page++ {
		var raw []struct {
			Filename  string `json:"filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
			Patch     string `json:"patch"`
		}
		target := fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=%d&page=%d", owner, name, number, userAPIPerPage, page)
		if err := getUserJSON(ctx, c.cfg.HTTPClient, c.cfg.Backoff, c.cfg.APIBaseURL, accessToken, target, &raw); err != nil {
			return nil, err
		}
		for _, f := range raw {
			out = append(out, entity.ChangedFile{Path: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch})
		}
		if len(raw) < userAPIPerPage {
			break
		}
	}
	return out, nil
}

// openPRsMaxPages bounds the backfill of a repository's open PRs (1,000 PRs).
const openPRsMaxPages = 10

// ListOpenPullRequests lists a repository's open PRs with the user's token. The list endpoint
// reports no merge state, so none is returned (it stays unknown until GitHub sends a webhook).
func (c *UserClient) ListOpenPullRequests(ctx context.Context, accessToken, owner, name string) ([]entity.RemotePullRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	var out []entity.RemotePullRequest
	for page := 1; page <= openPRsMaxPages; page++ {
		var raw []struct {
			ID      int64  `json:"id"`
			Number  int    `json:"number"`
			Title   string `json:"title"`
			HTMLURL string `json:"html_url"`
			Draft   bool   `json:"draft"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		target := fmt.Sprintf("/repos/%s/%s/pulls?state=open&per_page=%d&page=%d", owner, name, userAPIPerPage, page)
		if err := getUserJSON(ctx, c.cfg.HTTPClient, c.cfg.Backoff, c.cfg.APIBaseURL, accessToken, target, &raw); err != nil {
			return nil, err
		}
		for _, p := range raw {
			out = append(out, entity.RemotePullRequest{
				GitHubPRID: p.ID, Number: p.Number, Title: p.Title, AuthorLogin: p.User.Login,
				HTMLURL: p.HTMLURL, IsDraft: p.Draft, HeadSHA: p.Head.SHA, BaseRef: p.Base.Ref,
			})
		}
		if len(raw) < userAPIPerPage {
			break
		}
	}
	return out, nil
}

// prReviewsMaxPages bounds the reviews read for one PR (1,000 reviews).
const prReviewsMaxPages = 10

// ListPullRequestReviews lists the reviews recorded on a PR with the user's token. Draft
// (PENDING) reviews are skipped: they are visible only to their author and decide nothing yet.
func (c *UserClient) ListPullRequestReviews(ctx context.Context, accessToken, owner, name string, number int) ([]entity.PullRequestReview, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	var out []entity.PullRequestReview
	for page := 1; page <= prReviewsMaxPages; page++ {
		var raw []struct {
			ID          int64  `json:"id"`
			State       string `json:"state"`
			CommitID    string `json:"commit_id"`
			SubmittedAt string `json:"submitted_at"`
			User        *struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		target := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=%d&page=%d", owner, name, number, userAPIPerPage, page)
		if err := getUserJSON(ctx, c.cfg.HTTPClient, c.cfg.Backoff, c.cfg.APIBaseURL, accessToken, target, &raw); err != nil {
			return nil, err
		}
		for _, r := range raw {
			state := entity.ReviewStateFromGitHub(r.State)
			if state == entity.ReviewUnknown {
				continue
			}
			review := entity.PullRequestReview{GitHubReviewID: r.ID, State: state, CommitSHA: r.CommitID}
			if r.User != nil {
				review.ReviewerLogin = r.User.Login
			}
			review.SubmittedAt, _ = time.Parse(time.RFC3339, r.SubmittedAt)
			out = append(out, review)
		}
		if len(raw) < userAPIPerPage {
			break
		}
	}
	return out, nil
}

// ListCommitCheckRuns lists the check runs GitHub reported for a commit; a repository without CI
// returns none, never an invented run.
func (c *UserClient) ListCommitCheckRuns(ctx context.Context, accessToken, owner, name, sha string) ([]entity.CheckRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	var out []entity.CheckRun
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", owner, name, sha)
	err := c.paginate(ctx, accessToken, path, func(raw json.RawMessage) (int, int, error) {
		var page struct {
			TotalCount int `json:"total_count"`
			CheckRuns  []struct {
				ID         int64   `json:"id"`
				Name       string  `json:"name"`
				Status     string  `json:"status"`
				Conclusion *string `json:"conclusion"`
				HeadSHA    string  `json:"head_sha"`
			} `json:"check_runs"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, 0, err
		}
		for _, r := range page.CheckRuns {
			run := entity.CheckRun{ID: r.ID, HeadSHA: r.HeadSHA, Name: r.Name, Status: r.Status}
			if r.Conclusion != nil {
				run.Conclusion = *r.Conclusion
			}
			out = append(out, run)
		}
		return page.TotalCount, len(page.CheckRuns), nil
	})
	return out, err
}

// paginate fetches ?per_page=100&page=N until total_count items were seen or a page is short.
// consume decodes one page and returns (total_count, items on this page).
func (c *UserClient) paginate(ctx context.Context, accessToken, path string, consume func(json.RawMessage) (int, int, error)) error {
	seen := 0
	for page := 1; page <= userAPIMaxPages; page++ {
		var raw json.RawMessage
		target := fmt.Sprintf("%s?per_page=%d&page=%d", path, userAPIPerPage, page)
		if err := getUserJSON(ctx, c.cfg.HTTPClient, c.cfg.Backoff, c.cfg.APIBaseURL, accessToken, target, &raw); err != nil {
			return err
		}
		total, count, err := consume(raw)
		if err != nil {
			return fmt.Errorf("decode github page %s: %w", target, err)
		}
		seen += count
		if count < userAPIPerPage || seen >= total {
			return nil
		}
	}
	return nil
}

// getUserJSON performs a retried GET with a user access token. 401 means the token was
// revoked or expired (entity.ErrUnauthorized); 404 maps to entity.ErrNotFound.
func getUserJSON(ctx context.Context, client *http.Client, backoff resilience.BackoffConfig, apiBase, accessToken, path string, out any) error {
	_, err := resilience.Retry(ctx, backoff, func(reqCtx context.Context) (struct{}, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiBase+path, nil)
		if err != nil {
			return struct{}{}, resilience.Permanent(err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := client.Do(req)
		if err != nil {
			return struct{}{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return struct{}{}, resilience.Permanent(fmt.Errorf("%w: github rejected the user token", entity.ErrUnauthorized))
		}
		if err := classifyHTTPStatus(resp); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	})
	return err
}
