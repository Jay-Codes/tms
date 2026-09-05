'use client';

/**
 * Shared marks and ledgers for the Phase 10 expense screens (FLOWS flow 12).
 *
 * The house rule from SPEC §2.0 holds here as it does for payments: what has
 * happened is stamped, what is merely pending is pencilled. An expense only
 * ever has two states — recorded (quiet, no stamp needed on every row) and
 * voided (stamped, struck through, never deleted).
 *
 * Everything in this module is a pure renderer: the caller owns the fetching,
 * so the same ledger serves `/expenses`, a property page and a detail screen.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import { type ApiError, type Expense, type ExpenseSummary } from '../lib/api';
import { fmtDate, fmtTZS } from '../lib/format';
import { TableScroll } from '@tms/ui';

/** Voided is the only state loud enough to stamp. */
export function ExpenseStatusStamp({ status }: { status: string }) {
  if (status === 'voided') return <span className="stamp stamp-overdue">Voided</span>;
  return null;
}

/** A paperclip where there is a receipt, nothing where there is not. */
export function ReceiptMark({ expense }: { expense: Expense }) {
  if (!expense.receipt?.present) return <span className="pencil">—</span>;
  const pdf = expense.receipt.content_type === 'application/pdf';
  return (
    <Icon
      icon={pdf ? 'solar:file-text-linear' : 'solar:paperclip-linear'}
      width={18}
      aria-label={pdf ? 'PDF receipt attached' : 'Receipt attached'}
      role="img"
    />
  );
}

/** "Mbezi Beach Block A · Room 2", the unit dropped when there is none. */
export function whereLabel(e: Expense): string {
  return e.unit?.name ? `${e.property?.name ?? '—'} · ${e.unit.name}` : (e.property?.name ?? '—');
}

function Loading({ cols, text }: { cols: number; text: string }) {
  return (
    <tr>
      <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
        {text}
      </td>
    </tr>
  );
}

/* -------------------------------- the ledger ------------------------------ */

export function ExpensesTable({
  items,
  loading,
  totals,
  emptyText = 'No expenses in this period.',
  showProperty = true,
  label = 'Expenses',
}: {
  items: Expense[] | null;
  loading?: boolean;
  /**
   * The server's totals for the whole filter, not just the page on screen.
   * Omitted (a property tab with everything already loaded) falls back to
   * summing the rows, which is then exactly the same number.
   */
  totals?: { count: number; amount: number } | null;
  emptyText?: string;
  showProperty?: boolean;
  label?: string;
}) {
  const rows = items ?? [];
  const cols = 6 + (showProperty ? 1 : 0);
  const fallback = {
    count: rows.filter((e) => e.status !== 'voided').length,
    amount: rows.filter((e) => e.status !== 'voided').reduce((t, e) => t + (e.amount ?? 0), 0),
  };
  const sum = totals ?? fallback;

  return (
    <TableScroll label={label}>
      <table className="ledger">
        <thead>
          <tr>
            <th>Date</th>
            {showProperty ? <th>Where</th> : null}
            <th>Category</th>
            <th>Vendor</th>
            <th className="num">Amount</th>
            <th>Receipt</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {items === null || loading ? (
            <Loading cols={cols} text="Loading…" />
          ) : rows.length === 0 ? (
            <Loading cols={cols} text={emptyText} />
          ) : (
            <>
              {rows.map((e) => {
                const voided = e.status === 'voided';
                return (
                  <tr key={e.id} style={voided ? { color: 'var(--ink-soft)' } : undefined}>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      <Link href={`/expenses/${e.id}`} style={{ color: 'inherit' }}>
                        {fmtDate(e.incurred_on)}
                      </Link>
                    </td>
                    {showProperty ? (
                      <td>
                        {e.property?.id ? (
                          <Link href={`/properties/${e.property.id}`} style={{ color: 'inherit' }}>
                            {e.property.name}
                          </Link>
                        ) : (
                          (e.property?.name ?? '—')
                        )}
                        {e.unit?.name ? (
                          <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                            {e.unit.name}
                          </div>
                        ) : null}
                      </td>
                    ) : (
                      null
                    )}
                    <td>{e.category?.name ?? <span className="pencil">uncategorised</span>}</td>
                    <td>
                      {e.vendor || <span className="pencil">—</span>}
                      {e.reference ? (
                        <div className="num" style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                          {e.reference}
                        </div>
                      ) : null}
                    </td>
                    <td className="num" style={voided ? { textDecoration: 'line-through' } : undefined}>
                      {fmtTZS(e.amount)}
                    </td>
                    <td>
                      <ReceiptMark expense={e} />
                    </td>
                    <td style={{ whiteSpace: 'nowrap' }}>
                      {voided ? <ExpenseStatusStamp status={e.status} /> : null}
                    </td>
                  </tr>
                );
              })}
              <tr className="total">
                <td colSpan={cols - 3}>
                  Total{' '}
                  <span style={{ fontWeight: 400, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    ({sum.count} expense{sum.count === 1 ? '' : 's'})
                  </span>
                </td>
                <td className="num">{fmtTZS(sum.amount)}</td>
                <td colSpan={2} />
              </tr>
            </>
          )}
        </tbody>
      </table>
    </TableScroll>
  );
}

/* ------------------------------- the summary ------------------------------ */

/** "+18%" / "−4%" / "—" when there is nothing to compare against. */
export function ChangeMark({ pct }: { pct: number | null | undefined }) {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) {
    return (
      <span className="pencil" title="Nothing was spent in the previous period.">
        no comparison
      </span>
    );
  }
  const rounded = Math.round(pct);
  const up = rounded > 0;
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 'var(--sp-1, 4px)',
        // Spending more is not automatically bad, so this is not a stamp
        // colour — it is a direction, drawn quietly.
        color: rounded === 0 ? 'var(--ink-soft)' : up ? 'var(--stamp-overdue)' : 'var(--stamp-paid)',
        fontSize: 'var(--text-sm)',
        fontWeight: 600,
      }}
    >
      <Icon
        icon={rounded === 0 ? 'solar:minus-circle-linear' : up ? 'solar:arrow-right-up-linear' : 'solar:arrow-right-down-linear'}
        width={16}
        aria-hidden
      />
      {rounded > 0 ? '+' : ''}
      {rounded}%
    </span>
  );
}

