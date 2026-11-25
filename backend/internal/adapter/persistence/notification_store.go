package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence/sqlcgen"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// NotificationStore is the database-backed port.NotificationStore and port.NotifierMemory (ADR-0017).
type NotificationStore struct {
	q *sqlcgen.Queries
}

var (
	_ port.NotificationStore = (*NotificationStore)(nil)
	_ port.NotifierMemory    = (*NotificationStore)(nil)
)

func NewNotificationStore(db *sql.DB) *NotificationStore {
	return &NotificationStore{q: sqlcgen.New(db)}
}

func (s *NotificationStore) CreateNotifications(ctx context.Context, notifications []entity.Notification) ([]entity.Notification, error) {
	created := make([]entity.Notification, 0, len(notifications))
	for _, n := range notifications {
		if err := ctx.Err(); err != nil {
			return created, context.Cause(ctx)
		}
		details, err := json.Marshal(nonNilParams(n.Details))
		if err != nil {
			return created, err
		}
		id, err := s.q.InsertNotification(ctx, sqlcgen.InsertNotificationParams{
			UserID:         int64(n.UserID),
			DedupKey:       n.Key(),
			Kind:           string(n.Kind),
			Reason:         string(n.Reason),
			RepositoryID:   int64(n.RepositoryID),
			RepositoryName: n.RepositoryName,
			PullRequestID:  int64(n.PullRequestID),
			PrNumber:       int64(n.PRNumber),
			PrTitle:        n.PRTitle,
			CommitSha:      n.CommitSHA,
			ActorLogin:     n.ActorLogin,
			DetailsJson:    string(details),
			CreatedAt:      n.CreatedAt,
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue // already notified
		}
		if err != nil {
			return created, err
		}
		n.ID = uint64(id)
		created = append(created, n)
	}
	return created, nil
}

func (s *NotificationStore) ListNotifications(ctx context.Context, userID, beforeID uint64, limit int) ([]entity.Notification, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	before := int64(beforeID)
	if beforeID == 0 {
		before = 1<<63 - 1
	}
	rows, err := s.q.ListNotificationsBefore(ctx, sqlcgen.ListNotificationsBeforeParams{UserID: int64(userID), ID: before, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]entity.Notification, 0, len(rows))
	for _, r := range rows {
		out = append(out, toNotification(sqlcgen.ListNotificationsSinceRow(r)))
	}
	return out, nil
}

func (s *NotificationStore) ListNotificationsSince(ctx context.Context, afterID uint64, limit int) ([]entity.Notification, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	rows, err := s.q.ListNotificationsSince(ctx, sqlcgen.ListNotificationsSinceParams{ID: int64(afterID), Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]entity.Notification, 0, len(rows))
	for _, r := range rows {
		out = append(out, toNotification(r))
	}
	return out, nil
}

func (s *NotificationStore) LatestNotificationID(ctx context.Context) (uint64, error) {
	id, err := s.q.GetLatestNotificationID(ctx)
	return uint64(id), err
}

func (s *NotificationStore) CountUnreadNotifications(ctx context.Context, userID uint64) (int, error) {
	n, err := s.q.CountUnreadNotifications(ctx, int64(userID))
	return int(n), err
}

func (s *NotificationStore) MarkNotificationsRead(ctx context.Context, userID uint64, ids []uint64, at time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, context.Cause(ctx)
	}
	readAt := sql.NullTime{Time: at, Valid: true}
	if len(ids) == 0 {
		return s.q.MarkAllNotificationsRead(ctx, sqlcgen.MarkAllNotificationsReadParams{UserID: int64(userID), ReadAt: readAt})
	}
	var changed int64
	for _, id := range ids {
		n, err := s.q.MarkNotificationRead(ctx, sqlcgen.MarkNotificationReadParams{UserID: int64(userID), ID: int64(id), ReadAt: readAt})
		if err != nil {
			return changed, err
		}
		changed += n
	}
	return changed, nil
}

func (s *NotificationStore) PruneNotifications(ctx context.Context, before time.Time) (int64, error) {
	return s.q.DeleteNotificationsBefore(ctx, before)
}

func (s *NotificationStore) GetNotificationSettings(ctx context.Context, userID uint64) (entity.NotificationSettings, error) {
	row, err := s.q.GetNotificationSettings(ctx, int64(userID))
	if errors.Is(err, sql.ErrNoRows) {
		return entity.NotificationSettings{PopupsOff: []entity.NotificationKind{}}, nil
	}
	if err != nil {
		return entity.NotificationSettings{}, err
	}
	settings := entity.NotificationSettings{Paused: row.Paused, PopupsOff: []entity.NotificationKind{}}
	for _, kind := range strings.Split(row.PopupsOff, ",") {
		if kind != "" {
			settings.PopupsOff = append(settings.PopupsOff, entity.NotificationKind(kind))
		}
	}
	return settings, nil
}

