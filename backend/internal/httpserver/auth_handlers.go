package httpserver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/ratelimit"
	"tms/backend/internal/validate"
)

// Rate limits (SPEC §3, §8).
const (
	otpSendLimit    = 3
	otpSendWindow   = 10 * time.Minute
	otpVerifyLimit  = 5
	otpVerifyWindow = 10 * time.Minute
	loginLimit      = 10
	loginWindow     = time.Minute
	orgCreateLimit  = 5
	orgCreateWindow = time.Hour
)

// otpResendAfterSeconds mirrors auth.OTPResendCooldown for the API response.
const otpResendAfterSeconds = 60

// badRequest writes a 400 carrying per-field validation messages.
func badRequest(w http.ResponseWriter, fields validate.Fields) {
	httpx.WriteProblemFields(w, http.StatusBadRequest, "validation failed",
		"one or more fields are invalid", fields)
}

// tooMany writes a 429 with Retry-After.
func tooMany(w http.ResponseWriter, res ratelimit.Result, detail string) {
	if res.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(res.RetryAfter.Seconds()))))
	}
	httpx.WriteProblem(w, http.StatusTooManyRequests, "too many requests", detail)
}

// ---------------------------------------------------------------- OTP send --

func (s *Server) handleOTPSend(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	var body struct {
		Phone   string `json:"phone"`
		Purpose string `json:"purpose"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	phone := f.Phone("phone", body.Phone)
	purpose := f.OneOf("purpose", body.Purpose, "register", "login", "sign")
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if res := s.limiter.Allow(r.Context(), "otp:send:"+phone, otpSendLimit, otpSendWindow); !res.Allowed {
		tooMany(w, res, "too many verification codes requested for this number")
		return
	}

	code := auth.GenerateOTP()
	switch err := s.store.PutOTP(r.Context(), purpose, phone, code); {
	case errors.Is(err, auth.ErrCooldown):
		tooMany(w, ratelimit.Result{RetryAfter: auth.OTPResendCooldown},
			"a code was already sent to this number; wait before requesting another")
		return
	case errors.Is(err, auth.ErrCacheUnavailable):
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "verification unavailable",
			"one-time codes cannot be issued right now")
		return
	case err != nil:
		s.serverError(w, r, "otp.put", err)
		return
	}

	body_ := fmt.Sprintf("TMS: your verification code is %s. It expires in 5 minutes.", code)
	// Sender: the platform default. An OTP is sent before any org is known —
	// the number may not belong to a renter of anyone yet.
	if _, err := s.deps.SMS.Send(r.Context(), phone, body_, ""); err != nil {
		s.logger.Error("otp sms send failed", "error", err)
	}

	// Audit: the actor is only known if this phone already has an account.
	actorID := ""
	if u, err := s.q.GetUserByPhone(r.Context(), &phone); err == nil {
		actorID = db.UUIDString(u.ID)
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: actorID,
			Action:      audit.ActionOTPSend,
			EntityType:  audit.EntityUser,
			EntityID:    actorID,
			After:       map[string]any{"phone": phone, "purpose": purpose},
		})
	}); err != nil {
		s.serverError(w, r, "otp.audit", err)
		return
	}

	WriteJSON(w, http.StatusAccepted, map[string]any{
		"phone":                phone,
		"resend_after_seconds": otpResendAfterSeconds,
	})
}

// -------------------------------------------------------------- OTP verify --

func (s *Server) handleOTPVerify(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	var body struct {
		Phone   string `json:"phone"`
		Code    string `json:"code"`
		Purpose string `json:"purpose"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	phone := f.Phone("phone", body.Phone)
	purpose := f.OneOf("purpose", body.Purpose, "register", "login", "sign")
	if len(body.Code) != 6 || !validate.IsDigits(body.Code) {
		f.Add("code", "must be a 6-digit code")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if res := s.limiter.Allow(r.Context(), "otp:verify:"+phone, otpVerifyLimit, otpVerifyWindow); !res.Allowed {
		tooMany(w, res, "too many verification attempts for this number")
		return
	}

	switch err := s.store.CheckOTP(r.Context(), purpose, phone, body.Code); {
	case errors.Is(err, auth.ErrNotFound):
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid code",
			"the code is incorrect or has expired", map[string]string{"code": "invalid or expired"})
		return
	case errors.Is(err, auth.ErrCacheUnavailable):
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "verification unavailable",
			"one-time codes cannot be verified right now")
		return
	case err != nil:
		s.serverError(w, r, "otp.check", err)
		return
	}

	// The code was valid: record the verification (no org context — the phone
	// is not necessarily attached to an account yet).
	actorID := ""
	if u, uErr := s.q.GetUserByPhone(r.Context(), &phone); uErr == nil {
		actorID = db.UUIDString(u.ID)
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: actorID,
			Action:      audit.ActionOTPVerify,
			EntityType:  audit.EntityUser,
			EntityID:    actorID,
			After:       map[string]any{"phone": phone, "purpose": purpose},
		})
	}); err != nil {
		s.serverError(w, r, "otp.verify.audit", err)
		return
	}

	switch purpose {
	case "register":
		token, err := s.store.PutOTPToken(r.Context(), phone)
		if err != nil {
			s.serverError(w, r, "otp.token", err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"otp_token": token})

	case "login":
		user, err := s.q.GetUserByPhone(r.Context(), &phone)
		if isNoRows(err) {
			// Do not disclose whether the number has an account: answer
			// exactly as a wrong code does. The attempt was already counted
			// by the rate limiter above and the code has been consumed.
			httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid code",
				"the code is incorrect or has expired", map[string]string{"code": "invalid or expired"})
			return
		}
		if err != nil {
			s.serverError(w, r, "otp.login.user", err)
			return
		}
		if user.Kind != auth.KindRenter {
			httpx.WriteProblem(w, http.StatusForbidden, "forbidden", "this account signs in with email and password")
			return
		}
		// The PIN path refuses suspended accounts; the OTP path must too.
		if user.Status != "active" {
			httpx.WriteProblem(w, http.StatusForbidden, "forbidden", "this account is not active")
			return
		}
		s.finishLogin(w, r, user, "", "", nil)

	default: // "sign" — the signing flow itself lands in Phase 4.
		WriteJSON(w, http.StatusOK, map[string]any{"verified": true})
	}
}

