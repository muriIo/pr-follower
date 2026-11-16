package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
)

// syncJobDatabases runs the store's contract on SQLite, and on Postgres too when TEST_DATABASE_URL
// points at a throwaway database (the claim is a conditional upsert both must honour).
func syncJobDatabases(t *testing.T) map[string]*sql.DB {
	t.Helper()
	dbs := map[string]*sql.DB{"sqlite": openInMemoryTestDB(t)}
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		db, err := database.Connect(context.Background(), database.Options{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 1})
		if err != nil {
			t.Fatalf("connect and migrate postgres: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		dbs["postgres"] = db
	}
	return dbs
}

// testUserID keeps the Postgres runs (one shared database) from seeing each other's rows.
func testUserID(t *testing.T, n uint64) uint64 {
	t.Helper()
	return uint64(time.Now().UnixNano()%1_000_000_000)*100 + n
}

func TestSyncJobStore_OneRunAtATimeUntilItGoesStale(t *testing.T) {
	for name, db := range syncJobDatabases(t) {
		t.Run(name, func(t *testing.T) {
			store := persistence.NewSyncJobStore(db)
			ctx := context.Background()
			user := testUserID(t, 1)
			t0 := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)

			if _, err := store.GetSyncJob(ctx, user); !errors.Is(err, entity.ErrNotFound) {
				t.Fatalf("a user who never synced: %v, want ErrNotFound", err)
			}
			claimed, err := store.ClaimSyncJob(ctx, user, "run-a", t0, t0.Add(-15*time.Second))
			if err != nil || !claimed {
				t.Fatalf("first claim = %v, %v", claimed, err)
			}
			// Another replica, a second later: run-a is alive.
			t1 := t0.Add(time.Second)
			if claimed, err := store.ClaimSyncJob(ctx, user, "run-b", t1, t1.Add(-15*time.Second)); err != nil || claimed {
				t.Fatalf("claim over a live run = %v, %v; want not claimed", claimed, err)
			}

			progress := entity.SyncProgress{Total: 4, Done: 2, Current: "acme/web"}
			if cancel, err := store.HeartbeatSyncJob(ctx, user, "run-a", progress, t1); err != nil || cancel {
				t.Fatalf("heartbeat = %v, %v", cancel, err)
			}
			job, err := store.GetSyncJob(ctx, user)
			if err != nil || job.RunID != "run-a" || job.State != entity.SyncJobRunning || job.Progress != progress ||
				!job.StartedAt.Equal(t0) || !job.HeartbeatAt.Equal(t1) {
				t.Fatalf("job = %+v, %v", job, err)
			}

			// Twenty seconds without a heartbeat: stale, so a new run may claim it.
			t2 := t1.Add(20 * time.Second)
			if ok, _ := store.RequestSyncJobCancel(ctx, user, t2.Add(-15*time.Second)); ok {
				t.Fatal("a stale run is not running: nothing to cancel")
			}
			if claimed, err := store.ClaimSyncJob(ctx, user, "run-b", t2, t2.Add(-15*time.Second)); err != nil || !claimed {
				t.Fatalf("claim over a stale run = %v, %v", claimed, err)
			}
			if _, err := store.HeartbeatSyncJob(ctx, user, "run-a", progress, t2); !errors.Is(err, entity.ErrNotFound) {
				t.Fatalf("the old run's heartbeat: %v, want ErrNotFound (it lost its claim)", err)
			}
			if err := store.FinishSyncJob(ctx, entity.SyncJob{UserID: user, RunID: "run-a", State: entity.SyncJobDone, FinishedAt: t2}); err != nil {
				t.Fatalf("finish old run: %v", err)
			}
			if job, _ := store.GetSyncJob(ctx, user); job.RunID != "run-b" || job.State != entity.SyncJobRunning ||
				job.Progress != (entity.SyncProgress{}) {
				t.Fatalf("the old run overwrote the new one: %+v", job)
			}
		})
	}
}

