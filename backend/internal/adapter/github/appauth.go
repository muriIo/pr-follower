package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

const (
	// appJWTBackdate absorbs clock drift between this server and GitHub (GitHub's own advice).
	appJWTBackdate = 60 * time.Second
	// appJWTLifetime is below GitHub's 10 minute maximum.
	appJWTLifetime = 9 * time.Minute
	// installationTokenRenewWindow renews a token before it can expire in the middle of a request.
	installationTokenRenewWindow = 5 * time.Minute
)

// AppAuthConfig configures how the server authenticates as the GitHub App itself.
type AppAuthConfig struct {
	AppID string
	// PrivateKeyPEM is the app's private key (PKCS#1 or PKCS#8). Literal `\n` sequences are
	// accepted, so the key fits in one environment variable.
	PrivateKeyPEM string
	BaseURL       string
	HTTPClient    *http.Client
	Backoff       resilience.BackoffConfig
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

type cachedToken struct {
	token   string
	expires time.Time
}

// AppAuth signs short-lived app JWTs and exchanges them for installation access tokens, which
// act as the app on the repositories of one installation. It serves reads that have no user
// behind them (webhooks, analysis). Tokens are cached per installation until near expiry.
//
// replica-local: a cache of installation tokens; another replica exchanges its own, at the cost of one request.
type AppAuth struct {
	appID   string
	key     *rsa.PrivateKey
	baseURL string
	client  *http.Client
	backoff resilience.BackoffConfig
	now     func() time.Time

	mu     sync.Mutex
	cache  map[int64]cachedToken
	flight resilience.SingleFlightGroup[string]
}

// NewAppAuth validates the credentials. Errors never echo key material.
func NewAppAuth(cfg AppAuthConfig) (*AppAuth, error) {
	if strings.TrimSpace(cfg.AppID) == "" {
		return nil, errors.New("github app id is required")
	}
	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	backoff := cfg.Backoff
	if backoff.MaxAttempts <= 0 {
		backoff = resilience.DefaultBackoffConfig()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &AppAuth{appID: cfg.AppID, key: key, baseURL: baseURL, client: client, backoff: backoff, now: now, cache: map[int64]cachedToken{}}, nil
}

func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	text := strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("github app private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("github app private key is neither PKCS#1 nor PKCS#8")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app private key is not an RSA key")
	}
	return key, nil
}

// InstallationToken returns a token for the installation, reusing the cached one while it has
// more than five minutes left. ErrNotFound means the app is no longer installed there;
// ErrUnauthorized means GitHub rejected the app's own credentials.
func (a *AppAuth) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", context.Cause(ctx)
	}
	if installationID <= 0 {
		return "", fmt.Errorf("%w: an installation id is required", entity.ErrInvalidInput)
	}
	if token, ok := a.cached(installationID); ok {
		return token, nil
	}
	return a.flight.Do(ctx, fmt.Sprintf("installation:%d", installationID), func(callCtx context.Context) (string, error) {
		if token, ok := a.cached(installationID); ok {
			return token, nil
		}
		return a.exchange(callCtx, installationID)
	})
}

func (a *AppAuth) cached(installationID int64) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.cache[installationID]
	if !ok || c.expires.Sub(a.now()) <= installationTokenRenewWindow {
		return "", false
	}
	return c.token, true
}

func (a *AppAuth) exchange(ctx context.Context, installationID int64) (string, error) {
	jwt, err := a.signJWT()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.baseURL, installationID)
	granted, err := resilience.RetryNonIdempotent(ctx, a.backoff, func(reqCtx context.Context) (cachedToken, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, nil)
		if err != nil {
			return cachedToken{}, resilience.Permanent(err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		resp, err := a.client.Do(req)
		if err != nil {
			return cachedToken{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return cachedToken{}, resilience.Permanent(fmt.Errorf("%w: github rejected the app credentials", entity.ErrUnauthorized))
		}
		if err := classifyHTTPStatus(resp); err != nil {
			return cachedToken{}, err
		}
		var body struct {
			Token     string    `json:"token"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.Token == "" {
			return cachedToken{}, resilience.Permanent(errors.New("github returned an unreadable installation token"))
		}
		return cachedToken{token: body.Token, expires: body.ExpiresAt}, nil
	})
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.cache[installationID] = granted
	a.mu.Unlock()
	return granted.token, nil
}

// signJWT builds the RS256 app JWT GitHub expects.
func (a *AppAuth) signJWT() (string, error) {
	now := a.now()
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-appJWTBackdate).Unix(),
		"exp": now.Add(appJWTLifetime).Unix(),
		"iss": a.appID,
	})
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign github app jwt: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
