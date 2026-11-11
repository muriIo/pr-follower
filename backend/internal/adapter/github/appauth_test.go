package github_test

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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

func newKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return key, string(pkcs1)
}

// fakeApp plays api.github.com's installation token endpoint and verifies the app JWT.
type fakeApp struct {
	t        *testing.T
	key      *rsa.PrivateKey
	appID    string
	calls    atomic.Int32
	clock    func() time.Time
	lifetime time.Duration
	status   int
	mu       sync.Mutex
	claims   map[string]any
}

func (f *fakeApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/app/installations/") || !strings.HasSuffix(r.URL.Path, "/access_tokens") {
		http.NotFound(w, r)
		return
	}
	jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&f.key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	f.mu.Lock()
	_ = json.Unmarshal(payload, &f.claims)
	f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	id := strings.Split(r.URL.Path, "/")[3]
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":      "ghs_" + id + "_" + string(rune('a'+f.calls.Load())),
		"expires_at": f.clock().Add(f.lifetime).UTC().Format(time.RFC3339),
	})
}

func newAppAuth(t *testing.T, app *fakeApp, pemKey string, now func() time.Time) *github.AppAuth {
	t.Helper()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	auth, err := github.NewAppAuth(github.AppAuthConfig{
		AppID: app.appID, PrivateKeyPEM: pemKey, BaseURL: server.URL, Now: now,
		Backoff: resilience.BackoffConfig{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("NewAppAuth() error: %v", err)
	}
	return auth
}

func TestAppAuth_ExchangesASignedAppJWTForAnInstallationToken(t *testing.T) {
	t.Parallel()
	key, pemKey := newKey(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	app := &fakeApp{t: t, key: key, appID: "12345", clock: func() time.Time { return now }, lifetime: time.Hour}
	auth := newAppAuth(t, app, pemKey, func() time.Time { return now })

	token, err := auth.InstallationToken(context.Background(), 777)
	if err != nil || !strings.HasPrefix(token, "ghs_777_") {
		t.Fatalf("InstallationToken() = %q, %v", token, err)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.claims["iss"] != "12345" {
		t.Errorf("iss = %v, want the app id", app.claims["iss"])
	}
	iat, exp := int64(app.claims["iat"].(float64)), int64(app.claims["exp"].(float64))
	if iat > now.Unix() || exp <= now.Unix() || exp-iat > 10*60 {
		t.Errorf("JWT must be backdated for clock drift and live at most 10 minutes: iat=%d exp=%d now=%d", iat, exp, now.Unix())
	}
}

func TestAppAuth_CachesUntilNearExpiryAndPerInstallation(t *testing.T) {
	t.Parallel()
	key, pemKey := newKey(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	app := &fakeApp{t: t, key: key, appID: "1", clock: clock, lifetime: time.Hour}
	auth := newAppAuth(t, app, pemKey, clock)
	ctx := context.Background()

	first, _ := auth.InstallationToken(ctx, 1)
	again, _ := auth.InstallationToken(ctx, 1)
	other, _ := auth.InstallationToken(ctx, 2)
	if first != again || app.calls.Load() != 2 || first == other {
		t.Fatalf("one call per installation while the token is fresh: %q %q %q calls=%d", first, again, other, app.calls.Load())
	}

	mu.Lock()
	now = now.Add(56 * time.Minute) // under 5 minutes left: renew before it expires mid-request
	mu.Unlock()
	renewed, err := auth.InstallationToken(ctx, 1)
	if err != nil || renewed == first || app.calls.Load() != 3 {
		t.Fatalf("expected a renewed token: %q err=%v calls=%d", renewed, err, app.calls.Load())
	}
}

func TestAppAuth_ConcurrentCallersShareOneExchange(t *testing.T) {
	t.Parallel()
	key, pemKey := newKey(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	app := &fakeApp{t: t, key: key, appID: "1", clock: func() time.Time { return now }, lifetime: time.Hour}
	auth := newAppAuth(t, app, pemKey, func() time.Time { return now })

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := auth.InstallationToken(context.Background(), 5); err != nil {
				t.Errorf("InstallationToken() error: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := app.calls.Load(); got > 2 {
		t.Fatalf("callers must share the exchange, got %d calls for 8 callers", got)
	}
}

func TestAppAuth_MapsFailures(t *testing.T) {
	t.Parallel()
	key, pemKey := newKey(t)
	now := func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }

	gone := &fakeApp{t: t, key: key, appID: "1", clock: now, lifetime: time.Hour, status: http.StatusNotFound}
	if _, err := newAppAuth(t, gone, pemKey, now).InstallationToken(context.Background(), 1); !errors.Is(err, entity.ErrNotFound) {
		t.Errorf("an uninstalled app: expected ErrNotFound, got %v", err)
	}
	rejected := &fakeApp{t: t, key: key, appID: "1", clock: now, lifetime: time.Hour, status: http.StatusUnauthorized}
	if _, err := newAppAuth(t, rejected, pemKey, now).InstallationToken(context.Background(), 1); !errors.Is(err, entity.ErrUnauthorized) {
		t.Errorf("bad app credentials: expected ErrUnauthorized, got %v", err)
	}

	// A key that does not belong to the app is rejected by GitHub (signature mismatch).
	_, otherPEM := newKey(t)
	ok := &fakeApp{t: t, key: key, appID: "1", clock: now, lifetime: time.Hour}
	if _, err := newAppAuth(t, ok, otherPEM, now).InstallationToken(context.Background(), 1); !errors.Is(err, entity.ErrUnauthorized) {
		t.Errorf("wrong private key: expected ErrUnauthorized, got %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newAppAuth(t, ok, pemKey, now).InstallationToken(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: expected context.Canceled, got %v", err)
	}
	if _, err := newAppAuth(t, ok, pemKey, now).InstallationToken(context.Background(), 0); !errors.Is(err, entity.ErrInvalidInput) {
		t.Errorf("no installation id: expected ErrInvalidInput, got %v", err)
	}
}

func TestNewAppAuth_AcceptsPKCS8AndEscapedNewlinesAndRejectsGarbage(t *testing.T) {
	t.Parallel()
	key, pkcs1 := newKey(t)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pkcs8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	escaped := strings.ReplaceAll(strings.TrimSpace(pkcs1), "\n", `\n`) // how a key fits in one env var

	for name, in := range map[string]string{"pkcs1": pkcs1, "pkcs8": pkcs8, "escaped newlines": escaped} {
		if _, err := github.NewAppAuth(github.AppAuthConfig{AppID: "1", PrivateKeyPEM: in}); err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
	for name, cfg := range map[string]github.AppAuthConfig{
		"garbage key": {AppID: "1", PrivateKeyPEM: "not a key"},
		"no app id":   {PrivateKeyPEM: pkcs1},
		"no key":      {AppID: "1"},
	} {
		if _, err := github.NewAppAuth(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if strings.Contains(err.Error(), "not a key") {
			t.Errorf("%s: the error must not echo key material: %v", name, err)
		}
	}
}
