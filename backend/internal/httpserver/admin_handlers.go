package httpserver

import (
	"net/http"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/contract"
	"tms/backend/internal/db/sqlc"
)

// ------------------------------- POST /admin/jobs/contract-lifecycle --

// handleContractLifecycleJob runs the contract lifecycle sweep on demand.
//
// The same function runs hourly in cmd/api; exposing it lets a tester (or a
// UAT session) see a contract cross into `expiring` or `ended` without waiting
// for the tick. It is platform-wide, so it sits behind the platform-admin
// session (`tms_a`) rather than an org one.
func (s *Server) handleContractLifecycleJob(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	res, err := contract.RunLifecycle(r.Context(), s.deps.Pool)
	if err != nil {
		s.serverError(w, r, "admin.contract_lifecycle", err)
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionContractLifecycleRun,
			EntityType:  audit.EntityContract,
			After: map[string]any{
				"expiring": res.Expiring, "ended": res.Ended, "units_freed": res.Freed,
			},
		})
	}); err != nil {
		s.serverError(w, r, "admin.contract_lifecycle.audit", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"expiring": res.Expiring, "ended": res.Ended, "units_freed": res.Freed,
	})
}
