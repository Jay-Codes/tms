'use client';

/**
 * One expense (FLOWS flow 12 step 3): everything recorded about it, the
 * receipt, and the two things a landlord can do to it — correct it, or void it
 * with a reason.
 *
 * A voided expense is read-only. It keeps its place in the ledger with the
 * stamp, the reason and the name of whoever voided it, because the record of a
 * mistake is part of the books.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { ExpenseSheet } from '../../../../components/ExpenseSheet';
import { ExpenseStatusStamp, VoidExpenseSheet } from '../../../../components/ExpenseBits';
import { ProblemNote } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import {
  ApiError,
  expensesApi,
  isVoided,
  toApiError,
  unwrapExpense,
  type Expense,
} from '../../../../lib/api';
import { fmtDate, fmtDateTime, fmtTZS } from '../../../../lib/format';
import { useT } from '@tms/ui';

/** Label above, value below — the detail screen's whole vocabulary. */
function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div
      style={{
        display: 'grid',
        gap: 2,
        padding: 'var(--sp-3) 0',
        borderBottom: '1px solid var(--rule)',
        minWidth: 0,
      }}
    >
      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{label}</span>
      {/* `facts` earns the value's link a phone-sized target (tokens.css). */}
      <span className="facts" style={{ overflowWrap: 'anywhere' }}>
        {children}
      </span>
    </div>
  );
}

/**
 * The receipt. A presigned read URL is asked for only when there is a file, and
 * it is short-lived: an image is shown inline, a PDF gets a link because an
 * inline PDF on a phone is a worse experience than the OS viewer.
 */
function ReceiptPanel({ expense }: { expense: Expense }) {
  const t = useT();
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    setUrl(null);
    setError(null);
    if (!expense.receipt?.present) return;
    const ac = new AbortController();
    expensesApi
      .receiptUrl(expense.id, ac.signal)
      .then((r) => setUrl(r.url))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      });
    return () => ac.abort();
  }, [expense.id, expense.receipt?.present, expense.updated_at]);

  if (!expense.receipt?.present) {
    return (
      <p className="pencil" style={{ margin: 0 }}>
        {t('expenses.receipt.none_before')}
        <strong>{t('common.edit')}</strong>
        {t('expenses.receipt.none_after')}
      </p>
    );
  }

  const pdf = expense.receipt.content_type === 'application/pdf';

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
      <ProblemNote error={error} />
      {url === null && !error ? (
        <p className="pencil" style={{ margin: 0 }}>
          {t('expenses.receipt.opening')}
        </p>
      ) : null}
      {url && pdf ? (
        <div>
          <a className="btn btn-secondary" href={url} target="_blank" rel="noopener noreferrer">
            <Icon icon="solar:file-text-linear" width={20} /> {t('expenses.receipt.open')}
          </a>
        </div>
      ) : null}
      {url && !pdf ? (
        <a href={url} target="_blank" rel="noopener noreferrer" style={{ display: 'block', maxWidth: 480 }}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={url}
            alt={t('expenses.receipt.alt', {
              what: expense.vendor || expense.category?.name || t('expenses.this_expense'),
            })}
            style={{
              display: 'block',
              width: '100%',
              height: 'auto',
              border: '1px solid var(--rule)',
              borderRadius: 'var(--radius-sm)',
            }}
          />
        </a>
      ) : null}
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', margin: 0 }}>
        {expense.receipt.size ? `${Math.max(1, Math.round(expense.receipt.size / 1024))} KB · ` : ''}
        {expense.receipt.content_type ?? t('expenses.receipt.file')} · {t('expenses.receipt.link_expires')}
      </p>
    </div>
  );
}

