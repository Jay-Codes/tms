'use client';

/**
 * "Backfill history" (Phase 20.3).
 *
 * A landlord who joined TMS mid-tenancy puts the real move-in date on the
 * contract, so the generator writes every period since — and all the old ones
 * come out `overdue`, because as far as the book knows nobody ever closed them.
 * This sheet closes them in one call: either the money was paid (one payment per
 * period, dated on the period's own due date unless the landlord says otherwise)
 * or it is written off (the rows become `waived` with the reason).
 *
 * The preview is **display math on rows the server already sent** — a count and
 * a sum over the loaded schedule, nothing more. Which rows qualify, what each
 * one owes and what the total is are the server's answer, and the result panel
 * prints its numbers, not these.
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  PAYMENT_METHODS,
  contractsApi,
  isUnsettled,
  remainingOn,
  toApiError,
  type BackfillInput,
  type BackfillResult,
  type PaymentMethod,
  type ScheduleRow,
} from '../lib/api';
import { fmtTZS, todayISO } from '../lib/format';

/** Everything the sheet needs about this opening of it. */
export interface BackfillTarget {
  contractId: string;
  /** "Room 2 · Mbezi Court — Asha Juma", printed at the head of the sheet. */
  label?: string;
  /**
   * The rows the caller already has. When a caller has none (the Overdue list
   * only holds one row of the contract), the sheet reads them itself.
   */
  schedules?: ScheduleRow[];
}

/** Unsettled and already past its due date — the rows a backfill is for. */
export function needsBackfill(rows: ScheduleRow[] | null | undefined): boolean {
  const today = todayISO();
  return (rows ?? []).some((s) => isUnsettled(s) && s.due_date < today);
}

/**
 * The last period that has fully finished before today — the truthful default
 * for "settled up to", because the period we are standing in is not history yet.
 */
function defaultUntil(rows: ScheduleRow[]): string {
  const today = todayISO();
  const ended = rows.map((s) => s.period_end).filter((d) => d && d < today);
  if (ended.length > 0) return ended.reduce((a, b) => (a >= b ? a : b));
  const due = rows.map((s) => s.due_date).filter((d) => d && d < today);
  if (due.length > 0) return due.reduce((a, b) => (a >= b ? a : b));
  return today;
}

