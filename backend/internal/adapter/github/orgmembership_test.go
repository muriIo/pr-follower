package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// fakeOrgGitHub plays the endpoints the membership check uses: the org's installation (app JWT), the
// installation token, the membership check and the account lookup (installation token).
type fakeOrgGitHub struct {
	mu sync.Mutex
	// installStatus answers GET /orgs/{org}/installation when not 200.
	installStatus int
	// permissions of the installation (default members: read).
	permissions map[string]string
	// memberStatus per lowercase login: 204, 404, 302, 403, 502, ...
	memberStatus map[string]int
	// logins by GitHub account id, for GET /user/{id}.
	logins map[string]string
	calls  []string
}

func (f *fakeOrgGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	auth := r.Header.Get("Authorization")
	switch {
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "orgs" && parts[2] == "installation":
		if !strings.HasPrefix(auth, "Bearer ey") { // an app JWT, not a token
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.installStatus != 0 {
			w.WriteHeader(f.installStatus)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		perms := f.permissions
		if perms == nil {
			perms = map[string]string{"members": "read", "metadata": "read"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "permissions": perms})
	case r.Method == http.MethodPost && r.URL.Path == "/app/installations/77/access_tokens":
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_org", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "orgs" && parts[2] == "members":
		if auth != "Bearer ghs_org" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		status := f.memberStatus[strings.ToLower(parts[3])]
		if status == 0 {
			status = http.StatusNotFound
		}
		if status == http.StatusFound {
			w.Header().Set("Location", "/orgs/"+parts[1]+"/public_members/"+parts[3])
		}
		w.WriteHeader(status)
		if status == http.StatusForbidden {
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
		}
	case r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "orgs" && parts[2] == "public_members":
		w.WriteHeader(http.StatusNoContent) // must never be reached: a 302 is not followed
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "user":
		login, ok := f.logins[parts[1]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": parts[1], "login": login})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOrgGitHub) callLog() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func newOrgMembership(t *testing.T, fake *fakeOrgGitHub) *github.AppAuth {
	t.Helper()
	_, pemKey := newKey(t)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	auth, err := github.NewAppAuth(github.AppAuthConfig{
		AppID: "1", PrivateKeyPEM: pemKey, BaseURL: server.URL,
		Backoff: resilience.BackoffConfig{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestIsOrgMember_ConfirmsAMemberAsTheAppInstalledOnTheOrg(t *testing.T) {
	t.Parallel()
	fake := &fakeOrgGitHub{memberStatus: map[string]int{"octocat": http.StatusNoContent}}
	member, err := newOrgMembership(t, fake).IsOrgMember(context.Background(), "Acme", "octocat", 42)
	if err != nil || !member {
		t.Fatalf("IsOrgMember() = %v, %v; want a member", member, err)
	}
	if log := fake.callLog(); !strings.Contains(log, "GET /orgs/Acme/installation") || !strings.Contains(log, "GET /orgs/Acme/members/octocat") {
		t.Fatalf("expected the org's installation then the membership check, got:\n%s", log)
	}
}

func TestIsOrgMember_ANotFoundForTheCurrentLoginIsNotAMember(t *testing.T) {
	t.Parallel()
	fake := &fakeOrgGitHub{logins: map[string]string{"42": "OctoCat"}}
	member, err := newOrgMembership(t, fake).IsOrgMember(context.Background(), "acme", "octocat", 42)
	if err != nil || member {
		t.Fatalf("IsOrgMember() = %v, %v; want confirmed not a member", member, err)
	}
	if strings.Count(fake.callLog(), "/members/") != 1 {
		t.Fatalf("a 404 is an answer, never retried:\n%s", fake.callLog())
	}
}

func TestIsOrgMember_ARenamedAccountIsCheckedUnderItsCurrentLogin(t *testing.T) {
	t.Parallel()
	fake := &fakeOrgGitHub{
		memberStatus: map[string]int{"new-name": http.StatusNoContent},
		logins:       map[string]string{"42": "new-name"},
	}
	member, err := newOrgMembership(t, fake).IsOrgMember(context.Background(), "acme", "old-name", 42)
	if err != nil || !member {
		t.Fatalf("a stored login that is stale must not revoke a member: %v, %v\n%s", member, err, fake.callLog())
	}
}

func TestIsOrgMember_CouldNotVerifyIsAnErrorNeverANo(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		fake *fakeOrgGitHub
		kind error
	}{
		"app not installed on the org": {&fakeOrgGitHub{installStatus: http.StatusNotFound}, entity.ErrNotFound},
		"installation lacks members permission": {
			&fakeOrgGitHub{permissions: map[string]string{"metadata": "read"}, memberStatus: map[string]int{"octocat": http.StatusNoContent}},
			entity.ErrForbidden,
		},
		"github refuses the membership read":   {&fakeOrgGitHub{memberStatus: map[string]int{"octocat": http.StatusForbidden}}, entity.ErrForbidden},
		"requester cannot see private members": {&fakeOrgGitHub{memberStatus: map[string]int{"octocat": http.StatusFound}}, entity.ErrForbidden},
		"github down":                          {&fakeOrgGitHub{memberStatus: map[string]int{"octocat": http.StatusBadGateway}}, entity.ErrGitHubUnavailable},
		"account lookup fails after a 404":     {&fakeOrgGitHub{}, entity.ErrNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			member, err := newOrgMembership(t, tc.fake).IsOrgMember(context.Background(), "acme", "octocat", 42)
			if err == nil || member {
				t.Fatalf("IsOrgMember() = %v, %v; want an error", member, err)
			}
			if !errors.Is(err, tc.kind) {
				t.Fatalf("expected %v, got %v", tc.kind, err)
			}
		})
	}
}

func TestIsOrgMember_HonorsCanceledContextAndBadInput(t *testing.T) {
	t.Parallel()
	auth := newOrgMembership(t, &fakeOrgGitHub{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := auth.IsOrgMember(ctx, "acme", "octocat", 42); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, err := auth.IsOrgMember(context.Background(), "", "octocat", 42); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("empty org: %v", err)
	}
	if _, err := auth.IsOrgMember(context.Background(), "acme", "", 42); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("empty login: %v", err)
	}
}
