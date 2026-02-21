package db

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// Invitation represents a single-use registration invite.
type Invitation struct {
	Token     string             `json:"token"`
	CreatedBy int32              `json:"created_by"`
	UsedBy    pgtype.Int4        `json:"used_by"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
	UsedAt    pgtype.Timestamptz `json:"used_at"`
}

// ── Sentinel errors ───────────────────────────────────────────────────────────

var ErrInvitationNotFound = errors.New("invitation not found")
var ErrInvitationUsed     = errors.New("invitation already used")

// ── Store methods ─────────────────────────────────────────────────────────────

// CreateInvitation generates a new UUID invitation token owned by createdBy.
func (s *Store) CreateInvitation(ctx context.Context, createdBy int32) (Invitation, error) {
	token := uuid.New().String()
	const q = `
		INSERT INTO invitations (token, created_by)
		VALUES ($1, $2)
		RETURNING token, created_by, used_by, created_at, used_at
	`
	row := s.Pool.QueryRow(ctx, q, token, createdBy)
	var inv Invitation
	err := row.Scan(&inv.Token, &inv.CreatedBy, &inv.UsedBy, &inv.CreatedAt, &inv.UsedAt)
	return inv, err
}

// GetInvitation returns the invitation for the given token.
// Returns ErrInvitationNotFound when no row matches.
func (s *Store) GetInvitation(ctx context.Context, token string) (Invitation, error) {
	const q = `
		SELECT token, created_by, used_by, created_at, used_at
		FROM   invitations
		WHERE  token = $1
	`
	row := s.Pool.QueryRow(ctx, q, token)
	var inv Invitation
	if err := row.Scan(&inv.Token, &inv.CreatedBy, &inv.UsedBy, &inv.CreatedAt, &inv.UsedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invitation{}, ErrInvitationNotFound
		}
		return Invitation{}, err
	}
	return inv, nil
}

// UseInvitation atomically marks a token as consumed by usedBy.
// Returns ErrInvitationNotFound if the token doesn't exist,
// ErrInvitationUsed if it was already consumed.
func (s *Store) UseInvitation(ctx context.Context, token string, usedBy int32) error {
	const q = `
		UPDATE invitations
		SET    used_by = $2,
		       used_at = $3
		WHERE  token = $1
		  AND  used_by IS NULL
	`
	tag, err := s.Pool.Exec(ctx, q, token, usedBy, time.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Distinguish "not found" from "already used" so callers can return the
		// right HTTP status.
		if _, getErr := s.GetInvitation(ctx, token); getErr != nil {
			return ErrInvitationNotFound
		}
		return ErrInvitationUsed
	}
	return nil
}

// ListInvitationsByUser returns all invitations created by userID, newest first.
func (s *Store) ListInvitationsByUser(ctx context.Context, userID int32) ([]Invitation, error) {
	const q = `
		SELECT token, created_by, used_by, created_at, used_at
		FROM   invitations
		WHERE  created_by = $1
		ORDER  BY created_at DESC
	`
	rows, err := s.Pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invs []Invitation
	for rows.Next() {
		var inv Invitation
		if err := rows.Scan(&inv.Token, &inv.CreatedBy, &inv.UsedBy, &inv.CreatedAt, &inv.UsedAt); err != nil {
			return nil, err
		}
		invs = append(invs, inv)
	}
	return invs, rows.Err()
}
