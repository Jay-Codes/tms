'use client';

/**
 * Phase 22.5 (rest) — when a tenancy does not go to plan: relief on one
 * period, a notice to leave, tenancies that ran out with nobody saying what
 * happened, and eviction as stages with letters. The backend decides every
 * rule and figure (who may, how much, which dates); these pieces collect the
 * landlord's choices and put the answers in plain words.
 */

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { LOCALE_LABELS, TableScroll, useT, type Locale } from '@tms/ui';
import { AmendForm } from './AmendForm';
import { Field, Note, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  contractsApi,
  evictionsApi,
  noticeEarliest,
  schedulesApi,
  toApiError,
  unwrapContract,
  type Contract,
  type EvictionCase,
  type Holdover,
  type ScheduleRow,
} from '../lib/api';
import { fmtDate, fmtTZS, isoPlusDays, todayISO } from '../lib/format';

/* -------------------------------- relief --------------------------------- */

/** "Waived: reason" / "Discounted from TZS X: reason" under an adjusted row. */
export function adjustmentText(t: ReturnType<typeof useT>, s: ScheduleRow): string | null {
  if (!s.adjustment_kind) return null;
  const reason = s.adjustment_reason ?? '';
  return s.adjustment_kind === 'waive'
    ? t('relief.waived', { reason })
    : t('relief.discounted', { amount: fmtTZS(s.original_amount ?? s.amount), reason });
}

