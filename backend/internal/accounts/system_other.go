//go:build !linux

package accounts

import (
	"context"
	"errors"
)

// System is only available on Linux.
type System struct{}

func NewSystem() (*System, error) {
	return nil, errors.New("real system accounts are only supported on Linux")
}

func (s *System) Mode() Mode                                           { return ModeSystem }
func (s *System) List(context.Context) ([]Account, error)              { return nil, errors.New("unsupported") }
func (s *System) Lookup(context.Context, string) (*Account, error)     { return nil, ErrNotFound }
func (s *System) Create(context.Context, NewAccount) error             { return errors.New("unsupported") }
func (s *System) Delete(context.Context, string, bool) error           { return errors.New("unsupported") }
func (s *System) SetRole(context.Context, string, string) error        { return errors.New("unsupported") }
func (s *System) SetPassword(context.Context, string, string) error    { return errors.New("unsupported") }
func (s *System) SetDisabled(context.Context, string, bool) error      { return errors.New("unsupported") }
func (s *System) Verify(context.Context, string, string) (bool, error) { return false, nil }
func (s *System) RoleOf(context.Context, string) (string, bool, error) {
	return "", false, ErrNotFound
}
