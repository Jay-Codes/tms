package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
)

// pgUniqueViolation is the SQLSTATE for a unique constraint breach.
const pgUniqueViolation = "23505"

// userResponse is the `user` shape from API.md.
type userResponse struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Phone         *string   `json:"phone"`
	Email         *string   `json:"email"`
	FullName      string    `json:"full_name"`
	EmailVerified bool      `json:"email_verified"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

func toUser(u sqlc.User) userResponse {
	return userResponse{
		ID:            db.UUIDString(u.ID),
		Kind:          u.Kind,
		Phone:         u.Phone,
		Email:         u.Email,
		FullName:      u.FullName,
		EmailVerified: u.EmailVerifiedAt.Valid,
		Status:        u.Status,
		CreatedAt:     u.CreatedAt.Time,
	}
}

// sessionOrg is the compact org block returned alongside a user.
type sessionOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

// orgResponse is the full org shape (GET /org).
type orgResponse struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Slug      string      `json:"slug"`
	Status    string      `json:"status"`
	Settings  OrgSettings `json:"settings"`
	CreatedAt time.Time   `json:"created_at"`
}

func toOrg(o sqlc.Org) orgResponse {
	return orgResponse{
		ID:        db.UUIDString(o.ID),
		Name:      o.Name,
		Slug:      o.Slug,
		Status:    o.Status,
		Settings:  parseSettings(o.Settings),
		CreatedAt: o.CreatedAt.Time,
	}
}

// memberResponse is one row of GET /org/members.
type memberResponse struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Email     *string   `json:"email"`
	FullName  string    `json:"full_name"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// isUnique reports whether err is a Postgres unique-constraint violation.
func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// isNoRows reports whether err is pgx's "no rows in result set".
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// inTx runs fn inside a database transaction with a transaction-bound queries
// handle. Every mutating handler uses it so the mutation, its audit row and any
// session it issues commit together (SPEC §8).
func (s *Server) inTx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	if s.deps.Pool == nil {
		return errors.New("database unavailable")
	}
	tx, err := s.deps.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// serverError logs and writes a 500 without leaking internals to the client.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, op string, err error) {
	s.logger.Error("request failed", "op", op, "path", r.URL.Path, "error", err)
	httpx.WriteProblem(w, http.StatusInternalServerError, "internal error", "the request could not be completed")
}

// dbUnavailable reports whether the server can serve database-backed routes.
func (s *Server) dbUnavailable(w http.ResponseWriter) bool {
	if s.deps.Pool == nil || s.q == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "database unavailable", "the service is temporarily unable to reach its database")
		return true
	}
	return false
}

// OrgSettings is the validated shape of orgs.settings (API.md).
type OrgSettings struct {
	AutoApproveLinks     bool   `json:"auto_approve_links"`
	DueDay               *int   `json:"due_day"`
	GraceDays            int    `json:"grace_days"`
	ReminderOffsetsDays  []int  `json:"reminder_offsets_days"`
	UnsignedReminderDays int    `json:"unsigned_reminder_days"`
	SMSLanguage          string `json:"sms_language"`
	// BankAccount is the org's collection account (Phase 5). It is absent
	// until PUT /org/bank-account sets it, and is read through the dedicated
	// endpoint rather than PATCH /org.
	BankAccount *BankAccount `json:"bank_account,omitempty"`
}

// DefaultOrgSettings mirrors the column default in migration 000002.
func DefaultOrgSettings() OrgSettings {
	return OrgSettings{
		AutoApproveLinks:     false,
		DueDay:               nil,
		GraceDays: 0,
		ReminderOffsetsDays:  []int{7, 0},
		UnsignedReminderDays: 7,
		SMSLanguage:          "sw",
	}
}

// marshalSettings renders org settings back into the JSONB column.
func marshalSettings(s OrgSettings) ([]byte, error) { return json.Marshal(s) }

func parseSettings(raw []byte) OrgSettings {
	s := DefaultOrgSettings()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	if s.ReminderOffsetsDays == nil {
		s.ReminderOffsetsDays = []int{}
	}
	return s
}
