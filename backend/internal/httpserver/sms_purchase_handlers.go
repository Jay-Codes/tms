package httpserver

import (
	"context"
	"errors"
	"io"
	"math"
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
	"tms/backend/internal/smspay"
	"tms/backend/internal/snippe"
	"tms/backend/internal/validate"
)

// Phase 27 — landlords buy SMS credits with mobile money (Snippe), and the
// platform keeps track of its own SMS stock (API.md "Phase 27").

const (
	// smsOrderWindow / smsOrderWindowMax: at most five payment prompts per org
	// per ten minutes. A landlord tapping "Buy" again because the first prompt
	// is slow must not flood a phone (or Snippe's 60/min limit).
	smsOrderWindow    = 10 * time.Minute
	smsOrderWindowMax = 5
	// smsOrderListMax is how many orders the landlord's history shows.
	smsOrderListMax = 50
	// smsOrderRecheck: a pending order read by the landlord's polling screen
	// asks Snippe directly at most this often (the webhook is the normal path).
	smsOrderRecheck = 20 * time.Second
	// snippeWebhookMaxBody caps a webhook delivery.
	snippeWebhookMaxBody = 64 << 10

	smsPackageNameMax   = 60
	smsPackageCredits   = 1_000_000
	smsPackagePriceMax  = 100_000_000
	smsAdminOrdersMax   = 500
	platformSMSCountMax = 100_000_000
	platformSMSCostMax  = 10_000_000_000
	platformSMSRefMax   = 120
	platformSMSNoteMax  = 500
)

func purchasesDisabled(w http.ResponseWriter) {
	httpx.WriteProblemCode(w, http.StatusServiceUnavailable, "purchases_disabled",
		"purchases are switched off",
		"buying SMS credits is not switched on yet; contact the platform for a top-up")
}

func notFoundSMSOrder(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such order")
}

func notFoundSMSPackage(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such package")
}

// ------------------------------------------------------------------ DTOs --

type smsPackageDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Credits   int32     `json:"credits"`
	Price     int64     `json:"price"`
	Active    bool      `json:"active"`
	SortOrder int32     `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toSMSPackage(p sqlc.SmsCreditPackage) smsPackageDTO {
	return smsPackageDTO{
		ID: db.UUIDString(p.ID), Name: p.Name, Credits: p.Credits, Price: p.Price,
		Active: p.Active, SortOrder: p.SortOrder,
		CreatedAt: p.CreatedAt.Time.UTC(), UpdatedAt: p.UpdatedAt.Time.UTC(),
	}
}

type smsOrderDTO struct {
	ID            string     `json:"id"`
	OrderCode     string     `json:"order_code"`
	PackageID     *string    `json:"package_id"`
	PackageName   string     `json:"package_name"`
	Credits       int32      `json:"credits"`
	Amount        int64      `json:"amount"`
	PayerPhone    string     `json:"payer_phone"`
	Status        string     `json:"status"`
	FailureReason *string    `json:"failure_reason"`
	Reference     *string    `json:"reference"`
	CreatedAt     time.Time  `json:"created_at"`
	CreditedAt    *time.Time `json:"credited_at"`
	// The admin list adds the org.
	OrgID   string `json:"org_id,omitempty"`
	OrgName string `json:"org_name,omitempty"`
}

func toSMSOrder(o sqlc.SmsCreditOrder) smsOrderDTO {
	out := smsOrderDTO{
		ID: db.UUIDString(o.ID), OrderCode: o.OrderCode, PackageName: o.PackageName,
		Credits: o.Credits, Amount: o.Amount, PayerPhone: o.PayerPhone, Status: o.Status,
		FailureReason: o.FailureReason, Reference: o.SnippeReference,
		CreatedAt: o.CreatedAt.Time.UTC(),
	}
	if o.PackageID.Valid {
		id := db.UUIDString(o.PackageID)
		out.PackageID = &id
	}
	if o.CreditedAt.Valid {
		t := o.CreditedAt.Time.UTC()
		out.CreditedAt = &t
	}
	return out
}

// ------------------------------------------ GET /org/sms-credits/packages --

// handleOrgSMSPackages is the landlord's "Buy credits" screen: what is on
// sale, whether buying is switched on at all, and the phone the prompt goes to
// unless they name another.
func (s *Server) handleOrgSMSPackages(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListActiveSMSCreditPackages(r.Context())
	if err != nil {
		s.serverError(w, r, "org.sms_packages", err)
		return
	}
	items := make([]smsPackageDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toSMSPackage(row))
	}
	phone := ""
	if u, err := s.q.GetUserByID(r.Context(), p.UserID); err == nil && u.Phone != nil {
		phone = *u.Phone
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":       s.cfg.SnippeEnabled(),
		"default_phone": phone,
		"items":         items,
	})
}

// ------------------------------------------- POST /org/sms-credits/orders --

// handleCreateSMSOrder writes the order, then asks Snippe to push a payment
// prompt to the payer's phone. The order exists before the call so its code —
// the Idempotency-Key — is fixed whatever happens to the request; the credits
// arrive later, from the webhook or the reconciliation job, never from here.
func (s *Server) handleCreateSMSOrder(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.SnippeEnabled() || s.snippe == nil {
		purchasesDisabled(w)
		return
	}
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		PackageID string `json:"package_id"`
		Phone     string `json:"phone"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	packageID, err := db.ParseUUID(strings.TrimSpace(body.PackageID))
	if err != nil {
		f.Add("package_id", "package_id is required")
	}
	rawPhone := strings.TrimSpace(body.Phone)
	if rawPhone == "" {
		if u, err := s.q.GetUserByID(r.Context(), p.UserID); err == nil && u.Phone != nil {
			rawPhone = *u.Phone
		}
	}
	phone := f.Phone("phone", rawPhone)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	pkg, err := s.q.GetActiveSMSCreditPackage(r.Context(), packageID)
	if err != nil {
		if isNoRows(err) {
			f.Add("package_id", "that package is not on sale")
			badRequest(w, f)
			return
		}
		s.serverError(w, r, "org.sms_order.package", err)
		return
	}

	recent, err := s.q.CountRecentOrgSMSCreditOrders(r.Context(), sqlc.CountRecentOrgSMSCreditOrdersParams{
		OrgID: p.OrgID, Since: pgtype.Timestamptz{Time: time.Now().Add(-smsOrderWindow), Valid: true},
	})
	if err != nil {
		s.serverError(w, r, "org.sms_order.count", err)
		return
	}
	if recent >= smsOrderWindowMax {
		w.Header().Set("Retry-After", strconv.Itoa(int(smsOrderWindow.Seconds())))
		httpx.WriteProblemCode(w, http.StatusTooManyRequests, "too_many_orders", "too many payment requests",
			"wait a few minutes before asking for another payment prompt")
		return
	}

	code, err := smspay.NewOrderCode()
	if err != nil {
		s.serverError(w, r, "org.sms_order.code", err)
		return
	}
	var order sqlc.SmsCreditOrder
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		order, err = q.InsertSMSCreditOrder(r.Context(), sqlc.InsertSMSCreditOrderParams{
			OrgID: p.OrgID, PackageID: pkg.ID, PackageName: pkg.Name, Credits: pkg.Credits,
			Amount: pkg.Price, PayerPhone: phone, OrderCode: code, CreatedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionSMSCreditOrder,
			EntityType:  audit.EntitySMSCreditOrder,
			EntityID:    db.UUIDString(order.ID),
			After: map[string]any{
				"order_code": code, "package": pkg.Name, "credits": pkg.Credits,
				"amount": pkg.Price, "payer_phone": phone,
			},
		})
	}); err != nil {
		s.serverError(w, r, "org.sms_order.insert", err)
		return
	}

	payment, err := s.snippe.CreatePayment(r.Context(), snippe.CreateRequest{
		Amount: order.Amount, Phone: phone, IdempotencyKey: order.OrderCode,
		Metadata:   map[string]string{"order_code": order.OrderCode},
		WebhookURL: s.cfg.SnippeWebhookURL(),
	})
	if err != nil {
		var se *snippe.Error
		if errors.As(err, &se) && se.HTTPStatus < 500 {
			// Snippe said no (a bad number, a limit): the order is over and
			// the landlord is told why.
			reason := truncateRunes(firstNonBlank(se.Message, se.Code, "payment request refused"), 300)
			if _, ferr := s.q.FailNewSMSCreditOrder(r.Context(), sqlc.FailNewSMSCreditOrderParams{
				ID: order.ID, OrgID: p.OrgID, FailureReason: &reason,
			}); ferr != nil && !isNoRows(ferr) {
				s.logger.Error("close refused sms order", "order_code", order.OrderCode, "error", ferr)
			}
			httpx.WriteProblemCode(w, http.StatusBadGateway, "payment_request_refused",
				"the payment request was refused", reason)
			return
		}
		// No answer (or a 5xx): Snippe may or may not have sent the prompt. The order
		// stays pending — a webhook naming its code still credits it, and the
		// reconciliation job closes it after four hours otherwise.
		s.logger.Warn("snippe create payment failed", "order_code", order.OrderCode, "error", err)
		httpx.WriteProblemCode(w, http.StatusBadGateway, "payment_provider_unreachable",
			"the payment service did not answer",
			"we could not confirm the payment request; if a prompt reaches the phone, approving it still adds the credits")
		return
	}
	if ref := strings.TrimSpace(payment.Reference); ref != "" {
		if updated, err := s.q.SetSMSCreditOrderReference(r.Context(), sqlc.SetSMSCreditOrderReferenceParams{
			ID: order.ID, OrgID: p.OrgID, SnippeReference: &ref,
		}); err == nil {
			order = updated
		} else {
			s.logger.Error("store snippe reference", "order_code", order.OrderCode, "error", err)
		}
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"order": toSMSOrder(order)})
}

