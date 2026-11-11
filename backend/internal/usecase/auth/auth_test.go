package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/auth"
)

// --- fakes ---------------------------------------------------------------

type fakeOAuth struct {
	mu            sync.Mutex
	exchangeToken entity.OAuthToken
	exchangeErr   error
	refreshToken  entity.OAuthToken
	refreshErr    error
	profile       entity.GitHubProfile
	profileErr    error

	gotCode, gotVerifier, gotRefresh string
	refreshCalls                     int
}

func (f *fakeOAuth) AuthorizeURL(state, codeChallenge string) string {
	return "https://github.com/login/oauth/authorize?state=" + url.QueryEscape(state) +
		"&code_challenge=" + url.QueryEscape(codeChallenge) + "&code_challenge_method=S256"
}

func (f *fakeOAuth) ExchangeCode(_ context.Context, code, verifier string) (entity.OAuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotCode, f.gotVerifier = code, verifier
	return f.exchangeToken, f.exchangeErr
}

func (f *fakeOAuth) RefreshToken(_ context.Context, refresh string) (entity.OAuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotRefresh = refresh
	f.refreshCalls++
	return f.refreshToken, f.refreshErr
}

func (f *fakeOAuth) FetchProfile(_ context.Context, _ string) (entity.GitHubProfile, error) {
	return f.profile, f.profileErr
}

type fakeUsers struct {
	mu     sync.Mutex
	byID   map[uint64]*entity.User
	nextID uint64
}

func newFakeUsers() *fakeUsers { return &fakeUsers{byID: map[uint64]*entity.User{}} }

func (f *fakeUsers) Upsert(_ context.Context, u *entity.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, existing := range f.byID {
		if existing.GitHubID == u.GitHubID {
			u.ID = id
		}
	}
	if u.ID == 0 {
		f.nextID++
		u.ID = f.nextID
	}
	cp := *u
	f.byID[u.ID] = &cp
	return nil
}

func (f *fakeUsers) GetByID(_ context.Context, id uint64) (*entity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byID[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUsers) GetByGitHubID(_ context.Context, ghID int64) (*entity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byID {
		if u.GitHubID == ghID {
			cp := *u
			return &cp, nil
		}
	}
	return nil, entity.ErrNotFound
}

type fakeSessions struct {
	mu   sync.Mutex
	byID map[string]entity.Session
}

func newFakeSessions() *fakeSessions { return &fakeSessions{byID: map[string]entity.Session{}} }

func (f *fakeSessions) Create(_ context.Context, s *entity.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[s.TokenHash] = *s
	return nil
}

func (f *fakeSessions) GetByTokenHash(_ context.Context, h string) (*entity.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[h]
	if !ok {
		return nil, entity.ErrNotFound
	}
	return &s, nil
}

func (f *fakeSessions) Delete(_ context.Context, h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byID, h)
	return nil
}

// --- helpers -------------------------------------------------------------

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

type harness struct {
	uc       *auth.UseCase
	oauth    *fakeOAuth
	users    *fakeUsers
	sessions *fakeSessions
	clock    *time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := now
	h := &harness{
		oauth: &fakeOAuth{
			exchangeToken: entity.OAuthToken{
				AccessToken: "ghu_1", RefreshToken: "ghr_1",
				AccessTokenExpiresAt: now.Add(8 * time.Hour), RefreshTokenExpiresAt: now.Add(180 * 24 * time.Hour),
			},
			profile: entity.GitHubProfile{ID: 4242, Login: "octocat", Name: "The Octocat", AvatarURL: "https://a/u/4242", Teams: []string{"@acme/web"}},
		},
		users:    newFakeUsers(),
		sessions: newFakeSessions(),
		clock:    &clock,
	}
	h.uc = auth.NewUseCase(auth.Deps{
		OAuth:    h.oauth,
		Users:    h.users,
		Sessions: h.sessions,
		Now:      func() time.Time { return *h.clock },
	})
	return h
}

