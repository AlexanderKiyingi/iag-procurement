package rbac

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func dedupeInt64(in []int64) []int64 {
	if len(in) == 0 {
		return []int64{}
	}
	seen := make(map[int64]struct{}, len(in))
	out := make([]int64, 0, len(in))
	for _, x := range in {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

// GetUserByID loads a user row by primary key (includes password hash — do not serialize).
func (s *Store) GetUserByID(ctx context.Context, id int64) (*UserRecord, error) {
	var u UserRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, is_active, is_superuser
		FROM auth_users WHERE id = $1`, id).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.IsActive, &u.IsSuperuser)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateUserFlags updates is_active and/or is_superuser when the corresponding pointer is non-nil.
func (s *Store) UpdateUserFlags(ctx context.Context, id int64, active *bool, super *bool) error {
	if active != nil {
		tag, err := s.pool.Exec(ctx, `UPDATE auth_users SET is_active = $2 WHERE id = $1`, id, *active)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
	}
	if super != nil {
		tag, err := s.pool.Exec(ctx, `UPDATE auth_users SET is_superuser = $2 WHERE id = $1`, id, *super)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
	}
	return nil
}

// ReplaceUserGroups replaces auth_user_groups membership for the user (empty slice removes all groups).
func (s *Store) ReplaceUserGroups(ctx context.Context, userID int64, groupIDs []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM auth_user_groups WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, gid := range dedupeInt64(groupIDs) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_user_groups (user_id, group_id) VALUES ($1, $2)`, userID, gid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ReplaceGroupPermissions replaces auth_group_permissions for the group (empty clears all group permissions).
func (s *Store) ReplaceGroupPermissions(ctx context.Context, groupID int64, permissionIDs []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM auth_group_permissions WHERE group_id = $1`, groupID); err != nil {
		return err
	}
	for _, pid := range dedupeInt64(permissionIDs) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_group_permissions (group_id, permission_id) VALUES ($1, $2)`, groupID, pid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GroupExists returns whether an auth_groups row exists.
func (s *Store) GroupExists(ctx context.Context, groupID int64) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM auth_groups WHERE id = $1`, groupID).Scan(&n)
	return n > 0, err
}

// CreateUserWithGroups inserts a user and optional group memberships in one transaction.
func (s *Store) CreateUserWithGroups(ctx context.Context, email, passwordHash string, isSuperuser, isActive bool, groupIDs []int64) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO auth_users (email, password_hash, is_superuser, is_active)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		email, passwordHash, isSuperuser, isActive).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, gid := range dedupeInt64(groupIDs) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_user_groups (user_id, group_id) VALUES ($1, $2)`, id, gid); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateUserPasswordHash replaces the stored password hash.
func (s *Store) UpdateUserPasswordHash(ctx context.Context, userID int64, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE auth_users SET password_hash = $2 WHERE id = $1`, userID, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