// -------------------------------------------- GET /org/sms-credits/orders --

func (s *Server) handleListSMSOrders(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListOrgSMSCreditOrders(r.Context(), sqlc.ListOrgSMSCreditOrdersParams{
		OrgID: p.OrgID, RowLimit: smsOrderListMax,
	})
	if err != nil {
		s.serverError(w, r, "org.sms_orders", err)
		return
	}
	items := make([]smsOrderDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toSMSOrder(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "enabled": s.cfg.SnippeEnabled()})
}

// --------------------------------------- GET /org/sms-credits/orders/{id} --

// handleGetSMSOrder is what the "waiting for approval" screen polls. A pending
// order it reads may ask Snippe directly (at most every smsOrderRecheck), so
// the screen settles even where the webhook cannot reach the API.
func (s *Server) handleGetSMSOrder(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundSMSOrder(w)
		return
	}
	order, err := s.q.GetOrgSMSCreditOrder(r.Context(), sqlc.GetOrgSMSCreditOrderParams{ID: id, OrgID: p.OrgID})
	if err != nil {
		if isNoRows(err) {
			notFoundSMSOrder(w)
			return
		}
		s.serverError(w, r, "org.sms_order", err)
		return
	}
	if s.shouldRecheck(order) {
		if _, err := s.smspay.CheckOrder(r.Context(), order); err != nil {
			s.logger.Warn("on-demand sms order check failed", "order_code", order.OrderCode, "error", err)
		}
		if reread, err := s.q.GetOrgSMSCreditOrder(r.Context(),
			sqlc.GetOrgSMSCreditOrderParams{ID: id, OrgID: p.OrgID}); err == nil {
			order = reread
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"order": toSMSOrder(order)})
}

func (s *Server) shouldRecheck(o sqlc.SmsCreditOrder) bool {
	if o.Status != smspay.StatusPending || o.SnippeReference == nil || !s.snippe.Configured() {
		return false
	}
	now := time.Now()
	if now.Sub(o.CreatedAt.Time) < smsOrderRecheck {
		return false
	}
	return !o.LastCheckedAt.Valid || now.Sub(o.LastCheckedAt.Time) >= smsOrderRecheck
}

// ------------------------------------------------ POST /webhooks/snippe --

