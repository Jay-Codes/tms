'use client';

/**
 * "Backfills" on the contract page (Phase 26).
 *
 * Every backfill is one decision, listed once — the contract page's button or a
 * line of a `backfill` CSV import — with an Undo that takes the whole decision
 * back: the payments it wrote are reversed through the ordinary path and the
 * periods it waived are reopened. Whether an undo is still possible is the
 * server's answer (`can_undo`, `touched`), printed as it came.
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useState } from 'react';
import { TableScroll, useT } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import { ApiError, backfillsApi, toApiError, type BackfillBatch } from '../lib/api';
import { fmtDate, fmtTZS } from '../lib/format';

/** Problem `type`s the undo answers with that this list has words for. */
const UNDO_CODES: Record<string, string> = {
  touched_since: 'backfills.err.touched_since',
  already_undone: 'backfills.err.already_undone',
  payment_refunded: 'backfills.err.payment_refunded',
};

export function BackfillsList({
  contractId,
  refreshKey,
  onChanged,
}: {
  contractId: string;
  /** Anything that changes when the page reloads its ledger, so a new backfill shows up. */
  refreshKey?: unknown;
  /** Fired after an undo, so the page can redraw the schedule and the payments. */
  onChanged: () => void;
}) {
  const t = useT();
  const [items, setItems] = useState<BackfillBatch[] | null>(null);
  const [undoing, setUndoing] = useState<BackfillBatch | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const res = await backfillsApi.list(contractId, signal);
        setItems(res.items ?? []);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setItems([]);
      }
    },
    [contractId],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load, refreshKey]);

  const open = (b: BackfillBatch) => {
    setReason('');
    setError(null);
    setUndoing(b);
  };

  const submit = async () => {
    if (!undoing) return;
    setBusy(true);
    setError(null);
    try {
      const res = await backfillsApi.undo(undoing.id, reason.trim());
      setUndoing(null);
      setDone(
        t('backfills.undo.done', { reversed: res.payments_reversed, reopened: res.periods_reopened }) +
          (res.periods_removed ? ` ${t('backfills.undo.done_removed', { n: res.periods_removed })}` : ''),
      );
      await load();
      onChanged();
    } catch (e) {
      const err = toApiError(e);
      const key = UNDO_CODES[err.code];
      setError(key ? new ApiError(err.status, { type: err.code, detail: t(key) }) : err);
    } finally {
      setBusy(false);
    }
  };

  // Nothing was ever backfilled: the section would only be an empty heading.
  if (!items || items.length === 0) return null;

  return (
    <section style={{ marginTop: 'var(--sp-6)' }}>
      <hr className="rule rule-strong" />
      <div style={{ margin: 'var(--sp-4) 0' }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('backfills.title')}</h2>
        <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('backfills.lead')}
        </p>
      </div>
      {done ? (
        <p role="status" style={{ marginBottom: 'var(--sp-3)', color: 'var(--stamp-paid)' }}>
          {done}
        </p>
      ) : null}
      <TableScroll label={t('backfills.title')}>
        <table className="ledger">
          <thead>
            <tr>
              <th>{t('backfills.col.date')}</th>
              <th>{t('backfills.col.mode')}</th>
              <th>{t('backfills.col.until')}</th>
              <th className="num">{t('backfills.col.periods')}</th>
              <th className="num">{t('backfills.col.amount')}</th>
              <th>{t('backfills.col.by')}</th>
              <th aria-label={t('common.actions')} />
            </tr>
          </thead>
          <tbody>
            {items.map((b) => (
              <tr key={b.id} style={b.undone_at ? { color: 'var(--ink-faint)' } : undefined}>
                <td>
                  {fmtDate(b.created_at)}
                  {b.import_batch_id ? (
                    <span style={{ display: 'block', fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                      {t('backfills.from_import')}
                    </span>
                  ) : null}
                </td>
                <td>{t(`backfill.mode.${b.mode}`)}</td>
                <td>
                  {fmtDate(b.until)}
                  {b.from ? (
                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                      {t('backfills.from', { date: fmtDate(b.from), n: b.created_periods ?? 0 })}
                    </div>
                  ) : null}
                </td>
                <td className="num">{b.periods}</td>
                <td className="num">{fmtTZS(b.amount)}</td>
                <td>{b.created_by?.name || t('import.history.someone')}</td>
                <td style={{ textAlign: 'right' }}>
                  {b.undone_at ? (
                    <span style={{ fontSize: 'var(--text-sm)' }} title={b.undo_reason ?? undefined}>
                      {t('backfills.undone', {
                        when: fmtDate(b.undone_at),
                        who: b.undone_by?.name || t('import.history.someone'),
                      })}
                    </span>
                  ) : b.can_undo ? (
                    <button type="button" className="btn btn-quiet" onClick={() => open(b)}>
                      <Icon icon="solar:undo-left-linear" width={18} /> {t('backfills.undo.action')}
                    </button>
                  ) : (
                    <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      {t('backfills.touched')}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Sheet open={undoing !== null} title={t('backfills.undo.title')} onClose={() => setUndoing(null)} width={520}>
        {undoing ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
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
              <span>
                {undoing.mode === 'paid'
                  ? t('backfills.undo.warning_paid', { count: undoing.periods, amount: fmtTZS(undoing.amount) })
                  : t('backfills.undo.warning_waived', { count: undoing.periods })}
                {undoing.created_periods
                  ? ` ${t('backfills.undo.warning_created', { n: undoing.created_periods })}`
                  : ''}
              </span>
            </p>
            <Field
              id="bfu_reason"
              label={t('backfills.undo.reason')}
              hint={`${reason.length}/200`}
              error={error?.errors.reason}
            >
              <textarea
                id="bfu_reason"
                className="input"
                rows={3}
                maxLength={200}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </Field>
            <p style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>{t('backfills.undo.no_sms')}</p>
            <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
              <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
                {busy ? t('backfills.undo.busy') : t('backfills.undo.submit')}
              </button>
              <button type="button" className="btn btn-quiet" onClick={() => setUndoing(null)} disabled={busy}>
                {t('common.cancel')}
              </button>
            </div>
          </form>
        ) : null}
      </Sheet>
    </section>
  );
}
