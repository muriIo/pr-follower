package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/murilo/pr-follower/backend/internal/adapter/persistence/sqlcgen"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

// MemberAccessStore is the port.MemberAccessStore: who can read stored data, and revoking it (ADR-0015).
type MemberAccessStore struct {
	db *sql.DB
	q  *sqlcgen.Queries
}

var _ port.MemberAccessStore = (*MemberAccessStore)(nil)

func NewMemberAccessStore(db *sql.DB) *MemberAccessStore {
	return &MemberAccessStore{db: db, q: sqlcgen.New(db)}
}

// ListUsersWithAccess returns the users with a session or a follow: id, GitHub id and login only.
func (s *MemberAccessStore) ListUsersWithAccess(ctx context.Context) ([]entity.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	rows, err := s.q.ListUsersWithAccess(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]entity.User, 0, len(rows))
	for _, r := range rows {
		users = append(users, entity.User{ID: uint64(r.ID), GitHubID: r.GithubID, Login: r.Login})
	}
	return users, nil
}

// RevokeUserAccess deletes the user's sessions, follows, per-user PR state and sync job in one
// transaction. Running it again (another replica) removes nothing and is not an error.
func (s *MemberAccessStore) RevokeUserAccess(ctx context.Context, userID uint64) (entity.RevokedAccess, error) {
	if err := ctx.Err(); err != nil {
		return entity.RevokedAccess{}, context.Cause(ctx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return entity.RevokedAccess{}, fmt.Errorf("begin revoke: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)
	id := int64(userID)

	var removed entity.RevokedAccess
	if removed.Sessions, err = q.DeleteUserSessions(ctx, id); err != nil {
		return entity.RevokedAccess{}, err
	}
	if removed.Follows, err = q.DeleteUserFollows(ctx, id); err != nil {
		return entity.RevokedAccess{}, err
	}
	if removed.PRStates, err = q.DeleteUserPRStates(ctx, id); err != nil {
		return entity.RevokedAccess{}, err
	}
	if removed.SyncJobs, err = q.DeleteUserSyncJob(ctx, id); err != nil {
		return entity.RevokedAccess{}, err
	}
	if err := tx.Commit(); err != nil {
		return entity.RevokedAccess{}, fmt.Errorf("commit revoke: %w", err)
	}
	return removed, nil
}