// --------------------------------------------------------- renter register --

func (s *Server) handleRegisterRenter(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	var body struct {
		Phone    string `json:"phone"`
		OTPToken string `json:"otp_token"`
		PIN      string `json:"pin"`
		FullName string `json:"full_name"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	phone := f.Phone("phone", body.Phone)
	f.Required("otp_token", body.OTPToken)
	f.PIN("pin", body.PIN)
	fullName := f.MaxLen("full_name", f.Required("full_name", body.FullName), 120)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	tokenPhone, err := s.store.ConsumeOTPToken(r.Context(), body.OTPToken)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid token",
			"the verification token is invalid or has expired", map[string]string{"otp_token": "invalid or expired"})
		return
	case err != nil:
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "verification unavailable",
			"registration cannot be completed right now")
		return
	}
	if tokenPhone != phone {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid token",
			"the verification token was issued for a different phone number",
			map[string]string{"otp_token": "does not match phone"})
		return
	}

	if _, err := s.q.GetUserByPhone(r.Context(), &phone); err == nil {
		httpx.WriteProblem(w, http.StatusConflict, "already registered", "an account already exists for this phone number")
		return
	} else if !isNoRows(err) {
		s.serverError(w, r, "register.lookup", err)
		return
	}

	pinHash, err := auth.HashSecret(body.PIN)
	if err != nil {
		s.serverError(w, r, "register.hash", err)
		return
	}

	var (
		user    sqlc.User
		token   string
		session auth.Session
	)
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		user, err = q.CreateUser(r.Context(), sqlc.CreateUserParams{
			Kind:     auth.KindRenter,
			Phone:    &phone,
			FullName: fullName,
			PinHash:  &pinHash,
		})
		if err != nil {
			return err
		}
		if _, err := q.UpsertRenterProfile(r.Context(), sqlc.UpsertRenterProfileParams{
			UserID:   user.ID,
			FullName: fullName,
			EncKey:   s.cfg.NidaEncKey,
		}); err != nil {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: db.UUIDString(user.ID),
			Action:      audit.ActionRegisterRenter,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(user.ID),
			After:       map[string]any{"phone": phone, "full_name": fullName},
		}); err != nil {
			return err
		}
		token, session, err = s.sessions.Issue(r.Context(), q, user.ID, auth.KindRenter, "", "",
			audit.RequestInfoFrom(r.Context()).IP, r.UserAgent())
		return err
	})
	if isUnique(err) {
		httpx.WriteProblem(w, http.StatusConflict, "already registered", "an account already exists for this phone number")
		return
	}
	if err != nil {
		s.serverError(w, r, "register.tx", err)
		return
	}

	s.sessions.Cache(r.Context(), session)
	s.sessions.SetCookie(w, auth.AudienceRenter, token)
	WriteJSON(w, http.StatusCreated, map[string]any{"user": toUser(user)})
}

// ------------------------------------------------------------------- login --

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	var body struct {
		Phone    string `json:"phone"`
		PIN      string `json:"pin"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	ip := audit.RequestInfoFrom(r.Context()).IP
	if res := s.limiter.Allow(r.Context(), "login:"+ip, loginLimit, loginWindow); !res.Allowed {
		tooMany(w, res, "too many sign-in attempts; try again shortly")
		return
	}

	f := validate.Fields{}
	usePhone := body.Phone != "" || body.PIN != ""
	useEmail := body.Email != "" || body.Password != ""
	if usePhone == useEmail {
		f.Add("phone", "provide either phone + pin, or email + password")
	}

	var (
		user   sqlc.User
		err    error
		secret string
		hash   string
		ident  string
	)
	switch {
	case usePhone && !useEmail:
		phone := f.Phone("phone", body.Phone)
		f.Required("pin", body.PIN)
		if !f.Empty() {
			badRequest(w, f)
			return
		}
		ident, secret = phone, body.PIN
		user, err = s.q.GetUserByPhone(r.Context(), &phone)
		hash = db.StrVal(user.PinHash)
	case useEmail && !usePhone:
		email := f.Email("email", body.Email)
		f.Required("password", body.Password)
		if !f.Empty() {
			badRequest(w, f)
			return
		}
		ident, secret = email, body.Password
		user, err = s.q.GetUserByEmail(r.Context(), email)
		hash = db.StrVal(user.PasswordHash)
	default:
		badRequest(w, f)
		return
	}

	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "login.lookup", err)
		return
	}
	ok := err == nil && user.Status == "active" && hash != "" && auth.VerifySecret(hash, secret)
	if !ok {
		s.recordFailedLogin(r.Context(), user, ident)
		httpx.WriteProblem(w, http.StatusUnauthorized, "invalid credentials", "the credentials provided are not valid")
		return
	}

	// Resolve org membership for org users.
	var (
		orgID   string
		role    string
		orgResp *sessionOrg
	)
	if user.Kind == auth.KindOrgUser {
		m, mErr := s.q.GetMembershipForUser(r.Context(), user.ID)
		if mErr != nil && !isNoRows(mErr) {
			s.serverError(w, r, "login.membership", mErr)
			return
		}
		if mErr == nil {
			orgID, role = db.UUIDString(m.OrgID), m.Role
			if org, oErr := s.q.GetOrg(r.Context(), m.OrgID); oErr == nil {
				orgResp = &sessionOrg{ID: db.UUIDString(org.ID), Name: org.Name, Slug: org.Slug, Role: role}
			}
		}
	}

	s.limiter.Reset(r.Context(), "login:"+ip, loginWindow)
	s.finishLogin(w, r, user, orgID, role, orgResp)
}

