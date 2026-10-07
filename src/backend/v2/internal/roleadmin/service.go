package roleadmin

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrForbidden    = errors.New("role administration forbidden")
	ErrUserNotFound = errors.New("user not found")
)

type State struct {
	UserID                   int64  `json:"userId"`
	Moderator                bool   `json:"moderator"`
	ModeratorGrantedByUserID *int64 `json:"moderatorGrantedByUserId,omitempty"`
	Admin                    bool   `json:"admin"`
	AdminGrantedByUserID     *int64 `json:"adminGrantedByUserId,omitempty"`
}

type Store interface {
	GetUserRoleState(context.Context, int64, int64) (State, error)
	GrantModerator(context.Context, int64, int64) (State, error)
	RevokeModerator(context.Context, int64, int64) (State, error)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Get(ctx context.Context, actorUserID, targetUserID int64) (State, error) {
	if err := validateIDs(actorUserID, targetUserID); err != nil {
		return State{}, err
	}
	return s.store.GetUserRoleState(ctx, actorUserID, targetUserID)
}

func (s *Service) GrantModerator(ctx context.Context, actorUserID, targetUserID int64) (State, error) {
	if err := validateIDs(actorUserID, targetUserID); err != nil {
		return State{}, err
	}
	return s.store.GrantModerator(ctx, actorUserID, targetUserID)
}

func (s *Service) RevokeModerator(ctx context.Context, actorUserID, targetUserID int64) (State, error) {
	if err := validateIDs(actorUserID, targetUserID); err != nil {
		return State{}, err
	}
	return s.store.RevokeModerator(ctx, actorUserID, targetUserID)
}

func validateIDs(actorUserID, targetUserID int64) error {
	if actorUserID <= 0 {
		return fmt.Errorf("actor user id must be positive")
	}
	if targetUserID <= 0 {
		return fmt.Errorf("target user id must be positive")
	}
	return nil
}
