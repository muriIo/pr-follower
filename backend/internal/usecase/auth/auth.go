// Package auth implements GitHub-only sign-in with server-side sessions (ADR-0010).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

const (
	// DefaultSessionTTL is how long a sign-in lasts before GitHub sign-in is required again.
	DefaultSessionTTL = 30 * 24 * time.Hour
	// refreshLeeway refreshes access tokens this long before they expire.
	refreshLeeway = 5 * time.Minute
)

type Deps struct {
	OAuth      port.GitHubOAuth
	Users      port.UserRepository
	Sessions   port.SessionRepository
	Now        func() time.Time
	SessionTTL time.Duration
}

type UseCase struct {
	oauth      port.GitHubOAuth
	users      port.UserRepository
	sessions   port.SessionRepository
	now        func() time.Time
	sessionTTL time.Duration
}

// LoginChallenge carries what the HTTP layer keeps (State, CodeVerifier) and where to send the user.
type LoginChallenge struct {
	State        string
	CodeVerifier string
	AuthorizeURL string
}

// LoginResult carries the raw session token for the cookie; only its hash is persisted.
type LoginResult struct {
	SessionToken string
	ExpiresAt    time.Time
	User         *entity.User
}

func NewUseCase(deps Deps) *UseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	ttl := deps.SessionTTL
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &UseCase{
		oauth:      deps.OAuth,
		users:      deps.Users,
		sessions:   deps.Sessions,
		now:        now,
		sessionTTL: ttl,
	}
}

// BeginLogin creates the anti-forgery state and PKCE verifier for the GitHub redirect.
func (uc *UseCase) BeginLogin(ctx context.Context) (LoginChallenge, error) {
	if err := ctx.Err(); err != nil {
		return LoginChallenge{}, context.Cause(ctx)
	}
	state, err := randomToken()
	if err != nil {
		return LoginChallenge{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return LoginChallenge{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	return LoginChallenge{
		State:        state,
		CodeVerifier: verifier,
		AuthorizeURL: uc.oauth.AuthorizeURL(state, base64.RawURLEncoding.EncodeToString(challenge[:])),
	}, nil
}

// CompleteLogin exchanges the GitHub code, upserts the user by GitHub ID and opens a session.
// The caller must already have verified the state parameter.
func (uc *UseCase) CompleteLogin(ctx context.Context, code, codeVerifier string) (*LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if code == "" || codeVerifier == "" {
		return nil, entity.ErrInvalidInput
	}

	token, err := uc.oauth.ExchangeCode(ctx, code, codeVerifier)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, context.Cause(ctx)
		}
		return nil, fmt.Errorf("%w: github code exchange failed: %v", entity.ErrUnauthorized, err)
	}

	profile, err := uc.oauth.FetchProfile(ctx, token.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("fetch github profile: %w", err)
	}
	if profile.ID == 0 || profile.Login == "" {
		return nil, fmt.Errorf("%w: github returned an empty profile", entity.ErrUnauthorized)
	}

	user := &entity.User{
		GitHubID:    profile.ID,
		Login:       profile.Login,
		Name:        profile.Name,
		AvatarURL:   profile.AvatarURL,
		Teams:       profile.Teams,
		GitHubToken: token,
	}
	if user.Name == "" {
		user.Name = profile.Login
	}
	if err := uc.users.Upsert(ctx, user); err != nil {
		return nil, err
	}

	sessionToken, err := randomToken()
	if err != nil {
		return nil, err
	}
	expiresAt := uc.now().Add(uc.sessionTTL)
	if err := uc.sessions.Create(ctx, &entity.Session{
		TokenHash: hashToken(sessionToken),
		UserID:    user.ID,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, err
	}

	return &LoginResult{SessionToken: sessionToken, ExpiresAt: expiresAt, User: user}, nil
}

// Authenticate resolves the user behind a session cookie token.
func (uc *UseCase) Authenticate(ctx context.Context, sessionToken string) (*entity.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if sessionToken == "" {
		return nil, entity.ErrUnauthorized
	}
	hash := hashToken(sessionToken)
	session, err := uc.sessions.GetByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return nil, entity.ErrUnauthorized
		}
		return nil, err
	}
	if session.IsExpired(uc.now()) {
		_ = uc.sessions.Delete(ctx, hash)
		return nil, entity.ErrUnauthorized
	}
	user, err := uc.users.GetByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return nil, entity.ErrUnauthorized
		}
		return nil, err
	}
	return user, nil
}

// Logout ends the session behind the cookie token. An empty token is a no-op.
func (uc *UseCase) Logout(ctx context.Context, sessionToken string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if sessionToken == "" {
		return nil
	}
	return uc.sessions.Delete(ctx, hashToken(sessionToken))
}

// AccessToken returns a usable GitHub user access token, refreshing and persisting it when it
// is about to expire. ErrUnauthorized means the user must sign in with GitHub again.
func (uc *UseCase) AccessToken(ctx context.Context, userID uint64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", context.Cause(ctx)
	}
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	now := uc.now()
	if !user.GitHubToken.NeedsRefresh(now, refreshLeeway) {
		return user.GitHubToken.AccessToken, nil
	}
	if !user.GitHubToken.CanRefresh(now) {
		return "", fmt.Errorf("%w: github token expired, sign in again", entity.ErrUnauthorized)
	}

	refreshed, err := uc.oauth.RefreshToken(ctx, user.GitHubToken.RefreshToken)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", context.Cause(ctx)
		}
		return "", fmt.Errorf("%w: github token refresh failed: %v", entity.ErrUnauthorized, err)
	}
	user.GitHubToken = refreshed
	if err := uc.users.Upsert(ctx, user); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

// randomToken returns 32 random bytes as unpadded base64url (43 chars): valid as an OAuth
// state, a PKCE code verifier, and a session token.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
