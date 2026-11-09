package service_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

func TestComputeDependabotRiskVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       service.RiskScoringInput
		wantVerdict entity.RiskVerdict
		wantKey     string
	}{
		{
			name: "devDependency patch bump with green CI and zero direct imports is LOW risk",
			input: service.RiskScoringInput{
				DepScope:        entity.DependencyScopeDev,
				IsDirectDep:     true,
				SemverBump:      entity.SemverBumpPatch,
				CIStatus:        entity.CIStatusSuccess,
				DirectImports:   0,
				HasBreakingNote: false,
			},
			wantVerdict: entity.RiskVerdictLow,
			wantKey:     "dependabot.verdict.low.dev_safe",
		},
		{
			name: "transitive dependency minor bump with green CI and zero direct imports is LOW risk",
			input: service.RiskScoringInput{
				DepScope:        entity.DependencyScopeTransitive,
				IsDirectDep:     false,
				SemverBump:      entity.SemverBumpMinor,
				CIStatus:        entity.CIStatusSuccess,
				DirectImports:   0,
				HasBreakingNote: false,
			},
			wantVerdict: entity.RiskVerdictLow,
			wantKey:     "dependabot.verdict.low.transitive_zero_imports",
		},
		{
			name: "direct prod dependency minor bump with green CI is MEDIUM risk",
			input: service.RiskScoringInput{
				DepScope:        entity.DependencyScopeProd,
				IsDirectDep:     true,
				SemverBump:      entity.SemverBumpMinor,
				CIStatus:        entity.CIStatusSuccess,
				DirectImports:   3,
				HasBreakingNote: false,
			},
			wantVerdict: entity.RiskVerdictMedium,
			wantKey:     "dependabot.verdict.medium.direct_prod_minor",
		},
		{
			name: "transitive dependency with major bump is MEDIUM risk",
			input: service.RiskScoringInput{
				DepScope:        entity.DependencyScopeTransitive,
				IsDirectDep:     false,
				SemverBump:      entity.SemverBumpMajor,
				CIStatus:        entity.CIStatusSuccess,
				DirectImports:   0,
				HasBreakingNote: true,
			},
			wantVerdict: entity.RiskVerdictMedium,
			wantKey:     "dependabot.verdict.medium.transitive_major",
		},
		{
			name: "direct prod dependency with major bump and active imports is HIGH risk",
			input: service.RiskScoringInput{
				DepScope:        entity.DependencyScopeProd,
				IsDirectDep:     true,
				SemverBump:      entity.SemverBumpMajor,
				CIStatus:        entity.CIStatusSuccess,
				DirectImports:   4,
				HasBreakingNote: true,
			},
			wantVerdict: entity.RiskVerdictHigh,
			wantKey:     "dependabot.verdict.high.direct_prod_major",
		},
		{
			name: "failing CI on any bump is HIGH risk",
			input: service.RiskScoringInput{
				DepScope:        entity.DependencyScopeDev,
				IsDirectDep:     true,
				SemverBump:      entity.SemverBumpPatch,
				CIStatus:        entity.CIStatusFailure,
				DirectImports:   0,
				HasBreakingNote: false,
			},
			wantVerdict: entity.RiskVerdictHigh,
			wantKey:     "dependabot.verdict.high.ci_failing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := service.ComputeDependabotRiskVerdict(tt.input)
			if res.Verdict != tt.wantVerdict {
				t.Errorf("Verdict = %q, want %q", res.Verdict, tt.wantVerdict)
			}
			if res.SummaryKey != tt.wantKey {
				t.Errorf("SummaryKey = %q, want %q", res.SummaryKey, tt.wantKey)
			}
			if res.Score < 0 || res.Score > 100 {
				t.Errorf("Score = %d, want between 0 and 100", res.Score)
			}
		})
	}
}

func TestClassifySemverBump(t *testing.T) {
	t.Parallel()

	if got := service.ClassifySemverBump("1.2.3", "1.2.4"); got != entity.SemverBumpPatch {
		t.Errorf("expected PATCH, got %q", got)
	}
	if got := service.ClassifySemverBump("v1.2.3", "v1.3.0"); got != entity.SemverBumpMinor {
		t.Errorf("expected MINOR, got %q", got)
	}
	if got := service.ClassifySemverBump("1.9.0", "2.0.0"); got != entity.SemverBumpMajor {
		t.Errorf("expected MAJOR, got %q", got)
	}
	if got := service.ClassifySemverBump("invalid", "2.0.0"); got != entity.SemverBumpUnknown {
		t.Errorf("expected UNKNOWN, got %q", got)
	}
}