// finishLogin issues a session (with its audit row, in one transaction), sets
// the audience cookie and writes the `{user, org}` response.
func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, user sqlc.User, orgID, role string, org *sessionOrg) {
	var (
		token   string
		session auth.Session
	)
	err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		token, session, err = s.sessions.Issue(r.Context(), q, user.ID, user.Kind, orgID, role,
			audit.RequestInfoFrom(r.Context()).IP, r.UserAgent())
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       orgID,
			ActorUserID: db.UUIDString(user.ID),
			Action:      audit.ActionLogin,
			EntityType:  audit.EntitySession,
			EntityID:    db.UUIDString(user.ID),
			After:       map[string]any{"audience": auth.AudienceForKind(user.Kind)},
		})
	})
	if err != nil {
		s.serverError(w, r, "login.session", err)
		return
	}

	s.sessions.Cache(r.Context(), session)
	s.sessions.SetCookie(w, session.Audience, token)
	WriteJSON(w, http.StatusOK, map[string]any{"user": toUser(user), "org": org})
}

// recordFailedLogin writes an auth.login_failed audit row. The identifier is
// recorded (it is what an operator investigates); no secret ever is.
func (s *Server) recordFailedLogin(ctx context.Context, user sqlc.User, identifier string) {
	actor := ""
	if user.ID.Valid {
		actor = db.UUIDString(user.ID)
	}
	if err := s.inTx(ctx, func(q *sqlc.Queries) error {
		return audit.Record(ctx, q, audit.Entry{
			ActorUserID: actor,
			Action:      audit.ActionLoginFailed,
			EntityType:  audit.EntitySession,
			EntityID:    actor,
			After:       map[string]any{"identifier": identifier},
		})
	}); err != nil {
		s.logger.Error("failed to record failed login", "error", err)
	}
}

