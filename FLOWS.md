# FLOWS.md — TMS User Flows

Companion to [SPEC.md](SPEC.md). Actors: **Renter** (`apps/enduser`), **Landlord user** (`apps/tenant`), **Platform admin** (`apps/admin`), **System** (scheduler/jobs).

---

## 1. Landlord onboarding

1. Landlord opens signup link → registers org: business name, owner name, email, phone, password.
2. Email verification link → verified.
3. Guided setup wizard:
   1. **Branding** — display name (e.g. "JJnE Rentals"), logo upload, optional letterhead upload + document footer text (used on contract documents), theme color.
   2. **First property** — name (custom, e.g. "Mbezi Beach Block A"), location.
   3. **Payment periods** — list pre-seeded with recommended presets (Monthly 30d, Quarterly 90d, Half-year 180d, Yearly 365d, badged "Recommended"). Landlord keeps/removes any, and adds custom periods as a label + number of days (e.g. "Weekly" 7d, "3 weeks" 21d, "45 days") — no limit on count or value.
   4. **Units** — add units with custom names ("Room 1", "House B"), set price (amount per N days, default 30) and optionally restrict which payment periods this unit offers.
   5. **Contract template** — start from default terms, edit; set default due day and grace days.
   6. **Notification settings** — confirm reminder timings, SMS language (SW/EN), sender name.
4. Wizard ends on dashboard; empty-state cards prompt "Print QR codes" and "Invite staff".
5. Optional: invite `org_manager` staff by email.

**Edge cases:** duplicate org email → resend verification; abandoning wizard → resumable, dashboard shows setup checklist.

---

## 2. Renter onboarding (QR-first)

