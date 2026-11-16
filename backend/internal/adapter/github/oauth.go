package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// OAuthConfig configures the GitHub App user-authorization client (ADR-0010).
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	WebBaseURL   string // default https://github.com
	APIBaseURL   string // default https://api.github.com
	HTTPClient   *http.Client
	Backoff      resilience.BackoffConfig
	Now          func() time.Time
}

// OAuthClient implements port.GitHubOAuth with the web application flow + PKCE.
type OAuthClient struct {
	cfg OAuthConfig
}

func NewOAuthClient(cfg OAuthConfig) *OAuthClient {
	cfg.WebBaseURL = strings.TrimRight(cfg.WebBaseURL, "/")
	if cfg.WebBaseURL == "" {
		cfg.WebBaseURL = "https://github.com"
	}
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
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &OAuthClient{cfg: cfg}
}

func (c *OAuthClient) AuthorizeURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", c.cfg.RedirectURL)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return c.cfg.WebBaseURL + "/login/oauth/authorize?" + q.Encode()
}

// ExchangeCode is not retried: an authorization code is single-use.
func (c *OAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (entity.OAuthToken, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	form.Set("redirect_uri", c.cfg.RedirectURL)
	return c.requestToken(ctx, form)
}

func (c *OAuthClient) RefreshToken(ctx context.Context, refreshToken string) (entity.OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return c.requestToken(ctx, form)
}

type tokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

func (c *OAuthClient) requestToken(ctx context.Context, form url.Values) (entity.OAuthToken, error) {
	if err := ctx.Err(); err != nil {
		return entity.OAuthToken{}, context.Cause(ctx)
	}
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WebBaseURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return entity.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return entity.OAuthToken{}, context.Cause(ctx)
		}
		return entity.OAuthToken{}, noAnswer(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return entity.OAuthToken{}, fmt.Errorf("%w: github token endpoint returned %d", entity.ErrGitHubUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return entity.OAuthToken{}, fmt.Errorf("%w: github token endpoint returned %d", entity.ErrUnauthorized, resp.StatusCode)
	}

	var body tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return entity.OAuthToken{}, fmt.Errorf("%w: unreadable github token response: %v", entity.ErrGitHubUnavailable, err)
	}
	// GitHub reports OAuth failures as 200 with an "error" field.
	if body.Error != "" {
		return entity.OAuthToken{}, fmt.Errorf("%w: github oauth error %s: %s", entity.ErrUnauthorized, body.Error, body.ErrorDescription)
	}
	if body.AccessToken == "" {
		return entity.OAuthToken{}, fmt.Errorf("%w: github token response has no access_token", entity.ErrGitHubUnavailable)
	}

	now := c.cfg.Now()
	token := entity.OAuthToken{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken}
	if body.ExpiresIn > 0 {
		token.AccessTokenExpiresAt = now.Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	if body.RefreshTokenExpiresIn > 0 {
		token.RefreshTokenExpiresAt = now.Add(time.Duration(body.RefreshTokenExpiresIn) * time.Second)
	}
	return token, nil
}

// FetchProfile reads GET /user. Teams come from GET /user/teams when the app has the org
// Members permission; a failure there leaves Teams empty instead of failing sign-in.
func (c *OAuthClient) FetchProfile(ctx context.Context, accessToken string) (entity.GitHubProfile, error) {
	if err := ctx.Err(); err != nil {
		return entity.GitHubProfile{}, context.Cause(ctx)
	}
	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := c.getJSON(ctx, accessToken, "/user", &user); err != nil {
		return entity.GitHubProfile{}, err
	}

	profile := entity.GitHubProfile{ID: user.ID, Login: user.Login, Name: user.Name, AvatarURL: user.AvatarURL}

	var teams []struct {
		Slug         string `json:"slug"`
		Organization struct {
			Login string `json:"login"`
		} `json:"organization"`
	}
	if err := c.getJSON(ctx, accessToken, "/user/teams?per_page=100", &teams); err == nil {
		for _, t := range teams {
			profile.Teams = append(profile.Teams, "@"+t.Organization.Login+"/"+t.Slug)
		}
	}
	return profile, nil
}

func (c *OAuthClient) getJSON(ctx context.Context, accessToken, path string, out any) error {
	return getUserJSON(ctx, c.cfg.HTTPClient, c.cfg.Backoff, c.cfg.APIBaseURL, accessToken, path, out)
}
