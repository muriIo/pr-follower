package httpadapter_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/auth"
)

// fakeOrgMembers: octocat is a member of acme, hubot is not; "unverified" makes every check fail.
type fakeOrgMembers struct{ unverified bool }

func (f fakeOrgMembers) IsOrgMember(_ context.Context, org, login string, _ int64) (bool, error) {
	if f.unverified {
		return false, fmt.Errorf("%w: 502", entity.ErrGitHubUnavailable)
	}
	return org == "acme" && login == "octocat", nil
}

func callbackWithCode(t *testing.T, ts testServer, code string) *httptest.ResponseRecorder {
	t.Helper()
	start := ts.do(t, http.MethodGet, "/api/v1/auth/github/login", "", nil)
	location, _ := url.Parse(start.Header().Get("Location"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?code="+code+"&state="+url.QueryEscape(location.Query().Get("state")), nil)
	req.AddCookie(cookieNamed(start, "pf_oauth"))
	rec := httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	return rec
}

func TestAuth_CallbackRefusesAccountsOutsideTheAllowedOrgs(t *testing.T) {
	t.Parallel()
	ts := setupTestRouterWithAuth(t, fakeGitHubOAuth{}, func(d *auth.Deps) {
		d.AllowedOrgs = []string{"ACME"}
		d.Members = fakeOrgMembers{}
	})

	// hubot is not a member: back to the login screen with an honest reason and no session.
	rec := callbackWithCode(t, ts, "other")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error=not_org_member" {
		t.Fatalf("expected /?auth_error=not_org_member, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if cookieNamed(rec, "pf_session") != nil {
		t.Fatal("a refused account must get no session")
	}

	// octocat is a member of acme: signed in as usual.
	session := ts.signIn(t)
	if rec := ts.do(t, http.MethodGet, "/api/v1/auth/me", "", session); rec.Code != http.StatusOK {
		t.Fatalf("a member must reach the API, got %d", rec.Code)
	}
}

func TestAuth_CallbackFailsClosedWhenMembershipCannotBeVerified(t *testing.T) {
	t.Parallel()
	ts := setupTestRouterWithAuth(t, fakeGitHubOAuth{}, func(d *auth.Deps) {
		d.AllowedOrgs = []string{"acme"}
		d.Members = fakeOrgMembers{unverified: true}
	})
	rec := callbackWithCode(t, ts, "ok")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error=org_membership_unverified" {
		t.Fatalf("expected /?auth_error=org_membership_unverified, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if cookieNamed(rec, "pf_session") != nil {
		t.Fatal("an unverified account must get no session")
	}
}