func (s *NotificationStore) SaveNotificationSettings(ctx context.Context, userID uint64, settings entity.NotificationSettings, at time.Time) error {
	off := make([]string, 0, len(settings.PopupsOff))
	for _, kind := range settings.PopupsOff {
		off = append(off, string(kind))
	}
	return s.q.UpsertNotificationSettings(ctx, sqlcgen.UpsertNotificationSettingsParams{
		UserID: int64(userID), Paused: settings.Paused, PopupsOff: strings.Join(off, ","), UpdatedAt: at,
	})
}

func (s *NotificationStore) GetNotificationWatch(ctx context.Context, userID, prID uint64) (*entity.NotificationWatch, error) {
	row, err := s.q.GetNotificationWatch(ctx, sqlcgen.GetNotificationWatchParams{UserID: int64(userID), PullRequestID: int64(prID)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entity.NotificationWatch{
		UserID: uint64(row.UserID), PullRequestID: uint64(row.PullRequestID), RepositoryID: uint64(row.RepositoryID),
		Turn: entity.TurnState(row.Turn), HeadSHA: row.HeadSha, ApprovedSHA: row.ApprovedSha, Risk: entity.RiskVerdict(row.Risk),
	}, nil
}

func (s *NotificationStore) SaveNotificationWatch(ctx context.Context, w entity.NotificationWatch) error {
	return s.q.UpsertNotificationWatch(ctx, sqlcgen.UpsertNotificationWatchParams{
		UserID: int64(w.UserID), PullRequestID: int64(w.PullRequestID), RepositoryID: int64(w.RepositoryID),
		Turn: string(w.Turn), HeadSha: w.HeadSHA, ApprovedSha: w.ApprovedSHA, Risk: string(w.Risk),
	})
}

func (s *NotificationStore) HasNotificationBaseline(ctx context.Context, userID, repositoryID uint64) (bool, error) {
	n, err := s.q.HasNotificationBaseline(ctx, sqlcgen.HasNotificationBaselineParams{UserID: int64(userID), RepositoryID: int64(repositoryID)})
	return n > 0, err
}

func (s *NotificationStore) SaveNotificationBaseline(ctx context.Context, userID, repositoryID uint64, at time.Time) error {
	return s.q.InsertNotificationBaseline(ctx, sqlcgen.InsertNotificationBaselineParams{UserID: int64(userID), RepositoryID: int64(repositoryID), CreatedAt: at})
}

func (s *NotificationStore) ListFollowsWithoutBaseline(ctx context.Context, limit int) ([]port.Follow, error) {
	rows, err := s.q.ListFollowsWithoutNotificationBaseline(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]port.Follow, 0, len(rows))
	for _, r := range rows {
		out = append(out, port.Follow{UserID: uint64(r.UserID), RepositoryID: uint64(r.RepositoryID)})
	}
	return out, nil
}

func (s *NotificationStore) ForgetUnfollowed(ctx context.Context) error {
	if _, err := s.q.DeleteUnfollowedNotificationWatches(ctx); err != nil {
		return err
	}
	_, err := s.q.DeleteUnfollowedNotificationBaselines(ctx)
	return err
}

func (s *NotificationStore) LogPosition(ctx context.Context, name string) (int64, bool, error) {
	seq, err := s.q.GetLogPosition(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return seq, err == nil, err
}

func (s *NotificationStore) SaveLogPosition(ctx context.Context, name string, seq int64) error {
	return s.q.SaveLogPosition(ctx, sqlcgen.SaveLogPositionParams{Name: name, Seq: seq})
}

func toNotification(r sqlcgen.ListNotificationsSinceRow) entity.Notification {
	n := entity.Notification{
		ID:             uint64(r.ID),
		UserID:         uint64(r.UserID),
		Kind:           entity.NotificationKind(r.Kind),
		Reason:         entity.NotificationReason(r.Reason),
		RepositoryID:   uint64(r.RepositoryID),
		RepositoryName: r.RepositoryName,
		PullRequestID:  uint64(r.PullRequestID),
		PRNumber:       int(r.PrNumber),
		PRTitle:        r.PrTitle,
		CommitSHA:      r.CommitSha,
		ActorLogin:     r.ActorLogin,
		CreatedAt:      r.CreatedAt.UTC(),
	}
	_ = json.Unmarshal([]byte(r.DetailsJson), &n.Details)
	if len(n.Details) == 0 {
		n.Details = nil
	}
	if r.ReadAt.Valid {
		at := r.ReadAt.Time.UTC()
		n.ReadAt = &at
	}
	return n
}
