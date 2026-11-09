package entity_test

import (
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestPullRequest_IsReadyForReviewGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pr   entity.PullRequest
		want bool
	}{
		{
			name: "open non-draft PR with green CI and clean mergeable state is ready",
			pr: entity.PullRequest{
				State:          entity.PRStateOpen,
				IsDraft:        false,
				CIStatus:       entity.CIStatusSuccess,
				MergeableState: entity.MergeableStateClean,
			},
			want: true,
		},
		{
			name: "draft PR is gated out",
			pr: entity.PullRequest{
				State:          entity.PRStateOpen,
				IsDraft:        true,
				CIStatus:       entity.CIStatusSuccess,
				MergeableState: entity.MergeableStateClean,
			},
			want: false,
		},
		{
			name: "failing CI PR is gated out",
			pr: entity.PullRequest{
				State:          entity.PRStateOpen,
				IsDraft:        false,
				CIStatus:       entity.CIStatusFailure,
				MergeableState: entity.MergeableStateClean,
			},
			want: false,
		},
		{
			name: "pending CI PR is gated out",
			pr: entity.PullRequest{
				State:          entity.PRStateOpen,
				IsDraft:        false,
				CIStatus:       entity.CIStatusPending,
				MergeableState: entity.MergeableStateClean,
			},
			want: false,
		},
		{
			name: "conflicting PR is gated out",
			pr: entity.PullRequest{
				State:          entity.PRStateOpen,
				IsDraft:        false,
				CIStatus:       entity.CIStatusSuccess,
				MergeableState: entity.MergeableStateConflicting,
			},
			want: false,
		},
		{
			name: "closed or merged PR is gated out",
			pr: entity.PullRequest{
				State:          entity.PRStateMerged,
				IsDraft:        false,
				CIStatus:       entity.CIStatusSuccess,
				MergeableState: entity.MergeableStateClean,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.pr.IsReadyForReviewGate(); got != tt.want {
				t.Errorf("IsReadyForReviewGate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserPRState_IsSnoozedFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		state     entity.UserPRState
		headSHA   string
		ciStatus  entity.CIStatus
		wantSnooz bool
	}{
		{
			name: "not snoozed when condition is empty",
			state: entity.UserPRState{
				SnoozedUntilCondition: entity.SnoozeConditionNone,
			},
			headSHA:   "sha-1",
			ciStatus:  entity.CIStatusPending,
			wantSnooz: false,
		},
		{
			name: "snoozed until next commit remains snoozed on same SHA",
			state: entity.UserPRState{
				SnoozedUntilCondition: entity.SnoozeConditionNextCommit,
				SnoozedAtSHA:          "sha-1",
			},
			headSHA:   "sha-1",
			ciStatus:  entity.CIStatusSuccess,
			wantSnooz: true,
		},
		{
			name: "snoozed until next commit wakes up when SHA changes",
			state: entity.UserPRState{
				SnoozedUntilCondition: entity.SnoozeConditionNextCommit,
				SnoozedAtSHA:          "sha-1",
			},
			headSHA:   "sha-2",
			ciStatus:  entity.CIStatusSuccess,
			wantSnooz: false,
		},
		{
			name: "snoozed until CI green remains snoozed while CI is failing",
			state: entity.UserPRState{
				SnoozedUntilCondition: entity.SnoozeConditionCIGreen,
				SnoozedAtSHA:          "sha-1",
			},
			headSHA:   "sha-1",
			ciStatus:  entity.CIStatusFailure,
			wantSnooz: true,
		},
		{
			name: "snoozed until CI green wakes up when CI succeeds",
			state: entity.UserPRState{
				SnoozedUntilCondition: entity.SnoozeConditionCIGreen,
				SnoozedAtSHA:          "sha-1",
			},
			headSHA:   "sha-1",
			ciStatus:  entity.CIStatusSuccess,
			wantSnooz: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.state.IsSnoozedFor(tt.headSHA, tt.ciStatus); got != tt.wantSnooz {
				t.Errorf("IsSnoozedFor(%q, %q) = %v, want %v", tt.headSHA, tt.ciStatus, got, tt.wantSnooz)
			}
		})
	}
}

func TestRepositoryAndPullRequest_Validate(t *testing.T) {
	t.Parallel()

	invalidRepo := entity.Repository{}
	if err := invalidRepo.Validate(); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty repository, got %v", err)
	}

	validRepo := entity.Repository{
		GitHubRepoID: 101,
		Owner:        "arvore",
		Name:         "web-app",
		FullName:     "arvore/web-app",
	}
	if err := validRepo.Validate(); err != nil {
		t.Fatalf("expected valid repository, got %v", err)
	}

	invalidPR := entity.PullRequest{}
	if err := invalidPR.Validate(); !errors.Is(err, entity.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty pull request, got %v", err)
	}

	validPR := entity.PullRequest{
		RepositoryID: 1,
		GitHubPRID:   5001,
		Number:       42,
		Title:        "feat: add dashboard",
		HeadSHA:      "abc1234",
	}
	if err := validPR.Validate(); err != nil {
		t.Fatalf("expected valid pull request, got %v", err)
	}
}
