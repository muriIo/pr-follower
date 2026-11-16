package dependabot_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
)

// filesFailing is a GitHub gateway whose PR files read fails with err.
type filesFailing struct {
	fakeGHGateway
	err error
}

func (f *filesFailing) ListPRChangedFiles(context.Context, entity.Repository, int) ([]entity.ChangedFile, error) {
	return nil, f.err
}

type failureLog struct {
	mu       sync.Mutex
	recorded []entity.AnalysisFailure
}

func (l *failureLog) RecordAnalysisFailure(_ context.Context, f entity.AnalysisFailure) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recorded = append(l.recorded, f)
	return nil
}

func (l *failureLog) GetAnalysisFailure(context.Context, uint64, string) (*entity.AnalysisFailure, error) {
	return nil, entity.ErrNotFound
}

func analysisOf(gh port.GitHubGateway, log *failureLog) *dependabot.AnalyzeUseCase {
	prRepo := &fakePRRepo{pr: &entity.PullRequest{
		ID: 55, RepositoryID: 10, Number: 19, Title: "chore(deps): bump axios from 1.7.2 to 2.0.0",
		IsDependabot: true, State: entity.PRStateOpen, HeadSHA: "sha-axios-2",
	}}
	repoRepo := &fakeRepoRepo{repo: &entity.Repository{ID: 10, Owner: "arvore", Name: "api", FullName: "arvore/api"}}
	return dependabot.NewAnalyzeUseCase(prRepo, repoRepo, gh, []port.DependencyAnalyzer{&fakeAnalyzer{}}, &fakeScanner{}, &fakePub{},
		dependabot.WithFailureLog(log))
}

func TestAnalyzePR_RecordsWhyAnAnalysisFailedForTheCommit(t *testing.T) {
	t.Parallel()
	log := &failureLog{}
	uc := analysisOf(&filesFailing{err: fmt.Errorf("read files: %w", entity.ErrForbidden)}, log)

	before := time.Now()
	if _, err := uc.AnalyzePR(context.Background(), 55); !errors.Is(err, entity.ErrForbidden) {
		t.Fatalf("AnalyzePR() error = %v, want the GitHub refusal", err)
	}

	if len(log.recorded) != 1 {
		t.Fatalf("expected one recorded failure, got %+v", log.recorded)
	}
	got := log.recorded[0]
	if got.PullRequestID != 55 || got.HeadSHA != "sha-axios-2" || got.ErrorKind != "forbidden" ||
		got.Message != "read files: forbidden by github" || got.FailedAt.Before(before) {
		t.Fatalf("recorded failure = %+v", got)
	}
}

func TestAnalyzePR_RecordsAnUnreadableLockfileButNotACancel(t *testing.T) {
	t.Parallel()

	// No analyzer reads the lockfile: a lasting reason, recorded so the Radar can say it.
	log := &failureLog{}
	noLockfile := &filesFailing{}
	uc := analysisOf(noLockfile, log)
	if _, err := uc.AnalyzePR(context.Background(), 55); !errors.Is(err, entity.ErrNotAnalysable) {
		t.Fatalf("AnalyzePR() error = %v, want ErrNotAnalysable", err)
	}
	if len(log.recorded) != 1 || log.recorded[0].ErrorKind != "not_analysable" {
		t.Fatalf("expected the unreadable lockfile to be recorded, got %+v", log.recorded)
	}

	// Cancelled by the user or by shutdown: nothing failed, nothing is recorded.
	for _, cancelErr := range []error{entity.ErrOperationCanceled, context.Canceled} {
		log := &failureLog{}
		uc := analysisOf(&filesFailing{err: cancelErr}, log)
		_, _ = uc.AnalyzePR(context.Background(), 55)
		if len(log.recorded) != 0 {
			t.Fatalf("a cancelled analysis (%v) must not be recorded as failed: %+v", cancelErr, log.recorded)
		}
	}
}

func TestAnalyzePR_AFailureIsRecordedEvenWhenTheCallerGaveUp(t *testing.T) {
	t.Parallel()
	log := &failureLog{}
	uc := analysisOf(&filesFailing{err: context.DeadlineExceeded}, log)

	// The runner's per-job time limit ran out: that is a failure worth showing.
	if _, err := uc.AnalyzePR(context.Background(), 55); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AnalyzePR() error = %v", err)
	}
	if len(log.recorded) != 1 || log.recorded[0].ErrorKind != "timeout" {
		t.Fatalf("a timed-out analysis is recorded as a timeout, got %+v", log.recorded)
	}
}
