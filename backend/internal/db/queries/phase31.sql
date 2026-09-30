-- Phase 31: contract changes need an owner's approval (maker-checker).
--
-- An amendment is a contract row in status `draft` until an owner approves it;
-- `amendment_stage` carries where it is in review. Every transition names the
-- stage it leaves, so a double click or a race answers with no row.

-- UpdateAmendmentDraft rewrites everything a draft's document says, from a
-- fresh render. Only a draft may change; a submitted one only when the caller
-- allows it (an owner editing before approval).
-- name: UpdateAmendmentDraft :one
UPDATE contracts SET
    template_id = sqlc.arg(template_id),
    terms_snapshot_html = sqlc.arg(terms_snapshot_html),
    rent_amount = sqlc.arg(rent_amount),
    rent_period_days = sqlc.arg(rent_period_days),
    payment_period_id = sqlc.arg(payment_period_id),
    payment_period_days = sqlc.arg(payment_period_days),
    payment_period_months = sqlc.narg(payment_period_months),
    term_days = sqlc.arg(term_days),
    start_date = sqlc.arg(start_date),
    end_date = sqlc.arg(end_date),
    due_day = sqlc.narg(due_day),
    snapshot_hash = sqlc.arg(snapshot_hash),
    language = sqlc.arg(language),
    policy = sqlc.narg(policy),
    amendment_effective_date = sqlc.arg(amendment_effective_date),
    amendment_reason = sqlc.arg(amendment_reason),
    amendment_note_sw = sqlc.arg(amendment_note_sw),
    amendment_note_en = sqlc.arg(amendment_note_en),
    amendment_body_html = sqlc.narg(amendment_body_html)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'draft' AND deleted_at IS NULL
  AND amendment_stage = ANY(sqlc.arg(stages)::text[])
RETURNING *;

-- name: SubmitAmendment :one
UPDATE contracts
SET amendment_stage = 'submitted', amendment_submitted_by = sqlc.arg(actor),
    amendment_submitted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'draft' AND amendment_stage = 'draft' AND deleted_at IS NULL
RETURNING *;

-- ApproveAmendment hands the draft to the renter.
-- name: ApproveAmendment :one
UPDATE contracts
SET status = 'pending_signature', amendment_stage = 'approved',
    amendment_reviewed_by = sqlc.arg(actor), amendment_reviewed_at = now(),
    amendment_review_note = NULL
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'draft' AND amendment_stage = sqlc.arg(from_stage) AND deleted_at IS NULL
RETURNING *;

-- ReturnAmendment sends a submitted draft back to be edited.
-- name: ReturnAmendment :one
UPDATE contracts
SET amendment_stage = 'draft', amendment_reviewed_by = sqlc.arg(actor),
    amendment_reviewed_at = now(), amendment_review_note = sqlc.arg(note)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'draft' AND amendment_stage = 'submitted' AND deleted_at IS NULL
RETURNING *;

-- CloseAmendmentDraft ends a draft that never reached the renter: rejected by
-- an owner, or withdrawn by the org.
-- name: CloseAmendmentDraft :one
UPDATE contracts
SET status = 'terminated', terminated_at = now(), amendment_stage = sqlc.arg(to_stage),
    termination_reason = sqlc.arg(reason),
    amendment_reviewed_by = CASE WHEN sqlc.arg(to_stage)::text = 'rejected'
                                 THEN sqlc.arg(actor)::uuid ELSE amendment_reviewed_by END,
    amendment_reviewed_at = CASE WHEN sqlc.arg(to_stage)::text = 'rejected'
                                 THEN now() ELSE amendment_reviewed_at END,
    amendment_review_note = CASE WHEN sqlc.arg(to_stage)::text = 'rejected'
                                 THEN sqlc.arg(reason) ELSE amendment_review_note END
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND status = 'draft' AND amendment_stage = ANY(sqlc.arg(from_stages)::text[])
  AND deleted_at IS NULL
RETURNING *;

-- DeclineAmendment is the renter's no to an approved amendment they have not
-- signed. The contract it would have replaced is not touched.
-- name: DeclineAmendment :one
UPDATE contracts
SET status = 'terminated', terminated_at = now(), amendment_stage = 'declined',
    amendment_declined_at = now(), amendment_decline_reason = sqlc.arg(reason),
    termination_reason = sqlc.arg(reason)
WHERE contracts.id = sqlc.arg(id) AND contracts.renter_user_id = sqlc.arg(renter_user_id)
  AND contracts.status = 'pending_signature' AND contracts.amendment_stage = 'approved'
  AND contracts.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM contract_signatures s
      WHERE s.contract_id = contracts.id AND s.org_id = contracts.org_id
  )
RETURNING *;
