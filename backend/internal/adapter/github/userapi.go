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