// handleSnippeWebhook is public: Snippe holds no session. The signature over
// the raw body and the timestamp are the authorisation, checked before the
// body is even parsed; the event id dedupes retries. Anything verified is
// answered 2xx — an event for an unknown order or with the wrong amount is
// recorded, not bounced, so Snippe does not retry it five times.
func (s *Server) handleSnippeWebhook(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.cfg.SnippeWebhookSecret) == "" {
		purchasesDisabled(w)
		return
	}
	if s.dbUnavailable(w) {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, snippeWebhookMaxBody+1))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "bad request", "the body could not be read")
		return
	}
	if len(raw) > snippeWebhookMaxBody {
		httpx.WriteProblem(w, http.StatusRequestEntityTooLarge, "too large", "webhook body too large")
		return
	}
	if err := snippe.Verify(s.cfg.SnippeWebhookSecret, r.Header.Get(snippe.HeaderTimestamp),
		r.Header.Get(snippe.HeaderSignature), raw, time.Now()); err != nil {
		code := "invalid_signature"
		if errors.Is(err, snippe.ErrStale) {
			code = "stale_webhook"
		}
		s.logger.Warn("snippe webhook refused", "reason", code, "ip", audit.RequestInfoFrom(r.Context()).IP)
		httpx.WriteProblemCode(w, http.StatusUnauthorized, code, "webhook not verified",
			"the signature or timestamp did not verify")
		return
	}
	evt, err := snippe.ParseEvent(raw)
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "bad request", "the event could not be read")
		return
	}
	outcome, err := s.smspay.HandleWebhook(r.Context(), evt, raw)
	if err != nil {
		// A 500 makes Snippe retry, which is what a transient database
		// failure wants.
		s.serverError(w, r, "webhook.snippe", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"received": true, "outcome": outcome})
}

// ------------------------------------------------ admin: packages --

type smsPackageBody struct {
	Name      *string `json:"name"`
	Credits   *int    `json:"credits"`
	Price     *int64  `json:"price"`
	Active    *bool   `json:"active"`
	SortOrder *int    `json:"sort_order"`
}

// validatePackage checks a full package (after a PATCH is merged).
func validatePackage(f validate.Fields, name string, credits int, price int64, sortOrder int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		f.Add("name", "name is required")
	} else if len([]rune(name)) > smsPackageNameMax {
		f.Add("name", "must be at most 60 characters")
	}
	if credits < 1 || credits > smsPackageCredits {
		f.Add("credits", "must be between 1 and 1000000")
	}
	if price < snippe.MinAmount || price > smsPackagePriceMax {
		f.Add("price", "must be between 500 and 100000000 TZS (Snippe's minimum is 500)")
	}
	if sortOrder < -10_000 || sortOrder > 10_000 {
		f.Add("sort_order", "must be between -10000 and 10000")
	}
	return name
}

func (s *Server) handleAdminListSMSPackages(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	rows, err := s.q.AdminListSMSCreditPackages(r.Context())
	if err != nil {
		s.serverError(w, r, "admin.sms_packages", err)
		return
	}
	items := make([]smsPackageDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toSMSPackage(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "purchases_enabled": s.cfg.SnippeEnabled()})
}

func (s *Server) handleAdminCreateSMSPackage(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body smsPackageBody
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	name, credits, price, sortOrder, active := "", 0, int64(0), 0, true
	if body.Name != nil {
		name = *body.Name
	}
	if body.Credits != nil {
		credits = *body.Credits
	}
	if body.Price != nil {
		price = *body.Price
	}
	if body.SortOrder != nil {
		sortOrder = *body.SortOrder
	}
	if body.Active != nil {
		active = *body.Active
	}
	name = validatePackage(f, name, credits, price, sortOrder)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	var pkg sqlc.SmsCreditPackage
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		pkg, err = q.AdminCreateSMSCreditPackage(r.Context(), sqlc.AdminCreateSMSCreditPackageParams{
			Name: name, Credits: int32(credits), Price: price, Active: active,
			SortOrder: int32(sortOrder), CreatedByAdminID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionSMSPackageCreate,
			EntityType:  audit.EntitySMSCreditPackage,
			EntityID:    db.UUIDString(pkg.ID),
			After:       toSMSPackage(pkg),
		})
	}); err != nil {
		s.serverError(w, r, "admin.sms_package.create", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"package": toSMSPackage(pkg)})
}