func TestSyncJobStore_CancelRequestReachesTheRunningRun(t *testing.T) {
	for name, db := range syncJobDatabases(t) {
		t.Run(name, func(t *testing.T) {
			store := persistence.NewSyncJobStore(db)
			ctx := context.Background()
			user := testUserID(t, 2)
			t0 := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
			stale := t0.Add(-15 * time.Second)

			if ok, _ := store.RequestSyncJobCancel(ctx, user, stale); ok {
				t.Fatal("nothing running means nothing to cancel")
			}
			if _, err := store.ClaimSyncJob(ctx, user, "run-a", t0, stale); err != nil {
				t.Fatal(err)
			}
			if ok, err := store.RequestSyncJobCancel(ctx, user, stale); err != nil || !ok {
				t.Fatalf("cancel a running sync = %v, %v", ok, err)
			}
			if cancel, err := store.HeartbeatSyncJob(ctx, user, "run-a", entity.SyncProgress{}, t0); err != nil || !cancel {
				t.Fatalf("the run must learn of the cancel at its heartbeat: %v, %v", cancel, err)
			}
		})
	}
}

func TestSyncJobStore_FinishedRunsKeepTheirResultAndError(t *testing.T) {
	for name, db := range syncJobDatabases(t) {
		t.Run(name, func(t *testing.T) {
			store := persistence.NewSyncJobStore(db)
			ctx := context.Background()
			t0 := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
			reset := t0.Add(time.Hour)

			done := testUserID(t, 3)
			_, _ = store.ClaimSyncJob(ctx, done, "run", t0, t0)
			result := &entity.SyncResult{
				Repositories: 3, Added: 1, Updated: 2, Closed: 1, Merged: 1, Skipped: 1, RateLimitedUntil: reset,
				Failed: []entity.SyncFailure{{Repository: "acme/api", Err: fmt.Errorf("read: %w", entity.ErrForbidden)}},
			}
			finished := t0.Add(time.Minute)
			if err := store.FinishSyncJob(ctx, entity.SyncJob{
				UserID: done, RunID: "run", State: entity.SyncJobDone, Result: result,
				Progress: entity.SyncProgress{Total: 3, Done: 3}, StartedAt: t0, HeartbeatAt: finished, FinishedAt: finished,
			}); err != nil {
				t.Fatal(err)
			}
			job, err := store.GetSyncJob(ctx, done)
			if err != nil || job.State != entity.SyncJobDone || job.Result == nil || !job.FinishedAt.Equal(finished) {
				t.Fatalf("done job = %+v, %v", job, err)
			}
			got := job.Result
			if got.Repositories != 3 || got.Added != 1 || got.Updated != 2 || got.Closed != 1 || got.Merged != 1 ||
				got.Skipped != 1 || !got.RateLimitedUntil.Equal(reset) || len(got.Failed) != 1 ||
				got.Failed[0].Repository != "acme/api" || !errors.Is(got.Failed[0].Err, entity.ErrForbidden) {
				t.Fatalf("result = %+v", got)
			}

			failed := testUserID(t, 4)
			_, _ = store.ClaimSyncJob(ctx, failed, "run", t0, t0)
			if err := store.FinishSyncJob(ctx, entity.SyncJob{
				UserID: failed, RunID: "run", State: entity.SyncJobFailed, StartedAt: t0, FinishedAt: finished,
				Err: &entity.RateLimitedError{ResetAt: reset},
			}); err != nil {
				t.Fatal(err)
			}
			job, _ = store.GetSyncJob(ctx, failed)
			var limited *entity.RateLimitedError
			if job.State != entity.SyncJobFailed || job.Result != nil || !errors.As(job.Err, &limited) || !limited.ResetAt.Equal(reset) {
				t.Fatalf("failed job = %+v", job)
			}
		})
	}
}