// ------------------------------------------------------------------ logout --

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	audience := audienceParam(r)
	if audience == "" {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "validation failed", "unknown audience",
			map[string]string{"audience": "must be one of: renter, org, admin"})
		return
	}
	token := auth.TokenFrom(r, audience)
	if token != "" {
		hash := auth.HashToken(token)
		revoked := false
		if p, ok := s.sessions.Resolve(r, audience); ok && s.q != nil {
			// The revocation and its audit row share one transaction: a
			// rolled-back revoke must leave no "logged out" trail, and a
			// committed one must always have it.
			if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
				if err := q.RevokeSession(r.Context(), hash); err != nil {
					return err
				}
				return audit.Record(r.Context(), q, audit.Entry{
					OrgID:       p.OrgIDString(),
					ActorUserID: p.UserIDString(),
					Action:      audit.ActionLogout,
					EntityType:  audit.EntitySession,
					EntityID:    p.UserIDString(),
				})
			}); err != nil {
				s.logger.Error("failed to record logout", "error", err)
			} else {
				revoked = true
				// Postgres is committed; drop the cached copy so Lookup
				// stops answering from Redis.
				s.sessions.EvictCached(r.Context(), []string{hash})
			}
		}
		if !revoked {
			s.sessions.Revoke(r.Context(), token)
		}
	}
	s.sessions.ClearCookie(w, audience)
	NoContent(w)
}

// ---------------------------------------------------------------------- me --

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	audience := audienceParam(r)
	if audience == "" {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "validation failed", "unknown audience",
			map[string]string{"audience": "must be one of: renter, org, admin"})
		return
	}
	p, ok := s.sessions.Resolve(r, audience)
	if !ok {
		httpx.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "a valid session is required")
		return
	}
	user, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "a valid session is required")
		return
	}

	var org *sessionOrg
	if p.OrgID.Valid {
		if o, oErr := s.q.GetOrg(r.Context(), p.OrgID); oErr == nil {
			org = &sessionOrg{ID: db.UUIDString(o.ID), Name: o.Name, Slug: o.Slug, Role: p.Role}
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"user": toUser(user), "org": org})
}

func audienceParam(r *http.Request) string {
	switch r.URL.Query().Get("audience") {
	case auth.AudienceRenter, "":
		return auth.AudienceRenter
	case auth.AudienceOrg:
		return auth.AudienceOrg
	case auth.AudienceAdmin:
		return auth.AudienceAdmin
	default:
		return ""
	}
}

