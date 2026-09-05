package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/validate"
)

// ------------------------------------- GET /org/notification-settings --

func (s *Server) handleGetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such organisation")
		return
	}
	if err != nil {
		s.serverError(w, r, "org.notification_settings.get", err)
		return
	}
	WriteJSON(w, http.StatusOK, toNotificationSettings(parseSettings(org.Settings)))
}

// ------------------------------------- PUT /org/notification-settings --

// handlePutNotificationSettings merges the request onto the org's stored
// settings. It is a partial merge (API.md): an absent key keeps what is there,
// so a client saving one toggle does not have to send the whole catalogue back.
func (s *Server) handlePutNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body notificationSettingsPatch
	if !DecodeJSON(w, r, &body) {
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such organisation")
		return
	}
	if err != nil {
		s.serverError(w, r, "org.notification_settings.get", err)
		return
	}

	current := parseSettings(org.Settings)
	f := validate.Fields{}
	updated := applyNotificationPatch(current, body, f)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	raw, err := marshalSettings(updated)
	if err != nil {
		s.serverError(w, r, "org.notification_settings.marshal", err)
		return
	}

	before := toNotificationSettings(current)
	after := toNotificationSettings(updated)
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.UpdateOrg(r.Context(), sqlc.UpdateOrgParams{ID: p.OrgID, Settings: raw}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionNotificationSettings,
			EntityType:  audit.EntityOrg,
			EntityID:    p.OrgIDString(),
			Before:      before,
			After:       after,
		})
	}); err != nil {
		s.serverError(w, r, "org.notification_settings.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, after)
}

// ------------------------------------------ POST /notifications/custom --

// customRecipient is one resolved addressee of a broadcast.
type customRecipient struct {
	UserID   string
	Name     string
	Phone    string
	Unit     string
	Property string
	// Locale is the renter's own language, already resolved against the org's
	// default: the body they get is chosen by it (Phase 13).
	Locale string
}

// handleCustomSMS is the landlord's bulk send (FLOWS 8: "water outage
// notice"). It resolves recipients inside the org, renders the body per renter
// so `{{name}}` means something, and queues one row each under a shared batch.
//
// Anyone the org does not know, and anyone with no phone number on file, is
// counted as skipped rather than refused: a broadcast to forty renters must not
// fail whole because one of them has no number.
func (s *Server) handleCustomSMS(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Recipients    string   `json:"recipients"`
		RenterUserIDs []string `json:"renter_user_ids"`
		// Body is the legacy single-language field: it stands for both
		// languages, so an existing caller keeps working unchanged. It is a
		// pointer so a caller who sent it — and sent it blank — is told about
		// `body` rather than about a field they never used.
		Body *string `json:"body"`
		// BodySW / BodyEN are the Phase 13 pair. At least one is required;
		// a recipient whose language has no body gets the other one, because
		// the alternative is telling nobody about the water outage.
		BodySW string `json:"body_sw"`
		BodyEN string `json:"body_en"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	mode := f.OneOf("recipients", strings.TrimSpace(body.Recipients), recipientsAllActive, recipientsSelected)
	bodies := customBodies(f, body.Body, body.BodySW, body.BodyEN)
	ids := s.parseRecipientIDs(f, mode, body.RenterUserIDs)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// The limit is the org's, not the caller's: the cost of a broadcast falls
	// on the tenant, and two managers must not get twenty batches between them.
	if res := s.limiter.Allow(r.Context(), "notify:custom:"+p.OrgIDString(),
		customBatchLimit, customBatchWindow); !res.Allowed {
		tooMany(w, res, "too many bulk messages from this organisation; try again later")
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "notifications.custom.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand, err := s.q.GetOrgBranding(r.Context(), p.OrgID)
	orgName := org.Name
	if err == nil && brand.DisplayName != "" {
		orgName = brand.DisplayName
	}

	recipients, skipped, err := s.resolveRecipients(r.Context(), p.OrgID, mode, ids)
	if err != nil {
		s.serverError(w, r, "notifications.custom.recipients", err)
		return
	}

	batchID := notify.NewBatchID()
	var (
		queuedIDs []string
		queued    int
	)
	byLanguage := map[string]int{notify.LangSwahili: 0, notify.LangEnglish: 0}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		for _, rc := range recipients {
			lang := notify.LanguageFor(rc.Locale, settings.SMSLanguage)
			text, sent := bodies.For(lang)
			id, qErr := notify.Queue(r.Context(), q, notify.Msg{
				OrgID: p.OrgIDString(), UserID: rc.UserID, Kind: notify.KindCustom,
				DedupeKey: "custom:" + batchID + ":" + rc.UserID,
				Phone:     rc.Phone,
				Body: notify.Render(notify.KindCustom, sent, notify.Vars{
					Name: rc.Name, Unit: rc.Unit, Property: rc.Property, Org: orgName, Body: text,
				}, settings.notifyOverrides()),
				BatchID:  batchID,
				Language: sent,
			})
			if errors.Is(qErr, notify.ErrDuplicate) {
				skipped++
				continue
			}
			if qErr != nil {
				return qErr
			}
			queuedIDs = append(queuedIDs, id)
			queued++
			byLanguage[sent]++
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionNotificationCustom,
			EntityType:  audit.EntityNotification,
			After: map[string]any{
				"batch_id": batchID, "recipients": mode,
				"queued": queued, "skipped": skipped,
				"body_sw": bodies.SW, "body_en": bodies.EN,
				"by_language": byLanguage,
			},
		})
	}); err != nil {
		s.serverError(w, r, "notifications.custom.tx", err)
		return
	}

	s.enqueueNotifications(r.Context(), queuedIDs...)
	WriteJSON(w, http.StatusAccepted, map[string]any{
		"batch_id": batchID, "queued": queued, "skipped": skipped,
		"by_language": byLanguage,
	})
}

// ------------------------ GET /notifications/custom/recipients-preview --

// handleCustomRecipientsPreview answers the compose screen's question before
// anything is sent: how many renters this selector reaches, and how many of
// them read each language. It takes the same filters as the send and resolves
// them through the same helper, so the counts it shows are the counts the send
// will produce.
func (s *Server) handleCustomRecipientsPreview(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	qs := r.URL.Query()
	f := validate.Fields{}
	mode := f.OneOf("recipients", strings.TrimSpace(qs.Get("recipients")),
		recipientsAllActive, recipientsSelected)
	var raw []string
	if v := strings.TrimSpace(qs.Get("renter_user_ids")); v != "" {
		raw = strings.Split(v, ",")
	}
	ids := s.parseRecipientIDs(f, mode, raw)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "notifications.custom.preview.org", err)
		return
	}
	orgLang := parseSettings(org.Settings).SMSLanguage

	recipients, skipped, err := s.resolveRecipients(r.Context(), p.OrgID, mode, ids)
	if err != nil {
		s.serverError(w, r, "notifications.custom.preview.recipients", err)
		return
	}
	byLanguage := map[string]int{notify.LangSwahili: 0, notify.LangEnglish: 0}
	for _, rc := range recipients {
		byLanguage[notify.LanguageFor(rc.Locale, orgLang)]++
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"count": len(recipients), "skipped": skipped, "by_language": byLanguage,
	})
}

// customBodyPair is a broadcast's wording in each language.
type customBodyPair struct {
	SW string
	EN string
}

// For returns the body one recipient gets and the language it is actually in.
// A landlord who wrote only one of the two reaches everybody in that one:
// silence is not the safer failure for "the water is off tomorrow".
func (b customBodyPair) For(lang string) (body, sent string) {
	if lang == notify.LangEnglish {
		if b.EN != "" {
			return b.EN, notify.LangEnglish
		}
		return b.SW, notify.LangSwahili
	}
	if b.SW != "" {
		return b.SW, notify.LangSwahili
	}
	return b.EN, notify.LangEnglish
}

// customBodies validates the submitted wording. `body` is the legacy field and
// stands for both languages; `body_sw` / `body_en` are the Phase 13 pair, of
// which at least one is required. Each is validated on its own so the error a
// landlord sees names the tab they typed in.
func customBodies(f validate.Fields, legacy *string, sw, en string) customBodyPair {
	sw, en = strings.TrimSpace(sw), strings.TrimSpace(en)
	if legacy != nil && sw == "" && en == "" {
		text := strings.TrimSpace(*legacy)
		if text == "" {
			f.Add("body", "body is required")
			return customBodyPair{}
		}
		if !customBodyValid(f, "body", text) {
			return customBodyPair{}
		}
		return customBodyPair{SW: text, EN: text}
	}
	if sw == "" && en == "" {
		f.Add("body_sw", "provide body_sw, body_en, or both")
		return customBodyPair{}
	}
	if sw != "" {
		customBodyValid(f, "body_sw", sw)
	}
	if en != "" {
		customBodyValid(f, "body_en", en)
	}
	return customBodyPair{SW: sw, EN: en}
}

// customBodyValid applies the broadcast rules to one body, reporting under the
// field name it arrived as.
func customBodyValid(f validate.Fields, field, text string) bool {
	ok := true
	if len([]rune(text)) > customBodyMax {
		f.Add(field, "must be at most "+strconv.Itoa(customBodyMax)+" characters")
		ok = false
	}
	if notify.HasControlChars(text) {
		f.Add(field, "must not contain control characters")
		ok = false
	}
	if unknown := notify.UnknownVariables(text, notify.CustomVariables); len(unknown) > 0 {
		f.Add(field, "unknown variables: {{"+strings.Join(unknown, "}}, {{")+"}}")
		ok = false
	}
	return ok
}

// parseRecipientIDs validates the `selected` id list. `all_active` carries no
// ids, and sending them anyway is a mistake worth naming.
func (s *Server) parseRecipientIDs(f validate.Fields, mode string, in []string) []pgtype.UUID {
	if mode != recipientsSelected {
		if len(in) > 0 {
			f.Add("renter_user_ids", "only `selected` takes a list of renters")
		}
		return nil
	}
	if len(in) == 0 {
		f.Add("renter_user_ids", "at least one renter is required")
		return nil
	}
	if len(in) > customRecipentMax {
		f.Add("renter_user_ids", "at most "+strconv.Itoa(customRecipentMax)+" renters per batch")
		return nil
	}
	out := make([]pgtype.UUID, 0, len(in))
	for _, raw := range in {
		id, err := db.ParseUUID(strings.TrimSpace(raw))
		if err != nil {
			f.Add("renter_user_ids", "must all be renter ids (UUID)")
			return nil
		}
		out = append(out, id)
	}
	return out
}

// resolveRecipients turns the selector into the renters that will actually be
// texted, and counts the rest as skipped.
//
// For `selected`, an id the org has no relationship with simply does not come
// back from the query — so a landlord cannot use a broadcast to discover
// whether a renter of another org exists.
func (s *Server) resolveRecipients(
	ctx context.Context, orgID pgtype.UUID, mode string, ids []pgtype.UUID,
) ([]customRecipient, int, error) {
	var (
		out     []customRecipient
		skipped int
	)
	add := func(userID pgtype.UUID, name, phone, unit, property, locale string) {
		if strings.TrimSpace(phone) == "" {
			skipped++ // no number on file; the rest of the batch still goes
			return
		}
		out = append(out, customRecipient{
			UserID: db.UUIDString(userID), Name: name, Phone: phone,
			Unit: unit, Property: property, Locale: locale,
		})
	}

	if mode == recipientsAllActive {
		rows, err := s.q.ListActiveRenterRecipients(ctx, orgID)
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			add(row.RenterUserID, row.RenterName, db.StrVal(row.RenterPhone),
				row.UnitName, row.PropertyName, row.RenterLocale)
		}
		return out, skipped, nil
	}

	rows, err := s.q.ListSelectedRenterRecipients(ctx, sqlc.ListSelectedRenterRecipientsParams{
		OrgID: orgID, UserIds: ids,
	})
	if err != nil {
		return nil, 0, err
	}
	found := make(map[string]bool, len(rows))
	for _, row := range rows {
		found[db.UUIDString(row.RenterUserID)] = true
		add(row.RenterUserID, row.RenterName, db.StrVal(row.RenterPhone),
			row.UnitName, row.PropertyName, row.RenterLocale)
	}
	// Ids this org does not know are skipped, never a 404: the request itself
	// was well formed, and the count is the honest answer.
	for _, id := range ids {
		if !found[db.UUIDString(id)] {
			skipped++
		}
	}
	return out, skipped, nil
}

// -------------------------------------------- GET /notifications/log --

func (s *Server) handleListNotificationLog(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}

	params := sqlc.ListOrgNotificationsParams{OrgID: p.OrgID, RowLimit: notifyLogDefaultLimit}
	if v := strings.TrimSpace(qs.Get("kind")); v != "" {
		if !notify.KnownKind(v) && v != notify.KindCustom && v != notify.KindOTP {
			f.Add("kind", "unknown notification kind")
		} else {
			params.Kind = &v
		}
	}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		if !notificationStatuses[v] {
			f.Add("status", "must be one of: queued, sending, sent, failed")
		} else {
			params.Status = &v
		}
	}
	params.UserID = optQueryUUID(f, "user_id", qs.Get("user_id"))
	params.FromAt = optQueryTime(f, "from", qs.Get("from"))
	params.ToAt = optQueryTime(f, "to", qs.Get("to"))
	if v := strings.TrimSpace(qs.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > notifyLogMaxLimit {
			f.Add("limit", "must be between 1 and "+strconv.Itoa(notifyLogMaxLimit))
		} else {
			params.RowLimit = int32(n)
		}
	}
	if v := strings.TrimSpace(qs.Get("cursor")); v != "" {
		at, id, ok := decodeCursor(v)
		if !ok {
			f.Add("cursor", "malformed cursor")
		} else {
			params.CursorAt = db.TS(at)
			params.CursorID = id
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListOrgNotifications(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "notifications.log.list", err)
		return
	}
	items := make([]notificationLogItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, toNotificationLogItem(row))
	}
	var next *string
	if len(rows) == int(params.RowLimit) && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), params.RowLimit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ------------------------------- POST /notifications/log/{id}/retry --

// handleRetryNotification puts one failed message back on the queue.
//
// Only a `failed` row is retryable: a `queued` or `sending` one is already on
// its way, and a `sent` one would be a second SMS to the renter. A row of
// another org is a 404, like every other org-scoped id.
func (s *Server) handleRetryNotification(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	notifyID, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundNotification(w)
		return
	}
	existing, err := s.q.GetOrgNotification(r.Context(), sqlc.GetOrgNotificationParams{
		OrgID: p.OrgID, ID: notifyID,
	})
	if isNoRows(err) {
		notFoundNotification(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "notifications.retry.get", err)
		return
	}
	if existing.Status != "failed" {
		conflictCode(w, "not_failed", "nothing to retry",
			"only a failed message can be sent again")
		return
	}

	var requeued pgtype.UUID
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		requeued, err = q.RetryOrgNotification(r.Context(), sqlc.RetryOrgNotificationParams{
			OrgID: p.OrgID, ID: notifyID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionNotificationRetry,
			EntityType:  audit.EntityNotification,
			EntityID:    db.UUIDString(notifyID),
			Before:      map[string]any{"status": existing.Status, "attempts": existing.Attempts},
			After:       map[string]any{"status": "queued"},
		})
	}); err != nil {
		if isNoRows(err) {
			// It stopped being `failed` between the read and the update.
			conflictCode(w, "not_failed", "nothing to retry",
				"only a failed message can be sent again")
			return
		}
		s.serverError(w, r, "notifications.retry.tx", err)
		return
	}

	s.enqueueNotifications(r.Context(), db.UUIDString(requeued))
	WriteJSON(w, http.StatusAccepted, map[string]any{"id": db.UUIDString(requeued), "status": "queued"})
}

func notFoundNotification(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such notification")
}

// ------------------------------ POST /admin/jobs/notifications --

// handleNotificationsJob runs the scheduler sweep on demand, the way
// /admin/jobs/overdue runs the overdue one: a tester should not have to wait
// for 09:00 EAT, or for the next five-minute tick, to watch the Flow 8
// timeline fire.
func (s *Server) handleNotificationsJob(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Date      string `json:"date"`
		ForceHour bool   `json:"force_hour"`
	}
	if r.ContentLength > 0 && !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	opt := notify.Options{
		ForceHour: body.ForceHour,
		BaseURL:   s.cfg.PublicBaseURL,
		Settings:  SchedulerSettings,
	}
	if v := strings.TrimSpace(body.Date); v != "" {
		d, err := time.Parse(dateLayout, v)
		if err != nil {
			f.Add("date", "must be a date (YYYY-MM-DD)")
		} else {
			opt.Date = d
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	res, err := notify.RunOnce(r.Context(), s.q, time.Now(), opt)
	if err != nil {
		s.serverError(w, r, "admin.notifications", err)
		return
	}
	s.enqueueNotifications(r.Context(), res.IDs...)

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionNotificationRun,
			EntityType:  audit.EntityNotification,
			After:       map[string]any{"queued": res.Queued, "total": res.Total()},
		})
	}); err != nil {
		s.serverError(w, r, "admin.notifications.audit", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"queued": res.Queued})
}
