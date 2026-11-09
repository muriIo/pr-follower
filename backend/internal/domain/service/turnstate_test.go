package service_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

func TestComputeTurnState(t *testing.T) {
	t.Parallel()

	readyPR := entity.PullRequest{
		State:          entity.PRStateOpen,
		IsDraft:        false,
		CIStatus:       entity.CIStatusSuccess,
		MergeableState: entity.MergeableStateClean,
		HeadSHA:        "sha-100",
	}

	ownedFiles := []entity.ChangedFile{
		{Path: "apps/checkout/cart.ts", Additions: 10},
	}

	tests := []struct {
		name      string
		input     service.TurnStateInput
		wantState entity.TurnState
	}{
		{
			name: "merged or closed PR transitions to MERGED_OR_CLOSED",
			input: service.TurnStateInput{
				PR: entity.PullRequest{
					State:   entity.PRStateMerged,
					HeadSHA: "sha-100",
				},
				OwnedFiles: ownedFiles,
			},
			wantState: entity.TurnStateMergedOrClosed,
		},
		{
			name: "snoozed PR on same SHA transitions to SNOOZED",
			input: service.TurnStateInput{
				PR:         readyPR,
				OwnedFiles: ownedFiles,
				ExistingState: entity.UserPRState{
					SnoozedUntilCondition: entity.SnoozeConditionNextCommit,
					SnoozedAtSHA:          "sha-100",
				},
			},
			wantState: entity.TurnStateSnoozed,
		},
		{
			name: "draft or failing CI PR transitions to HIDDEN_GATE",
			input: service.TurnStateInput{
				PR: entity.PullRequest{
					State:          entity.PRStateOpen,
					IsDraft:        true,
					CIStatus:       entity.CIStatusPending,
					MergeableState: entity.MergeableStateClean,
					HeadSHA:        "sha-100",
				},
				OwnedFiles: ownedFiles,
			},
			wantState: entity.TurnStateHiddenGate,
		},
		{
			name: "PR with zero owned files touched transitions to HIDDEN_GATE",
			input: service.TurnStateInput{
				PR:         readyPR,
				OwnedFiles: nil,
			},
			wantState: entity.TurnStateHiddenGate,
		},
		{
			name: "PR already approved by current user transitions to APPROVED_BY_ME",
			input: service.TurnStateInput{
				PR:              readyPR,
				OwnedFiles:      ownedFiles,
				ApprovedByMeSHA: "sha-100",
			},
			wantState: entity.TurnStateApprovedByMe,
		},
		{
			name: "PR approved by fellow codeowner group member transitions to TEAMMATE_APPROVED",
			input: service.TurnStateInput{
				PR:                    readyPR,
				OwnedFiles:            ownedFiles,
				TeammateApprovedAtSHA: "sha-100",
			},
			wantState: entity.TurnStateTeammateApproved,
		},
		{
			name: "PR where user requested changes or commented on current SHA and author has not replied transitions to WAITING_ON_AUTHOR",
			input: service.TurnStateInput{
				PR:                        readyPR,
				OwnedFiles:                ownedFiles,
				UserActedAtSHA:            "sha-100",
				AuthorRepliedAfterUserAct: false,
			},
			wantState: entity.TurnStateWaitingOnAuthor,
		},
		{
			name: "PR where author pushed new commit or replied after user action transitions back to WAITING_ON_ME",
			input: service.TurnStateInput{
				PR:                        readyPR,
				OwnedFiles:                ownedFiles,
				UserActedAtSHA:            "sha-099",
				AuthorRepliedAfterUserAct: false,
			},
			wantState: entity.TurnStateWaitingOnMe,
		},
		{
			name: "ready PR touching owned files with no prior action is WAITING_ON_ME",
			input: service.TurnStateInput{
				PR:         readyPR,
				OwnedFiles: ownedFiles,
			},
			wantState: entity.TurnStateWaitingOnMe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := service.ComputeTurnState(tt.input)
			if got != tt.wantState {
				t.Errorf("ComputeTurnState() = %q, want %q", got, tt.wantState)
			}
		})
	}
}
