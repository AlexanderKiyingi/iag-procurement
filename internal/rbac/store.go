package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type UserRecord struct {
	ID           int64
	Email        string
	PasswordHash string
	IsActive     bool
	IsSuperuser  bool
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*UserRecord, error) {
	var u UserRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, is_active, is_superuser
		FROM auth_users WHERE lower(email) = lower($1)`, email).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.IsActive, &u.IsSuperuser)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) ListPermissionCodesForUser(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT p.code
		FROM auth_permissions p
		JOIN auth_group_permissions gp ON gp.permission_id = p.id
		JOIN auth_user_groups ug ON ug.group_id = gp.group_id
		WHERE ug.user_id = $1
		UNION
		SELECT p.code
		FROM auth_permissions p
		JOIN auth_user_permissions up ON up.permission_id = p.id
		WHERE up.user_id = $1
		ORDER BY 1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

type PermissionRow struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Store) ListPermissions(ctx context.Context) ([]PermissionRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, code, name, description FROM auth_permissions ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PermissionRow
	for rows.Next() {
		var p PermissionRow
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type GroupRow struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Permissions  []string `json:"permissions"`
}

func (s *Store) ListGroups(ctx context.Context) ([]GroupRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.name, g.description,
		       coalesce(array_agg(p.code ORDER BY p.code) FILTER (WHERE p.code IS NOT NULL), '{}')
		FROM auth_groups g
		LEFT JOIN auth_group_permissions gp ON gp.group_id = g.id
		LEFT JOIN auth_permissions p ON p.id = gp.permission_id
		GROUP BY g.id, g.name, g.description
		ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupRow
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.Permissions); err != nil {
			return nil, err
		}
		if g.Permissions == nil {
			g.Permissions = []string{}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

type UserListRow struct {
	ID           int64    `json:"id"`
	Email        string   `json:"email"`
	IsActive     bool     `json:"isActive"`
	IsSuperuser  bool     `json:"isSuperuser"`
	Groups       []string `json:"groups"`
	Permissions  []string `json:"permissions"`
}

func (s *Store) ListUsers(ctx context.Context) ([]UserListRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, email, is_active, is_superuser FROM auth_users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserListRow
	for rows.Next() {
		var u UserListRow
		if err := rows.Scan(&u.ID, &u.Email, &u.IsActive, &u.IsSuperuser); err != nil {
			return nil, err
		}
		grows, err := s.pool.Query(ctx, `
			SELECT g.name FROM auth_groups g
			JOIN auth_user_groups ug ON ug.group_id = g.id
			WHERE ug.user_id = $1 ORDER BY g.name`, u.ID)
		if err != nil {
			return nil, err
		}
		for grows.Next() {
			var n string
			if err := grows.Scan(&n); err != nil {
				grows.Close()
				return nil, err
			}
			u.Groups = append(u.Groups, n)
		}
		grows.Close()
		perms, err := s.ListPermissionCodesForUser(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		u.Permissions = perms
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) PermissionExists(ctx context.Context, code string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM auth_permissions WHERE code = $1`, code).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) InsertUser(ctx context.Context, email, hash string, super bool) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO auth_users (email, password_hash, is_superuser)
		VALUES ($1, $2, $3) RETURNING id`, email, hash, super).Scan(&id)
	return id, err
}

func (s *Store) AddUserGroup(ctx context.Context, userID, groupID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_user_groups (user_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, groupID)
	return err
}

func (s *Store) GetGroupIDByName(ctx context.Context, name string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM auth_groups WHERE name = $1`, name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("group %q: %w", name, err)
	}
	return id, nil
}
