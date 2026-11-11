package repoimport_test

import (
	"context"
	"sort"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
)

type recordingScheduler struct{ ids []uint64 }

func (r *recordingScheduler) ScheduleAnalysis(_ context.Context, prID uint64) {
	r.ids = append(r.ids, prID)
}

func TestImport_SchedulesTheAnalysisOfOpenDependabotPRs(t *testing.T) {
	t.Parallel()
	_, api, repos, events, prs, checks := setupBackfill()
	twoOpenPRs(api) // #7 and #9 are human PRs, #8 is Dependabot's
	scheduler := &recordingScheduler{}
	uc := repoimport.NewUseCase(fakeTokens{}, api, repos, prs, checks, fakeUsers{}, events, nil, nil, repoimport.WithAnalysisScheduler(scheduler))
	ctx := context.Background()

	repo, err := uc.Import(ctx, userID, 20, 200)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}
	if want := prs.prs[[2]uint64{repo.ID, 8}].ID; len(scheduler.ids) != 1 || scheduler.ids[0] != want {
		t.Fatalf("only the Dependabot PR is scheduled (id %d): %v", want, scheduler.ids)
	}

	// Importing again schedules it again (analysis converges: a commit already analysed is a no-op).
	if _, err := uc.Import(ctx, userID, 20, 200); err != nil {
		t.Fatal(err)
	}
	ids := append([]uint64(nil), scheduler.ids...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("expected the same PR scheduled on each import: %v", ids)
	}
}

func TestImport_WorksWithoutAScheduler(t *testing.T) {
	t.Parallel()
	uc, api, _, _, _, _ := setupBackfill()
	twoOpenPRs(api)
	if _, err := uc.Import(context.Background(), userID, 20, 200); err != nil {
		t.Fatalf("the app credentials are optional: %v", err)
	}
}
