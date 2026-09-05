package httpserver

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// notFoundRenter is the answer for a renter this org has no relationship with.
// It is the same 404 as for a user id that does not exist at all: the
// directory must not confirm that an account exists elsewhere on the platform.
func notFoundRenter(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such renter")
}

// --------------------------------------------------------- GET /renters --

func (s *Server) handleListRenters(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)
	qs := r.URL.Query()

	params := sqlc.ListOrgRentersParams{
		OrgID: p.OrgID, CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		// The value lands in an ILIKE pattern; escaping the wildcards keeps a
		// search for "_" from matching every renter.
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(v)
		params.Q = &escaped
	}
	if v := strings.TrimSpace(qs.Get("kyc_status")); v != "" {
		status := f.OneOf("kyc_status", v, kycNone, kycSubmitted, kycVerified, kycRejected)
		stored := kycIn(status)
		params.KycStatus = &stored
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListOrgRenters(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "renters.list", err)
		return
	}

	items := make([]renterDirectoryEntry, 0, len(rows))
	for _, row := range rows {
		units, err := s.renterUnits(r, p, row.UserID)
		if err != nil {
			s.serverError(w, r, "renters.list.units", err)
			return
		}
		items = append(items, renterDirectoryEntry{
			UserID:    db.UUIDString(row.UserID),
			FullName:  row.ProfileName,
			Phone:     row.Phone,
			Email:     row.Email,
			KycStatus: kycOut(row.KycStatus),
			Units:     units,
			CreatedAt: row.CreatedAt.Time,
		})
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.UserID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// renterUnits lists the units of THIS org a renter is connected to.
func (s *Server) renterUnits(r *http.Request, p auth.Principal, userID pgtype.UUID) ([]renterUnitLink, error) {
	rows, err := s.q.ListRenterUnitsForOrg(r.Context(), sqlc.ListRenterUnitsForOrgParams{
		OrgID: p.OrgID, RenterUserID: userID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]renterUnitLink, 0, len(rows))
	for _, row := range rows {
		out = append(out, renterUnitLink{
			UnitID:       db.UUIDString(row.UnitID),
			UnitName:     row.UnitName,
			PropertyName: row.PropertyName,
			LinkStatus:   row.LinkStatus,
		})
	}
	return out, nil
}

// ------------------------------------------------ GET /renters/{user_id} --

// orgRenter loads a renter the caller's org knows, writing the 404 itself.
func (s *Server) orgRenter(w http.ResponseWriter, r *http.Request, p auth.Principal) (sqlc.GetOrgRenterRow, bool) {
	userID, err := db.ParseUUID(chi.URLParam(r, "user_id"))
	if err != nil {
		notFoundRenter(w)
		return sqlc.GetOrgRenterRow{}, false
	}
	row, err := s.q.GetOrgRenter(r.Context(), sqlc.GetOrgRenterParams{UserID: userID, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundRenter(w)
		return sqlc.GetOrgRenterRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "renters.get", err)
		return sqlc.GetOrgRenterRow{}, false
	}
	return row, true
}

func (s *Server) handleGetRenter(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgRenter(w, r, p)
	if !ok {
		return
	}

	profile, err := s.loadProfile(r.Context(), row.UserID)
	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "renters.get.profile", err)
		return
	}
	block := renterProfileBlock{FullName: row.ProfileName, KycStatus: kycOut(row.KycStatus)}
	if err == nil {
		block = renterProfileBlock{
			FullName:       profile.FullName,
			NidaMasked:     maskNIDA(profile.NidaNumber),
			NextOfKinName:  profile.NextOfKinName,
			NextOfKinPhone: profile.NextOfKinPhone,
			KycStatus:      kycOut(profile.KycStatus),
			KycDocUploaded: profile.KycDocObjectKey != nil && *profile.KycDocObjectKey != "",
		}
	}

	requests, err := s.q.ListLinkRequests(r.Context(), sqlc.ListLinkRequestsParams{
		OrgID: p.OrgID, RenterUserID: row.UserID, RowLimit: listMaxLimit,
	})
	if err != nil {
		s.serverError(w, r, "renters.get.requests", err)
		return
	}
	items := make([]linkRequestResponse, 0, len(requests))
	for _, req := range requests {
		lr := linkRowOfList(req)
		item := toLinkRequest(lr, true)
		item.SchedulePreview = previewFor(lr)
		items = append(items, item)
	}
	units, err := s.renterUnits(r, p, row.UserID)
	if err != nil {
		s.serverError(w, r, "renters.get.units", err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"renter": renterDirectoryEntry{
			UserID:    db.UUIDString(row.UserID),
			FullName:  row.ProfileName,
			Phone:     row.Phone,
			Email:     row.Email,
			KycStatus: kycOut(row.KycStatus),
			Units:     units,
			CreatedAt: row.CreatedAt.Time,
		},
		"profile":       block,
		"link_requests": items,
		// Contracts arrive in Phase 4; the key is present so the client shape
		// does not change when they do.
		"contracts": []any{},
	})
}

// --------------------------------------- GET /renters/{user_id}/kyc-doc --

func (s *Server) handleRenterKYCDoc(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	// The relationship check runs first: a landlord may only open the ID of a
	// renter who has approached their org.
	row, ok := s.orgRenter(w, r, p)
	if !ok {
		return
	}
	s.writeKYCDocURL(w, r, row.UserID, p.OrgIDString())
}