// handleAdminPatchSMSPackage edits a package. Orders copied its name, credits
// and price when they were placed, so an edit changes only what is sold next.
// There is no delete: `active: false` takes a package off sale and keeps it
// for the orders that name it.
func (s *Server) handleAdminPatchSMSPackage(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundSMSPackage(w)
		return
	}
	var body smsPackageBody
	if !DecodeJSON(w, r, &body) {
		return
	}
	var (
		before, after sqlc.SmsCreditPackage
		invalid       validate.Fields
	)
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		before, err = q.AdminGetSMSCreditPackage(r.Context(), id)
		if err != nil {
			return err
		}
		name, credits, price := before.Name, int(before.Credits), before.Price
		sortOrder, active := int(before.SortOrder), before.Active
		if body.Name != nil {
			name = *body.Name
		}
		if body.Credits != nil {
			credits = *body.Credits
		}
		if body.Price != nil {
			price = *body.Price
		}
		if body.SortOrder != nil {
			sortOrder = *body.SortOrder
		}
		if body.Active != nil {
			active = *body.Active
		}
		f := validate.Fields{}
		name = validatePackage(f, name, credits, price, sortOrder)
		if !f.Empty() {
			invalid = f
			return errValidation
		}
		after, err = q.AdminUpdateSMSCreditPackage(r.Context(), sqlc.AdminUpdateSMSCreditPackageParams{
			ID: id, Name: name, Credits: int32(credits), Price: price, Active: active,
			SortOrder: int32(sortOrder),
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionSMSPackageUpdate,
			EntityType:  audit.EntitySMSCreditPackage,
			EntityID:    db.UUIDString(id),
			Before:      toSMSPackage(before),
			After:       toSMSPackage(after),
		})
	}); err != nil {
		switch {
		case invalid != nil:
			badRequest(w, invalid)
		case isNoRows(err):
			notFoundSMSPackage(w)
		default:
			s.serverError(w, r, "admin.sms_package.patch", err)
		}
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"package": toSMSPackage(after)})
}

// errValidation aborts a transaction whose input failed validation part-way.
var errValidation = errors.New("validation failed")

// ------------------------------------------------ admin: orders --

func (s *Server) handleAdminListSMSOrders(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	qs := r.URL.Query()
	f := validate.Fields{}
	params := sqlc.AdminListSMSCreditOrdersParams{RowLimit: 100}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		st := f.OneOf("status", v, smspay.StatusPending, smspay.StatusCompleted, smspay.StatusFailed,
			smspay.StatusExpired, smspay.StatusMismatch)
		params.Status = &st
	}
	if v := strings.TrimSpace(qs.Get("org_id")); v != "" {
		id, err := db.ParseUUID(v)
		if err != nil {
			f.Add("org_id", "must be an org id")
		}
		params.FilterOrgID = id
	}
	if v := strings.TrimSpace(qs.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > smsAdminOrdersMax {
			f.Add("limit", "must be between 1 and "+strconv.Itoa(smsAdminOrdersMax))
		}
		params.RowLimit = int32(n)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	rows, err := s.q.AdminListSMSCreditOrders(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "admin.sms_orders", err)
		return
	}
	counts, err := s.q.AdminCountSMSCreditOrdersByStatus(r.Context())
	if err != nil {
		s.serverError(w, r, "admin.sms_orders.counts", err)
		return
	}
	items := make([]smsOrderDTO, 0, len(rows))
	for _, row := range rows {
		o := toSMSOrder(sqlc.SmsCreditOrder{
			ID: row.ID, OrgID: row.OrgID, PackageID: row.PackageID, PackageName: row.PackageName,
			Credits: row.Credits, Amount: row.Amount, PayerPhone: row.PayerPhone, OrderCode: row.OrderCode,
			SnippeReference: row.SnippeReference, Status: row.Status, FailureReason: row.FailureReason,
			CreditedAt: row.CreditedAt, CreatedAt: row.CreatedAt,
		})
		o.OrgID, o.OrgName = db.UUIDString(row.OrgID), row.OrgName
		items = append(items, o)
	}
	byStatus := map[string]int64{}
	for _, c := range counts {
		byStatus[c.Status] = c.N
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "counts": byStatus})
}