func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// --- tests ---------------------------------------------------------------

func TestBeginLogin_ProducesStateAndPKCEChallenge(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	a, err := h.uc.BeginLogin(context.Background())
	if err != nil {
		t.Fatalf("BeginLogin() error: %v", err)
	}
	b, _ := h.uc.BeginLogin(context.Background())

	if len(a.State) < 32 || a.State == b.State {
		t.Fatalf("expected long, unique state, got %q and %q", a.State, b.State)
	}
	if len(a.CodeVerifier) < 43 || len(a.CodeVerifier) > 128 || a.CodeVerifier == b.CodeVerifier {
		t.Fatalf("code verifier must be 43-128 chars and unique, got %q", a.CodeVerifier)
	}

	sum := sha256.Sum256([]byte(a.CodeVerifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	u, err := url.Parse(a.AuthorizeURL)
	if err != nil {
		t.Fatalf("invalid authorize URL: %v", err)
	}
	if u.Query().Get("state") != a.State || u.Query().Get("code_challenge") != wantChallenge {
		t.Fatalf("authorize URL does not carry state/challenge: %s", a.AuthorizeURL)
	}
}

func TestCompleteLogin_CreatesUserAndSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res, err := h.uc.CompleteLogin(context.Background(), "code-1", "verifier-1")
	if err != nil {
		t.Fatalf("CompleteLogin() error: %v", err)
	}
	if h.oauth.gotCode != "code-1" || h.oauth.gotVerifier != "verifier-1" {
		t.Fatalf("exchange called with %q/%q", h.oauth.gotCode, h.oauth.gotVerifier)
	}
	if res.User.GitHubID != 4242 || res.User.Login != "octocat" || res.User.Name != "The Octocat" ||
		len(res.User.Teams) != 1 || res.User.GitHubToken.AccessToken != "ghu_1" {
		t.Fatalf("unexpected user: %#v", res.User)
	}
	if len(res.SessionToken) < 43 {
		t.Fatalf("session token too short: %q", res.SessionToken)
	}
	if !res.ExpiresAt.Equal(now.Add(auth.DefaultSessionTTL)) {
		t.Fatalf("expires at %v, want %v", res.ExpiresAt, now.Add(auth.DefaultSessionTTL))
	}

	stored, err := h.sessions.GetByTokenHash(context.Background(), hashOf(res.SessionToken))
	if err != nil || stored.UserID != res.User.ID {
		t.Fatalf("session not stored by hash: %#v, %v", stored, err)
	}
	if _, err := h.sessions.GetByTokenHash(context.Background(), res.SessionToken); err == nil {
		t.Fatal("raw session token must never be stored")
	}

	// Signing in again updates the same user instead of creating a new one.
	h.oauth.profile.Name = "Renamed"
	again, err := h.uc.CompleteLogin(context.Background(), "code-2", "verifier-2")
	if err != nil || again.User.ID != res.User.ID || again.User.Name != "Renamed" {
		t.Fatalf("expected same user updated, got %#v, %v", again.User, err)
	}
}

func TestCompleteLogin_RejectsBadInputAndProviderErrors(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	if _, err := h.uc.CompleteLogin(context.Background(), "", "v"); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("empty code: expected ErrInvalidInput, got %v", err)
	}
	if _, err := h.uc.CompleteLogin(context.Background(), "c", ""); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("empty verifier: expected ErrInvalidInput, got %v", err)
	}

	h.oauth.exchangeErr = errors.New("bad_verification_code")
	if _, err := h.uc.CompleteLogin(context.Background(), "c", "v"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("exchange failure: expected ErrUnauthorized, got %v", err)
	}

	h2 := newHarness(t)
	h2.oauth.profileErr = errors.New("boom")
	if _, err := h2.uc.CompleteLogin(context.Background(), "c", "v"); err == nil {
		t.Fatal("profile failure: expected error")
	}

	h3 := newHarness(t)
	h3.oauth.profile = entity.GitHubProfile{}
	if _, err := h3.uc.CompleteLogin(context.Background(), "c", "v"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("empty profile: expected ErrUnauthorized, got %v", err)
	}
	if len(h3.users.byID) != 0 {
		t.Fatal("no user may be created from an empty profile")
	}
}

