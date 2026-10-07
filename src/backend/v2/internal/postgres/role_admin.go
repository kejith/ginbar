package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kejith/ginbar/backend/v2/internal/role"
	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
)

const getUserRoleStateSQL = `
WITH actor AS MATERIALIZED (
	SELECT EXISTS (
		SELECT 1
		FROM user_roles
		WHERE user_id = $1
		  AND role = $3
	) AS allowed
), target AS MATERIALIZED (
	SELECT id
	FROM users
	WHERE id = $2
)
SELECT
	actor.allowed,
	target.id IS NOT NULL AS found,
	COALESCE(target.id, 0),
	moderator.user_id IS NOT NULL,
	moderator.granted_by_user_id,
	admin.user_id IS NOT NULL,
	admin.granted_by_user_id
FROM actor
LEFT JOIN target ON true
LEFT JOIN user_roles AS moderator
  ON moderator.user_id = target.id
 AND moderator.role = $4
LEFT JOIN user_roles AS admin
  ON admin.user_id = target.id
 AND admin.role = $3
`

const grantModeratorSQL = `
WITH actor AS MATERIALIZED (
	SELECT EXISTS (
		SELECT 1
		FROM user_roles
		WHERE user_id = $1
		  AND role = $3
	) AS allowed
), target AS MATERIALIZED (
	SELECT id
	FROM users
	WHERE id = $2
), changed AS (
	INSERT INTO user_roles AS existing (user_id, role, granted_by_user_id)
	SELECT target.id, $4, $1
	FROM actor, target
	WHERE actor.allowed
	ON CONFLICT (user_id, role) DO UPDATE
	SET granted_by_user_id = existing.granted_by_user_id
	RETURNING user_id, granted_by_user_id
), admin_role AS MATERIALIZED (
	SELECT ur.user_id, ur.granted_by_user_id
	FROM user_roles AS ur
	JOIN target ON target.id = ur.user_id
	WHERE ur.role = $3
)
SELECT
	actor.allowed,
	target.id IS NOT NULL AS found,
	COALESCE(target.id, 0),
	changed.user_id IS NOT NULL,
	changed.granted_by_user_id,
	admin_role.user_id IS NOT NULL,
	admin_role.granted_by_user_id
FROM actor
LEFT JOIN target ON true
LEFT JOIN changed ON true
LEFT JOIN admin_role ON true
`

const revokeModeratorSQL = `
WITH actor AS MATERIALIZED (
	SELECT EXISTS (
		SELECT 1
		FROM user_roles
		WHERE user_id = $1
		  AND role = $3
	) AS allowed
), target AS MATERIALIZED (
	SELECT id
	FROM users
	WHERE id = $2
), changed AS (
	DELETE FROM user_roles AS ur
	USING actor, target
	WHERE actor.allowed
	  AND ur.user_id = target.id
	  AND ur.role = $4
	RETURNING ur.user_id
), admin_role AS MATERIALIZED (
	SELECT ur.user_id, ur.granted_by_user_id
	FROM user_roles AS ur
	JOIN target ON target.id = ur.user_id
	WHERE ur.role = $3
)
SELECT
	actor.allowed,
	target.id IS NOT NULL AS found,
	COALESCE(target.id, 0),
	false AS moderator,
	NULL::bigint AS moderator_granted_by_user_id,
	admin_role.user_id IS NOT NULL,
	admin_role.granted_by_user_id
FROM actor
LEFT JOIN target ON true
LEFT JOIN changed ON true
LEFT JOIN admin_role ON true
`

func (s *Store) GetUserRoleState(
	ctx context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	return s.scanUserRoleState(
		s.pool.QueryRow(
			ctx,
			getUserRoleStateSQL,
			actorUserID,
			targetUserID,
			role.Admin,
			role.Moderator,
		),
		"get user role state",
	)
}

func (s *Store) GrantModerator(
	ctx context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	return s.scanUserRoleState(
		s.pool.QueryRow(
			ctx,
			grantModeratorSQL,
			actorUserID,
			targetUserID,
			role.Admin,
			role.Moderator,
		),
		"grant moderator",
	)
}

func (s *Store) RevokeModerator(
	ctx context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	return s.scanUserRoleState(
		s.pool.QueryRow(
			ctx,
			revokeModeratorSQL,
			actorUserID,
			targetUserID,
			role.Admin,
			role.Moderator,
		),
		"revoke moderator",
	)
}

func (s *Store) scanUserRoleState(row rowScanner, operation string) (roleadmin.State, error) {
	var (
		allowed                  bool
		found                    bool
		state                    roleadmin.State
		moderatorGrantedByUserID pgtype.Int8
		adminGrantedByUserID     pgtype.Int8
	)
	if err := row.Scan(
		&allowed,
		&found,
		&state.UserID,
		&state.Moderator,
		&moderatorGrantedByUserID,
		&state.Admin,
		&adminGrantedByUserID,
	); err != nil {
		return roleadmin.State{}, fmt.Errorf("%s: %w", operation, err)
	}
	if !allowed {
		return roleadmin.State{}, roleadmin.ErrForbidden
	}
	if !found {
		return roleadmin.State{}, roleadmin.ErrUserNotFound
	}
	if state.UserID <= 0 {
		return roleadmin.State{}, fmt.Errorf("%s: incomplete authoritative state", operation)
	}
	state.ModeratorGrantedByUserID = roleAdminOptionalInt64(moderatorGrantedByUserID)
	state.AdminGrantedByUserID = roleAdminOptionalInt64(adminGrantedByUserID)
	return state, nil
}

func roleAdminOptionalInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
