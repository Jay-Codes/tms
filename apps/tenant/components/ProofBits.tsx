'use client';

/**
 * Proof of payment — the landlord's side (PLAN2 §16.1, FLOWS 7).
 *
 * A proof is a *claim*: the renter says money left their hands and attaches
 * the evidence. Nothing here moves a shilling. The queue puts the claim beside
 * what the schedule expects and offers two answers — Accept, which hands the
 * claim to the ordinary Record-payment sheet (so the allocator, the overpay
 * confirm and every validation error are the Phase 5 ones, unchanged), and
 * Reject, which needs a reason because the renter is told it by SMS.
 *
 * Deliberately no thumbnails in the list: a `view_url` is minted only by
 * `GET /proofs/{id}` and issuing one is audited `proof.view`, so drawing a
 * page of rows would write a page of audit entries for files nobody opened.
 * The list shows the file's kind and weight; the sheet shows the file.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Field, ProblemNote } from './FormBits';
import { refreshNavBadges } from './NavBadges';
import { methodText } from './PaymentBits';
import { RecordPaymentSheet, type RecordPaymentTarget } from './RecordPaymentSheet';
import { Sheet } from './Sheet';
import {
  ApiError,
  PROOF_STATUSES,
  PROOF_VIEW_TTL_MS,
  contractsApi,
  isPendingProof,
  paymentsApi,
  proofsApi,
  remainingOn,
  toApiError,
  unwrapPayment,
  type PaymentInput,
  type PaymentMethod,
  type Payment,
  type Proof,
  type ProofStatus,
  type ScheduleRow,
} from '../lib/api';
import { ageParts, fmtDate, fmtDateTime, fmtTZS } from '../lib/format';
import { useT, type Translator } from '@tms/ui';

/* --------------------------------- marks --------------------------------- */

/**
 * Accepted is a fact and is stamped; rejected is a fact too, and carries the
 * overdue ink because it is the answer that leaves money still owing.
 * Submitted has not happened yet, so it is only pencilled (SPEC §2.0).
 */
export function ProofStatusStamp({ status }: { status: string }) {
  const t = useT();
  if (status === 'accepted') return <span className="stamp stamp-paid">{t('proofs.status.accepted')}</span>;
  if (status === 'rejected') return <span className="stamp stamp-overdue">{t('proofs.status.rejected')}</span>;
  return <span className="pencil">{t('proofs.status.submitted')}</span>;
}

/** "2 h ago" — how long the claim has been waiting. */
export function ProofAge({ at }: { at: string | null | undefined }) {
  const t = useT();
  const parts = ageParts(at);
  if (!parts) return null;
  const text =
    parts.unit === 'just_now' ? t('proofs.age.just_now') : t.n(`proofs.age.${parts.unit}`, parts.count);
  return (
    <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)', whiteSpace: 'nowrap' }}>{text}</span>
  );
}

/** The file, described rather than drawn (see the note at the top of the file). */
function FileMark({ proof }: { proof: Proof }) {
  const t = useT();
  const pdf = proof.content_type === 'application/pdf';
  const kb = Math.max(1, Math.round((proof.size_bytes ?? 0) / 1024));
  return (
    <span
      style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-xs)' }}
    >
      <Icon icon={pdf ? 'solar:file-text-linear' : 'solar:gallery-linear'} width={18} />
      {pdf ? t('proofs.file.pdf') : t('proofs.file.image')} · {kb} KB
    </span>
  );
}

/* -------------------------- the schedule it claims ------------------------ */

/**
 * Contract ledgers, read once per contract and kept for the session. A proof
 * names a schedule id but not what that schedule expects, and the landlord
 * cannot judge the claim without the second figure — so the existing
 * `GET /contracts/{id}/schedules` fills it in, one call per distinct contract
 * on screen rather than one per row.
 */
const scheduleCache = new Map<string, ScheduleRow[]>();

