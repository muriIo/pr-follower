package triage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestSnoozePR_UntilCIPassesOnlyWhileCICanStillPass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ci      entity.CIStatus
		allowed bool
	}{
		{entity.CIStatusPending, true},
		{entity.CIStatusFailure, true},
		{entity.CIStatusSuccess, false}, // it already passed: nothing to wait for
		{entity.CIStatusNone, false},    // nothing reports CI: it would never wake
	} {
		f := newTurnFixture(openPR(1, func(p *entity.PullRequest) { p.CIStatus = tc.ci }))
		err := f.uc.SnoozePR(context.Background(), 1, 1, entity.SnoozeConditionCIGreen)
		if tc.allowed && err != nil {
			t.Errorf("CI %q: expected the snooze, got %v", tc.ci, err)
		}
		if !tc.allowed {
			if !errors.Is(err, entity.ErrConflict) {
				t.Errorf("CI %q: expected ErrConflict, got %v", tc.ci, err)
			}
			if _, stored := f.prs.states[1]; stored {
				t.Errorf("CI %q: a refused snooze must store nothing", tc.ci)
			}
		}
	}
}

func TestSnoozePR_UntilTheNextCommitIsNotAffectedByCI(t *testing.T) {
	t.Parallel()
	f := newTurnFixture(openPR(1, func(p *entity.PullRequest) { p.CIStatus = entity.CIStatusSuccess }))
	if err := f.uc.SnoozePR(context.Background(), 1, 1, entity.SnoozeConditionNextCommit); err != nil {
		t.Fatalf("snoozing until the next commit does not depend on CI: %v", err)
	}
}