/**
 * The strip above the ledger: what this window cost, what the one before it
 * cost, and where the money went. The grouping toggle is client-side only in
 * the sense that it re-asks the server with a different `group_by` — nothing
 * is re-aggregated here.
 */
export function ExpenseSummaryStrip({
  summary,
  groupBy,
  onGroupBy,
  loading,
}: {
  summary: ExpenseSummary | null;
  groupBy: 'category' | 'property';
  onGroupBy: (g: 'category' | 'property') => void;
  loading?: boolean;
}) {
  const top = (summary?.groups ?? []).slice().sort((a, b) => b.amount - a.amount).slice(0, 5);
  const total = summary?.total?.amount ?? 0;

  return (
    <section
      style={{
        display: 'grid',
        gap: 'var(--sp-5)',
        gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))',
        borderTop: '1px solid var(--rule-strong)',
        paddingTop: 'var(--sp-4)',
      }}
      aria-label="Expense summary"
    >
      <div style={{ display: 'grid', gap: 'var(--sp-2)', alignContent: 'start' }}>
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>Spent this period</span>
        <strong style={{ fontSize: 'var(--text-2xl)', fontVariantNumeric: 'tabular-nums lining-nums' }}>
          {loading && !summary ? '—' : fmtTZS(total)}
        </strong>
        <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          <ChangeMark pct={summary?.change_pct} />
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            vs {summary ? fmtTZS(summary.previous_total?.amount ?? 0) : '—'} previously
          </span>
        </span>
      </div>

      <div style={{ display: 'grid', gap: 'var(--sp-3)', alignContent: 'start' }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--sp-3)' }}>
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {groupBy === 'category' ? 'By category' : 'By property'}
          </span>
          <div className="segmented" role="group" aria-label="Group the summary by">
            <button type="button" aria-pressed={groupBy === 'category'} onClick={() => onGroupBy('category')}>
              Category
            </button>
            <button type="button" aria-pressed={groupBy === 'property'} onClick={() => onGroupBy('property')}>
              Property
            </button>
          </div>
        </div>

        {top.length === 0 ? (
          <p className="pencil" style={{ margin: 0 }}>
            {loading && !summary ? 'Reading the summary…' : 'Nothing spent in this period.'}
          </p>
        ) : (
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 'var(--sp-2)' }}>
            {top.map((g) => {
              const share = total > 0 ? Math.round((g.amount / total) * 100) : 0;
              return (
                <li key={g.id || g.name} style={{ display: 'grid', gap: 2 }}>
                  <span style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--sp-3)' }}>
                    <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {g.name}
                    </span>
                    <span className="num" style={{ fontVariantNumeric: 'tabular-nums lining-nums' }}>
                      {fmtTZS(g.amount)}
                    </span>
                  </span>
                  <span
                    aria-hidden
                    style={{ height: 3, background: 'var(--rule)', borderRadius: 2, overflow: 'hidden' }}
                  >
                    <span style={{ display: 'block', height: '100%', width: `${share}%`, background: 'var(--primary)' }} />
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </section>
  );
}

/* --------------------------------- voiding -------------------------------- */

export function VoidExpenseForm({
  expense,
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  expense: Expense;
  busy: boolean;
  error: ApiError | null;
  onSubmit: (reason: string) => void;
  onCancel: () => void;
}) {
  const [reason, setReason] = useState('');

  // A second void on a different row must not inherit the first one's words.
  useEffect(() => setReason(''), [expense.id]);

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
        <span>
          {fmtTZS(expense.amount)} on {fmtDate(expense.incurred_on)} stops counting towards your
          totals and reports. The row is kept and marked voided — never deleted — and the reason is
          written to the audit log.
        </span>
      </p>
      <Field
        id="void_reason"
        label="Why is this being voided?"
        hint={`${reason.length}/200`}
        error={error?.errors.reason}
      >
        <textarea
          id="void_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="e.g. Entered twice — this is the duplicate."
        />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? 'Voiding…' : 'Void expense'}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}

export function VoidExpenseSheet({
  expense,
  busy,
  error,
  onClose,
  onSubmit,
}: {
  expense: Expense | null;
  busy: boolean;
  error: ApiError | null;
  onClose: () => void;
  onSubmit: (reason: string) => void;
}) {
  return (
    <Sheet open={expense !== null} title="Void this expense" onClose={onClose} width={520}>
      {expense ? (
        <VoidExpenseForm
          expense={expense}
          busy={busy}
          error={error}
          onCancel={onClose}
          onSubmit={onSubmit}
        />
      ) : null}
    </Sheet>
  );
}
