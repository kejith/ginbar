package adminbootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
)

var (
	ErrAlreadyInitialized = errors.New("admin bootstrap already initialized")
	ErrUserNotFound       = errors.New("user not found")
)

type Store interface {
	BootstrapFirstAdmin(context.Context, int64) (roleadmin.State, error)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Bootstrap(ctx context.Context, userID int64) (roleadmin.State, error) {
	if userID <= 0 {
		return roleadmin.State{}, fmt.Errorf("user id must be positive")
	}
	return s.store.BootstrapFirstAdmin(ctx, userID)
}
