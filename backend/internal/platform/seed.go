package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"tms/backend/internal/auth"
	"tms/backend/internal/config"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// SeedAdmin creates the first platform_admin account from ADMIN_EMAIL /
// ADMIN_PASSWORD when no platform admin exists yet. It is a no-op when the
// variables are unset or an admin is already present, so it is safe to call on
// every start.
func SeedAdmin(ctx context.Context, pool *db.Pool, cfg config.Config, logger *slog.Logger) error {
	if pool == nil {
		return nil
	}
	email := strings.ToLower(strings.TrimSpace(cfg.AdminEmail))
	if email == "" || cfg.AdminPassword == "" {
		return nil
	}

	q := sqlc.New(pool)
	count, err := q.CountPlatformAdmins(ctx)
	if err != nil {
		return fmt.Errorf("platform: count admins: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := q.GetUserByEmail(ctx, email); err == nil {
		logger.Warn("platform admin not seeded: email already belongs to another account", "email", email)
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("platform: lookup admin email: %w", err)
	}

	hash, err := auth.HashSecret(cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("platform: hash admin password: %w", err)
	}
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		Kind:            auth.KindPlatformAdmin,
		Email:           &email,
		FullName:        "Platform Admin",
		PasswordHash:    &hash,
		EmailVerifiedAt: db.TS(time.Now().UTC()),
	})
	if err != nil {
		return fmt.Errorf("platform: create admin: %w", err)
	}
	logger.Info("platform admin seeded", "email", email, "user_id", db.UUIDString(user.ID))
	return nil
}