1. Renter scans QR sticker on the house/unit → opens `/enduser/u/{unit_code}`.
2. Landing shows **org branding** + property/unit name, rent price, terms summary. CTA: "Register to connect" (or "Log in").
3. Register: phone number → OTP SMS (Beem) → verify → set PIN.
4. KYC form: full name, **NIDA number**, next of kin (name + phone), contact number (prefilled), email. Optional ID photo upload.
5. Choose **payment period** from the landlord's offered list for this unit (recommended presets first, then custom ones like "21 days"), each showing the prorated amount. Choose **tenancy length** (term) and **start date** — end date auto-derived, shown, with the resulting schedule preview (N payments of X).
6. Review terms (from landlord's template) → accept → **link request** submitted.
   (Signing happens after approval — step 7b — so the renter signs the final document with the landlord-confirmed dates.)
7. Status screen: "Waiting for landlord approval" (skipped if org auto-approve on).
7b. On approval, SMS: "Your contract for {unit} is ready to sign." → renter opens the contract document (letterhead, full terms, schedule) → **Accept & sign**: OTP sent to registered phone → enter code → optional draw signature → signed. Status: "Waiting for landlord to countersign."
8. Landlord activates (countersigns) → SMS: "Welcome to {org}. Your tenancy at {unit} starts {date}." → renter dashboard live; signed contract available to print anytime.

**Alternate entries:** landlord manually adds renter (sends SMS invite link with the same flow, unit pre-linked); renter with existing account scans a new QR → jumps straight to step 5.

**Edge cases:** QR of occupied unit → show "unit occupied — contact landlord" unless landlord enabled waitlist; wrong unit scanned → renter cancels request; OTP retries rate-limited with resend cooldown.

---

## 3. Link approval & contract activation (landlord)

1. Dashboard badge: pending link requests.
2. Open request → renter KYC details, chosen duration, start date.
3. Approve → contract created from template (terms + price **snapshotted**, hash computed), status `pending_signature`, renter SMS'd to sign.
4. Reject (with reason) → renter notified by SMS.
5. Renter signs (OTP + optional drawn signature) → landlord sees "Ready to countersign" → **Activate** records the landlord signature, generates payment schedules for the whole span, unit → occupied.
6. Contract visible to both parties as an in-app document (org letterhead + logo, resolved terms, parties, schedule summary, signature block with names/timestamps/phone last-4/drawn signatures, verification hash). "Print / Save as PDF" uses the browser. Landlord can also **manually add a renter** and sign on their behalf only if the renter has no phone — flagged as `landlord_recorded` in the audit log (no renter signature row); avoid where possible.

**Edge cases:** renter doesn't sign within N days (org setting, default 7) → reminder SMS, then landlord can cancel; renter disputes → landlord terminates and issues a new contract (old one kept, never edited).

---

## 4. Vacancy management (landlord)

1. Units board filterable by status: **vacant / occupied / maintenance / unlisted**.
2. Contract ends or is terminated → unit auto-flips to vacant → appears on vacancy board.
3. Landlord can override status (e.g. maintenance) and reorder/rename units anytime — names are custom for quick reference.
4. Vacant unit's QR keeps working — next renter scans the same code.
5. Report tile: occupancy rate, vacant unit list with days-vacant.

---

## 5. Pricing management (landlord)

1. Unit page → "Prices" → current price (amount per N days) + full history.
2. New price with `effective_from` date → applies to **future contracts only**; active contracts keep their snapshotted rent.
2a. Org settings → "Payment periods": manage the list (add custom days, rename, reorder, deactivate; recommended presets restorable). Changes affect future contracts only.
3. Bulk price update across selected units (same %, or set amount).
4. Audit log records every price change (who, when, old → new).

---

## 6. Contracts & terms management (landlord)

1. Templates page: create/edit named templates in an in-app rich-text editor (headings, lists, bold, variables like `{{renter_name}}`, `{{rent}}`), set default. Live preview shows the document with the org's uploaded letterhead/logo.
2. Editing a template never changes active contracts (snapshot rule) — banner states this.
3. Contract lifecycle: `draft → pending_signature → active → expiring → ended | terminated`.
4. ~30 days before end date contract flags **expiring**; landlord prompted to renew (new contract, current price, renter confirms via SMS link) or let lapse.
5. Early termination: landlord terminates with reason; remaining schedules cancelled/waived; unit → vacant; SMS to renter.

---

## 7. Payment collection — MVP offline flow

**Renter:**
1. Dashboard shows: **next payment due** (date + amount), status chip (paid / pending / overdue), payment history.
2. Renter pays outside the system (cash / bank transfer / mobile money) to the org's **bank collection account** shown on the payment screen with reference instructions.

**Landlord:**
1. Money arrives → landlord opens renter or schedule → **Record payment**: amount, method, reference no., date, note.
2. Full amount → schedule `paid`; underpayment → `partial` (remainder tracked); overpayment → applied to next schedule (confirm prompt).
3. System sends **thank-you SMS** with next due date.
4. Mistake → **reverse payment** (audited), schedule reverts.

**System:**
- Due date passes unpaid → schedule `overdue`; overdue SMS daily until resolved.
- All movements appear in reports and payment history immediately.

**Post-MVP:** step 2 replaced by **Scan to Pay** — renter scans pay-QR, gateway intent, webhook auto-confirms. Same schedule/payment records; only the entry method changes.

---

## 8. Notifications timeline (system)

For each payment schedule:

```
due-7d ──────── due date ─────── overdue ───────────── paid
   │               │                │                    │
reminder SMS   reminder SMS    daily overdue SMS    thank-you SMS
"due in 1wk"   "due today"     until resolved       + next due date
```

- All sends deduped per schedule per day; logged in notification log (landlord can view delivery status).
- Landlord can also send **custom bulk SMS** to all/selected renters (e.g. water outage notice) — permission-gated, audited.

---

## 9. Reports & dashboard (landlord)

Dashboard (layout per org's saved **dashboard preferences**):

- **Total assets:** properties count, units count, occupancy %.
- **Total renters** (active contracts).
- **Payment status per renter:** Paid / Pending (within cycle) / Overdue — filterable table, CSV export.
- **Collections:** expected vs collected this period, trend chart.
- Drill-down: renter → contract → schedules → payment history (full retained record).

---

## 10. Audit trail (landlord + admin)

- Org-scoped audit page: filter by actor, entity, date range.
- Every action recorded: logins, KYC edits, price changes, contract events, payments recorded/reversed, SMS sends, settings changes.
- Append-only; platform admin sees cross-org for support/disputes.

---

## 11. Platform admin

1. Admin logs into `apps/admin`.
2. Org list: activate / suspend orgs, view platform metrics (orgs, renters, SMS volume, failed sends).
3. Cross-org audit search for support cases.

---

## 12. Client-requirement traceability

| Client requirement | Covered by |
|---|---|
| Register tenants/users + KYC (names, NIDA, next of kin, contact, email) | Flow 2 |
| Payment duration 1/3/6/12 months (recommended) + landlord custom periods in days (7, 21, 45…) | Flow 1 step 3, Flow 2 step 5, Flow 5 |
| Select start/end dates | Flow 2 step 5 |
| Choose payment method (Scan to Pay) | Flow 7 (offline MVP; gateway post-MVP) |
| Make payment | Flow 7 |
| Reminder 1wk / due day / daily overdue / thank-you + next due | Flow 8 |
| Reports: assets, tenant count, per-tenant status, history | Flow 9 |
| Audit trail for all actions | Flow 10 |
| Payments to bank collection account | Flow 7 |
| Lavatory service (Haja Ndogo/Kubwa/Kuoga, 30/70 commission) | **Out of scope** (confirmed) |
| Landlord customization: names, pricing, terms, due dates, theme, logo, dashboard | Flows 1, 4, 5, 6, 9 |
| Bulk SMS via Beem | Flow 8 |
| QR-first tenant access | Flow 2 |
