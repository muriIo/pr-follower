package entity

import "time"

// OAuthToken is a GitHub App user access token pair (ADR-0010). Zero expiry times mean
// GitHub did not report one (non-expiring tokens).
type OAuthToken struct {
	AccessToken           string
	RefreshToken          string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
}

// NeedsRefresh reports whether the access token is missing or expires within leeway.
func (t OAuthToken) NeedsRefresh(now time.Time, leeway time.Duration) bool {
	if t.AccessToken == "" {
		return true
	}
	if t.AccessTokenExpiresAt.IsZero() {
		return false
	}
	return !now.Add(leeway).Before(t.AccessTokenExpiresAt)
}

// CanRefresh reports whether a usable refresh token is present.
func (t OAuthToken) CanRefresh(now time.Time) bool {
	if t.RefreshToken == "" {
		return false
	}
	return t.RefreshTokenExpiresAt.IsZero() || now.Before(t.RefreshTokenExpiresAt)
}

// GitHubProfile is the identity GitHub reports for a user access token.
type GitHubProfile struct {
	ID        int64
	Login     string
	Name      string
	AvatarURL string
	Teams     []string
}

// Session is a server-side login session. Only the SHA-256 hash of the cookie token is kept.
type Session struct {
	TokenHash string
	UserID    uint64
	ExpiresAt time.Time
	CreatedAt time.Time
}

// IsExpired reports whether the session is no longer valid at now.
func (s Session) IsExpired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}