function useScheduleIndex(items: Proof[] | null): Map<string, ScheduleRow> {
  const [tick, bump] = useState(0);

  const wanted = useMemo(() => {
    const ids = new Set<string>();
    for (const p of items ?? []) {
      if (p.schedule_id && p.contract?.id && !scheduleCache.has(p.contract.id)) ids.add(p.contract.id);
    }
    return [...ids].sort().join(',');
  }, [items]);

  useEffect(() => {
    if (!wanted) return;
    const ac = new AbortController();
    let live = true;
    void Promise.all(
      wanted.split(',').map((id) =>
        contractsApi
          .schedules(id, ac.signal)
          .then((r) => {
            scheduleCache.set(id, (r.items ?? []) as ScheduleRow[]);
          })
          .catch((e) => {
            // An abort must not poison the cache — the next render retries.
            if (!(e instanceof DOMException)) scheduleCache.set(id, []);
          }),
      ),
    ).then(() => {
      if (live) bump((n) => n + 1);
    });
    return () => {
      live = false;
      ac.abort();
    };
  }, [wanted]);

  return useMemo(() => {
    // `tick` is read, not used: the cache is mutated outside React, and the
    // counter is the only thing that tells this memo the rows have arrived.
    void tick;
    const out = new Map<string, ScheduleRow>();
    for (const p of items ?? []) {
      if (!p.schedule_id) continue;
      const row = (scheduleCache.get(p.contract?.id ?? '') ?? []).find((s) => s.id === p.schedule_id);
      if (row) out.set(p.schedule_id, row);
    }
    return out;
  }, [items, tick]);
}

/* ---------------------------------- list ---------------------------------- */

function whatIsOwed(t: Translator, proof: Proof, schedule: ScheduleRow | undefined): string {
  if (!proof.schedule_id) return t('proofs.unassigned');
  if (!schedule) return t('common.loading');
  return t('proofs.expects', { amount: fmtTZS(remainingOn(schedule)) });
}

/**
 * One claim as a card. Cards rather than a ledger because a proof is read, not
 * scanned: five short facts and one decision, which is the shape that survives
 * a 375px screen without a sideways scroll (Phase 15).
 */
function ProofCard({
  proof,
  schedule,
  onOpen,
}: {
  proof: Proof;
  schedule: ScheduleRow | undefined;
  onOpen: () => void;
}) {
  const t = useT();
  return (
    <button
      type="button"
      onClick={onOpen}
      style={{
        display: 'grid',
        gap: 'var(--sp-2)',
        width: '100%',
        textAlign: 'left',
        font: 'inherit',
        color: 'var(--ink)',
        background: 'transparent',
        border: '1px solid var(--rule)',
        borderRadius: 'var(--radius-md)',
        padding: 'var(--sp-4)',
        cursor: 'pointer',
        minHeight: 'var(--touch-min)',
      }}
    >
      <span style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 'var(--sp-3)' }}>
        <strong style={{ minWidth: 0, overflowWrap: 'anywhere' }}>{proof.contract?.renter_name ?? '—'}</strong>
        <ProofAge at={proof.created_at} />
      </span>

      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        {proof.contract?.unit_name ?? '—'} · {proof.contract?.property_name ?? ''}
      </span>

      <span style={{ display: 'flex', alignItems: 'baseline', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
        <strong className="num" style={{ fontSize: 'var(--text-lg)' }}>{fmtTZS(proof.amount)}</strong>
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {whatIsOwed(t, proof, schedule)}
        </span>
      </span>

      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        {t('proofs.paid_on', { date: fmtDate(proof.paid_at) })} · {methodText(t, proof.method)}
      </span>

      <span style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
        <FileMark proof={proof} />
        {isPendingProof(proof) ? (
          <span style={{ color: 'var(--primary)', fontSize: 'var(--text-sm)', fontWeight: 600 }}>
            {t('proofs.review')}
          </span>
        ) : (
          <ProofStatusStamp status={String(proof.status)} />
        )}
      </span>
    </button>
  );
}

export function ProofList({
  items,
  emptyText,
  onOpen,
}: {
  items: Proof[] | null;
  emptyText?: string;
  onOpen: (p: Proof) => void;
}) {
  const t = useT();
  const index = useScheduleIndex(items);

  if (items === null) return <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>;
  if (items.length === 0)
    return <p style={{ color: 'var(--ink-soft)' }}>{emptyText ?? t('proofs.empty.submitted')}</p>;

  return (
    <ul
      aria-label={t('proofs.list_label')}
      style={{
        listStyle: 'none',
        margin: 0,
        padding: 0,
        display: 'grid',
        gap: 'var(--sp-3)',
        gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))',
      }}
    >
      {items.map((p) => (
        <li key={p.id} style={{ minWidth: 0 }}>
          <ProofCard
            proof={p}
            schedule={p.schedule_id ? index.get(p.schedule_id) : undefined}
            onOpen={() => onOpen(p)}
          />
        </li>
      ))}
    </ul>
  );
}

