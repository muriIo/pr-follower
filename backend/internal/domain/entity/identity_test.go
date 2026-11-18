package entity_test

import (
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestOAuthToken_NeedsRefresh(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	leeway := 5 * time.Minute

	cases := []struct {
		name  string
		token entity.OAuthToken
		want  bool
	}{
		{"non-expiring token", entity.OAuthToken{AccessToken: "a"}, false},
		{"fresh token", entity.OAuthToken{AccessToken: "a", AccessTokenExpiresAt: now.Add(time.Hour)}, false},
		{"inside leeway", entity.OAuthToken{AccessToken: "a", AccessTokenExpiresAt: now.Add(4 * time.Minute)}, true},
		{"already expired", entity.OAuthToken{AccessToken: "a", AccessTokenExpiresAt: now.Add(-time.Minute)}, true},
		{"empty access token", entity.OAuthToken{}, true},
	}
	for _, tc := range cases {
		if got := tc.token.NeedsRefresh(now, leeway); got != tc.want {
			t.Errorf("%s: NeedsRefresh() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOAuthToken_CanRefresh(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	if (entity.OAuthToken{}).CanRefresh(now) {
		t.Error("token without refresh token must not be refreshable")
	}
	if !(entity.OAuthToken{RefreshToken: "r"}).CanRefresh(now) {
		t.Error("refresh token without expiry must be refreshable")
	}
	if !(entity.OAuthToken{RefreshToken: "r", RefreshTokenExpiresAt: now.Add(time.Hour)}).CanRefresh(now) {
		t.Error("unexpired refresh token must be refreshable")
	}
	if (entity.OAuthToken{RefreshToken: "r", RefreshTokenExpiresAt: now.Add(-time.Hour)}).CanRefresh(now) {
		t.Error("expired refresh token must not be refreshable")
	}
}

func TestSession_IsExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	if (entity.Session{ExpiresAt: now.Add(time.Second)}).IsExpired(now) {
		t.Error("session expiring in the future must not be expired")
	}
	if !(entity.Session{ExpiresAt: now}).IsExpired(now) {
		t.Error("session expiring now must be expired")
	}
}

func TestRevokedAccess_Any(t *testing.T) {
	t.Parallel()
	if (entity.RevokedAccess{}).Any() {
		t.Fatal("nothing removed")
	}
	for _, r := range []entity.RevokedAccess{{Sessions: 1}, {Follows: 1}, {PRStates: 1}, {SyncJobs: 1}} {
		if !r.Any() {
			t.Fatalf("%+v removed something", r)
		}
	}
}
