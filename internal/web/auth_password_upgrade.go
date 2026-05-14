package web

import (
	"fmt"

	"github.com/screwys/igloo/internal/auth"
)

func (s *Server) upgradePasswordHashAfterLogin(username, password string, observed auth.PasswordRecord) error {
	if !auth.PasswordNeedsRehash(observed) {
		return nil
	}
	if s.cfg == nil || s.cfg.AuthUsersPath == "" {
		return nil
	}

	auth.LockUsers()
	defer auth.UnlockUsers()

	users, err := auth.LoadUsers(s.cfg.AuthUsersPath)
	if err != nil {
		return err
	}
	rec, ok := users[username]
	if !ok {
		return nil
	}
	if rec.Password != observed {
		return nil
	}
	if !auth.VerifyPassword(password, rec.Password) {
		return nil
	}

	rec.Password = auth.HashPassword(password)
	users[username] = rec
	if err := auth.SaveUsers(s.cfg.AuthUsersPath, users); err != nil {
		return fmt.Errorf("save upgraded password hash: %w", err)
	}
	auth.InvalidateCache()
	return nil
}