export function BackfillSheet({
  open,
  target,
  onClose,
  onDone,
}: {
  open: boolean;
  target: BackfillTarget | null;
  onClose: () => void;
  /** Fired once the server has answered, so the caller can refresh its ledger. */
  onDone: (result: BackfillResult) => void;
}) {
  const t = useT();
  const [rows, setRows] = useState<ScheduleRow[] | null>(null);
  const [until, setUntil] = useState(todayISO());
  const [mode, setMode] = useState<'paid' | 'waived'>('paid');
  const [method, setMethod] = useState<PaymentMethod>('cash');
  const [dateMode, setDateMode] = useState<'due_date' | 'fixed'>('due_date');
  const [paidAt, setPaidAt] = useState(todayISO());
  const [reference, setReference] = useState('');
  const [note, setNote] = useState('');

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [result, setResult] = useState<BackfillResult | null>(null);
  /** The landlord moved the date; stop re-deriving it from the rows. */
  const [untilTouched, setUntilTouched] = useState(false);

  const contractId = target?.contractId ?? '';
  const given = target?.schedules;

  // Reset on every open, so a second backfill never inherits the first's boxes.
  useEffect(() => {
    if (!open) return;
    setRows(given && given.length > 0 ? given : null);
    setMode('paid');
    setMethod('cash');
    setDateMode('due_date');
    setPaidAt(todayISO());
    setReference('');
    setNote('');
    setError(null);
    setResult(null);
    setUntilTouched(false);
  }, [open, contractId, given]);

  // Only read the ledger when the caller had none to hand.
  useEffect(() => {
    if (!open || !contractId || (given && given.length > 0)) return;
    const ac = new AbortController();
    contractsApi
      .schedules(contractId, ac.signal)
      .then((r) => setRows(r.items ?? []))
      .catch((e) => {
        if (!(e instanceof DOMException)) setRows([]);
      });
    return () => ac.abort();
  }, [open, contractId, given]);

  useEffect(() => {
    if (!rows || untilTouched) return;
    setUntil(defaultUntil(rows));
  }, [rows, untilTouched]);

  const qualifying = (rows ?? []).filter((s) => isUnsettled(s) && s.due_date <= until);
  const previewCount = qualifying.length;
  const previewTotal = qualifying.reduce((sum, s) => sum + remainingOn(s), 0);

  const send = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const body: BackfillInput = { until, mode };
      if (mode === 'paid') {
        body.method = method;
        body.paid_at = dateMode === 'due_date' ? 'due_date' : paidAt;
        if (reference.trim()) body.reference = reference.trim();
        if (note.trim()) body.note = note.trim();
      } else {
        body.note = note.trim();
      }
      const res = await contractsApi.backfill(contractId, body);
      setResult(res);
      onDone(res);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  }, [contractId, dateMode, mode, method, note, onDone, paidAt, reference, until]);

  const ready =
    contractId !== '' && until !== '' && (mode === 'paid' ? true : note.trim().length > 0);

  return (
    <Sheet open={open} title={t('backfill.title')} onClose={onClose} width={560}>
      {target?.label ? (
        <p style={{ marginBottom: 'var(--sp-4)', color: 'var(--ink-soft)' }}>{target.label}</p>
      ) : null}

      {result ? (
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <p
            role="status"
            style={{
              display: 'flex',
              gap: 'var(--sp-3)',
              alignItems: 'center',
              color: 'var(--stamp-paid)',
              border: '1px solid var(--stamp-paid)',
              borderRadius: 'var(--radius-sm)',
              padding: 'var(--sp-3) var(--sp-4)',
            }}
          >
            <Icon icon="solar:check-circle-linear" width={20} />
            <span>
              {t('backfill.done', {
                settled: result.settled,
                amount: fmtTZS(result.total),
                skipped: result.skipped,
              })}
            </span>
          </p>
          <p style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>{t('backfill.sms_note')}</p>
          <div>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              {t('common.done')}
            </button>
          </div>
        </div>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void send();
          }}
          style={{ display: 'grid', gap: 'var(--sp-4)' }}
          noValidate
        >
          <ProblemNote error={error} />
          <p style={{ color: 'var(--ink-soft)' }}>{t('backfill.lead')}</p>

          <Field
            id="bf_until"
            label={t('backfill.until')}
            hint={t('backfill.until.hint')}
            error={error?.errors.until}
          >
            <input
              id="bf_until"
              className="input"
              type="date"
              value={until}
              max={todayISO()}
              onChange={(e) => {
                setUntil(e.target.value);
                setUntilTouched(true);
              }}
            />
          </Field>

          <div className="field">
            <label htmlFor="bf_mode">{t('backfill.mode')}</label>
            {/* `.field` is a grid, so the inline control needs `justifySelf`
                or it stretches into something that reads like a text box. */}
            <div className="segmented" role="group" aria-label={t('backfill.mode')} style={{ justifySelf: 'start' }}>
              <button
                type="button"
                id="bf_mode"
                aria-pressed={mode === 'paid'}
                onClick={() => setMode('paid')}
              >
                {t('backfill.mode.paid')}
              </button>
              <button type="button" aria-pressed={mode === 'waived'} onClick={() => setMode('waived')}>
                {t('backfill.mode.waived')}
              </button>
            </div>
          </div>

          {mode === 'paid' ? (
            <>
              <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
                <Field id="bf_method" label={t('backfill.method')} error={error?.errors.method}>
                  <select
                    id="bf_method"
                    className="input"
                    value={method}
                    onChange={(e) => setMethod(e.target.value as PaymentMethod)}
                  >
                    {PAYMENT_METHODS.map((m) => (
                      <option key={m.value} value={m.value}>
                        {t(`payments.method.${m.value}`)}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  id="bf_date_mode"
                  label={t('backfill.paid_at')}
                  hint={t('backfill.paid_at.hint')}
                  error={error?.errors.paid_at}
                >
                  <select
                    id="bf_date_mode"
                    className="input"
                    value={dateMode}
                    onChange={(e) => setDateMode(e.target.value as 'due_date' | 'fixed')}
                  >
                    <option value="due_date">{t('backfill.paid_at.due_date')}</option>
                    <option value="fixed">{t('backfill.paid_at.fixed')}</option>
                  </select>
                </Field>
              </div>

              {dateMode === 'fixed' ? (
                <Field id="bf_paid_at" label={t('backfill.paid_at.fixed')} error={error?.errors.paid_at}>
                  <input
                    id="bf_paid_at"
                    className="input"
                    type="date"
                    value={paidAt}
                    max={todayISO()}
                    onChange={(e) => setPaidAt(e.target.value)}
                  />
                </Field>
              ) : null}

              <Field
                id="bf_reference"
                label={t('backfill.reference')}
                hint={t('backfill.reference.hint')}
                error={error?.errors.reference}
              >
                <input
                  id="bf_reference"
                  className="input"
                  maxLength={80}
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                />
              </Field>
            </>
          ) : null}

          <Field
            id="bf_note"
            label={mode === 'waived' ? t('backfill.note.required') : t('backfill.note')}
            hint={t('backfill.note.hint', { n: note.length })}
            error={error?.errors.note}
          >
            <textarea
              id="bf_note"
              className="input"
              rows={2}
              maxLength={500}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </Field>

          <div
            style={{
              display: 'grid',
              gap: 'var(--sp-2)',
              padding: 'var(--sp-3) var(--sp-4)',
              border: '1px solid var(--rule-strong)',
              borderRadius: 'var(--radius-sm)',
            }}
          >
            <strong>
              {previewCount === 0
                ? t('backfill.preview.none')
                : t('backfill.preview', { count: previewCount, amount: fmtTZS(previewTotal) })}
            </strong>
            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
              {t('backfill.preview.note')}
            </span>
          </div>

          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button type="submit" className="btn btn-primary" disabled={busy || !ready}>
              {busy ? t('backfill.busy') : t('backfill.submit')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={onClose} disabled={busy}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      )}
    </Sheet>
  );
}