function ExpenseBody({ id }: { id: string }) {
  const t = useT();
  const [expense, setExpense] = useState<Expense | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [editing, setEditing] = useState(false);
  const [voiding, setVoiding] = useState(false);
  const [voidBusy, setVoidBusy] = useState(false);
  const [voidError, setVoidError] = useState<ApiError | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        setExpense(unwrapExpense(await expensesApi.get(id, signal)));
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
    },
    [id],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const doVoid = async (reason: string) => {
    if (!expense) return;
    setVoidBusy(true);
    setVoidError(null);
    try {
      setExpense(unwrapExpense(await expensesApi.void(expense.id, reason)));
      setVoiding(false);
    } catch (e) {
      setVoidError(toApiError(e));
    } finally {
      setVoidBusy(false);
    }
  };

  if (error && !expense) {
    return (
      <>
        <PageHead title={t('expenses.one')} />
        <hr className="rule rule-strong" />
        <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <ProblemNote error={error} />
          <div>
            <Link href="/expenses" className="btn btn-secondary">
              {t('expenses.back_to_list')}
            </Link>
          </div>
        </div>
      </>
    );
  }

  const voided = expense ? isVoided(expense) : false;

  return (
    <>
      <PageHead
        title={expense ? fmtTZS(expense.amount) : t('common.loading')}
        lead={
          expense
            ? `${expense.category?.name ?? t('expenses.uncategorised')} · ${expense.property?.name ?? ''}${
                expense.unit?.name ? ` · ${expense.unit.name}` : ''
              } · ${fmtDate(expense.incurred_on)}`
            : undefined
        }
        actions={
          <>
            <Link href="/expenses" className="btn btn-quiet">
              {t('common.back')}
            </Link>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => setEditing(true)}
              disabled={!expense || voided}
            >
              <Icon icon="solar:pen-linear" width={18} /> {t('common.edit')}
            </button>
            <button
              type="button"
              className="btn btn-danger"
              onClick={() => {
                setVoidError(null);
                setVoiding(true);
              }}
              disabled={!expense || voided}
            >
              {t('expenses.void.action')}
            </button>
          </>
        }
      />
      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-5)' }}>
        <ProblemNote error={error} />

        {expense && voided ? (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-4)',
              flexWrap: 'wrap',
              border: '1px solid var(--stamp-overdue)',
              borderRadius: 'var(--radius-sm)',
              padding: 'var(--sp-4)',
            }}
          >
            <ExpenseStatusStamp status={expense.status} />
            <span style={{ minWidth: 0 }}>
              <strong style={{ display: 'block' }}>
                {expense.void_reason || t('expenses.void.no_reason')}
              </strong>
              <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {expense.recorded_by?.name
                  ? t('expenses.void.stamp_by', {
                      when: fmtDateTime(expense.voided_at),
                      who: expense.recorded_by.name,
                    })
                  : t('expenses.void.stamp', { when: fmtDateTime(expense.voided_at) })}
              </span>
            </span>
          </div>
        ) : null}

        {expense ? (
          <div
            style={{
              display: 'grid',
              gap: 'var(--sp-5)',
              gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))',
              alignItems: 'start',
            }}
          >
            <section aria-label={t('expenses.details_label')}>
              <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-2)' }}>
                {t('expenses.details')}
              </h2>
              <Row label={t('common.amount')}>
                <strong className="num" style={{ fontSize: 'var(--text-xl)' }}>
                  {fmtTZS(expense.amount)}
                </strong>
              </Row>
              <Row label={t('expenses.incurred_on')}>{fmtDate(expense.incurred_on)}</Row>
              <Row label={t('common.property')}>
                {expense.property?.id ? (
                  <Link href={`/properties/${expense.property.id}`}>{expense.property.name}</Link>
                ) : (
                  (expense.property?.name ?? '—')
                )}
              </Row>
              <Row label={t('common.unit')}>
                {expense.unit?.name ?? <span className="pencil">{t('expenses.whole_property')}</span>}
              </Row>
              <Row label={t('expenses.category')}>
                {expense.category?.name ?? <span className="pencil">{t('expenses.uncategorised')}</span>}
              </Row>
              <Row label={t('expenses.vendor')}>{expense.vendor || <span className="pencil">—</span>}</Row>
              <Row label={t('expenses.reference')}>
                {expense.reference ? <span className="num">{expense.reference}</span> : <span className="pencil">—</span>}
              </Row>
              <Row label={t('common.note')}>{expense.note || <span className="pencil">—</span>}</Row>
              <Row label={t('expenses.recorded_by')}>
                {expense.recorded_by?.name ?? '—'} · {fmtDateTime(expense.created_at)}
              </Row>
              {expense.updated_at && expense.updated_at !== expense.created_at ? (
                <Row label={t('expenses.last_changed')}>{fmtDateTime(expense.updated_at)}</Row>
              ) : null}
            </section>

            <section aria-label={t('expenses.receipt')}>
              <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>
                {t('expenses.receipt')}
              </h2>
              <ReceiptPanel expense={expense} />
            </section>
          </div>
        ) : (
          <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
        )}

        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('expenses.corrections_note_detail')}
        </p>
      </div>

      <ExpenseSheet
        open={editing}
        expense={expense}
        onClose={() => setEditing(false)}
        onSaved={(next) => setExpense(next)}
      />

      <VoidExpenseSheet
        expense={voiding ? expense : null}
        busy={voidBusy}
        error={voidError}
        onClose={() => setVoiding(false)}
        onSubmit={(reason) => void doVoid(reason)}
      />
    </>
  );
}

export default function ExpenseDetailPage() {
  const params = useParams<{ id: string }>();
  const id = typeof params?.id === 'string' ? params.id : '';
  return <ExpenseBody id={id} />;
}