/* --------------------------------- viewer -------------------------------- */

/**
 * The evidence. The presigned link lives 300 s, so it is re-asked for a little
 * before it dies and again whenever the browser tells us the image failed —
 * an expired link must never leave the landlord staring at a broken box.
 */
function ProofViewer({ proof, onRefresh }: { proof: Proof; onRefresh: () => void }) {
  const t = useT();
  const [zoom, setZoom] = useState(false);
  const pdf = proof.content_type === 'application/pdf';
  const url = proof.view_url ?? null;

  if (!url) {
    return <p style={{ color: 'var(--ink-soft)' }}>{t('proofs.viewer.opening')}</p>;
  }

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
      {pdf ? (
        <iframe
          src={url}
          title={t('proofs.viewer.pdf_title')}
          style={{ width: '100%', height: '55vh', border: '1px solid var(--rule)', borderRadius: 'var(--radius-sm)', background: 'var(--paper)' }}
        />
      ) : (
        <div style={{ overflow: 'auto', maxHeight: '55vh', border: '1px solid var(--rule)', borderRadius: 'var(--radius-sm)' }}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={url}
            alt={t('proofs.viewer.alt', { name: proof.contract?.renter_name ?? '' })}
            onClick={() => setZoom((z) => !z)}
            onError={onRefresh}
            style={
              zoom
                ? { display: 'block', maxWidth: 'none', cursor: 'zoom-out' }
                : { display: 'block', width: '100%', height: 'auto', cursor: 'zoom-in' }
            }
          />
        </div>
      )}
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center' }}>
        <a className="btn btn-quiet" href={url} target="_blank" rel="noopener noreferrer">
          <Icon icon="solar:square-top-down-linear" width={18} /> {t('proofs.viewer.open')}
        </a>
        <button type="button" className="btn btn-quiet" onClick={onRefresh}>
          <Icon icon="solar:refresh-linear" width={18} /> {t('proofs.viewer.refresh')}
        </button>
        <span style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>
          {pdf ? t('proofs.viewer.link_expires') : t('proofs.viewer.zoom_hint')}
        </span>
      </div>
    </div>
  );
}

/* ------------------------------ claim vs ledger --------------------------- */

function Cell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'grid', gap: 2, padding: 'var(--sp-2) 0', borderBottom: '1px solid var(--rule)', minWidth: 0 }}>
      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{label}</span>
      <span className="facts" style={{ overflowWrap: 'anywhere' }}>{children}</span>
    </div>
  );
}

function ClaimVsSchedule({ proof, schedule }: { proof: Proof; schedule: ScheduleRow | null }) {
  const t = useT();
  return (
    <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
      <div>
        <h3 style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', margin: '0 0 var(--sp-2)' }}>
          {t('proofs.claimed')}
        </h3>
        <Cell label={t('common.amount')}>
          <span className="num">{fmtTZS(proof.amount)}</span>
        </Cell>
        <Cell label={t('payments.col.paid_at')}>{fmtDateTime(proof.paid_at)}</Cell>
        <Cell label={t('payments.record.reference_label')}>
          <span className="num">{proof.reference || '—'}</span>
        </Cell>
        <Cell label={t('payments.col.method')}>{methodText(t, proof.method)}</Cell>
      </div>
      <div>
        <h3 style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', margin: '0 0 var(--sp-2)' }}>
          {t('proofs.expected')}
        </h3>
        {schedule ? (
          <>
            <Cell label={t('common.amount')}>
              <span className="num">{fmtTZS(remainingOn(schedule))}</span>
            </Cell>
            <Cell label={t('payments.col.due')}>{fmtDate(schedule.due_date)}</Cell>
            <Cell label={t('common.status')}>
              {t(`payments.schedule_status.${schedule.status}`)}
            </Cell>
            <Cell label={t('payments.col.paid')}>
              <span className="num">{fmtTZS(schedule.paid_amount)}</span>
            </Cell>
          </>
        ) : (
          <p className="pencil" style={{ margin: 0 }}>
            {proof.schedule_id ? t('common.loading') : t('proofs.unassigned_hint')}
          </p>
        )}
      </div>
    </div>
  );
}

/* --------------------------------- review --------------------------------- */

