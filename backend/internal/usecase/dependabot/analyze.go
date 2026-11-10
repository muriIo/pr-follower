package dependabot

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

var dependabotTitleRegex = regexp.MustCompile(`(?i)bump\s+(@?[a-z0-9._/-]+)\s+from\s+v?([0-9a-z.-]+)\s+to\s+v?([0-9a-z.-]+)`)

type AnalyzeUseCase struct {
	prRepo    port.PullRequestRepository
	repoRepo  port.RepositoryRepository
	ghGateway port.GitHubGateway
	analyzers []port.DependencyAnalyzer
	scanner   port.BlastRadiusScanner
	pub       port.EventPublisher
}

func NewAnalyzeUseCase(
	prRepo port.PullRequestRepository,
	repoRepo port.RepositoryRepository,
	ghGateway port.GitHubGateway,
	analyzers []port.DependencyAnalyzer,
	scanner port.BlastRadiusScanner,
	pub port.EventPublisher,
) *AnalyzeUseCase {
	return &AnalyzeUseCase{
		prRepo:    prRepo,
		repoRepo:  repoRepo,
		ghGateway: ghGateway,
		analyzers: analyzers,
		scanner:   scanner,
		pub:       pub,
	}
}

func (uc *AnalyzeUseCase) AnalyzePR(ctx context.Context, prID uint64) (*entity.DependabotAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}

	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return nil, err
	}

	if cached, err := uc.prRepo.GetAnalysisByPRAndSHA(ctx, pr.ID, pr.HeadSHA); err == nil && cached != nil {
		return cached, nil
	}

	repo, err := uc.repoRepo.GetByID(ctx, pr.RepositoryID)
	if err != nil {
		return nil, err
	}

	pkgName, fromVer, toVer := ParseDependabotTitle(pr.Title)
	changedFiles, err := uc.ghGateway.ListPRChangedFiles(ctx, *repo, pr.Number)
	if err != nil {
		return nil, err
	}

	var matchedAnalyzer port.DependencyAnalyzer
	var lockfilePath string
	for _, f := range changedFiles {
		baseName := path.Base(f.Path)
		for _, a := range uc.analyzers {
			if a.CanAnalyze(baseName) {
				matchedAnalyzer = a
				lockfilePath = f.Path
				break
			}
		}
		if matchedAnalyzer != nil {
			break
		}
	}

	if matchedAnalyzer == nil {
		return nil, fmt.Errorf("%w: no analyzer found for PR #%d", entity.ErrInvalidInput, pr.Number)
	}

	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}

	pkgDir := path.Dir(lockfilePath)
	pkgJSONPath := "package.json"
	if pkgDir != "." && pkgDir != "" {
		pkgJSONPath = path.Join(pkgDir, "package.json")
	}

	pkgJSONRaw, _ := uc.ghGateway.GetFileContent(ctx, *repo, pr.HeadSHA, pkgJSONPath)
	lockfileRaw, err := uc.ghGateway.GetFileContent(ctx, *repo, pr.HeadSHA, lockfilePath)
	if err != nil {
		return nil, err
	}

	analysis, err := matchedAnalyzer.AnalyzeLockfile(ctx, port.DependencyAnalysisInput{
		PullRequest:  *pr,
		Repository:   *repo,
		PackageName:  pkgName,
		FromVersion:  fromVer,
		ToVersion:    toVer,
		PackageJSON:  pkgJSONRaw,
		LockfileName: path.Base(lockfilePath),
		LockfileRaw:  lockfileRaw,
	})
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}

	tarStream, err := uc.ghGateway.StreamRepoTarball(ctx, *repo, pr.HeadSHA)
	if err == nil && tarStream != nil {
		defer tarStream.Close()
		targetPkgs := []string{pkgName}
		usages, scanErr := uc.scanner.ScanTarball(ctx, tarStream, targetPkgs)
		if scanErr != nil {
			return nil, scanErr
		}
		analysis.BlastRadiusUsages = usages
	}

	hasBreaking := false
	for _, n := range analysis.AdvisoriesAndNotes {
		if n.IsBreakingNote {
			hasBreaking = true
			break
		}
	}

	scoring := service.ComputeDependabotRiskVerdict(service.RiskScoringInput{
		DepScope:        analysis.DepScope,
		IsDirectDep:     analysis.IsDirectDep,
		SemverBump:      analysis.SemverBumpType,
		CIStatus:        pr.CIStatus,
		DirectImports:   len(analysis.BlastRadiusUsages),
		HasBreakingNote: hasBreaking,
	})

	analysis.RiskVerdict = scoring.Verdict
	analysis.RiskScore = scoring.Score
	analysis.VerdictSummaryKey = scoring.SummaryKey
	analysis.UpdatedAt = time.Now().UTC()

	if err := uc.prRepo.SaveAnalysis(ctx, analysis); err != nil {
		return nil, err
	}

	_ = uc.pub.Publish(ctx, entity.ActivityEvent{
		ID:             fmt.Sprintf("analysis-%d-%s", pr.ID, pr.HeadSHA),
		Type:           "dependabot.analyzed",
		RepositoryID:   repo.ID,
		RepositoryName: repo.FullName,
		PullRequestID:  pr.ID,
		PRNumber:       pr.Number,
		ActorLogin:     "dependabot[bot]",
		Params:         map[string]string{"package": pkgName, "from": fromVer, "to": toVer, "verdict": string(analysis.RiskVerdict)},
		OccurredAt:     time.Now().UTC(),
	})

	return analysis, nil
}

func ParseDependabotTitle(title string) (pkg, fromVer, toVer string) {
	m := dependabotTitleRegex.FindStringSubmatch(title)
	if len(m) == 4 {
		return m[1], m[2], m[3]
	}
	return "unknown", "0.0.0", "0.0.0"
}
