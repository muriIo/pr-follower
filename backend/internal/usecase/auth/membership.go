package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// MembershipRecheck reports one membership sweep (ADR-0015): users checked, users whose access was
// revoked, and users whose membership could not be verified (left untouched).
type MembershipRecheck struct {
	Checked    int
	Revoked    int
	Unverified int
}

// restrictsSignIn reports whether sign-in is limited to members of allowed organizations.
func (uc *UseCase) restrictsSignIn() bool {
	return len(uc.allowedOrgs) > 0
}

// requireAllowedOrgMember lets the account through when sign-in is open or GitHub confirms it belongs
// to at least one allowed organization. It fails closed: entity.ErrNotOrgMember only when every
// allowed organization answered "not a member", entity.ErrOrgMembershipUnverified (wrapping the
// cause) when any of them could not be asked and none confirmed membership.
func (uc *UseCase) requireAllowedOrgMember(ctx context.Context, login string, githubID int64) error {
	if !uc.restrictsSignIn() {
		return nil
	}
	if uc.members == nil {
		return fmt.Errorf("%w: no membership checker is configured", entity.ErrOrgMembershipUnverified)
	}
	var unverified error
	for _, org := range uc.allowedOrgs {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		member, err := uc.members.IsOrgMember(ctx, org, login, githubID)
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			if unverified == nil {
				unverified = fmt.Errorf("%w: %s: %w", entity.ErrOrgMembershipUnverified, org, err)
			}
			continue
		}
		if member {
			return nil
		}
	}
	if unverified != nil {
		return unverified
	}
	return entity.ErrNotOrgMember
}

// RecheckMembership takes away the access of every user with a session or a follow whom GitHub
// confirms is no longer a member of any allowed organization: their sessions, follows, per-user PR
// state and sync job go. A user whose membership cannot be verified keeps everything (never revoke
// on a guess). Without an allowlist it does nothing. Every replica may run it at once: revoking is
// idempotent, and a revoked user has nothing left to list (ADR-0011, ADR-0012).
func (uc *UseCase) RecheckMembership(ctx context.Context) (MembershipRecheck, error) {
	var report MembershipRecheck
	if err := ctx.Err(); err != nil {
		return report, context.Cause(ctx)
	}
	if !uc.restrictsSignIn() || uc.access == nil {
		return report, nil
	}
	users, err := uc.access.ListUsersWithAccess(ctx)
	if err != nil {
		return report, fmt.Errorf("list users with access: %w", err)
	}
	for _, user := range users {
		if err := ctx.Err(); err != nil {
			return report, context.Cause(ctx)
		}
		report.Checked++
		err := uc.requireAllowedOrgMember(ctx, user.Login, user.GitHubID)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return report, context.Cause(ctx)
		case errors.Is(err, entity.ErrNotOrgMember):
			removed, revokeErr := uc.access.RevokeUserAccess(ctx, user.ID)
			if revokeErr != nil {
				return report, fmt.Errorf("revoke access of user %d: %w", user.ID, revokeErr)
			}
			if removed.Any() {
				report.Revoked++
				slog.Info("revoked access of a user who left the allowed github organizations",
					"user_id", user.ID, "login", user.Login, "sessions", removed.Sessions, "follows", removed.Follows,
					"pr_states", removed.PRStates)
			}
		default:
			report.Unverified++
			slog.Warn("could not verify github organization membership; access left unchanged",
				"user_id", user.ID, "login", user.Login, "error", err)
		}
	}
	return report, nil
}

// RecheckMembershipEvery sweeps now and then every interval until ctx is done.
func (uc *UseCase) RecheckMembershipEvery(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if _, err := uc.RecheckMembership(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("github organization membership recheck failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