// handleAdminReconcileSMSOrders runs the reconciliation sweep now — the same
// sweep the API runs every minute — and reports what it did.
func (s *Server) handleAdminReconcileSMSOrders(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	res, err := s.smspay.Reconcile(r.Context())
	if err != nil {
		s.serverError(w, r, "admin.sms_orders.reconcile", err)
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionSMSOrderReconcile,
			EntityType:  audit.EntitySMSCreditOrder,
			After:       res,
		})
	}); err != nil {
		s.serverError(w, r, "admin.sms_orders.reconcile.audit", err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

// ------------------------------------------ admin: platform SMS stock --

type platformSMSPurchaseDTO struct {
	ID          string    `json:"id"`
	SMSCount    int32     `json:"sms_count"`
	Cost        int64     `json:"cost"`
	PurchasedOn string    `json:"purchased_on"`
	Reference   *string   `json:"reference"`
	Note        string    `json:"note"`
	AdminName   string    `json:"admin_name"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) handleAdminListPlatformSMSPurchases(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	rows, err := s.q.AdminListPlatformSMSPurchases(r.Context(), 200)
	if err != nil {
		s.serverError(w, r, "admin.platform_sms", err)
		return
	}
	items := make([]platformSMSPurchaseDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, platformSMSPurchaseDTO{
			ID: db.UUIDString(row.ID), SMSCount: row.SmsCount, Cost: row.Cost,
			PurchasedOn: row.PurchasedOn.Time.Format(dateLayout), Reference: row.Reference,
			Note: row.Note, AdminName: row.AdminName, CreatedAt: row.CreatedAt.Time.UTC(),
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleAdminCreatePlatformSMSPurchase records a Beem bundle the platform
// bought. An opening balance (stock bought before this screen existed) is a
// purchase with cost 0.
func (s *Server) handleAdminCreatePlatformSMSPurchase(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		SMSCount    int    `json:"sms_count"`
		Cost        *int64 `json:"cost"`
		PurchasedOn string `json:"purchased_on"`
		Reference   string `json:"reference"`
		Note        string `json:"note"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if body.SMSCount < 1 || body.SMSCount > platformSMSCountMax {
		f.Add("sms_count", "must be between 1 and 100000000")
	}
	if body.Cost == nil {
		f.Add("cost", "cost is required (0 for an opening balance)")
	} else if *body.Cost < 0 || *body.Cost > platformSMSCostMax {
		f.Add("cost", "must be between 0 and 10000000000 TZS")
	}
	on := time.Now().UTC()
	if v := strings.TrimSpace(body.PurchasedOn); v != "" {
		on = requiredDate(f, "purchased_on", v)
	}
	ref := f.MaxLen("reference", strings.TrimSpace(body.Reference), platformSMSRefMax)
	note := f.MaxLen("note", strings.TrimSpace(body.Note), platformSMSNoteMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	var refPtr *string
	if ref != "" {
		refPtr = &ref
	}
	var row sqlc.PlatformSmsPurchase
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		row, err = q.AdminInsertPlatformSMSPurchase(r.Context(), sqlc.AdminInsertPlatformSMSPurchaseParams{
			SmsCount: int32(body.SMSCount), Cost: *body.Cost,
			PurchasedOn: pgtype.Date{Time: on, Valid: true}, Reference: refPtr, Note: note,
			CreatedByAdminID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPlatformSMSPurchase,
			EntityType:  audit.EntityPlatformSMSPurchase,
			EntityID:    db.UUIDString(row.ID),
			After: map[string]any{
				"sms_count": body.SMSCount, "cost": *body.Cost,
				"purchased_on": on.Format(dateLayout), "reference": ref, "note": note,
			},
		})
	}); err != nil {
		s.serverError(w, r, "admin.platform_sms.create", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"purchase": platformSMSPurchaseDTO{
		ID: db.UUIDString(row.ID), SMSCount: row.SmsCount, Cost: row.Cost,
		PurchasedOn: row.PurchasedOn.Time.Format(dateLayout), Reference: row.Reference,
		Note: row.Note, CreatedAt: row.CreatedAt.Time.UTC(),
	}})
}

// avgCostPerSMS is what one SMS of platform stock cost on average, to two
// decimals of a shilling.
func avgCostPerSMS(cost, bought int64) float64 {
	if bought <= 0 {
		return 0
	}
	return math.Round(float64(cost)/float64(bought)*100) / 100
}

// handleAdminSMSStock sets the platform's SMS stock against what orgs hold.
// Every unused credit is an SMS the platform has promised to send, so the
// credits orgs hold are the liability; stock under liability + buffer raises
// `low_stock`.
func (s *Server) handleAdminSMSStock(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	st, err := s.q.AdminSMSStock(r.Context())
	if err != nil {
		s.serverError(w, r, "admin.sms_stock", err)
		return
	}
	buffer := int64(s.cfg.SMSStockBuffer)
	if buffer < 0 {
		buffer = 0
	}
	sent := st.CreditsDebited + st.UnchargedSent
	stock := st.SmsBought - sent
	WriteJSON(w, http.StatusOK, map[string]any{
		"sms_bought":        st.SmsBought,
		"beem_cost":         st.BeemCost,
		"avg_cost_per_sms":  avgCostPerSMS(st.BeemCost, st.SmsBought),
		"sms_sent":          sent,
		"credits_debited":   st.CreditsDebited,
		"uncharged_sent":    st.UnchargedSent,
		"stock":             stock,
		"liability":         st.CreditsUnused,
		"credits_purchased": st.CreditsPurchased,
		"credits_granted":   st.CreditsGranted,
		"buffer":            buffer,
		"headroom":          stock - st.CreditsUnused,
		"low_stock":         stock < st.CreditsUnused+buffer,
	})
}

// handleAdminSMSMargin: sales of credits in [from, to] less Snippe's 2.5% and
// what those credits cost at the average Beem price.
func (s *Server) handleAdminSMSMargin(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	qs := r.URL.Query()
	f := validate.Fields{}
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if v := strings.TrimSpace(qs.Get("from")); v != "" {
		from = requiredDate(f, "from", v)
	}
	if v := strings.TrimSpace(qs.Get("to")); v != "" {
		to = requiredDate(f, "to", v)
	}
	if f.Empty() && to.Before(from) {
		f.Add("to", "must not be before from")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	// `to` is inclusive: the window ends at the start of the next day.
	fromAt := pgtype.Timestamptz{Time: from, Valid: true}
	toAt := pgtype.Timestamptz{Time: to.AddDate(0, 0, 1), Valid: true}
	sales, err := s.q.AdminSMSSales(r.Context(), sqlc.AdminSMSSalesParams{FromAt: fromAt, ToAt: toAt})
	if err != nil {
		s.serverError(w, r, "admin.sms_margin", err)
		return
	}
	byPkg, err := s.q.AdminSMSSalesByPackage(r.Context(), sqlc.AdminSMSSalesByPackageParams{FromAt: fromAt, ToAt: toAt})
	if err != nil {
		s.serverError(w, r, "admin.sms_margin.packages", err)
		return
	}
	st, err := s.q.AdminSMSStock(r.Context())
	if err != nil {
		s.serverError(w, r, "admin.sms_margin.stock", err)
		return
	}
	avg := avgCostPerSMS(st.BeemCost, st.SmsBought)
	beemCost := int64(math.Round(avg * float64(sales.CreditsSold)))
	fee := snippe.Fee(sales.Sales)
	packages := make([]map[string]any, 0, len(byPkg))
	for _, p := range byPkg {
		packages = append(packages, map[string]any{
			"package_name": p.PackageName, "orders": p.Orders, "sales": p.Sales, "credits_sold": p.CreditsSold,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"from":             from.Format(dateLayout),
		"to":               to.Format(dateLayout),
		"orders":           sales.Orders,
		"sales":            sales.Sales,
		"credits_sold":     sales.CreditsSold,
		"snippe_fee":       fee,
		"fee_percent":      float64(snippe.FeeBasisPoints) / 100,
		"avg_cost_per_sms": avg,
		"beem_cost":        beemCost,
		"margin":           sales.Sales - fee - beemCost,
		"by_package":       packages,
	})
}

// ------------------------------------------------------------------ misc --

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// RunSMSOrderReconciler runs the Phase 27 reconciliation ticker until ctx
// ends (cmd/api starts it beside the lifecycle and overdue jobs).
func (s *Server) RunSMSOrderReconciler(ctx context.Context) {
	if s.deps.Pool == nil {
		s.logger.Warn("sms order reconciliation not started: postgres unavailable")
		return
	}
	s.smspay.RunTicker(ctx)
}
