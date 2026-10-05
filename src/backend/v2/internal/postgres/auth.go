package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kejith/ginbar/backend/v2/internal/auth"
)

func (s *Store) CheckInvitation(ctx context.Context, tokenHash [32]byte, now time.Time) error {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT true
		FROM invitations
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND claimed_by_user_id IS NULL
		  AND claimed_at IS NULL
		  AND (expires_at IS NULL OR expires_at > $2)
	`, tokenHash[:], now).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.ErrInvalidInvitation
	}
	if err != nil {
		return fmt.Errorf("check invitation: %w", err)
	}
	return nil
}

func (s *Store) RegisterUser(ctx context.Context, tokenHash [32]byte, username, verifier string, now time.Time) (auth.Principal, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return auth.Principal{}, fmt.Errorf("begin registration transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	var invitationID int64
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM invitations
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND claimed_by_user_id IS NULL
		  AND claimed_at IS NULL
		  AND (expires_at IS NULL OR expires_at > $2)
		FOR UPDATE
	`, tokenHash[:], now).Scan(&invitationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Principal{}, auth.ErrInvalidInvitation
		}
		return auth.Principal{}, fmt.Errorf("lock invitation: %w", err)
	}

	var principal auth.Principal
	if err := tx.QueryRow(ctx,
		"INSERT INTO users (username) VALUES ($1) RETURNING id, username",
		username,
	).Scan(&principal.UserID, &principal.Username); err != nil {
		if usernameConflict(err) {
			return auth.Principal{}, auth.ErrUsernameUnavailable
		}
		return auth.Principal{}, fmt.Errorf("create registered user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_credentials (user_id, kind, secret_hash)
		VALUES ($1, $2, $3)
	`, principal.UserID, auth.CredentialKindPassword, verifier); err != nil {
		return auth.Principal{}, fmt.Errorf("create password credential: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE invitations
		SET claimed_by_user_id = $1, claimed_at = $2
		WHERE id = $3
	`, principal.UserID, now, invitationID); err != nil {
		return auth.Principal{}, fmt.Errorf("claim invitation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return auth.Principal{}, fmt.Errorf("commit registration transaction rolled back: %w", err)
		}
		return auth.Principal{}, fmt.Errorf("%w: commit registration transaction: %v", auth.ErrCommitOutcomeUnknown, err)
	}
	return principal, nil
}

func (s *Store) LookupPasswordCredential(ctx context.Context, username string) (auth.PasswordCredential, error) {
	var credential auth.PasswordCredential
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.username, u.status, c.secret_hash
		FROM users AS u
		JOIN user_credentials AS c
		  ON c.user_id = u.id
		 AND c.kind = $2
		WHERE lower(u.username) = lower($1)
	`, username, auth.CredentialKindPassword).Scan(
		&credential.UserID,
		&credential.Username,
		&credential.Status,
		&credential.Verifier,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.PasswordCredential{}, auth.ErrInvalidCredentials
	}
	if err != nil {
		return auth.PasswordCredential{}, fmt.Errorf("lookup password credential: %w", err)
	}
	return credential, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash [32]byte, createdAt, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO user_sessions (token_hash, user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4)
	`, tokenHash[:], userID, createdAt, expiresAt); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) ResolveSession(ctx context.Context, tokenHash [32]byte, now time.Time) (auth.Principal, error) {
	var principal auth.Principal
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.username
		FROM user_sessions AS s
		JOIN users AS u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > $2
		  AND u.status = $3
	`, tokenHash[:], now, auth.UserStatusActive).Scan(&principal.UserID, &principal.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Principal{}, fmt.Errorf("resolve session: %w", err)
	}
	return principal, nil
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash [32]byte, now time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE user_sessions
		SET revoked_at = $2
		WHERE token_hash = $1
		  AND revoked_at IS NULL
	`, tokenHash[:], now); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func usernameConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_username_lower_uidx"
}