function RejectForm({
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  busy: boolean;
  error: ApiError | null;
  onSubmit: (reason: string) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [reason, setReason] = useState('');
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(reason.trim());
      }}
      style={{ display: 'grid', gap: 'var(--sp-4)' }}
      noValidate
    >
      <ProblemNote error={error} />
      <p
        style={{
          display: 'flex',
          gap: 'var(--sp-3)',
          padding: 'var(--sp-3) var(--sp-4)',
          border: '1px solid var(--rule-strong)',
          borderRadius: 'var(--radius-sm)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:danger-triangle-linear" width={20} />
        <span>{t('proofs.reject.warning')}</span>
      </p>
      <Field
        id="proof_reason"
        label={t('proofs.reject.reason_label')}
        hint={`${reason.length}/200`}
        error={error?.errors.reason}
      >
        <textarea
          id="proof_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('proofs.reject.reason_placeholder')}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? t('proofs.reject.busy') : t('proofs.reject.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/**
 * One claim, opened. Owns both sheets: the detail itself, and the ordinary
 * Record-payment sheet that Accept hands over to — the second one submits
 * `POST /proofs/{id}/accept` instead of `POST /payments`, which is the whole
 * of the difference (API.md §16.1: the accept route takes the payment body and
 * answers with the same `{payment, schedules}`, 409s included).
 */
export function ProofReview({
  proofId,
  onClose,
  onChanged,
}: {
  proofId: string | null;
  onClose: () => void;
  /** Fired after an accept or a reject, so the caller reloads its list. */
  onChanged: () => void;
}) {
  const t = useT();
  const [proof, setProof] = useState<Proof | null>(null);
  const [schedule, setSchedule] = useState<ScheduleRow | null>(null);
  const [payment, setPayment] = useState<Payment | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [rejecting, setRejecting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [accepting, setAccepting] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      if (!proofId) return;
      try {
        const r = await proofsApi.get(proofId, signal);
        if (signal?.aborted) return;
        setProof(r.proof);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
    },
    [proofId],
  );

  // Open, then keep the presigned link alive while the sheet is up.
  useEffect(() => {
    setProof(null);
    setSchedule(null);
    setPayment(null);
    setError(null);
    setRejecting(false);
    setAccepting(false);
    if (!proofId) return;
    const ac = new AbortController();
    void load(ac.signal);
    const timer = window.setInterval(() => void load(), Math.max(30_000, PROOF_VIEW_TTL_MS - 30_000));
    return () => {
      window.clearInterval(timer);
      ac.abort();
    };
  }, [proofId, load]);

  // The instalment the claim names, for the side-by-side.
  const contractId = proof?.contract?.id ?? '';
  const scheduleId = proof?.schedule_id ?? '';
  useEffect(() => {
    setSchedule(null);
    if (!contractId || !scheduleId) return;
    const ac = new AbortController();
    contractsApi
      .schedules(contractId, ac.signal)
      .then((r) => {
        const rows = (r.items ?? []) as ScheduleRow[];
        scheduleCache.set(contractId, rows);
        setSchedule(rows.find((s) => s.id === scheduleId) ?? null);
      })
      .catch(() => undefined);
    return () => ac.abort();
  }, [contractId, scheduleId]);

  // An accepted proof points at a payment; the payment is where a later
  // reversal shows up (the proof stays accepted — API.md §16.1).
  const paymentId = proof?.payment_id ?? '';
  useEffect(() => {
    setPayment(null);
    if (!paymentId) return;
    const ac = new AbortController();
    paymentsApi
      .get(paymentId, ac.signal)
      .then((r) => setPayment(unwrapPayment(r)))
      .catch(() => undefined);
    return () => ac.abort();
  }, [paymentId]);

  const reject = async (reason: string) => {
    if (!proof) return;
    setBusy(true);
    setError(null);
    try {
      const r = await proofsApi.reject(proof.id, reason);
      setProof(r.proof);
      setRejecting(false);
      refreshNavBadges();
      onChanged();
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  /**
   * Accept, expressed as a submit function the Record-payment sheet can call.
   * The sheet builds the payment body; only `amount`, `schedule_id` and
   * `paid_at` are corrections the accept route acts on, so the rest rides
   * along and is ignored by the backend.
   */
  const acceptTarget: RecordPaymentTarget | null = proof
    ? {
        contractId: proof.contract?.id ?? '',
        scheduleId: proof.schedule_id ?? undefined,
        label: `${proof.contract?.unit_name ?? ''} · ${proof.contract?.property_name ?? ''} — ${proof.contract?.renter_name ?? ''}`,
        amount: proof.amount,
        method: proof.method as PaymentMethod,
        paidAt: proof.paid_at,
        reference: proof.reference ?? '',
        note: proof.note ?? '',
        readOnlyFields: ['method', 'reference', 'note'],
        readOnlyHint: t('proofs.accept.locked_hint'),
        title: t('proofs.accept.title'),
        submitLabel: t('proofs.accept.submit'),
        submit: (body: PaymentInput) =>
          proofsApi.accept(proof.id, {
            amount: body.amount,
            schedule_id: body.schedule_id,
            paid_at: body.paid_at,
            allow_overpay_rollover: body.allow_overpay_rollover,
          }),
      }
    : null;

  if (accepting && acceptTarget) {
    return (
      <RecordPaymentSheet
        open
        target={acceptTarget}
        onClose={() => {
          setAccepting(false);
          onClose();
        }}
        onRecorded={() => {
          refreshNavBadges();
          onChanged();
        }}
      />
    );
  }

  return (
    <Sheet open={proofId !== null} title={t('proofs.detail.title')} onClose={onClose} width={720}>
      <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        {!proof ? (
          <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
        ) : rejecting ? (
          <RejectForm busy={busy} error={error} onSubmit={(r) => void reject(r)} onCancel={() => setRejecting(false)} />
        ) : (
          <>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-3)', alignItems: 'center' }}>
              <ProofStatusStamp status={String(proof.status)} />
              <ProofAge at={proof.created_at} />
              <FileMark proof={proof} />
            </div>

            <p style={{ margin: 0, color: 'var(--ink-soft)' }}>
              {proof.contract?.renter_user_id ? (
                <Link href={`/renters/${proof.contract.renter_user_id}`} style={{ color: 'var(--primary)' }}>
                  {proof.contract.renter_name}
                </Link>
              ) : (
                (proof.contract?.renter_name ?? '—')
              )}
              {' · '}
              {proof.contract?.id ? (
                <Link href={`/contracts/${proof.contract.id}`} style={{ color: 'var(--primary)' }}>
                  {proof.contract.unit_name} · {proof.contract.property_name}
                </Link>
              ) : null}
            </p>

            <ProofViewer proof={proof} onRefresh={() => void load()} />

            <ClaimVsSchedule proof={proof} schedule={schedule} />

            {proof.note ? (
              <Cell label={t('common.note')}>{proof.note}</Cell>
            ) : null}

            {proof.status === 'rejected' ? (
              <Cell label={t('proofs.rejected_because')}>
                {proof.rejection_reason ?? '—'}
                <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                  {t('proofs.reviewed_by', {
                    name: proof.reviewed_by_name ?? '—',
                    when: fmtDateTime(proof.reviewed_at),
                  })}
                </div>
              </Cell>
            ) : null}

            {proof.status === 'accepted' ? (
              <Cell label={t('proofs.accepted_payment')}>
                {payment ? (
                  <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: 'var(--sp-2)', alignItems: 'center' }}>
                    <span
                      className="num"
                      style={payment.status === 'reversed' ? { textDecoration: 'line-through' } : undefined}
                    >
                      {fmtTZS(payment.amount)}
                    </span>
                    <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                      {fmtDateTime(payment.paid_at)}
                    </span>
                    {payment.status === 'reversed' ? (
                      <span className="stamp stamp-overdue">{t('payments.status.reversed')}</span>
                    ) : (
                      <span className="stamp stamp-paid">{t('payments.status.recorded')}</span>
                    )}
                    <Link href={`/contracts/${proof.contract?.id ?? ''}`} style={{ color: 'var(--primary)' }}>
                      {t('proofs.open_payment')}
                    </Link>
                  </span>
                ) : (
                  <span className="pencil">{t('common.loading')}</span>
                )}
                <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                  {t('proofs.reviewed_by', {
                    name: proof.reviewed_by_name ?? '—',
                    when: fmtDateTime(proof.reviewed_at),
                  })}
                </div>
              </Cell>
            ) : null}

            <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
              {isPendingProof(proof) ? (
                <>
                  <button type="button" className="btn btn-primary" onClick={() => setAccepting(true)}>
                    {t('proofs.accept.action')}
                  </button>
                  <button type="button" className="btn btn-danger" onClick={() => setRejecting(true)}>
                    {t('proofs.reject.action')}
                  </button>
                </>
              ) : null}
              <button type="button" className="btn btn-quiet" onClick={onClose}>
                {t('common.close')}
              </button>
            </div>
          </>
        )}
      </div>
    </Sheet>
  );
}

/* ---------------------------------- queue --------------------------------- */

const PROOF_PAGE = 20;

/**
 * The review queue: one status at a time, oldest claim first (the backend's
 * own ordering — a queue is worked forward), with the cursor paging the
 * listing hands back.
 */
export function ProofsQueue({ onCountChanged }: { onCountChanged?: () => void }) {
  const t = useT();
  const [status, setStatus] = useState<ProofStatus>('submitted');
  const [items, setItems] = useState<Proof[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [more, setMore] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);

  const load = useCallback(
    async (which: ProofStatus, signal?: AbortSignal) => {
      setItems(null);
      setCursor(null);
      setError(null);
      try {
        const r = await proofsApi.list({ status: which, limit: PROOF_PAGE }, signal);
        if (signal?.aborted) return;
        setItems(r.items ?? []);
        setCursor(r.next_cursor ?? null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(status, ac.signal);
    return () => ac.abort();
  }, [status, load]);

  const loadMore = async () => {
    if (!cursor) return;
    setMore(true);
    try {
      const r = await proofsApi.list({ status, cursor, limit: PROOF_PAGE });
      setItems((prev) => [...(prev ?? []), ...(r.items ?? [])]);
      setCursor(r.next_cursor ?? null);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setMore(false);
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <div
        className="wrap-sm"
        role="group"
        aria-label={t('proofs.filter_label')}
        style={{ display: 'flex', gap: 'var(--sp-2)' }}
      >
        {PROOF_STATUSES.map((s) => {
          const active = status === s;
          return (
            <button
              key={s}
              type="button"
              aria-pressed={active}
              onClick={() => setStatus(s)}
              style={{
                minHeight: 'var(--touch-min)',
                padding: '0 var(--sp-4)',
                border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
                borderRadius: 'var(--radius-md)',
                background: active ? 'var(--primary-soft)' : 'transparent',
                color: 'var(--ink)',
                fontWeight: active ? 600 : 400,
                fontSize: 'var(--text-sm)',
                cursor: 'pointer',
              }}
            >
              {t(`proofs.status.${s}`)}
            </button>
          );
        })}
      </div>

      <ProblemNote error={error} />

      <p style={{ color: 'var(--ink-soft)', margin: 0, fontSize: 'var(--text-sm)' }}>{t('proofs.lead')}</p>

      <ProofList items={items} emptyText={t(`proofs.empty.${status}`)} onOpen={(p) => setOpenId(p.id)} />

      {cursor ? (
        <div>
          <button type="button" className="btn btn-secondary" disabled={more} onClick={() => void loadMore()}>
            {more ? t('common.loading') : t('proofs.load_more')}
          </button>
        </div>
      ) : null}

      <ProofReview
        proofId={openId}
        onClose={() => setOpenId(null)}
        onChanged={() => {
          void load(status);
          onCountChanged?.();
        }}
      />
    </div>
  );
}

/* -------------------------- renter / contract panel ----------------------- */

/**
 * The proofs belonging to one renter or one contract. There is no server-side
 * filter for either (API.md §16.1 lists by status only), so the three statuses
 * are read and narrowed here — deliberately simple, because a single renter's
 * claims are few and the queue endpoint is the one that pages.
 */
export function ProofsFor({
  renterUserId,
  contractId,
}: {
  renterUserId?: string;
  contractId?: string;
}) {
  const t = useT();
  const [items, setItems] = useState<Proof[] | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const pages = await Promise.all(
          PROOF_STATUSES.map((s) => proofsApi.list({ status: s, limit: 100 }, signal)),
        );
        if (signal?.aborted) return;
        const all = pages.flatMap((p) => p.items ?? []);
        const mine = all.filter(
          (p) =>
            (!renterUserId || p.contract?.renter_user_id === renterUserId) &&
            (!contractId || p.contract?.id === contractId),
        );
        mine.sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
        setItems(mine);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setItems([]);
      }
    },
    [renterUserId, contractId],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
      <ProofList items={items} emptyText={t('proofs.empty.none_here')} onOpen={(p) => setOpenId(p.id)} />
      <ProofReview proofId={openId} onClose={() => setOpenId(null)} onChanged={() => void load()} />
    </div>
  );
}
