package auth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/auth"
)

// fakeMembers answers membership per "org/login" (lowercase): true, false, or an error.
type fakeMembers struct {
	mu      sync.Mutex
	answers map[string]any
	calls   []string
	// cancel, when set, is called on every check (to cancel the caller mid-sweep).
	cancel func()
}

func (f *fakeMembers) IsOrgMember(ctx context.Context, org, login string, githubID int64) (bool, error) {
	f.mu.Lock()
	key := strings.ToLower(org + "/" + login)
	f.calls = append(f.calls, fmt.Sprintf("%s#%d", key, githubID))
	answer, ok := f.answers[key]
	cancel := f.cancel
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	switch a := answer.(type) {
	case bool:
		return a, nil
	case error:
		return false, a
	}
	return false, nil
}

func (f *fakeMembers) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeAccess plays the store of who can read stored data (users with a session or a follow).
type fakeAccess struct {
	mu      sync.Mutex
	users   []entity.User
	revoked []uint64
	listErr error
}

func (f *fakeAccess) ListUsersWithAccess(ctx context.Context) ([]entity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]entity.User, 0, len(f.users))
	for _, u := range f.users {
		if !f.isRevoked(u.ID) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeAccess) RevokeUserAccess(_ context.Context, userID uint64) (entity.RevokedAccess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isRevoked(userID) {
		return entity.RevokedAccess{}, nil
	}
	f.revoked = append(f.revoked, userID)
	return entity.RevokedAccess{Sessions: 1, Follows: 2}, nil
}

func (f *fakeAccess) isRevoked(id uint64) bool {
	for _, r := range f.revoked {
		if r == id {
			return true
		}
	}
	return false
}

func (f *fakeAccess) revokedIDs() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.revoked...)
}

func newAllowlistHarness(t *testing.T, orgs []string, answers map[string]any) (*harness, *fakeMembers, *fakeAccess) {
	t.Helper()
	h := newHarness(t)
	members := &fakeMembers{answers: answers}
	access := &fakeAccess{}
	h.uc = auth.NewUseCase(auth.Deps{
		OAuth:       h.oauth,
		Users:       h.users,
		Sessions:    h.sessions,
		Now:         func() time.Time { return *h.clock },
		AllowedOrgs: orgs,
		Members:     members,
		Access:      access,
	})
	return h, members, access
}

func TestCompleteLogin_AllowlistAdmitsAMemberOfAnyAllowedOrg(t *testing.T) {
	t.Parallel()
	h, members, _ := newAllowlistHarness(t, []string{" Acme ", "acme-labs"}, map[string]any{
		"acme/octocat":      false,
		"acme-labs/octocat": true,
	})

	res, err := h.uc.CompleteLogin(context.Background(), "c", "v")
	if err != nil {
		t.Fatalf("a member of an allowed org must sign in: %v", err)
	}
	if res.User.ID == 0 || h.sessions.count() != 1 {
		t.Fatalf("expected a user and a session, got %#v and %d sessions", res.User, h.sessions.count())
	}
	if len(members.calls) == 0 || !strings.Contains(strings.Join(members.calls, ","), "#4242") {
		t.Fatalf("membership must be checked with the GitHub account id too: %v", members.calls)
	}
}

func TestCompleteLogin_AllowlistRefusesANonMemberBeforeStoringAnything(t *testing.T) {
	t.Parallel()
	h, _, _ := newAllowlistHarness(t, []string{"acme", "acme-labs"}, map[string]any{
		"acme/octocat":      false,
		"acme-labs/octocat": false,
	})

	_, err := h.uc.CompleteLogin(context.Background(), "c", "v")
	if !errors.Is(err, entity.ErrNotOrgMember) {
		t.Fatalf("expected ErrNotOrgMember, got %v", err)
	}
	if errors.Is(err, entity.ErrOrgMembershipUnverified) {
		t.Fatal("a confirmed non-member must not be reported as unverified")
	}
	if len(h.users.byID) != 0 || h.sessions.count() != 0 {
		t.Fatalf("no user row and no session for a refused sign-in: %d users, %d sessions", len(h.users.byID), h.sessions.count())
	}
}

func TestCompleteLogin_AllowlistFailsClosedWhenMembershipCannotBeVerified(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]error{
		"github down":          fmt.Errorf("%w: 502", entity.ErrGitHubUnavailable),
		"rate limited":         &entity.RateLimitedError{ResetAt: now.Add(time.Hour)},
		"app not installed":    fmt.Errorf("%w: app not installed on acme", entity.ErrNotFound),
		"missing members perm": fmt.Errorf("%w: resource not accessible by integration", entity.ErrForbidden),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// One org could not be checked and the other says "not a member": the user may still be a
			// member of the first, so this is unverified, never "not a member".
			h, _, _ := newAllowlistHarness(t, []string{"acme", "acme-labs"}, map[string]any{
				"acme/octocat":      answer,
				"acme-labs/octocat": false,
			})
			_, err := h.uc.CompleteLogin(context.Background(), "c", "v")
			if !errors.Is(err, entity.ErrOrgMembershipUnverified) {
				t.Fatalf("expected ErrOrgMembershipUnverified, got %v", err)
			}
			if errors.Is(err, entity.ErrNotOrgMember) {
				t.Fatal("an unverified membership must never be reported as not a member")
			}
			if len(h.users.byID) != 0 || h.sessions.count() != 0 {
				t.Fatal("fail closed: no user row and no session")
			}
		})
	}
}