/** Waive one period, or lower it; the reason is required and audited. */
export function ReliefForm({
  schedule,
  onDone,
  onCancel,
}: {
  schedule: ScheduleRow;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [kind, setKind] = useState<'waive' | 'discount'>('discount');
  const [discount, setDiscount] = useState('');
  const [reason, setReason] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await schedulesApi.adjust(schedule.id, {
        kind,
        discount: kind === 'discount' ? Math.round(Number(discount)) : undefined,
        reason: reason.trim(),
      });
      onDone();
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>
        {t('relief.lead', {
          period: `${fmtDate(schedule.period_start)} → ${fmtDate(schedule.period_end)}`,
          amount: fmtTZS(schedule.amount),
        })}
      </p>
      <fieldset style={{ border: 0, margin: 0, padding: 0, display: 'grid', gap: 'var(--sp-2)' }}>
        <legend style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('relief.kind')}</legend>
        {(['discount', 'waive'] as const).map((k) => (
          <label
            key={k}
            htmlFor={`rl_${k}`}
            style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)', cursor: 'pointer' }}
          >
            <input
              id={`rl_${k}`}
              type="radio"
              name="rl_kind"
              checked={kind === k}
              onChange={() => setKind(k)}
              style={{ width: 18, height: 18 }}
            />
            {t(`relief.kind.${k}`)}
          </label>
        ))}
      </fieldset>
      {kind === 'discount' ? (
        <Field
          id="rl_discount"
          label={t('relief.discount')}
          hint={
            discount
              ? t('relief.discount_hint', { amount: fmtTZS(Math.max(0, schedule.amount - Number(discount))) })
              : undefined
          }
          error={error?.errors.discount}
        >
          <input
            id="rl_discount"
            className="input num"
            type="number"
            min={1}
            max={Math.max(1, schedule.amount - 1)}
            step={1}
            inputMode="numeric"
            value={discount}
            onChange={(e) => setDiscount(e.target.value)}
          />
        </Field>
      ) : (
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('relief.waive_note')}</p>
      )}
      <Field
        id="rl_reason"
        label={t('relief.reason')}
        hint={t('contracts.chars_max', { n: reason.length })}
        error={error?.errors.reason}
      >
        <textarea
          id="rl_reason"
          className="input"
          rows={2}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('relief.reason_ph')}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button
          type="submit"
          className="btn btn-primary"
          disabled={busy || !reason.trim() || (kind === 'discount' && !(Number(discount) > 0))}
        >
          {busy ? t('common.saving') : t('relief.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/* ------------------------------ notice to leave --------------------------- */

/** A notice the renter gave in person, recorded by the landlord. */
export function RecordNoticeForm({
  contract,
  onDone,
  onCancel,
}: {
  contract: Contract;
  onDone: (c: Contract) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const minDays = contract.policy?.tenant_notice_days ?? 0;
  const [leaveOn, setLeaveOn] = useState(isoPlusDays(minDays));
  const [reason, setReason] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const earliest = noticeEarliest(error);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onDone(unwrapContract(await contractsApi.giveNotice(contract.id, { leave_on: leaveOn, reason: reason.trim() || undefined })));
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>
        {minDays ? t('notice.record.lead_days', { days: minDays }) : t('notice.record.lead')}
      </p>
      <Field
        id="nt_leave_on"
        label={t('notice.leave_on')}
        hint={earliest ? undefined : t('notice.before_end', { date: fmtDate(contract.end_date) })}
        error={earliest ? t('notice.earliest', { date: fmtDate(earliest) }) : error?.errors.leave_on}
      >
        <input
          id="nt_leave_on"
          className="input"
          type="date"
          value={leaveOn}
          max={contract.end_date}
          onChange={(e) => setLeaveOn(e.target.value)}
        />
      </Field>
      {earliest ? (
        <div>
          <button type="button" className="btn btn-quiet" onClick={() => setLeaveOn(earliest)}>
            {t('notice.use_earliest', { date: fmtDate(earliest) })}
          </button>
        </div>
      ) : null}
      <Field id="nt_reason" label={t('notice.reason')} error={error?.errors.reason}>
        <textarea
          id="nt_reason"
          className="input"
          rows={2}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy || !leaveOn}>
          {busy ? t('common.saving') : t('notice.record.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/* -------------------------------- eviction -------------------------------- */

/**
 * Opens the letter in a window of its own and prints it. The window is opened
 * before the request so a popup blocker sees the click; the HTML is the
 * backend's letter as sent, with just enough CSS to print cleanly.
 */
async function printLetter(caseId: string, kind: 'demand' | 'notice', lang: Locale, onError: (e: ApiError) => void) {
  const w = window.open('', '_blank');
  try {
    const letter = await evictionsApi.letter(caseId, kind, lang);
    if (!w) return;
    w.document.open();
    w.document.write(
      `<!doctype html><html lang="${lang}"><head><meta charset="utf-8"><title>${kind}</title>` +
        '<style>body{font-family:Georgia,serif;max-width:720px;margin:32px auto;padding:0 16px;color:#111;line-height:1.5}' +
        'table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #ccc;padding:4px 6px;text-align:left}' +
        '@media print{body{margin:0 auto}}</style></head><body>' +
        letter.html +
        '</body></html>',
    );
    w.document.close();
    w.focus();
    w.print();
  } catch (e) {
    w?.close();
    onError(toApiError(e));
  }
}

function StageStep({ done, label, detail }: { done: boolean; label: string; detail: string | null }) {
  return (
    <li style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'baseline' }}>
      <span
        aria-hidden
        style={{
          width: 12,
          height: 12,
          borderRadius: '50%',
          flex: '0 0 auto',
          border: '2px solid var(--primary)',
          background: done ? 'var(--primary)' : 'transparent',
        }}
      />
      <span>
        <span style={{ fontWeight: done ? 600 : 400 }}>{label}</span>
        {detail ? <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}> — {detail}</span> : null}
      </span>
    </li>
  );
}

/**
 * The contract page's "Eviction" section. `hasOverdue` comes from the
 * contract's schedules summary, so the demand button can say why it is off
 * before the landlord tries; the backend's 409 `no_arrears` is the final word.
 */
export function EvictionSection({
  contractId,
  running,
  hasOverdue,
}: {
  contractId: string;
  running: boolean;
  hasOverdue: boolean;
}) {
  const t = useT();
  const [open, setOpen] = useState<EvictionCase | null>(null);
  const [history, setHistory] = useState<EvictionCase[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [sheet, setSheet] = useState<'none' | 'demand' | 'withdraw'>('none');
  const [payBy, setPayBy] = useState(isoPlusDays(7));
  const [reason, setReason] = useState('');
  const [sheetError, setSheetError] = useState<ApiError | null>(null);
  const [lang, setLang] = useState<Locale>('sw');
  const [noArrears, setNoArrears] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const res = await evictionsApi.forContract(contractId, signal);
        setOpen(res.open);
        setHistory(res.history ?? []);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setHistory([]);
      }
    },
    [contractId],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const run = async (call: () => Promise<unknown>, done: string, inSheet: boolean) => {
    setBusy(true);
    setError(null);
    setSheetError(null);
    setNote(null);
    try {
      await call();
      setSheet('none');
      setNote(done);
      await load();
    } catch (e) {
      const err = toApiError(e);
      if (err.code === 'no_arrears') setNoArrears(true);
      if (inSheet) setSheetError(err);
      else setError(err);
    } finally {
      setBusy(false);
    }
  };

  const demandOff = !hasOverdue || noArrears;

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
      <ProblemNote error={error} />
      {note ? <Note>{note}</Note> : null}

      {open ? (
        <>
          <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 'var(--sp-2)' }}>
            <StageStep
              done
              label={t('eviction.step.demand')}
              detail={t('eviction.step.demand_detail', {
                date: fmtDate(open.demand_issued_at),
                pay_by: fmtDate(open.pay_by),
              })}
            />
            <StageStep
              done={open.stage === 'notice'}
              label={t('eviction.step.notice')}
              detail={
                open.vacate_by
                  ? t('eviction.step.notice_detail', {
                      date: fmtDate(open.notice_issued_at),
                      vacate_by: fmtDate(open.vacate_by),
                    })
                  : t('eviction.step.notice_pending', { days: open.notice_days })
              }
            />
          </ol>
          <p>
            {t('eviction.arrears_now', { amount: fmtTZS(open.arrears_now ?? open.arrears_at_open) })}{' '}
            <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('eviction.arrears_at_open', { amount: fmtTZS(open.arrears_at_open) })}
            </span>
          </p>
          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
            {open.stage === 'demand' ? (
              <button
                type="button"
                className="btn btn-danger"
                disabled={busy}
                onClick={() => {
                  if (!window.confirm(t('eviction.notice_confirm', { days: open.notice_days }))) return;
                  void run(() => evictionsApi.notice(open.id), t('eviction.notice_done'), false);
                }}
              >
                {t('eviction.issue_notice')}
              </button>
            ) : null}
            <button
              type="button"
              className="btn btn-quiet"
              disabled={busy}
              onClick={() => {
                setReason('');
                setSheetError(null);
                setSheet('withdraw');
              }}
            >
              {t('eviction.withdraw')}
            </button>
          </div>
          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <Field id="ev_lang" label={t('eviction.letter_lang')}>
              <select id="ev_lang" className="input" value={lang} onChange={(e) => setLang(e.target.value as Locale)}>
                {(['sw', 'en'] as const).map((l) => (
                  <option key={l} value={l}>
                    {LOCALE_LABELS[l]}
                  </option>
                ))}
              </select>
            </Field>
            <button type="button" className="btn btn-secondary" onClick={() => void printLetter(open.id, 'demand', lang, setError)}>
              {t('eviction.print_demand')}
            </button>
            {open.stage === 'notice' ? (
              <button type="button" className="btn btn-secondary" onClick={() => void printLetter(open.id, 'notice', lang, setError)}>
                {t('eviction.print_notice')}
              </button>
            ) : null}
          </div>
        </>
      ) : running ? (
        <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
          <p style={{ color: 'var(--ink-soft)' }}>{t('eviction.none')}</p>
          <div>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy || demandOff}
              onClick={() => {
                setPayBy(isoPlusDays(7));
                setSheetError(null);
                setSheet('demand');
              }}
            >
              {t('eviction.send_demand')}
            </button>
          </div>
          {demandOff ? (
            <p style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>{t('eviction.no_arrears')}</p>
          ) : null}
        </div>
      ) : null}

      {history && history.length ? (
        <>
          <h3 style={{ fontSize: 'var(--text-md)' }}>{t('eviction.history')}</h3>
          <ul style={{ margin: 0, paddingLeft: '1.2em', display: 'grid', gap: 'var(--sp-1)', fontSize: 'var(--text-sm)' }}>
            {history.map((c) => (
              <li key={c.id}>
                {t(`eviction.stage.${c.stage}`)} · {t('eviction.history_line', {
                  opened: fmtDate(c.demand_issued_at),
                  closed: fmtDate(c.closed_at),
                  amount: fmtTZS(c.arrears_at_open),
                })}
                {c.close_reason ? <span style={{ color: 'var(--ink-soft)' }}> — {c.close_reason}</span> : null}
              </li>
            ))}
          </ul>
        </>
      ) : null}

      <Sheet open={sheet === 'demand'} title={t('eviction.send_demand')} onClose={() => setSheet('none')} width={480}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void run(() => evictionsApi.open(contractId, payBy || undefined), t('eviction.demand_done'), true);
          }}
          style={{ display: 'grid', gap: 'var(--sp-4)' }}
          noValidate
        >
          <ProblemNote error={sheetError} />
          <p style={{ color: 'var(--ink-soft)' }}>{t('eviction.demand_lead')}</p>
          <Field id="ev_pay_by" label={t('eviction.pay_by')} error={sheetError?.errors.pay_by}>
            <input
              id="ev_pay_by"
              className="input"
              type="date"
              min={todayISO()}
              value={payBy}
              onChange={(e) => setPayBy(e.target.value)}
            />
          </Field>
          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button type="submit" className="btn btn-danger" disabled={busy}>
              {busy ? t('common.saving') : t('eviction.demand_submit')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setSheet('none')} disabled={busy}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      </Sheet>

      <Sheet open={sheet === 'withdraw'} title={t('eviction.withdraw')} onClose={() => setSheet('none')} width={480}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (open) void run(() => evictionsApi.withdraw(open.id, reason.trim()), t('eviction.withdraw_done'), true);
          }}
          style={{ display: 'grid', gap: 'var(--sp-4)' }}
          noValidate
        >
          <ProblemNote error={sheetError} />
          <p style={{ color: 'var(--ink-soft)' }}>{t('eviction.withdraw_lead')}</p>
          <Field id="ev_reason" label={t('eviction.withdraw_reason')} error={sheetError?.errors.reason}>
            <textarea
              id="ev_reason"
              className="input"
              rows={2}
              maxLength={200}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t('eviction.withdraw_ph')}
            />
          </Field>
          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button type="submit" className="btn btn-primary" disabled={busy || !reason.trim()}>
              {busy ? t('common.saving') : t('eviction.withdraw')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setSheet('none')} disabled={busy}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      </Sheet>
    </div>
  );
}

