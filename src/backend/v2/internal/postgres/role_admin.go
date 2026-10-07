package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kejith/ginbar/backend/v2/internal/adminbootstrap"
	"github.com/kejith/ginbar/backend/v2/internal/role"
	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
)

const adminRoleMutationLockKey int64 = 5136722896463484466 // "GINBARV2"

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

const grantAdminSQL = `
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
	SELECT target.id, $3, $1
	FROM actor, target
	WHERE actor.allowed
	ON CONFLICT (user_id, role) DO UPDATE
	SET granted_by_user_id = existing.granted_by_user_id
	RETURNING user_id, granted_by_user_id
), moderator_role AS MATERIALIZED (
	SELECT ur.user_id, ur.granted_by_user_id
	FROM user_roles AS ur
	JOIN target ON target.id = ur.user_id
	WHERE ur.role = $4
)
SELECT
	actor.allowed,
	target.id IS NOT NULL AS found,
	COALESCE(target.id, 0),
	moderator_role.user_id IS NOT NULL,
	moderator_role.granted_by_user_id,
	changed.user_id IS NOT NULL,
	changed.granted_by_user_id
FROM actor
LEFT JOIN target ON true
LEFT JOIN moderator_role ON true
LEFT JOIN changed ON true
`

const revokeAdminSQL = `
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
	  AND $1 <> target.id
	  AND ur.user_id = target.id
	  AND ur.role = $3
	RETURNING ur.user_id
), moderator_role AS MATERIALIZED (
	SELECT ur.user_id, ur.granted_by_user_id
	FROM user_roles AS ur
	JOIN target ON target.id = ur.user_id
	WHERE ur.role = $4
)
SELECT
	actor.allowed,
	target.id IS NOT NULL AS found,
	COALESCE(target.id, 0),
	COALESCE(actor.allowed AND target.id = $1, false) AS self_revoke,
	moderator_role.user_id IS NOT NULL,
	moderator_role.granted_by_user_id,
	false AS admin,
	NULL::bigint AS admin_granted_by_user_id
FROM actor
LEFT JOIN target ON true
LEFT JOIN changed ON true
LEFT JOIN moderator_role ON true
`

const bootstrapFirstAdminSQL = `
WITH target AS MATERIALIZED (
	SELECT id
	FROM users
	WHERE id = $1
), existing_admin AS MATERIALIZED (
	SELECT user_id
	FROM user_roles
	WHERE role = $2
	LIMIT 1
), changed AS (
	INSERT INTO user_roles (user_id, role, granted_by_user_id)
	SELECT target.id, $2, target.id
	FROM target
	WHERE NOT EXISTS (SELECT 1 FROM existing_admin)
	ON CONFLICT (user_id, role) DO NOTHING
	RETURNING user_id, granted_by_user_id
), moderator_role AS MATERIALIZED (
	SELECT ur.user_id, ur.granted_by_user_id
	FROM user_roles AS ur
	JOIN target ON target.id = ur.user_id
	WHERE ur.role = $3
)
SELECT
	target.id IS NOT NULL AS found,
	existing_admin.user_id IS NOT NULL AS initialized,
	COALESCE(target.id, 0),
	moderator_role.user_id IS NOT NULL,
	moderator_role.granted_by_user_id,
	changed.user_id IS NOT NULL,
	changed.granted_by_user_id
FROM (SELECT true) AS one
LEFT JOIN target ON true
LEFT JOIN existing_admin ON true
LEFT JOIN moderator_role ON true
LEFT JOIN changed ON true
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

func (s *Store) GrantAdmin(
	ctx context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	return s.scanUserRoleState(
		s.pool.QueryRow(
			ctx,
			grantAdminSQL,
			actorUserID,
			targetUserID,
			role.Admin,
			role.Moderator,
		),
		"grant admin",
	)
}

func (s *Store) RevokeAdmin(
	ctx context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return roleadmin.State{}, fmt.Errorf("begin revoke-admin transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", adminRoleMutationLockKey); err != nil {
		return roleadmin.State{}, fmt.Errorf("serialize admin revocation: %w", err)
	}

	var (
		allowed                  bool
		found                    bool
		selfRevoke               bool
		state                    roleadmin.State
		moderatorGrantedByUserID pgtype.Int8
		adminGrantedByUserID     pgtype.Int8
	)
	if err := tx.QueryRow(
		ctx,
		revokeAdminSQL,
		actorUserID,
		targetUserID,
		role.Admin,
		role.Moderator,
	).Scan(
		&allowed,
		&found,
		&state.UserID,
		&selfRevoke,
		&state.Moderator,
		&moderatorGrantedByUserID,
		&state.Admin,
		&adminGrantedByUserID,
	); err != nil {
		return roleadmin.State{}, fmt.Errorf("revoke admin: %w", err)
	}
	if !allowed {
		return roleadmin.State{}, roleadmin.ErrForbidden
	}
	if !found {
		return roleadmin.State{}, roleadmin.ErrUserNotFound
	}
	if selfRevoke {
		return roleadmin.State{}, roleadmin.ErrSelfAdminRevocation
	}
	if state.UserID <= 0 || state.Admin || adminGrantedByUserID.Valid {
		return roleadmin.State{}, fmt.Errorf("revoke admin: incomplete authoritative state")
	}
	state.ModeratorGrantedByUserID = roleAdminOptionalInt64(moderatorGrantedByUserID)
	if err := tx.Commit(ctx); err != nil {
		return roleadmin.State{}, fmt.Errorf("commit revoke-admin transaction: %w", err)
	}
	return state, nil
}

func (s *Store) BootstrapFirstAdmin(ctx context.Context, userID int64) (roleadmin.State, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return roleadmin.State{}, fmt.Errorf("begin admin-bootstrap transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", adminRoleMutationLockKey); err != nil {
		return roleadmin.State{}, fmt.Errorf("serialize admin bootstrap: %w", err)
	}

	var (
		found                    bool
		initialized              bool
		state                    roleadmin.State
		moderatorGrantedByUserID pgtype.Int8
		adminGrantedByUserID     pgtype.Int8
	)
	if err := tx.QueryRow(
		ctx,
		bootstrapFirstAdminSQL,
		userID,
		role.Admin,
		role.Moderator,
	).Scan(
		&found,
		&initialized,
		&state.UserID,
		&state.Moderator,
		&moderatorGrantedByUserID,
		&state.Admin,
		&adminGrantedByUserID,
	); err != nil {
		return roleadmin.State{}, fmt.Errorf("bootstrap first admin: %w", err)
	}
	if !found {
		return roleadmin.State{}, adminbootstrap.ErrUserNotFound
	}
	if initialized {
		return roleadmin.State{}, adminbootstrap.ErrAlreadyInitialized
	}
	if state.UserID != userID || !state.Admin || !adminGrantedByUserID.Valid || adminGrantedByUserID.Int64 != userID {
		return roleadmin.State{}, fmt.Errorf("bootstrap first admin: incomplete authoritative state")
	}
	state.ModeratorGrantedByUserID = roleAdminOptionalInt64(moderatorGrantedByUserID)
	state.AdminGrantedByUserID = roleAdminOptionalInt64(adminGrantedByUserID)
	if err := tx.Commit(ctx); err != nil {
		return roleadmin.State{}, fmt.Errorf("commit admin-bootstrap transaction: %w", err)
	}
	return state, nil
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