// ------------------------------------------------------- email verification --

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	f.Required("token", body.Token)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	userID, err := s.store.ConsumeToken(r.Context(), auth.PrefixEmailVerify, body.Token)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid token",
			"the verification link is invalid or has expired", map[string]string{"token": "invalid or expired"})
		return
	case err != nil:
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "verification unavailable",
			"the link cannot be verified right now")
		return
	}

	uid, err := db.ParseUUID(userID)
	if err != nil {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid token", "the verification link is invalid",
			map[string]string{"token": "invalid"})
		return
	}

	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		user, err := q.MarkEmailVerified(r.Context(), uid)
		if err != nil {
			return err
		}
		orgID := ""
		if m, mErr := q.GetMembershipForUser(r.Context(), uid); mErr == nil {
			orgID = db.UUIDString(m.OrgID)
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       orgID,
			ActorUserID: db.UUIDString(user.ID),
			Action:      audit.ActionVerifyEmail,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(user.ID),
			After:       map[string]any{"email_verified": true},
		})
	})
	if err != nil {
		s.serverError(w, r, "verify_email", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"verified": true})
}

func (s *Server) handleVerifyEmailResend(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	user, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "verify_email.resend.user", err)
		return
	}
	if user.EmailVerifiedAt.Valid {
		WriteJSON(w, http.StatusAccepted, map[string]any{"sent": false, "reason": "already_verified"})
		return
	}
	if err := s.sendVerificationEmail(r.Context(), user); err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "email unavailable",
			"the verification email could not be issued right now")
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"sent": true})
}

// sendVerificationEmail mints a 24h token and hands the link to the email
// provider (dev: logged as `email_link`).
func (s *Server) sendVerificationEmail(ctx context.Context, user sqlc.User) error {
	if user.Email == nil {
		return errors.New("user has no email")
	}
	token, err := s.store.PutToken(ctx, auth.PrefixEmailVerify, db.UUIDString(user.ID), auth.EmailVerifyTTL)
	if err != nil {
		return err
	}
	link := fmt.Sprintf("%s/tenant/verify?token=%s", s.cfg.AppBaseURL, token)
	_, err = s.deps.Email.Send(ctx, *user.Email, "Verify your TMS email address", link,
		"Confirm your email address to finish setting up your TMS account.")
	return err
}

// ------------------------------------------------------------ invite accept --

func (s *Server) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	f.Required("token", body.Token)
	f.Password("password", body.Password)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	userID, err := s.store.ConsumeToken(r.Context(), auth.PrefixInvite, body.Token)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid token",
			"the invite link is invalid or has expired", map[string]string{"token": "invalid or expired"})
		return
	case err != nil:
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "invites unavailable",
			"the invite cannot be accepted right now")
		return
	}
	uid, err := db.ParseUUID(userID)
	if err != nil {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid token", "the invite link is invalid",
			map[string]string{"token": "invalid"})
		return
	}

	hash, err := auth.HashSecret(body.Password)
	if err != nil {
		s.serverError(w, r, "invite.hash", err)
		return
	}

	var user sqlc.User
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if err := q.SetUserPassword(r.Context(), sqlc.SetUserPasswordParams{ID: uid, PasswordHash: &hash}); err != nil {
			return err
		}
		user, err = q.MarkEmailVerified(r.Context(), uid)
		if err != nil {
			return err
		}
		orgID := ""
		if m, mErr := q.GetMembershipForUser(r.Context(), uid); mErr == nil {
			orgID = db.UUIDString(m.OrgID)
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       orgID,
			ActorUserID: db.UUIDString(uid),
			Action:      audit.ActionInviteAccept,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(uid),
			After:       map[string]any{"password_set": true, "email_verified": true},
		})
	}); err != nil {
		s.serverError(w, r, "invite.tx", err)
		return
	}

	var (
		orgID   string
		role    string
		orgResp *sessionOrg
	)
	if m, mErr := s.q.GetMembershipForUser(r.Context(), uid); mErr == nil {
		orgID, role = db.UUIDString(m.OrgID), m.Role
		if o, oErr := s.q.GetOrg(r.Context(), m.OrgID); oErr == nil {
			orgResp = &sessionOrg{ID: db.UUIDString(o.ID), Name: o.Name, Slug: o.Slug, Role: role}
		}
	}
	s.finishLogin(w, r, user, orgID, role, orgResp)
}