func TestAuthenticate_ValidExpiredAndUnknownSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	res, _ := h.uc.CompleteLogin(ctx, "c", "v")
	user, err := h.uc.Authenticate(ctx, res.SessionToken)
	if err != nil || user.ID != res.User.ID {
		t.Fatalf("Authenticate() = %#v, %v", user, err)
	}

	for _, bad := range []string{"", "unknown-token"} {
		if _, err := h.uc.Authenticate(ctx, bad); !errors.Is(err, entity.ErrUnauthorized) {
			t.Fatalf("token %q: expected ErrUnauthorized, got %v", bad, err)
		}
	}

	*h.clock = res.ExpiresAt
	if _, err := h.uc.Authenticate(ctx, res.SessionToken); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("expired session: expected ErrUnauthorized, got %v", err)
	}
	if _, err := h.sessions.GetByTokenHash(ctx, hashOf(res.SessionToken)); err == nil {
		t.Fatal("expired session should be deleted")
	}
}

func TestLogout_DeletesSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	res, _ := h.uc.CompleteLogin(ctx, "c", "v")
	if err := h.uc.Logout(ctx, res.SessionToken); err != nil {
		t.Fatalf("Logout() error: %v", err)
	}
	if _, err := h.uc.Authenticate(ctx, res.SessionToken); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("expected session gone, got %v", err)
	}
	if err := h.uc.Logout(ctx, ""); err != nil {
		t.Fatalf("Logout(\"\") should be a no-op, got %v", err)
	}
}

func TestAccessToken_ReturnsFreshTokenOrRefreshes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	res, _ := h.uc.CompleteLogin(ctx, "c", "v")

	tok, err := h.uc.AccessToken(ctx, res.User.ID)
	if err != nil || tok != "ghu_1" || h.oauth.refreshCalls != 0 {
		t.Fatalf("fresh token: got %q, %v, refreshes=%d", tok, err, h.oauth.refreshCalls)
	}

	*h.clock = now.Add(8*time.Hour - time.Minute)
	h.oauth.refreshToken = entity.OAuthToken{
		AccessToken: "ghu_2", RefreshToken: "ghr_2",
		AccessTokenExpiresAt: h.clock.Add(8 * time.Hour), RefreshTokenExpiresAt: h.clock.Add(180 * 24 * time.Hour),
	}
	tok, err = h.uc.AccessToken(ctx, res.User.ID)
	if err != nil || tok != "ghu_2" || h.oauth.gotRefresh != "ghr_1" {
		t.Fatalf("refresh: got %q, %v, refreshed with %q", tok, err, h.oauth.gotRefresh)
	}
	stored, _ := h.users.GetByID(ctx, res.User.ID)
	if stored.GitHubToken.AccessToken != "ghu_2" || stored.GitHubToken.RefreshToken != "ghr_2" {
		t.Fatalf("refreshed token not persisted: %#v", stored.GitHubToken)
	}
}

func TestAccessToken_RequiresReloginWhenRefreshImpossible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	h := newHarness(t)
	res, _ := h.uc.CompleteLogin(ctx, "c", "v")
	*h.clock = now.Add(200 * 24 * time.Hour) // refresh token expired too
	if _, err := h.uc.AccessToken(ctx, res.User.ID); !errors.Is(err, entity.ErrUnauthorized) || !errors.Is(err, entity.ErrGitHubTokenExpired) {
		t.Fatalf("expected the user's GitHub access to be reported as gone, got %v", err)
	}

	h2 := newHarness(t)
	res2, _ := h2.uc.CompleteLogin(ctx, "c", "v")
	*h2.clock = now.Add(9 * time.Hour)
	h2.oauth.refreshErr = errors.New("bad_refresh_token")
	if _, err := h2.uc.AccessToken(ctx, res2.User.ID); !errors.Is(err, entity.ErrUnauthorized) || !errors.Is(err, entity.ErrGitHubTokenExpired) {
		t.Fatalf("expected the user's GitHub access to be reported as gone on refresh failure, got %v", err)
	}

	if _, err := h2.uc.AccessToken(ctx, 9999); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("unknown user: expected ErrNotFound, got %v", err)
	}
}

