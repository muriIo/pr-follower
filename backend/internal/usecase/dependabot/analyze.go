package dependabot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// inflight makes concurrent requests for the same commit (a webhook, an import and a click)
	// share one analysis: one tarball scan, one stored result, one event.
	inflight flightGroup[*entity.DependabotAnalysis]
	// failures (optional) remembers why an analysis failed, so the Radar can say so.
	failures port.AnalysisFailureStore
}

// AnalyzeOption adjusts an AnalyzeUseCase.
type AnalyzeOption func(*AnalyzeUseCase)

// WithFailureLog records every failed analysis (not a cancelled one) in store.
func WithFailureLog(store port.AnalysisFailureStore) AnalyzeOption {
	return func(uc *AnalyzeUseCase) { uc.failures = store }
}

// recordTimeout bounds recording a failure, which happens even when the analysis ran out of time.
const recordTimeout = 5 * time.Second

func NewAnalyzeUseCase(
	prRepo port.PullRequestRepository,
	repoRepo port.RepositoryRepository,
	ghGateway port.GitHubGateway,
	analyzers []port.DependencyAnalyzer,
	scanner port.BlastRadiusScanner,
	pub port.EventPublisher,
	opts ...AnalyzeOption,
) *AnalyzeUseCase {
	uc := &AnalyzeUseCase{
		prRepo:    prRepo,
		repoRepo:  repoRepo,
		ghGateway: ghGateway,
		analyzers: analyzers,
		scanner:   scanner,
		pub:       pub,
	}
	for _, opt := range opts {
		opt(uc)
	}
	return uc
}

// AnalyzePRForUser analyses a PR on behalf of a signed-in user. The analysis reads GitHub as the
// app, so only a follower of the PR's repository may ask for it (and see its result).
func (uc *AnalyzeUseCase) AnalyzePRForUser(ctx context.Context, userID, prID uint64) (*entity.DependabotAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return nil, err
	}
	followed, err := uc.repoRepo.ListFollowedByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, repo := range followed {
		if repo.ID == pr.RepositoryID {
			return uc.AnalyzePR(ctx, prID)
		}
	}
	return nil, entity.ErrNotFound
}

// AnalyzePR analyses an open Dependabot PR at its head commit and stores the result. It converges
// (ADR-0011): a commit already analysed is returned as is, and concurrent calls for the same
// commit share one run. A PR that names no single package (a grouped update) is refused rather
// than analysed as "unknown".
func (uc *AnalyzeUseCase) AnalyzePR(ctx context.Context, prID uint64) (*entity.DependabotAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}

	pr, err := uc.prRepo.GetByID(ctx, prID)
	if err != nil {
		return nil, err
	}
	if !pr.IsDependabot {
		return nil, fmt.Errorf("%w: PR #%d is not a Dependabot PR", entity.ErrInvalidInput, pr.Number)
	}
	if pr.State != entity.PRStateOpen {
		return nil, fmt.Errorf("%w: PR #%d is not open", entity.ErrConflict, pr.Number)
	}
	if _, _, _, ok := parseDependabotTitle(pr.Title); !ok {
		return nil, fmt.Errorf("%w: PR #%d does not name a single package (a grouped update?)", entity.ErrNotAnalysable, pr.Number)
	}

	return uc.inflight.do(ctx, fmt.Sprintf("analysis:%d:%s", pr.ID, pr.HeadSHA), func(callCtx context.Context) (*entity.DependabotAnalysis, error) {
		analysis, err := uc.analyze(callCtx, pr)
		if err != nil {
			uc.recordFailure(callCtx, pr, err)
		}
		return analysis, err
	})
}

// recordFailure remembers why the commit's analysis failed. A cancel (the user's, or the server
// stopping) is not a failure: the next webhook, sync or click runs it again.
func (uc *AnalyzeUseCase) recordFailure(ctx context.Context, pr *entity.PullRequest, err error) {
	if uc.failures == nil || errors.Is(err, context.Canceled) || errors.Is(err, entity.ErrOperationCanceled) {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	failure := entity.AnalysisFailure{
		PullRequestID: pr.ID, HeadSHA: pr.HeadSHA,
		ErrorKind: entity.ErrorKindOf(err), Message: err.Error(), FailedAt: time.Now().UTC(),
	}
	if recordErr := uc.failures.RecordAnalysisFailure(recordCtx, failure); recordErr != nil {
		slog.Warn("could not record a failed dependabot analysis", "pr_id", pr.ID, "err", recordErr)
	}
}

func (uc *AnalyzeUseCase) analyze(ctx context.Context, pr *entity.PullRequest) (*entity.DependabotAnalysis, error) {
	if cached, err := uc.prRepo.GetAnalysisByPRAndSHA(ctx, pr.ID, pr.HeadSHA); err == nil && cached != nil {
		return cached, nil
	}

	repo, err := uc.repoRepo.GetByID(ctx, pr.RepositoryID)
	if err != nil {
		return nil, err
	}

	pkgName, fromVer, toVer, _ := parseDependabotTitle(pr.Title)
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
		return nil, fmt.Errorf("%w: no analyzer reads the lockfile of PR #%d", entity.ErrNotAnalysable, pr.Number)
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
		DepScope:    analysis.DepScope,
		IsDirectDep: analysis.IsDirectDep,
		SemverBump:  analysis.SemverBumpType,
		// CI is judged live (it gates the batch merge), never frozen into a verdict kept for the commit.
		CIStatus:        entity.CIStatusNone,
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
	if pkg, fromVer, toVer, ok := parseDependabotTitle(title); ok {
		return pkg, fromVer, toVer
	}
	return "unknown", "0.0.0", "0.0.0"
}

// parseDependabotTitle reads "bump <package> from <a> to <b>"; ok is false for any other title.
func parseDependabotTitle(title string) (pkg, fromVer, toVer string, ok bool) {
	m := dependabotTitleRegex.FindStringSubmatch(title)
	if len(m) != 4 {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}
