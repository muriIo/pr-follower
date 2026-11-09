package service

import (
	"strconv"
	"strings"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

type RiskScoringInput struct {
	DepScope        entity.DependencyScope
	IsDirectDep     bool
	SemverBump      entity.SemverBumpType
	CIStatus        entity.CIStatus
	DirectImports   int
	HasBreakingNote bool
}

type RiskScoringResult struct {
	Verdict    entity.RiskVerdict
	Score      int
	SummaryKey string
}

func ComputeDependabotRiskVerdict(in RiskScoringInput) RiskScoringResult {
	if in.CIStatus == entity.CIStatusFailure {
		return RiskScoringResult{
			Verdict:    entity.RiskVerdictHigh,
			Score:      95,
			SummaryKey: "dependabot.verdict.high.ci_failing",
		}
	}

	if in.DepScope == entity.DependencyScopeProd &&
		in.IsDirectDep &&
		(in.SemverBump == entity.SemverBumpMajor || in.HasBreakingNote) {
		return RiskScoringResult{
			Verdict:    entity.RiskVerdictHigh,
			Score:      88,
			SummaryKey: "dependabot.verdict.high.direct_prod_major",
		}
	}

	if in.DepScope == entity.DependencyScopeDev &&
		in.SemverBump != entity.SemverBumpMajor &&
		!in.HasBreakingNote {
		return RiskScoringResult{
			Verdict:    entity.RiskVerdictLow,
			Score:      12,
			SummaryKey: "dependabot.verdict.low.dev_safe",
		}
	}

	if in.DepScope == entity.DependencyScopeTransitive &&
		in.DirectImports == 0 &&
		in.SemverBump != entity.SemverBumpMajor &&
		!in.HasBreakingNote {
		return RiskScoringResult{
			Verdict:    entity.RiskVerdictLow,
			Score:      18,
			SummaryKey: "dependabot.verdict.low.transitive_zero_imports",
		}
	}

	if in.DepScope == entity.DependencyScopeTransitive &&
		(in.SemverBump == entity.SemverBumpMajor || in.HasBreakingNote) {
		return RiskScoringResult{
			Verdict:    entity.RiskVerdictMedium,
			Score:      58,
			SummaryKey: "dependabot.verdict.medium.transitive_major",
		}
	}

	return RiskScoringResult{
		Verdict:    entity.RiskVerdictMedium,
		Score:      45,
		SummaryKey: "dependabot.verdict.medium.direct_prod_minor",
	}
}

func ClassifySemverBump(fromVersion, toVersion string) entity.SemverBumpType {
	fMaj, fMin, fPatch, ok1 := parseSemverParts(fromVersion)
	tMaj, tMin, tPatch, ok2 := parseSemverParts(toVersion)
	if !ok1 || !ok2 {
		return entity.SemverBumpUnknown
	}
	if tMaj > fMaj {
		return entity.SemverBumpMajor
	}
	if tMaj == fMaj && tMin > fMin {
		return entity.SemverBumpMinor
	}
	if tMaj == fMaj && tMin == fMin && tPatch >= fPatch {
		return entity.SemverBumpPatch
	}
	return entity.SemverBumpUnknown
}

func parseSemverParts(v string) (int, int, int, bool) {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(clean, "-+"); idx != -1 {
		clean = clean[:idx]
	}
	parts := strings.Split(clean, ".")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return maj, min, pat, true
}
