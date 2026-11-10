package entity_test

import (
	"encoding/json"
	"errors"
	"strings"
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

func TestEntities_JSONSerializationSnakeCase(t *testing.T) {
	t.Parallel()

	repo := entity.Repository{
		ID:            10,
		GitHubRepoID:  5001,
		Owner:         "arvore",
		Name:          "hive-web",
		FullName:      "arvore/hive-web",
		DefaultBranch: "main",
	}
	repoJSON, err := json.Marshal(repo)
	if err != nil {
		t.Fatalf("marshal repo: %v", err)
	}
	for _, key := range []string{`"id":10`, `"full_name":"arvore/hive-web"`, `"default_branch":"main"`} {
		if !strings.Contains(string(repoJSON), key) {
			t.Errorf("expected repo JSON %s to contain %s", string(repoJSON), key)
		}
	}

	pr := entity.PullRequest{
		ID:             142,
		RepositoryID:   10,
		RepositoryName: "arvore/hive-web",
		Number:         142,
		Title:          "feat: live pulse",
		AuthorLogin:    "bianca-dev",
		IsDependabot:   false,
		CIStatus:       entity.CIStatusSuccess,
		MergeableState: entity.MergeableStateClean,
		HeadSHA:        "7f8a9b1",
		BaseRef:        "main",
	}
	prJSON, err := json.Marshal(pr)
	if err != nil {
		t.Fatalf("marshal pr: %v", err)
	}
	for _, key := range []string{`"id":142`, `"repository_name":"arvore/hive-web"`, `"author_login":"bianca-dev"`, `"ci_status":"SUCCESS"`} {
		if !strings.Contains(string(prJSON), key) {
			t.Errorf("expected pr JSON %s to contain %s", string(prJSON), key)
		}
	}

	analysis := entity.DependabotAnalysis{
		PullRequestID:  145,
		PackageName:    "vitest",
		FromVersion:    "2.1.1",
		ToVersion:      "2.1.4",
		SemverBumpType: entity.SemverBumpPatch,
		DepScope:       entity.DependencyScopeDev,
		RiskVerdict:    entity.RiskVerdictLow,
		RiskScore:      12,
	}
	analysisJSON, err := json.Marshal(analysis)
	if err != nil {
		t.Fatalf("marshal analysis: %v", err)
	}
	for _, key := range []string{`"package_name":"vitest"`, `"from_version":"2.1.1"`, `"to_version":"2.1.4"`, `"risk_verdict":"LOW"`, `"risk_score":12`} {
		if !strings.Contains(string(analysisJSON), key) {
			t.Errorf("expected analysis JSON %s to contain %s", string(analysisJSON), key)
		}
	}

	userState := entity.UserPRState{
		PullRequestID: 142,
		UserID:        1,
		TurnState:     entity.TurnStateWaitingOnMe,
	}
	stateJSON, err := json.Marshal(userState)
	if err != nil {
		t.Fatalf("marshal user state: %v", err)
	}
	for _, key := range []string{`"pull_request_id":142`, `"turn_state":"WAITING_ON_ME"`} {
		if !strings.Contains(string(stateJSON), key) {
			t.Errorf("expected userState JSON %s to contain %s", string(stateJSON), key)
		}
	}
}

// The client builds the sentence in the user's language, so activity carries data, never prose.
func TestActivityEvent_JSONCarriesParamsNotASentence(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(entity.ActivityEvent{ID: "e", Type: "pr.opened", Params: map[string]string{"title": "feat: x"}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["summary"]; ok {
		t.Fatalf("activity must not carry a prebuilt summary: %s", raw)
	}
	params, _ := got["params"].(map[string]any)
	if params["title"] != "feat: x" {
		t.Fatalf("params.title: %s", raw)
	}
}