func TestCompleteLogin_AllowlistMemberOfOneOrgWinsOverAnUnverifiedOther(t *testing.T) {
	t.Parallel()
	h, _, _ := newAllowlistHarness(t, []string{"acme", "acme-labs"}, map[string]any{
		"acme/octocat":      fmt.Errorf("%w: 502", entity.ErrGitHubUnavailable),
		"acme-labs/octocat": true,
	})
	if _, err := h.uc.CompleteLogin(context.Background(), "c", "v"); err != nil {
		t.Fatalf("confirmed member of one allowed org must sign in: %v", err)
	}
}

func TestCompleteLogin_WithoutAllowlistNeverChecksMembership(t *testing.T) {
	t.Parallel()
	h, members, _ := newAllowlistHarness(t, []string{"", " "}, nil)
	if _, err := h.uc.CompleteLogin(context.Background(), "c", "v"); err != nil {
		t.Fatalf("open sign-in: %v", err)
	}
	if members.callCount() != 0 {
		t.Fatalf("no allowlist means no membership check, got %v", members.calls)
	}
}

func TestRecheckMembership_RevokesOnlyConfirmedLeavers(t *testing.T) {
	t.Parallel()
	h, _, access := newAllowlistHarness(t, []string{"acme"}, map[string]any{
		"acme/stays":   true,
		"acme/left":    false,
		"acme/unknown": fmt.Errorf("%w: 503", entity.ErrGitHubUnavailable),
	})
	access.users = []entity.User{
		{ID: 1, GitHubID: 11, Login: "stays"},
		{ID: 2, GitHubID: 22, Login: "left"},
		{ID: 3, GitHubID: 33, Login: "unknown"},
	}

	got, err := h.uc.RecheckMembership(context.Background())
	if err != nil {
		t.Fatalf("RecheckMembership() error: %v", err)
	}
	if want := (auth.MembershipRecheck{Checked: 3, Revoked: 1, Unverified: 1}); got != want {
		t.Fatalf("RecheckMembership() = %+v, want %+v", got, want)
	}
	if ids := access.revokedIDs(); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("only the confirmed leaver loses access, revoked %v", ids)
	}

	// Idempotent: a second sweep (or another replica's) finds nothing left to revoke.
	again, err := h.uc.RecheckMembership(context.Background())
	if err != nil || again.Revoked != 0 || len(access.revokedIDs()) != 1 {
		t.Fatalf("second sweep = %+v, %v; revoked %v", again, err, access.revokedIDs())
	}
}

func TestRecheckMembership_DoesNothingWithoutAllowlist(t *testing.T) {
	t.Parallel()
	h, members, access := newAllowlistHarness(t, nil, nil)
	access.users = []entity.User{{ID: 1, GitHubID: 11, Login: "anyone"}}
	got, err := h.uc.RecheckMembership(context.Background())
	if err != nil || got != (auth.MembershipRecheck{}) || members.callCount() != 0 || len(access.revokedIDs()) != 0 {
		t.Fatalf("open sign-in must not sweep: %+v, %v, calls %v", got, err, members.calls)
	}
}

func TestRecheckMembership_StopsWhenCanceled(t *testing.T) {
	t.Parallel()
	h, members, access := newAllowlistHarness(t, []string{"acme"}, map[string]any{})
	access.users = []entity.User{{ID: 1, GitHubID: 11, Login: "a"}, {ID: 2, GitHubID: 22, Login: "b"}}
	ctx, cancel := context.WithCancel(context.Background())
	members.cancel = cancel // canceled while checking the first user

	if _, err := h.uc.RecheckMembership(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if members.callCount() != 1 || len(access.revokedIDs()) != 0 {
		t.Fatalf("nothing may be revoked on a canceled check, nor the next user checked: calls %v, revoked %v", members.calls, access.revokedIDs())
	}

	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := h.uc.RecheckMembership(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled before start: %v", err)
	}
}

func TestRecheckMembership_ReportsAStoreFailure(t *testing.T) {
	t.Parallel()
	h, _, access := newAllowlistHarness(t, []string{"acme"}, nil)
	access.listErr = errors.New("db down")
	if _, err := h.uc.RecheckMembership(context.Background()); err == nil {
		t.Fatal("expected the store failure")
	}
}

func TestRecheckMembershipEvery_RunsUntilStopped(t *testing.T) {
	t.Parallel()
	h, _, access := newAllowlistHarness(t, []string{"acme"}, map[string]any{"acme/left": false})
	access.users = []entity.User{{ID: 2, GitHubID: 22, Login: "left"}}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		h.uc.RecheckMembershipEvery(ctx, 5*time.Millisecond)
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for len(access.revokedIDs()) == 0 {
		select {
		case <-deadline:
			t.Fatal("the leaver was never revoked")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the recheck loop did not stop with its context")
	}
}
