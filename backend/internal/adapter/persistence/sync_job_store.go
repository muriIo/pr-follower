package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence/sqlcgen"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// SyncJobStore is the port.SyncJobStore over the sync_jobs table.
type SyncJobStore struct {
	q *sqlcgen.Queries
}

var _ port.SyncJobStore = (*SyncJobStore)(nil)

func NewSyncJobStore(db *sql.DB) *SyncJobStore {
	return &SyncJobStore{q: sqlcgen.New(db)}
}

// storedError keeps an error so that errors.Is/As answer the same after it is read back.
type storedError struct {
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	ResetAt time.Time `json:"reset_at,omitzero"`
}

func toStoredError(err error) *storedError {
	if err == nil {
		return nil
	}
	s := &storedError{Kind: entity.ErrorKindOf(err), Message: err.Error()}
	var limited *entity.RateLimitedError
	if errors.As(err, &limited) {
		s.ResetAt = limited.ResetAt.UTC()
	}
	return s
}

func (s *storedError) restore() error {
	if s == nil {
		return nil
	}
	return entity.ErrorOfKind(s.Kind, s.Message, s.ResetAt)
}

type storedFailure struct {
	Repository string       `json:"repository"`
	Err        *storedError `json:"err,omitempty"`
}

type storedResult struct {
	Repositories     int             `json:"repositories"`
	Added            int             `json:"added"`
	Updated          int             `json:"updated"`
	Closed           int             `json:"closed"`
	Merged           int             `json:"merged"`
	Skipped          int             `json:"skipped"`
	RateLimitedUntil time.Time       `json:"rate_limited_until,omitzero"`
	Failed           []storedFailure `json:"failed"`
}

func encodeResult(r *entity.SyncResult) (string, error) {
	if r == nil {
		return "", nil
	}
	s := storedResult{
		Repositories: r.Repositories, Added: r.Added, Updated: r.Updated, Closed: r.Closed,
		Merged: r.Merged, Skipped: r.Skipped, RateLimitedUntil: r.RateLimitedUntil.UTC(),
		Failed: make([]storedFailure, 0, len(r.Failed)),
	}
	for _, f := range r.Failed {
		s.Failed = append(s.Failed, storedFailure{Repository: f.Repository, Err: toStoredError(f.Err)})
	}
	b, err := json.Marshal(s)
	return string(b), err
}

func decodeResult(raw string) (*entity.SyncResult, error) {
	if raw == "" {
		return nil, nil
	}
	var s storedResult
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, err
	}
	r := &entity.SyncResult{
		Repositories: s.Repositories, Added: s.Added, Updated: s.Updated, Closed: s.Closed,
		Merged: s.Merged, Skipped: s.Skipped, RateLimitedUntil: s.RateLimitedUntil,
	}
	for _, f := range s.Failed {
		r.Failed = append(r.Failed, entity.SyncFailure{Repository: f.Repository, Err: f.Err.restore()})
	}
	return r, nil
}

func encodeError(err error) (string, error) {
	s := toStoredError(err)
	if s == nil {
		return "", nil
	}
	b, jerr := json.Marshal(s)
	return string(b), jerr
}

func decodeError(raw string) (error, error) {
	if raw == "" {
		return nil, nil
	}
	var s storedError
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, err
	}
	return s.restore(), nil
}

func (s *SyncJobStore) ClaimSyncJob(ctx context.Context, userID uint64, runID string, now, staleBefore time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, context.Cause(ctx)
	}
	_, err := s.q.ClaimSyncJob(ctx, sqlcgen.ClaimSyncJobParams{
		UserID: int64(userID), RunID: runID, StartedAt: now.UTC(),
		HeartbeatMs: now.UnixMilli(), StaleBeforeMs: staleBefore.UnixMilli(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // a live run holds it
	}
	return err == nil, err
}

func (s *SyncJobStore) GetSyncJob(ctx context.Context, userID uint64) (entity.SyncJob, error) {
	if err := ctx.Err(); err != nil {
		return entity.SyncJob{}, context.Cause(ctx)
	}
	row, err := s.q.GetSyncJob(ctx, int64(userID))
	if errors.Is(err, sql.ErrNoRows) {
		return entity.SyncJob{}, entity.ErrNotFound
	}
	if err != nil {
		return entity.SyncJob{}, err
	}
	result, err := decodeResult(row.ResultJson)
	if err != nil {
		return entity.SyncJob{}, err
	}
	jobErr, err := decodeError(row.ErrorJson)
	if err != nil {
		return entity.SyncJob{}, err
	}
	job := entity.SyncJob{
		UserID: uint64(row.UserID), RunID: row.RunID, State: entity.SyncJobState(row.State),
		Progress: entity.SyncProgress{
			Total: int(row.ProgressTotal), Done: int(row.ProgressDone), Current: row.ProgressCurrent,
		},
		Result: result, Err: jobErr, CancelRequested: row.CancelRequested,
		StartedAt: row.StartedAt.UTC(), HeartbeatAt: time.UnixMilli(row.HeartbeatMs).UTC(),
	}
	if row.FinishedAt.Valid {
		job.FinishedAt = row.FinishedAt.Time.UTC()
	}
	return job, nil
}

func (s *SyncJobStore) HeartbeatSyncJob(ctx context.Context, userID uint64, runID string, p entity.SyncProgress, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, context.Cause(ctx)
	}
	cancel, err := s.q.HeartbeatSyncJob(ctx, sqlcgen.HeartbeatSyncJobParams{
		ProgressTotal: int64(p.Total), ProgressDone: int64(p.Done), ProgressCurrent: p.Current,
		HeartbeatMs: now.UnixMilli(), UserID: int64(userID), RunID: runID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, entity.ErrNotFound // another run took over, or this one already ended
	}
	return cancel, err
}

func (s *SyncJobStore) FinishSyncJob(ctx context.Context, job entity.SyncJob) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	result, err := encodeResult(job.Result)
	if err != nil {
		return err
	}
	jobErr, err := encodeError(job.Err)
	if err != nil {
		return err
	}
	return s.q.FinishSyncJob(ctx, sqlcgen.FinishSyncJobParams{
		State: string(job.State), ProgressTotal: int64(job.Progress.Total), ProgressDone: int64(job.Progress.Done),
		ProgressCurrent: job.Progress.Current, ResultJson: result, ErrorJson: jobErr,
		HeartbeatMs: job.FinishedAt.UnixMilli(),
		FinishedAt:  sql.NullTime{Time: job.FinishedAt.UTC(), Valid: !job.FinishedAt.IsZero()},
		UserID:      int64(job.UserID), RunID: job.RunID,
	})
}

func (s *SyncJobStore) RequestSyncJobCancel(ctx context.Context, userID uint64, staleBefore time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, context.Cause(ctx)
	}
	n, err := s.q.RequestSyncJobCancel(ctx, sqlcgen.RequestSyncJobCancelParams{
		UserID: int64(userID), StaleBeforeMs: staleBefore.UnixMilli(),
	})
	return n > 0, err
}