func TestUseCase_HonorsCanceledContext(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := h.uc.BeginLogin(ctx); err == nil {
		t.Error("BeginLogin: expected cancellation error")
	}
	if _, err := h.uc.CompleteLogin(ctx, "c", "v"); err == nil {
		t.Error("CompleteLogin: expected cancellation error")
	}
	if _, err := h.uc.Authenticate(ctx, "t"); err == nil {
		t.Error("Authenticate: expected cancellation error")
	}
	if err := h.uc.Logout(ctx, "t"); err == nil {
		t.Error("Logout: expected cancellation error")
	}
	if _, err := h.uc.AccessToken(ctx, 1); err == nil {
		t.Error("AccessToken: expected cancellation error")
	}
	if strings.Contains(h.oauth.gotCode, "c") {
		t.Error("canceled CompleteLogin must not call GitHub")
	}
}

type failingUsers struct {
	*fakeUsers
	upsertErr, getErr error
}

func (f *failingUsers) Upsert(ctx context.Context, u *entity.User) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	return f.fakeUsers.Upsert(ctx, u)
}

func (f *failingUsers) GetByID(ctx context.Context, id uint64) (*entity.User, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.fakeUsers.GetByID(ctx, id)
}

type failingSessions struct {
	*fakeSessions
	createErr, getErr error
}

func (f *failingSessions) Create(ctx context.Context, s *entity.Session) error {
	if f.createErr != nil {
		return f.createErr
	}
	return f.fakeSessions.Create(ctx, s)
}

func (f *failingSessions) GetByTokenHash(ctx context.Context, h string) (*entity.Session, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.fakeSessions.GetByTokenHash(ctx, h)
}

// cancelingOAuth cancels the caller's context while GitHub is being called.
type cancelingOAuth struct {
	*fakeOAuth
	cancel context.CancelFunc
}

func (c *cancelingOAuth) ExchangeCode(context.Context, string, string) (entity.OAuthToken, error) {
	c.cancel()
	return entity.OAuthToken{}, errors.New("request canceled")
}

func (c *cancelingOAuth) RefreshToken(context.Context, string) (entity.OAuthToken, error) {
	c.cancel()
	return entity.OAuthToken{}, errors.New("request canceled")
}

func TestNewUseCase_DefaultsClockAndTTL(t *testing.T) {
	t.Parallel()
	before := time.Now()
	uc := auth.NewUseCase(auth.Deps{OAuth: newHarness(t).oauth, Users: newFakeUsers(), Sessions: newFakeSessions()})
	res, err := uc.CompleteLogin(context.Background(), "c", "v")
	if err != nil {
		t.Fatalf("CompleteLogin() error: %v", err)
	}
	if res.ExpiresAt.Before(before.Add(auth.DefaultSessionTTL)) {
		t.Fatalf("expected wall-clock based expiry, got %v", res.ExpiresAt)
	}
}

func TestCompleteLogin_FallsBackToLoginForName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.oauth.profile.Name = ""
	res, err := h.uc.CompleteLogin(context.Background(), "c", "v")
	if err != nil || res.User.Name != "octocat" {
		t.Fatalf("expected name fallback to login, got %#v, %v", res.User, err)
	}
}

