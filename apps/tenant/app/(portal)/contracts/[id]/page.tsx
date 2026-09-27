'use client';

/**
 * One contract (FLOWS flow 3 steps 5–6).
 *
 * The page is the document: the org's letterhead, the terms exactly as they
 * were snapshotted, the payment schedule, the signature block and the hash that
 * proves none of it moved. "Print / Save as PDF" is the browser's own — there
 * is no server-rendered PDF (SPEC §5.5).
 *
 * The two decisions a landlord can take live above the paper: **Activate**,
 * which countersigns and generates the schedule rows, and **Terminate**.
 * Activate is disabled until the renter has signed; the only way past that is
 * the landlord-recorded escape hatch (FLOWS 3.6), which is deliberately a
 * separate, reasoned sheet because it lands in the audit log flagged.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import {
  ContractStatusStamp,
  Facts,
  ScheduleStatusStamp,
  landlordSignature,
  renterSignature,
} from '../../../../components/ContractBits';
import { DocumentPaper, PrintStyles } from '../../../../components/DocumentPaper';
import { Field, Note, ProblemNote } from '../../../../components/FormBits';
import { AmendForm, type AmendMode } from '../../../../components/AmendForm';
import { BackfillSheet, needsBackfill } from '../../../../components/BackfillSheet';
import { DaysOverdue, PaymentsTable, ReverseSheet, SourceChip } from '../../../../components/PaymentBits';
import { ProofsFor } from '../../../../components/ProofBits';
import { RecordPaymentSheet, type RecordPaymentTarget } from '../../../../components/RecordPaymentSheet';
import { PageHead } from '../../../../components/PageHead';
import { CorrectPaymentSheet } from '../../../../components/CorrectPaymentSheet';
import { LoadMore } from '../../../../components/Paging';
import { PolicyFacts } from '../../../../components/PolicyBits';
import { DepositSection, SettlementSummary } from '../../../../components/SettleBits';
import { useTemplateList } from '../../../../components/TemplateBits';
import { EvictionSection, RecordNoticeForm, ReliefForm, adjustmentText } from '../../../../components/UnhappyBits';
import { Sheet } from '../../../../components/Sheet';
import {
  ApiError,
  PAYMENT_METHODS,
  contractsApi,
  isUnsettled,
  paymentsApi,
  remainingOn,
  schedulesApi,
  toApiError,
  unwrapContract,
  type CashMethod,
  type Contract,
  type ContractDocument,
  type ContractSignature,
  type ContractVerification,
  type Payment,
  type Schedule,
  type ScheduleRow,
  type SettlementPreview,
} from '../../../../lib/api';
import { useMe } from '../../../../lib/auth';
import { Amount, fmtDate, fmtTZS, todayISO } from '../../../../lib/format';
import { LOCALE_LABELS, TableScroll, isLocale, useT } from '@tms/ui';

/** `YYYY-MM-DD` plus one day — the day a superseded contract hands over. */
function dayAfter(iso: string): string {
  const d = new Date(`${iso}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().slice(0, 10);
}

/* ----------------------------- signature block ---------------------------- */

function SignatureCard({ label, signature }: { label: string; signature: ContractSignature | null }) {
  const t = useT();
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-2)', alignContent: 'start' }}>
      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</span>
      {signature ? (
        <>
          {signature.signature_image_url ? (
            /* eslint-disable-next-line @next/next/no-img-element -- presigned MinIO URL */
            <img
              src={signature.signature_image_url}
              alt={t('contracts.sig.alt', { name: signature.name })}
              style={{ height: 64, width: 'auto', objectFit: 'contain', objectPosition: 'left' }}
            />
          ) : null}
          <span style={{ fontWeight: 600 }}>{signature.name}</span>
          <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {t('contracts.sig.signed', { date: fmtDate(signature.signed_at) })}
            {signature.phone_masked ? ` ${t('contracts.sig.via_phone', { phone: signature.phone_masked })}` : ''}
            {signature.method ? ` · ${signature.method.replace('_', ' ')}` : ''}
          </span>
        </>
      ) : (
        <span className="pencil">{t('contracts.sig.not_signed')}</span>
      )}
      <hr className="rule" style={{ marginTop: 'var(--sp-2)' }} />
    </div>
  );
}

/* -------------------------------- sheets --------------------------------- */

function RecordOnBehalfForm({
  renterName,
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  renterName: string;
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
          border: '1px solid var(--stamp-overdue)',
          borderRadius: 'var(--radius-sm)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:danger-triangle-linear" width={20} />
        <span>{t('contracts.behalf.warning', { name: renterName })}</span>
      </p>
      <Field
        id="lr_reason"
        label={t('contracts.behalf.reason')}
        hint={t('contracts.chars_max', { n: reason.length })}
      >
        <textarea
          id="lr_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('contracts.behalf.reason_placeholder')}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? t('contracts.behalf.submitting') : t('contracts.behalf.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/** What the terminate sheet sends (Phase 22.5 adds the settle-up choices). */
type TerminateBody = Parameters<typeof contractsApi.terminate>[1];

function TerminateForm({
  contractId,
  settles,
  initialDate,
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  contractId: string;
  /** Phase 22.5: a running contract settles up; an unsigned one keeps the simple sheet. */
  settles: boolean;
  /** Phase 22.5: the renter's notice date, when the sheet is opened from it. */
  initialDate?: string;
  busy: boolean;
  error: ApiError | null;
  onSubmit: (body: TerminateBody) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [reason, setReason] = useState('');
  const [date, setDate] = useState(initialDate ?? todayISO());
  const [choice, setChoice] = useState<'refund' | 'forfeit' | ''>('');
  const [refundMethod, setRefundMethod] = useState<CashMethod>('cash');
  const [refundReference, setRefundReference] = useState('');
  const [preview, setPreview] = useState<SettlementPreview | null>(null);
  const [previewError, setPreviewError] = useState<ApiError | null>(null);

  // The backend works the settle-up out for the chosen last day (and choice);
  // re-read it whenever either changes.
  useEffect(() => {
    if (!settles || !date) return;
    const ac = new AbortController();
    setPreviewError(null);
    contractsApi
      .settlementPreview(contractId, { effective_date: date, prepaid_action: choice || undefined }, ac.signal)
      .then(setPreview)
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setPreview(null);
        setPreviewError(toApiError(e));
      });
    return () => ac.abort();
  }, [settles, contractId, date, choice]);

  const settlement = preview?.settlement ?? null;
  const needsChoice = Boolean(preview?.needs_choice) || Boolean(settlement?.prepaid && choice);
  const refunding = (settlement?.refund ?? 0) > 0;
  const choiceMissing = error?.code === 'prepaid_choice_required';

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit({
          reason: reason.trim(),
          effective_date: date,
          prepaid_action: settles && choice ? choice : undefined,
          refund_method: settles && refunding ? refundMethod : undefined,
          refund_reference: settles && refunding ? refundReference.trim() || undefined : undefined,
        });
      }}
      style={{ display: 'grid', gap: 'var(--sp-4)' }}
      noValidate
    >
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>{t('contracts.terminate.lead')}</p>
      <Field
        id="t_reason"
        label={t('contracts.terminate.reason')}
        hint={`${t('contracts.chars_max', { n: reason.length })} — ${t('contracts.terminate.reason_note')}`}
        error={error?.errors.reason}
      >
        <textarea
          id="t_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('contracts.terminate.reason_placeholder')}
        />
      </Field>
      <Field
        id="t_date"
        label={t('contracts.terminate.effective_date')}
        hint={settles ? t('settle.date_hint') : undefined}
        error={error?.errors.effective_date}
      >
        <input id="t_date" className="input" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
      </Field>

      {settles ? (
        <section style={{ display: 'grid', gap: 'var(--sp-3)', borderTop: '1px solid var(--rule)', paddingTop: 'var(--sp-3)' }}>
          <h3 style={{ fontSize: 'var(--text-md)' }}>{t('settle.title')}</h3>
          <ProblemNote error={previewError} />
          {settlement ? (
            <SettlementSummary settlement={settlement} />
          ) : previewError ? null : (
            <p style={{ color: 'var(--ink-soft)' }}>{t('settle.loading')}</p>
          )}

          {needsChoice ? (
            <fieldset style={{ border: 0, margin: 0, padding: 0, display: 'grid', gap: 'var(--sp-2)' }}>
              <legend style={{ fontSize: 'var(--text-sm)', color: choiceMissing ? 'var(--stamp-overdue)' : 'var(--ink-soft)' }}>
                {choiceMissing ? t('settle.choice_required') : t('settle.choice')}
              </legend>
              {(['refund', 'forfeit'] as const).map((v) => (
                <label
                  key={v}
                  htmlFor={`t_choice_${v}`}
                  style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)', cursor: 'pointer' }}
                >
                  <input
                    id={`t_choice_${v}`}
                    type="radio"
                    name="t_choice"
                    checked={choice === v}
                    onChange={() => setChoice(v)}
                    style={{ width: 18, height: 18 }}
                  />
                  {t(`settle.choice.${v}`)}
                </label>
              ))}
            </fieldset>
          ) : null}

          {refunding ? (
            <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
              <Field id="t_refund_method" label={t('settle.refund_method')} error={error?.errors.refund_method}>
                <select
                  id="t_refund_method"
                  className="input"
                  value={refundMethod}
                  onChange={(e) => setRefundMethod(e.target.value as CashMethod)}
                >
                  {PAYMENT_METHODS.map((m) => (
                    <option key={m.value} value={m.value}>
                      {t(`payments.method.${m.value}`)}
                    </option>
                  ))}
                </select>
              </Field>
              <Field id="t_refund_ref" label={t('settle.refund_reference')} error={error?.errors.refund_reference}>
                <input
                  id="t_refund_ref"
                  className="input"
                  maxLength={80}
                  value={refundReference}
                  onChange={(e) => setRefundReference(e.target.value)}
                />
              </Field>
            </div>
          ) : null}
        </section>
      ) : null}

      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button
          type="submit"
          className="btn btn-danger"
          disabled={busy || reason.trim().length === 0 || (settles && Boolean(preview?.needs_choice) && !choice)}
        >
          {busy ? t('contracts.terminate.submitting') : t('contracts.terminate.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/**
 * Phase 22.3 — withdraw an unsigned contract and write it again, on the same
 * template's new wording or on another. The sheet owns its call because the
 * answer is a different contract: the page moves to it rather than reloading.
 */
function ReissueForm({
  contract,
  onDone,
  onCancel,
}: {
  contract: Contract;
  onDone: (created: Contract) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const templates = useTemplateList();
  const [reason, setReason] = useState('');
  const [templateId, setTemplateId] = useState(contract.template_id ?? '');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await contractsApi.reissue(contract.id, {
        reason: reason.trim() || undefined,
        // Only a real change is sent; otherwise the backend keeps its own template.
        template_id: templateId && templateId !== contract.template_id ? templateId : undefined,
      });
      onDone(unwrapContract(res));
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>{t('contracts.reissue.lead')}</p>
      <Field id="ri_template" label={t('tpl.one')} error={error?.errors.template_id}>
        <select
          id="ri_template"
          className="input"
          value={templateId}
          disabled={templates === null}
          onChange={(e) => setTemplateId(e.target.value)}
        >
          {!contract.template_id ? <option value="">{t('contracts.reissue.template_resolved')}</option> : null}
          {(templates ?? []).map((x) => (
            <option key={x.id} value={x.id}>
              {x.id === contract.template_id ? t('contracts.reissue.template_same', { name: x.name }) : x.name}
            </option>
          ))}
        </select>
      </Field>
      <Field
        id="ri_reason"
        label={t('contracts.reissue.reason')}
        hint={t('contracts.chars_max', { n: reason.length })}
        error={error?.errors.reason}
      >
        <textarea
          id="ri_reason"
          className="input"
          rows={2}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('contracts.reissue.reason_placeholder')}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('contracts.reissue.busy') : t('contracts.reissue.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/**
 * Phase 21 — writing off what a former tenant still owes. The reason is
 * required because the write-off is audited; the warning says plainly that it
 * is not a deletion and can be undone.
 */
function WriteOffForm({
  amount,
  periods,
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  amount: string;
  periods: string;
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
          border: '1px solid var(--stamp-overdue)',
          borderRadius: 'var(--radius-sm)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:danger-triangle-linear" width={20} />
        <span>{t('contracts.writeoff.warning', { amount, periods })}</span>
      </p>
      <Field
        id="wo_reason"
        label={t('contracts.writeoff.reason')}
        hint={t('contracts.chars_max', { n: reason.length })}
        error={error?.errors.reason}
      >
        <textarea
          id="wo_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('contracts.writeoff.reason_placeholder')}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? t('contracts.writeoff.submitting') : t('contracts.writeoff.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/* --------------------------------- page ---------------------------------- */

function ContractBody({ id }: { id: string }) {
  const t = useT();
  const router = useRouter();
  const { org: me } = useMe();
  const [contract, setContract] = useState<Contract | null>(null);
  const [doc, setDoc] = useState<ContractDocument | null>(null);
  const [schedules, setSchedules] = useState<ScheduleRow[] | null>(null);
  const [payments, setPayments] = useState<Payment[] | null>(null);
  // Phase 23: the payment history pages; this is its next page.
  const [paymentsCursor, setPaymentsCursor] = useState<string | null>(null);
  const [paymentsMore, setPaymentsMore] = useState(false);
  const [verification, setVerification] = useState<ContractVerification | null>(null);

  const [recordTarget, setRecordTarget] = useState<RecordPaymentTarget | null>(null);
  const [reversing, setReversing] = useState<Payment | null>(null);
  const [correcting, setCorrecting] = useState<Payment | null>(null);
  const [reverseBusy, setReverseBusy] = useState(false);
  const [reverseError, setReverseError] = useState<ApiError | null>(null);

  const [error, setError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [behalfOpen, setBehalfOpen] = useState(false);
  const [terminateOpen, setTerminateOpen] = useState(false);
  const [backfillOpen, setBackfillOpen] = useState(false);
  const [writeOffOpen, setWriteOffOpen] = useState(false);
  const [reissueOpen, setReissueOpen] = useState(false);
  const [amendMode, setAmendMode] = useState<AmendMode | null>(null);
  // Phase 22.5 (rest): relief on one period, a notice recorded in person, and
  // the terminate sheet opened at the renter's notice date.
  const [reliefFor, setReliefFor] = useState<ScheduleRow | null>(null);
  const [noticeOpen, setNoticeOpen] = useState(false);
  const [terminateDate, setTerminateDate] = useState<string | undefined>(undefined);
  // Phase 22.4: an unsigned amendment of this contract, if one is open.
  const [openAmendment, setOpenAmendment] = useState<Contract | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const c = unwrapContract(await contractsApi.get(id, signal));
        setContract(c);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        return;
      }
      // The document and the ledger are separate reads; neither should take the
      // page down if it fails on its own.
      try {
        setDoc(await contractsApi.document(id, signal));
      } catch (e) {
        if (!(e instanceof DOMException)) setDoc(null);
      }
      try {
        const res = await contractsApi.schedules(id, signal);
        setSchedules(res.items ?? []);
      } catch (e) {
        if (!(e instanceof DOMException)) setSchedules([]);
      }
      // Payment history is a Phase 5 read; a contract written before the
      // payments API existed must still render, so this failure is swallowed
      // into an empty ledger like the two above it.
      try {
        const res = await paymentsApi.list({ contract_id: id, limit: 200 }, signal);
        setPayments(res.items ?? []);
        setPaymentsCursor(res.next_cursor ?? null);
      } catch (e) {
        if (!(e instanceof DOMException)) setPayments([]);
      }
    },
    [id],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  // Phase 22.4: the backend allows one open amendment per contract; find it
  // among the unit's unsigned contracts so this page can point at it.
  const unitId = contract?.unit?.id;
  const runningNow = contract?.status === 'active' || contract?.status === 'expiring';
  useEffect(() => {
    if (!unitId || !runningNow) {
      setOpenAmendment(null);
      return;
    }
    const ac = new AbortController();
    contractsApi
      .list({ unit_id: unitId, status: 'pending_signature', limit: 50 }, ac.signal)
      .then((r) =>
        setOpenAmendment(
          (r.items ?? []).find((c) => c.supersedes_contract_id === id && c.amendment_effective_date) ?? null,
        ),
      )
      .catch(() => setOpenAmendment(null));
    return () => ac.abort();
  }, [id, unitId, runningNow]);

  const act = async (run: () => Promise<unknown>, done: string) => {
    setBusy(true);
    setActionError(null);
    try {
      await run();
      setNote(done);
      setBehalfOpen(false);
      setTerminateOpen(false);
      setWriteOffOpen(false);
      setReliefFor(null);
      await load();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const morePayments = async () => {
    if (!paymentsCursor) return;
    setPaymentsMore(true);
    try {
      const res = await paymentsApi.list({ contract_id: id, limit: 200, cursor: paymentsCursor });
      setPayments((prev) => [...(prev ?? []), ...(res.items ?? [])]);
      setPaymentsCursor(res.next_cursor ?? null);
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setPaymentsMore(false);
    }
  };

  const reverse = async (reason: string) => {
    if (!reversing) return;
    setReverseBusy(true);
    setReverseError(null);
    try {
      await paymentsApi.reverse(reversing.id, reason);
      setReversing(null);
      setNote(t('contracts.payments.reversed'));
      await load();
    } catch (e) {
      setReverseError(toApiError(e));
    } finally {
      setReverseBusy(false);
    }
  };

  const verify = async () => {
    setVerifying(true);
    setActionError(null);
    try {
      setVerification(await contractsApi.verify(id));
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setVerifying(false);
    }
  };

  if (error && !contract) {
    return (
      <>
        <PageHead title={t('contracts.detail.title')} />
        <ProblemNote error={error} />
        <p style={{ marginTop: 'var(--sp-4)' }}>
          <Link href="/contracts" className="btn btn-quiet">
            {t('contracts.detail.back')}
          </Link>
        </p>
      </>
    );
  }

  if (!contract) {
    return (
      <>
        <PageHead title={t('contracts.detail.title')} />
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
      </>
    );
  }

  const signedByRenter = renterSignature(contract);
  const signedByLandlord = landlordSignature(contract);
  const canActivate = contract.status === 'pending_signature' && signedByRenter !== null;
  const canTerminate = ['pending_signature', 'active', 'expiring'].includes(contract.status);
  /** Phase 22.3 — only a contract nobody has signed can be written again. */
  const canReissue =
    contract.status === 'pending_signature' && !signedByRenter && !signedByLandlord && !contract.amendment_effective_date;
  /** Phase 22.4 — an amendment (or renewal) of another contract. */
  const isAmendment = Boolean(contract.amendment_effective_date && contract.supersedes_contract_id);
  const canAmend =
    (contract.status === 'active' || contract.status === 'expiring') && !contract.superseded_by_contract_id;
  /**
   * Phase 22.5 — a tenancy that ran out with nobody saying what happened
   * (a holdover): renew it on its end date, or confirm the unit is empty.
   */
  const holdover =
    contract.status === 'ended' && !contract.superseded_by_contract_id && !contract.moved_out_confirmed_at;
  const rows = schedules ?? [];
  const org = doc?.org;
  const summary = contract.schedules_summary;
  const running = contract.status === 'active' || contract.status === 'expiring';
  /**
   * Phase 21 — a closed tenancy still owes the periods the renter lived
   * through, so money can land on it after move-out too.
   */
  const closed = contract.status === 'ended' || contract.status === 'terminated';
  const canRecord = running || closed;
  const owing = rows.filter(isUnsettled);
  const owingTotal = owing.reduce((sum, s) => sum + remainingOn(s), 0);
  const writtenOff = rows.filter((s) => s.status === 'written_off');
  const writtenOffTotal = writtenOff.reduce((sum, s) => sum + remainingOn(s), 0);
  const writeOffReason = writtenOff.find((s) => s.write_off_reason)?.write_off_reason ?? '';
  /** The write-off is the owner's call (the API answers 403 to a manager). */
  const isOwner = me?.role === 'org_owner';
  const contractLabel = `${contract.unit?.name ?? ''} · ${contract.unit?.property_name ?? ''} — ${
    contract.renter?.full_name ?? ''
  }`;
  const renterName = contract.renter?.full_name ?? t('contracts.the_renter');
  const docLanguage = isLocale(contract.language) ? LOCALE_LABELS[contract.language] : null;
  const openRecord = (scheduleId?: string) =>
    setRecordTarget({ contractId: id, scheduleId, label: contractLabel });

  return (
    <>
      <PrintStyles />

      <div className="no-print">
        <PageHead
          title={`${contract.unit?.name ?? t('contracts.detail.title')} · ${contract.renter?.full_name ?? ''}`}
          lead={`${contract.unit?.property_name ?? ''} · ${fmtDate(contract.start_date)} → ${fmtDate(contract.end_date)}`}
          actions={
            <>
              <Link href="/contracts" className="btn btn-quiet">
                <Icon icon="solar:arrow-left-linear" width={20} /> {t('nav.contracts')}
              </Link>
              <button type="button" className="btn btn-secondary" onClick={() => window.print()}>
                <Icon icon="solar:printer-linear" width={20} /> {t('contracts.detail.print')}
              </button>
            </>
          }
        />

        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
          <ContractStatusStamp status={contract.status} />
          {contract.superseded_by_contract_id ? (
            <span className="stamp">{t('contracts.amend.chip_replaced')}</span>
          ) : isAmendment ? (
            <span className="stamp">{t('contracts.amend.chip')}</span>
          ) : null}
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {t('contracts.meta.written', { date: fmtDate(contract.created_at) })}
            {contract.activated_at
              ? ` · ${t('contracts.meta.activated', { date: fmtDate(contract.activated_at) })}`
              : ''}
            {contract.terminated_at
              ? ` · ${t('contracts.meta.terminated', { date: fmtDate(contract.terminated_at) })}`
              : ''}
            {contract.moved_out_confirmed_at
              ? ` · ${t('contracts.meta.moved_out', { date: fmtDate(contract.moved_out_confirmed_at) })}`
              : ''}
          </span>
        </div>

        {/* The money at a glance (API.md `schedules_summary`), so the state of
            the tenancy is readable without scrolling to the ledger. */}
        {summary ? (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-5)',
              flexWrap: 'wrap',
              marginTop: 'var(--sp-4)',
              paddingTop: 'var(--sp-3)',
              borderTop: '1px solid var(--rule)',
              fontSize: 'var(--text-sm)',
            }}
          >
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.paid')} </span>
              <strong className="num">
                {summary.paid_count}/{summary.count}
              </strong>
            </span>
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.overdue')} </span>
              {summary.overdue_count > 0 ? (
                <strong className="num" style={{ color: 'var(--stamp-overdue)' }}>
                  {summary.overdue_count}
                </strong>
              ) : (
                <span className="pencil">{t('contracts.summary.overdue_none')}</span>
              )}
            </span>
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.next_due')} </span>
              {summary.next_due_date ? (
                <strong>
                  {fmtDate(summary.next_due_date)} · {fmtTZS(summary.next_due_amount ?? 0)}
                </strong>
              ) : (
                <span className="pencil">{t('contracts.summary.nothing_outstanding')}</span>
              )}
            </span>
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.total')} </span>
              <strong>{fmtTZS(summary.total)}</strong>
            </span>
          </div>
        ) : null}

        {isAmendment && contract.supersedes_contract_id ? (
          <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)' }}>
            <Link href={`/contracts/${contract.supersedes_contract_id}`} style={{ color: 'inherit' }}>
              {t('contracts.amend.of', {
                ref: contract.supersedes_contract_id.slice(0, 8),
                date: fmtDate(contract.amendment_effective_date),
              })}
            </Link>
            {contract.amendment_reason ? (
              <span style={{ color: 'var(--ink-soft)' }}>
                {' '}
                {t('contracts.amend.reason_line', { reason: contract.amendment_reason })}
              </span>
            ) : null}
          </p>
        ) : null}

        {contract.superseded_by_contract_id ? (
          <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)' }}>
            <Link href={`/contracts/${contract.superseded_by_contract_id}`} style={{ color: 'inherit' }}>
              {contract.termination_effective_date
                ? t('contracts.amend.replaced_by', {
                    ref: contract.superseded_by_contract_id.slice(0, 8),
                    date: fmtDate(dayAfter(contract.termination_effective_date)),
                  })
                : t('contracts.amend.replaced_by_nodate', { ref: contract.superseded_by_contract_id.slice(0, 8) })}
            </Link>
          </p>
        ) : null}

        {openAmendment && !contract.superseded_by_contract_id ? (
          <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)' }}>
            <Link href={`/contracts/${openAmendment.id}`} style={{ color: 'inherit' }}>
              {t('contracts.amend.open_pending', { date: fmtDate(openAmendment.amendment_effective_date) })}
            </Link>
          </p>
        ) : null}

        {contract.supersedes_contract_id && !isAmendment ? (
          <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)' }}>
            <Link href={`/contracts/${contract.supersedes_contract_id}`} style={{ color: 'var(--ink-soft)' }}>
              {t('contracts.reissue.replaces', { ref: contract.supersedes_contract_id.slice(0, 8) })}
            </Link>
          </p>
        ) : null}

        {canReissue && contract.template_changed ? (
          <p
            role="status"
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-3)',
              flexWrap: 'wrap',
              marginTop: 'var(--sp-4)',
              padding: 'var(--sp-3) var(--sp-4)',
              border: '1px solid var(--rule)',
              borderLeft: '3px solid var(--stamp-overdue)',
              borderRadius: 'var(--radius-sm)',
              fontSize: 'var(--text-sm)',
              maxWidth: 'none',
            }}
          >
            <span style={{ flex: '1 1 280px' }}>{t('contracts.reissue.changed')}</span>
            <button type="button" className="btn btn-primary" onClick={() => setReissueOpen(true)} disabled={busy}>
              {t('contracts.reissue.open_new')}
            </button>
          </p>
        ) : null}

        {contract.termination_reason ? (
          <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)' }}>
            {t('contracts.reason_given', { reason: contract.termination_reason })}
          </p>
        ) : null}

        {/* Phase 22.5: the renter said they are leaving before the end date. */}
        {running && contract.notice_leave_on ? (
          <div
            role="status"
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-3)',
              flexWrap: 'wrap',
              marginTop: 'var(--sp-4)',
              padding: 'var(--sp-3) var(--sp-4)',
              border: '1px solid var(--rule)',
              borderLeft: '3px solid var(--primary)',
              borderRadius: 'var(--radius-sm)',
              fontSize: 'var(--text-sm)',
            }}
          >
            <span style={{ flex: '1 1 280px' }}>
              {contract.notice_reason
                ? t('notice.banner_reason', { date: fmtDate(contract.notice_leave_on), reason: contract.notice_reason })
                : t('notice.banner', { date: fmtDate(contract.notice_leave_on) })}
            </span>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy}
              onClick={() => {
                setTerminateDate(contract.notice_leave_on ?? undefined);
                setTerminateOpen(true);
              }}
            >
              {t('notice.end_on_date')}
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              disabled={busy}
              onClick={() => void act(() => contractsApi.withdrawNotice(id), t('notice.withdrawn'))}
            >
              {t('notice.withdraw')}
            </button>
          </div>
        ) : null}

        <div style={{ display: 'grid', gap: 'var(--sp-3)', marginTop: 'var(--sp-4)' }}>
          {note ? <Note>{note}</Note> : null}
          <ProblemNote error={actionError} />
        </div>

        {holdover ? (
          <section style={{ marginTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-2)' }}>
            <p style={{ color: 'var(--ink-soft)' }}>{t('holdover.contract_lead')}</p>
            <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={busy}
                onClick={() => void act(() => contractsApi.movedOut(id), t('holdover.moved_out_done'))}
              >
                {t('holdover.moved_out')}
              </button>
              <button type="button" className="btn btn-quiet" disabled={busy} onClick={() => setAmendMode('renew')}>
                <Icon icon="solar:refresh-linear" width={20} /> {t('contracts.amend.renew')}
              </button>
            </div>
          </section>
        ) : null}

        {/* ------------------------------ decisions ----------------------------- */}
        {contract.status === 'pending_signature' || canTerminate ? (
          <section style={{ marginTop: 'var(--sp-5)' }}>
            <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap', alignItems: 'center' }}>
              {contract.status === 'pending_signature' ? (
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={!canActivate || busy}
                  title={canActivate ? undefined : t('contracts.activate.blocked')}
                  onClick={() =>
                    void act(
                      () => contractsApi.activate(id),
                      t('contracts.activate.done'),
                    )
                  }
                >
                  <Icon icon="solar:check-circle-linear" width={20} /> {t('contracts.activate')}
                </button>
              ) : null}
              {contract.status === 'pending_signature' && !canActivate ? (
                <button type="button" className="btn btn-quiet" onClick={() => setBehalfOpen(true)} disabled={busy}>
                  {t('contracts.behalf.open')}
                </button>
              ) : null}
              {canReissue && !contract.template_changed ? (
                <button type="button" className="btn btn-quiet" onClick={() => setReissueOpen(true)} disabled={busy}>
                  {t('contracts.reissue.open')}
                </button>
              ) : null}
              {canAmend ? (
                <>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    onClick={() => setAmendMode('amend')}
                    disabled={busy || openAmendment !== null}
                    title={openAmendment ? t('contracts.amend.blocked') : undefined}
                  >
                    <Icon icon="solar:pen-linear" width={20} /> {t('contracts.amend.open')}
                  </button>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    onClick={() => setAmendMode('renew')}
                    disabled={busy || openAmendment !== null}
                    title={openAmendment ? t('contracts.amend.blocked') : undefined}
                  >
                    <Icon icon="solar:refresh-linear" width={20} /> {t('contracts.amend.renew')}
                  </button>
                </>
              ) : null}
              {running && !contract.notice_leave_on ? (
                <button type="button" className="btn btn-quiet" onClick={() => setNoticeOpen(true)} disabled={busy}>
                  {t('notice.record')}
                </button>
              ) : null}
              {canTerminate ? (
                <button
                  type="button"
                  className="btn btn-danger"
                  onClick={() => {
                    setTerminateDate(undefined);
                    setTerminateOpen(true);
                  }}
                  disabled={busy}
                >
                  <Icon icon="solar:close-circle-linear" width={20} />{' '}
                  {isAmendment && contract.status === 'pending_signature'
                    ? t('contracts.amend.withdraw')
                    : t('contracts.terminate')}
                </button>
              ) : null}
            </div>
            {contract.status === 'pending_signature' && !canActivate ? (
              <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {t('contracts.activate.waiting', { name: renterName })}
              </p>
            ) : null}
          </section>
        ) : null}

        {/* -------------------------------- facts ------------------------------- */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('contracts.terms.title')}</h2>
          <div style={{ maxWidth: 640 }}>
            <Facts
              rows={[
                [t('common.unit'), `${contract.unit?.name ?? '—'} · ${contract.unit?.property_name ?? ''}`],
                [
                  t('common.renter'),
                  `${contract.renter?.full_name ?? '—'}${contract.renter?.phone ? ` · ${contract.renter.phone}` : ''}`,
                ],
                [
                  t('contracts.col.rent'),
                  <Amount key="rent" value={contract.rent_amount} per={contract.rent_period_days} />,
                ],
                [
                  t('contracts.fact.payment_period'),
                  contract.payment_period
                    ? t('contracts.fact.period_days', {
                        label: contract.payment_period.label,
                        days: contract.payment_period.days,
                      })
                    : '—',
                ],
                [
                  t('contracts.fact.term_length'),
                  contract.term_days ? t.n('common.day', contract.term_days) : '—',
                ],
                [t('contracts.fact.starts'), fmtDate(contract.start_date)],
                [t('contracts.fact.ends'), fmtDate(contract.end_date)],
                [
                  t('contracts.fact.due_day'),
                  contract.due_day ? String(contract.due_day) : t('contracts.fact.due_day_default'),
                ],
                /* Phase 13: which language the document itself was written in. */
                [t('contracts.language'), docLanguage ?? <span key="lang" className="pencil">{t('contracts.language.unknown')}</span>],
              ]}
            />
          </div>
        </section>

        {/* Phase 22.2: the rules copied from the template when this was written. */}
        {contract.policy ? (
          <section style={{ marginTop: 'var(--sp-6)' }}>
            <hr className="rule rule-strong" />
            <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('policy.title')}</h2>
            <div style={{ maxWidth: 640 }}>
              <PolicyFacts policy={contract.policy} />
            </div>
          </section>
        ) : null}

        {/* Phase 22.5: what termination settled, as stored with it. */}
        {contract.settlement ? (
          <section style={{ marginTop: 'var(--sp-6)' }}>
            <hr className="rule rule-strong" />
            <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('settle.summary_title')}</h2>
            <div style={{ maxWidth: 640, fontSize: 'var(--text-sm)' }}>
              <p style={{ color: 'var(--ink-soft)', marginBottom: 'var(--sp-2)' }}>
                {t('settle.summary_lead', { date: fmtDate(contract.settlement.effective_date) })}
              </p>
              <SettlementSummary settlement={contract.settlement} />
            </div>
          </section>
        ) : null}

        {/* Phase 22.5: the deposit ledger, once the tenancy has been signed. */}
        {running || (closed && contract.activated_at) ? (
          <section style={{ marginTop: 'var(--sp-6)' }}>
            <hr className="rule rule-strong" />
            <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('deposit.title')}</h2>
            <DepositSection contractId={id} onChanged={() => void load()} />
          </section>
        ) : null}

        {/* Phase 22.5: eviction as stages with letters; past cases stay listed. */}
        {running || (closed && contract.activated_at) ? (
          <section style={{ marginTop: 'var(--sp-6)' }}>
            <hr className="rule rule-strong" />
            <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('eviction.title')}</h2>
            <EvictionSection
              contractId={id}
              running={running}
              hasOverdue={(summary?.overdue_count ?? 0) > 0}
            />
          </section>
        ) : null}

        {/* ------------------------------ schedules ----------------------------- */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <div
            style={{
              display: 'flex',
              alignItems: 'flex-end',
              justifyContent: 'space-between',
              gap: 'var(--sp-4)',
              margin: 'var(--sp-4) 0',
            }}
          >
            <div>
              <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('contracts.schedule.title')}</h2>
              <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {t('contracts.schedule.lead')}
              </p>
            </div>
            <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
              {/* Phase 20.3 — only while something past its due date is still
                  open, which is exactly the state a pre-TMS tenancy lands in. */}
              {running && needsBackfill(rows) ? (
                <button type="button" className="btn btn-quiet" onClick={() => setBackfillOpen(true)}>
                  <Icon icon="solar:history-linear" width={20} /> {t('backfill.open')}
                </button>
              ) : null}
              <button
                type="button"
                className="btn btn-secondary"
                disabled={!canRecord || rows.length === 0}
                title={
                  canRecord ? t('contracts.schedule.record_hint') : t('contracts.schedule.record_blocked')
                }
                onClick={() => openRecord()}
              >
                <Icon icon="solar:wallet-money-linear" width={20} /> {t('contracts.schedule.record')}
              </button>
            </div>
          </div>

          {/* Phase 21 — arrears after move-out: collect, or write off. */}
          {closed && owing.length > 0 ? (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: 'var(--sp-3)',
                flexWrap: 'wrap',
                padding: 'var(--sp-3) var(--sp-4)',
                marginBottom: 'var(--sp-4)',
                border: '1px solid var(--stamp-overdue)',
                borderRadius: 'var(--radius-sm)',
              }}
            >
              <div>
                <strong>
                  {t('contracts.arrears.owes', {
                    amount: fmtTZS(owingTotal),
                    periods: t.n('contracts.arrears.periods', owing.length),
                  })}
                </strong>
                <p style={{ marginTop: 'var(--sp-1)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                  {isOwner ? t('contracts.arrears.lead') : t('contracts.arrears.lead_manager')}
                </p>
              </div>
              <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
                <button type="button" className="btn btn-secondary" onClick={() => openRecord()}>
                  <Icon icon="solar:wallet-money-linear" width={20} /> {t('contracts.schedule.record')}
                </button>
                {isOwner ? (
                  <button
                    type="button"
                    className="btn btn-quiet"
                    onClick={() => {
                      setActionError(null);
                      setWriteOffOpen(true);
                    }}
                    disabled={busy}
                  >
                    {t('contracts.writeoff.open')}
                  </button>
                ) : null}
              </div>
            </div>
          ) : null}

          {writtenOff.length > 0 ? (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: 'var(--sp-3)',
                flexWrap: 'wrap',
                padding: 'var(--sp-3) var(--sp-4)',
                marginBottom: 'var(--sp-4)',
                border: '1px solid var(--rule)',
                borderRadius: 'var(--radius-sm)',
              }}
            >
              <span>
                {writeOffReason
                  ? t('contracts.writeoff.summary', { amount: fmtTZS(writtenOffTotal), reason: writeOffReason })
                  : t('contracts.writeoff.summary_no_reason', { amount: fmtTZS(writtenOffTotal) })}
              </span>
              {isOwner ? (
                <button
                  type="button"
                  className="btn btn-quiet"
                  disabled={busy}
                  onClick={() => {
                    if (!window.confirm(t('contracts.writeoff.undo_confirm'))) return;
                    void act(() => contractsApi.undoWriteOff(id), t('contracts.writeoff.undone'));
                  }}
                >
                  <Icon icon="solar:undo-left-linear" width={20} /> {t('contracts.writeoff.undo')}
                </button>
              ) : null}
            </div>
          ) : null}

          <TableScroll label={t('contracts.schedule.table_label')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('contracts.schedule.col.period')}</th>
                <th>{t('contracts.schedule.col.due')}</th>
                <th className="num">{t('common.amount')}</th>
                <th className="num">{t('contracts.schedule.col.paid')}</th>
                <th>{t('common.status')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {schedules === null ? (
                <tr>
                  <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                    {t('common.loading')}
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                    {t('contracts.schedule.empty')}
                  </td>
                </tr>
              ) : (
                <>
                  {rows.map((s) => (
                    <tr key={s.id}>
                      <td style={{ fontSize: 'var(--text-sm)' }}>
                        {fmtDate(s.period_start)} → {fmtDate(s.period_end)}
                      </td>
                      <td>
                        {fmtDate(s.due_date)}
                        <DaysOverdue days={(s as Schedule).days_overdue} />
                      </td>
                      <td className="num">{fmtTZS(s.amount)}</td>
                      <td className="num">{s.paid_amount ? fmtTZS(s.paid_amount) : <span className="pencil">—</span>}</td>
                      <td>
                        <ScheduleStatusStamp status={s.status} />
                        {s.status === 'partial' ? (
                          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                            {t('contracts.schedule.still_owing', { amount: fmtTZS(remainingOn(s)) })}
                          </div>
                        ) : null}
                        {/* Phase 22.5: relief the landlord granted on this period. */}
                        {s.adjustment_kind ? (
                          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                            {adjustmentText(t, s)}{' '}
                            <button
                              type="button"
                              className="btn btn-quiet"
                              style={{ minHeight: 0, padding: '0 var(--sp-1)', fontSize: 'var(--text-xs)' }}
                              disabled={busy}
                              onClick={() => void act(() => schedulesApi.undoAdjust(s.id), t('relief.undone'))}
                            >
                              {t('relief.undo')}
                            </button>
                          </div>
                        ) : null}
                        {s.status === 'written_off' && s.write_off_reason ? (
                          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                            {s.write_off_reason}
                          </div>
                        ) : null}
                        <SourceChip source={s.last_payment_source} />
                      </td>
                      <td>
                        <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', justifyContent: 'flex-end' }}>
                          {canRecord && isUnsettled(s) ? (
                            <button
                              type="button"
                              className="btn btn-secondary"
                              style={{ minHeight: 36 }}
                              onClick={() => openRecord(s.id)}
                            >
                              {t('contracts.schedule.record')}
                            </button>
                          ) : null}
                          {canRecord && isUnsettled(s) && !s.adjustment_kind ? (
                            <button
                              type="button"
                              className="btn btn-quiet"
                              style={{ minHeight: 36 }}
                              onClick={() => setReliefFor(s)}
                            >
                              {t('relief.open')}
                            </button>
                          ) : null}
                        </div>
                      </td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td colSpan={2}>{t('common.total')}</td>
                    <td className="num">{fmtTZS(rows.reduce((t, s) => t + (s.amount ?? 0), 0))}</td>
                    <td className="num">{fmtTZS(rows.reduce((t, s) => t + (s.paid_amount ?? 0), 0))}</td>
                    <td colSpan={2} />
                  </tr>
                </>
              )}
            </tbody>
          </table>
          </TableScroll>
        </section>

        {/* ------------------------------ payments ------------------------------ */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <div style={{ margin: 'var(--sp-4) 0' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('contracts.payments.title')}</h2>
            <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('contracts.payments.lead')}
            </p>
          </div>
          <PaymentsTable
            items={payments}
            showRenter={false}
            showUnit={false}
            emptyText={t('contracts.payments.empty')}
            onReverse={(p) => {
              setReverseError(null);
              setReversing(p);
            }}
            onCorrect={setCorrecting}
          />
          <LoadMore cursor={paymentsCursor} loading={paymentsMore} onLoad={() => void morePayments()} />
        </section>

        {/* -------------------------------- proofs ------------------------------- */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <div style={{ margin: 'var(--sp-4) 0' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('proofs.title')}</h2>
            <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('proofs.lead')}
            </p>
          </div>
          <ProofsFor contractId={id} />
        </section>

        <hr className="rule rule-strong" style={{ marginTop: 'var(--sp-6)' }} />
        <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('contracts.doc.title')}</h2>
      </div>

      {/* -------------------------------- paper -------------------------------- */}
      {doc ? (
        <DocumentPaper
          html={doc.terms_html}
          displayName={org?.display_name}
          logoUrl={org?.logo_url}
          letterheadUrl={org?.letterhead_url}
          footerText={org?.footer_text}
        >
          <section style={{ marginTop: 'var(--sp-5)' }}>
            <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-3)' }}>{t('contracts.doc.parties')}</h3>
            <Facts
              rows={[
                [t('contracts.doc.landlord'), doc.parties?.landlord?.name ?? '—'],
                [
                  t('common.renter'),
                  `${doc.parties?.renter?.name ?? '—'}${
                    doc.parties?.renter?.phone_masked ? ` · ${doc.parties.renter.phone_masked}` : ''
                  }`,
                ],
              ]}
            />
          </section>

          {(doc.schedule ?? []).length > 0 ? (
            <section style={{ marginTop: 'var(--sp-5)' }}>
              <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-3)' }}>{t('contracts.doc.payments')}</h3>
              <div className="doc-scroll-x">
              <table className="ledger">
                <thead>
                  <tr>
                    <th>{t('contracts.schedule.col.period')}</th>
                    <th>{t('contracts.schedule.col.due')}</th>
                    <th className="num">{t('common.amount')}</th>
                  </tr>
                </thead>
                <tbody>
                  {doc.schedule.map((s, i) => (
                    <tr key={`${s.due_date}-${i}`}>
                      <td style={{ fontSize: 'var(--text-sm)' }}>
                        {fmtDate(s.period_start)} → {fmtDate(s.period_end)}
                      </td>
                      <td>{fmtDate(s.due_date)}</td>
                      <td className="num">{fmtTZS(s.amount)}</td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td colSpan={2}>{t('common.total')}</td>
                    <td className="num">{fmtTZS(doc.schedule.reduce((t, s) => t + (s.amount ?? 0), 0))}</td>
                  </tr>
                </tbody>
              </table>
              </div>
            </section>
          ) : null}

          <section style={{ marginTop: 'var(--sp-6)' }}>
            <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-4)' }}>{t('contracts.doc.signatures')}</h3>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-6)' }}>
              <SignatureCard
                label={t('common.renter')}
                signature={(doc.signatures ?? []).find((s) => s.party === 'renter') ?? signedByRenter}
              />
              <SignatureCard
                label={t('contracts.doc.landlord')}
                signature={(doc.signatures ?? []).find((s) => s.party === 'landlord') ?? signedByLandlord}
              />
            </div>
          </section>

          <section style={{ marginTop: 'var(--sp-6)' }}>
            <hr className="rule" />
            <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
              {t('contracts.verify.hash')}{' '}
              <span className="num" style={{ wordBreak: 'break-all', whiteSpace: 'normal' }}>
                {doc.snapshot_hash ?? contract.snapshot_hash}
              </span>
            </p>
            <div className="no-print" style={{ marginTop: 'var(--sp-3)', display: 'flex', gap: 'var(--sp-3)', alignItems: 'center', flexWrap: 'wrap' }}>
              <button type="button" className="btn btn-quiet" onClick={() => void verify()} disabled={verifying}>
                <Icon icon="solar:shield-check-linear" width={20} /> {verifying ? t('contracts.verify.checking') : t('contracts.verify.action')}
              </button>
              {verification ? (
                verification.valid ? (
                  <span className="stamp stamp-paid">{t('contracts.verify.valid')}</span>
                ) : (
                  <span className="stamp stamp-overdue">{t('contracts.verify.invalid')}</span>
                )
              ) : null}
            </div>
          </section>
        </DocumentPaper>
      ) : (
        <p className="no-print" style={{ color: 'var(--ink-soft)' }}>
          {t('contracts.doc.unavailable')}
        </p>
      )}

      <RecordPaymentSheet
        open={recordTarget !== null}
        target={recordTarget}
        onClose={() => setRecordTarget(null)}
        onRecorded={() => void load()}
      />

      <BackfillSheet
        open={backfillOpen}
        target={{ contractId: id, label: contractLabel, schedules: schedules ?? undefined }}
        onClose={() => setBackfillOpen(false)}
        onDone={() => void load()}
      />

      <CorrectPaymentSheet
        payment={correcting}
        onClose={() => setCorrecting(null)}
        onDone={() => {
          setCorrecting(null);
          setNote(t('payments.correct.done'));
          void load();
        }}
      />

      <ReverseSheet
        payment={reversing}
        busy={reverseBusy}
        error={reverseError}
        onClose={() => setReversing(null)}
        onSubmit={(reason) => void reverse(reason)}
      />

      <Sheet
        open={behalfOpen}
        title={t('contracts.behalf.title')}
        onClose={() => setBehalfOpen(false)}
        width={520}
      >
        <RecordOnBehalfForm
          renterName={renterName}
          busy={busy}
          error={actionError}
          onCancel={() => setBehalfOpen(false)}
          onSubmit={(reason) =>
            void act(
              () => contractsApi.activate(id, { landlord_recorded: true, reason }),
              t('contracts.behalf.done'),
            )
          }
        />
      </Sheet>

      <Sheet open={writeOffOpen} title={t('contracts.writeoff.title')} onClose={() => setWriteOffOpen(false)} width={520}>
        <WriteOffForm
          amount={fmtTZS(owingTotal)}
          periods={t.n('contracts.arrears.periods', owing.length)}
          busy={busy}
          error={actionError}
          onCancel={() => setWriteOffOpen(false)}
          onSubmit={(reason) =>
            void act(() => contractsApi.writeOff(id, reason), t('contracts.writeoff.done'))
          }
        />
      </Sheet>

      <Sheet
        open={amendMode !== null}
        title={amendMode === 'renew' ? t('contracts.amend.title_renew') : t('contracts.amend.title')}
        onClose={() => setAmendMode(null)}
        width={640}
      >
        {amendMode ? (
          <AmendForm
            contract={contract}
            schedules={rows}
            mode={amendMode}
            onCancel={() => setAmendMode(null)}
            onDone={(created) => {
              setAmendMode(null);
              router.push(`/contracts/${created.id}`);
            }}
          />
        ) : null}
      </Sheet>

      <Sheet open={reissueOpen} title={t('contracts.reissue.title')} onClose={() => setReissueOpen(false)} width={520}>
        <ReissueForm
          contract={contract}
          onCancel={() => setReissueOpen(false)}
          onDone={(created) => {
            setReissueOpen(false);
            router.push(`/contracts/${created.id}`);
          }}
        />
      </Sheet>

      <Sheet open={reliefFor !== null} title={t('relief.title')} onClose={() => setReliefFor(null)} width={480}>
        {reliefFor ? (
          <ReliefForm
            schedule={reliefFor}
            onCancel={() => setReliefFor(null)}
            onDone={() => {
              setReliefFor(null);
              setNote(t('relief.done'));
              void load();
            }}
          />
        ) : null}
      </Sheet>

      <Sheet open={noticeOpen} title={t('notice.record')} onClose={() => setNoticeOpen(false)} width={480}>
        {noticeOpen ? (
          <RecordNoticeForm
            contract={contract}
            onCancel={() => setNoticeOpen(false)}
            onDone={(c) => {
              setNoticeOpen(false);
              setContract(c);
              setNote(t('notice.recorded'));
            }}
          />
        ) : null}
      </Sheet>

      <Sheet open={terminateOpen} title={t('contracts.terminate.title')} onClose={() => setTerminateOpen(false)} width={520}>
        <TerminateForm
          key={terminateDate ?? 'today'}
          contractId={id}
          settles={running}
          initialDate={terminateDate}
          busy={busy}
          error={actionError}
          onCancel={() => setTerminateOpen(false)}
          onSubmit={(body) =>
            void act(
              () => contractsApi.terminate(id, body),
              'Terminated. The unit is back on the vacancy board and the renter has been notified.',
            )
          }
        />
      </Sheet>
    </>
  );
}

export default function ContractPage() {
  const params = useParams<{ id: string }>();
  const id = String(params?.id ?? '');
  return (
    <>
      <ContractBody id={id} />
    </>
  );
}