/* ------------------------- contracts list: cards -------------------------- */

/** Loads one ended contract and offers the existing renewal sheet on it. */
function HoldoverRenew({ contractId, onDone, onCancel }: { contractId: string; onDone: (c: Contract) => void; onCancel: () => void }) {
  const t = useT();
  const [contract, setContract] = useState<Contract | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  useEffect(() => {
    const ac = new AbortController();
    contractsApi
      .get(contractId, ac.signal)
      .then((r) => setContract(unwrapContract(r)))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      });
    return () => ac.abort();
  }, [contractId]);
  if (error) return <ProblemNote error={error} />;
  if (!contract) return <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>;
  // An ended contract has no future periods; its end date is the only choice.
  return <AmendForm contract={contract} schedules={[]} mode="renew" onDone={onDone} onCancel={onCancel} />;
}

/**
 * "Tenancies that ran out — did the tenant leave?" Hidden when there are none.
 * `onRenewed` gets the new (unsigned) renewal so the page can open it.
 */
export function HoldoversCard({ onRenewed }: { onRenewed: (c: Contract) => void }) {
  const t = useT();
  const [items, setItems] = useState<Holdover[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [renewing, setRenewing] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      setItems((await contractsApi.holdovers(signal)).items ?? []);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setItems([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const movedOut = async (id: string) => {
    setBusy(id);
    setError(null);
    try {
      await contractsApi.movedOut(id);
      await load();
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(null);
    }
  };

  if (!items || items.length === 0) return null;

  return (
    <section className="sheet" style={{ padding: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-3)' }}>
      <h2 style={{ fontSize: 'var(--text-md)' }}>{t('holdover.title')}</h2>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('holdover.lead')}</p>
      <ProblemNote error={error} />
      <TableScroll label={t('holdover.title')}>
        <table className="ledger">
          <tbody>
            {items.map((h) => (
              <tr key={h.contract_id}>
                <td style={{ fontWeight: 600 }}>
                  <Link href={`/contracts/${h.contract_id}`} style={{ color: 'inherit' }}>
                    {h.unit_name}
                  </Link>
                  <div style={{ fontWeight: 400, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{h.property_name}</div>
                </td>
                <td>{h.renter.full_name}</td>
                <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                  {t('holdover.ended', { date: fmtDate(h.end_date) })}
                </td>
                <td>
                  <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', justifyContent: 'flex-end' }}>
                    <button
                      type="button"
                      className="btn btn-secondary"
                      style={{ minHeight: 36 }}
                      disabled={busy !== null}
                      onClick={() => void movedOut(h.contract_id)}
                    >
                      {busy === h.contract_id ? t('common.saving') : t('holdover.moved_out')}
                    </button>
                    <button
                      type="button"
                      className="btn btn-quiet"
                      style={{ minHeight: 36 }}
                      disabled={busy !== null}
                      onClick={() => setRenewing(h.contract_id)}
                    >
                      {t('contracts.amend.renew')}
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Sheet open={renewing !== null} title={t('contracts.amend.title_renew')} onClose={() => setRenewing(null)} width={640}>
        {renewing ? (
          <HoldoverRenew
            contractId={renewing}
            onCancel={() => setRenewing(null)}
            onDone={(c) => {
              setRenewing(null);
              onRenewed(c);
            }}
          />
        ) : null}
      </Sheet>
    </section>
  );
}

/** The org's open eviction cases, each linked to its contract. Hidden when none. */
export function EvictionsCard() {
  const t = useT();
  const [items, setItems] = useState<EvictionCase[] | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    evictionsApi
      .list(ac.signal)
      .then((r) => setItems(r.items ?? []))
      .catch(() => setItems([]));
    return () => ac.abort();
  }, []);

  if (!items || items.length === 0) return null;

  return (
    <section className="sheet" style={{ padding: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-3)' }}>
      <h2 style={{ fontSize: 'var(--text-md)' }}>{t.n('eviction.card_title', items.length)}</h2>
      <ul style={{ margin: 0, padding: 0, listStyle: 'none', display: 'grid', gap: 'var(--sp-2)' }}>
        {items.map((c) => (
          <li key={c.id} style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap', alignItems: 'baseline' }}>
            <Link href={`/contracts/${c.contract_id}`} style={{ fontWeight: 600, color: 'inherit' }}>
              {c.unit_name ?? '—'} · {c.renter_name ?? ''}
            </Link>
            <span className="stamp stamp-overdue">{t(`eviction.stage.${c.stage}`)}</span>
            <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {c.stage === 'notice' && c.vacate_by
                ? t('eviction.card_vacate', { date: fmtDate(c.vacate_by) })
                : t('eviction.card_pay_by', { date: fmtDate(c.pay_by) })}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