func TestUseCase_PropagatesStoreFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storeErr := errors.New("db down")

	build := func(users *failingUsers, sessions *failingSessions, clock *time.Time) *auth.UseCase {
		return auth.NewUseCase(auth.Deps{
			OAuth: newHarness(t).oauth, Users: users, Sessions: sessions,
			Now: func() time.Time { return *clock },
		})
	}

	clock := now
	users := &failingUsers{fakeUsers: newFakeUsers(), upsertErr: storeErr}
	if _, err := build(users, &failingSessions{fakeSessions: newFakeSessions()}, &clock).CompleteLogin(ctx, "c", "v"); !errors.Is(err, storeErr) {
		t.Fatalf("user upsert failure: got %v", err)
	}

	sessions := &failingSessions{fakeSessions: newFakeSessions(), createErr: storeErr}
	if _, err := build(&failingUsers{fakeUsers: newFakeUsers()}, sessions, &clock).CompleteLogin(ctx, "c", "v"); !errors.Is(err, storeErr) {
		t.Fatalf("session create failure: got %v", err)
	}

	sessions = &failingSessions{fakeSessions: newFakeSessions(), getErr: storeErr}
	if _, err := build(&failingUsers{fakeUsers: newFakeUsers()}, sessions, &clock).Authenticate(ctx, "t"); !errors.Is(err, storeErr) {
		t.Fatalf("session lookup failure: got %v", err)
	}

	// Session whose user disappeared, then a user store failure.
	users = &failingUsers{fakeUsers: newFakeUsers()}
	sessions = &failingSessions{fakeSessions: newFakeSessions()}
	uc := build(users, sessions, &clock)
	res, _ := uc.CompleteLogin(ctx, "c", "v")
	delete(users.byID, res.User.ID)
	if _, err := uc.Authenticate(ctx, res.SessionToken); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("orphan session: expected ErrUnauthorized, got %v", err)
	}
	users.getErr = storeErr
	if _, err := uc.Authenticate(ctx, res.SessionToken); !errors.Is(err, storeErr) {
		t.Fatalf("user lookup failure: got %v", err)
	}

	// Refresh succeeds but persisting it fails.
	users = &failingUsers{fakeUsers: newFakeUsers()}
	uc = build(users, &failingSessions{fakeSessions: newFakeSessions()}, &clock)
	res, _ = uc.CompleteLogin(ctx, "c", "v")
	clock = now.Add(9 * time.Hour)
	users.upsertErr = storeErr
	if _, err := uc.AccessToken(ctx, res.User.ID); !errors.Is(err, storeErr) {
		t.Fatalf("refresh persist failure: got %v", err)
	}
}

func TestUseCase_CancellationDuringGitHubCalls(t *testing.T) {
	t.Parallel()
	clock := now

	ctx, cancel := context.WithCancel(context.Background())
	oauth := &cancelingOAuth{fakeOAuth: newHarness(t).oauth, cancel: cancel}
	uc := auth.NewUseCase(auth.Deps{OAuth: oauth, Users: newFakeUsers(), Sessions: newFakeSessions(), Now: func() time.Time { return clock }})
	if _, err := uc.CompleteLogin(ctx, "c", "v"); !errors.Is(err, context.Canceled) {
		t.Fatalf("exchange canceled: expected context.Canceled, got %v", err)
	}

	users := newFakeUsers()
	setup := auth.NewUseCase(auth.Deps{OAuth: newHarness(t).oauth, Users: users, Sessions: newFakeSessions(), Now: func() time.Time { return clock }})
	res, _ := setup.CompleteLogin(context.Background(), "c", "v")
	clock = now.Add(9 * time.Hour)

	ctx2, cancel2 := context.WithCancel(context.Background())
	oauth2 := &cancelingOAuth{fakeOAuth: newHarness(t).oauth, cancel: cancel2}
	uc2 := auth.NewUseCase(auth.Deps{OAuth: oauth2, Users: users, Sessions: newFakeSessions(), Now: func() time.Time { return clock }})
	if _, err := uc2.AccessToken(ctx2, res.User.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh canceled: expected context.Canceled, got %v", err)
	}
}
