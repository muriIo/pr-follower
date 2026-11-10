package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

var oauthNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func newOAuthClient(server *httptest.Server) *github.OAuthClient {
	return github.NewOAuthClient(github.OAuthConfig{
		ClientID:     "Iv1.client",
		ClientSecret: "shh",
		RedirectURL:  "http://localhost:4200/api/v1/auth/github/callback",
		WebBaseURL:   server.URL,
		APIBaseURL:   server.URL + "/api",
		HTTPClient:   server.Client(),
		Backoff:      resilience.BackoffConfig{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		Now:          func() time.Time { return oauthNow },
	})
}

func TestOAuthClient_AuthorizeURL(t *testing.T) {
	t.Parallel()
	c := github.NewOAuthClient(github.OAuthConfig{ClientID: "Iv1.client", RedirectURL: "http://localhost:4200/api/v1/auth/github/callback"})

	u, err := url.Parse(c.AuthorizeURL("state-1", "challenge-1"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Scheme+"://"+u.Host+u.Path != "https://github.com/login/oauth/authorize" {
		t.Fatalf("unexpected endpoint: %s", u)
	}
	q := u.Query()
	want := map[string]string{
		"client_id":             "Iv1.client",
		"redirect_uri":          "http://localhost:4200/api/v1/auth/github/callback",
		"state":                 "state-1",
		"code_challenge":        "challenge-1",
		"code_challenge_method": "S256",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
}

func TestOAuthClient_ExchangeCode(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/login/oauth/access_token" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected JSON accept header")
		}
		_ = r.ParseForm()
		if r.PostForm.Get("code") == "bad" {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code", "error_description": "The code passed is incorrect or expired."})
			return
		}
		for k, v := range map[string]string{"client_id": "Iv1.client", "client_secret": "shh", "code": "good", "code_verifier": "verifier",
			"redirect_uri": "http://localhost:4200/api/v1/auth/github/callback"} {
			if r.PostForm.Get(k) != v {
				t.Errorf("form %s = %q, want %q", k, r.PostForm.Get(k), v)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ghu_a", "refresh_token": "ghr_r", "token_type": "bearer",
			"expires_in": 28800, "refresh_token_expires_in": 15897600,
		})
	}))
	defer server.Close()
	c := newOAuthClient(server)

	tok, err := c.ExchangeCode(context.Background(), "good", "verifier")
	if err != nil {
		t.Fatalf("ExchangeCode() error: %v", err)
	}
	if tok.AccessToken != "ghu_a" || tok.RefreshToken != "ghr_r" ||
		!tok.AccessTokenExpiresAt.Equal(oauthNow.Add(8*time.Hour)) ||
		!tok.RefreshTokenExpiresAt.Equal(oauthNow.Add(15897600*time.Second)) {
		t.Fatalf("unexpected token: %#v", tok)
	}

	if _, err := c.ExchangeCode(context.Background(), "bad", "verifier"); err == nil || !strings.Contains(err.Error(), "bad_verification_code") {
		t.Fatalf("expected GitHub OAuth error, got %v", err)
	}
}

func TestOAuthClient_RefreshTokenWithoutExpiry(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "ghr_old" {
			t.Errorf("unexpected refresh form: %v", r.PostForm)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ghu_new", "token_type": "bearer"})
	}))
	defer server.Close()

	tok, err := newOAuthClient(server).RefreshToken(context.Background(), "ghr_old")
	if err != nil || tok.AccessToken != "ghu_new" || !tok.AccessTokenExpiresAt.IsZero() || tok.RefreshToken != "" {
		t.Fatalf("RefreshToken() = %#v, %v", tok, err)
	}
}

func TestOAuthClient_TokenEndpointFailures(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.PostForm.Get("code") {
		case "http500":
			w.WriteHeader(http.StatusInternalServerError)
		case "garbage":
			_, _ = w.Write([]byte("not json"))
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"token_type": "bearer"})
		}
	}))
	defer server.Close()
	c := newOAuthClient(server)

	for _, code := range []string{"http500", "garbage", "empty"} {
		if _, err := c.ExchangeCode(context.Background(), code, "v"); err == nil {
			t.Errorf("code %q: expected error", code)
		}
	}
}

func TestOAuthClient_FetchProfileWithTeams(t *testing.T) {
	t.Parallel()
	var userCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/user":
			if userCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadGateway) // transient: retried
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 583231, "login": "octocat", "name": "The Octocat", "avatar_url": "https://a/u/583231"})
		case "/api/user/teams":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"slug": "web", "organization": map[string]any{"login": "acme"}},
				{"slug": "infra", "organization": map[string]any{"login": "acme"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p, err := newOAuthClient(server).FetchProfile(context.Background(), "ghu_a")
	if err != nil {
		t.Fatalf("FetchProfile() error: %v", err)
	}
	if p.ID != 583231 || p.Login != "octocat" || p.Name != "The Octocat" || p.AvatarURL != "https://a/u/583231" {
		t.Fatalf("unexpected profile: %#v", p)
	}
	if len(p.Teams) != 2 || p.Teams[0] != "@acme/web" || p.Teams[1] != "@acme/infra" {
		t.Fatalf("unexpected teams: %v", p.Teams)
	}
}

func TestOAuthClient_FetchProfileTeamsAreBestEffort(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/teams" {
			w.WriteHeader(http.StatusForbidden) // app lacks org Members permission
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "login": "solo"})
	}))
	defer server.Close()

	p, err := newOAuthClient(server).FetchProfile(context.Background(), "ghu_a")
	if err != nil || p.Login != "solo" || len(p.Teams) != 0 {
		t.Fatalf("FetchProfile() = %#v, %v", p, err)
	}
}

func TestOAuthClient_FetchProfileUnauthorized(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	if _, err := newOAuthClient(server).FetchProfile(context.Background(), "ghu_a"); err == nil {
		t.Fatal("expected error for revoked token")
	}
}

func TestOAuthClient_HonorsCanceledContext(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x"})
	}))
	defer server.Close()
	c := newOAuthClient(server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.ExchangeCode(ctx, "c", "v"); !errors.Is(err, context.Canceled) {
		t.Errorf("ExchangeCode: expected context.Canceled, got %v", err)
	}
	if _, err := c.RefreshToken(ctx, "r"); !errors.Is(err, context.Canceled) {
		t.Errorf("RefreshToken: expected context.Canceled, got %v", err)
	}
	if _, err := c.FetchProfile(ctx, "t"); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchProfile: expected context.Canceled, got %v", err)
	}
}
