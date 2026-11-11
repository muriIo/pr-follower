package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence/sqlcgen"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// ActivityLog is the database-backed port.ActivityLog shared by every API replica.
type ActivityLog struct {
	q *sqlcgen.Queries
}

var _ port.ActivityLog = (*ActivityLog)(nil)

func NewActivityLog(db *sql.DB) *ActivityLog {
	return &ActivityLog{q: sqlcgen.New(db)}
}

func (l *ActivityLog) Append(ctx context.Context, ev entity.ActivityEvent) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, context.Cause(ctx)
	}
	params, err := json.Marshal(nonNilParams(ev.Params))
	if err != nil {
		return 0, err
	}
	return l.q.InsertActivityEvent(ctx, sqlcgen.InsertActivityEventParams{
		EventID:        ev.ID,
		Type:           ev.Type,
		RepositoryID:   int64(ev.RepositoryID),
		RepositoryName: ev.RepositoryName,
		PullRequestID:  int64(ev.PullRequestID),
		PrNumber:       int64(ev.PRNumber),
		ActorLogin:     ev.ActorLogin,
		ParamsJson:     string(params),
		OccurredAt:     ev.OccurredAt,
	})
}

func (l *ActivityLog) ListSince(ctx context.Context, afterSeq int64, limit int) ([]port.LoggedActivity, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	rows, err := l.q.ListActivityEventsSince(ctx, sqlcgen.ListActivityEventsSinceParams{Seq: afterSeq, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]port.LoggedActivity, 0, len(rows))
	for _, r := range rows {
		out = append(out, port.LoggedActivity{Seq: r.Seq, Event: toActivityEvent(r)})
	}
	return out, nil
}

func (l *ActivityLog) LatestSeq(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, context.Cause(ctx)
	}
	return l.q.GetLatestActivitySeq(ctx)
}

// Recent reads each repository's newest events and merges them, so the query stays portable
// (no array parameter) and uses the (repository_id, seq) index.
func (l *ActivityLog) Recent(ctx context.Context, repositoryIDs []uint64, limit int) ([]entity.ActivityEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	var all []sqlcgen.ActivityEvent
	for _, id := range repositoryIDs {
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}
		rows, err := l.q.ListRecentActivityEventsByRepository(ctx, sqlcgen.ListRecentActivityEventsByRepositoryParams{
			RepositoryID: int64(id), Limit: int32(limit),
		})
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Seq > all[j].Seq })
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]entity.ActivityEvent, 0, len(all))
	for _, r := range all {
		out = append(out, toActivityEvent(r))
	}
	return out, nil
}

func (l *ActivityLog) Prune(ctx context.Context, before time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, context.Cause(ctx)
	}
	return l.q.DeleteActivityEventsBefore(ctx, before)
}

func nonNilParams(p map[string]string) map[string]string {
	if p == nil {
		return map[string]string{}
	}
	return p
}

func toActivityEvent(r sqlcgen.ActivityEvent) entity.ActivityEvent {
	var params map[string]string
	_ = json.Unmarshal([]byte(r.ParamsJson), &params)
	if len(params) == 0 {
		params = nil
	}
	return entity.ActivityEvent{
		ID:             r.EventID,
		Type:           r.Type,
		RepositoryID:   uint64(r.RepositoryID),
		RepositoryName: r.RepositoryName,
		PullRequestID:  uint64(r.PullRequestID),
		PRNumber:       int(r.PrNumber),
		ActorLogin:     r.ActorLogin,
		Params:         params,
		OccurredAt:     r.OccurredAt,
	}
}
