'use client';

/**
 * Payments — FLOWS.md flow 7, the renter half.
 *
 * Four things, in the order a renter needs them:
 *   1. What is due next (date, amount, status) and whether anything is late.
 *   2. How to pay — the org's collection account, since the money moves
 *      outside the system and the landlord records it afterwards.
 *   3. The full schedule, one ledger per tenancy.
 *   4. What has already been received, reversals included.
 *
 * Everything comes from `GET /me/schedules` and `GET /me/payments`; nothing is
 * computed here that the server also computes. The receipts list is
 * best-effort — a rent book that cannot reach the history endpoint still
 * shows what is owed.
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { Icon } from '@iconify/react';
import { useLocale, useT } from '@tms/ui';
import {
  ApiError,
  paymentMethodLabel,
  renterApi,
  scheduleOutstanding,
  type BankAccount,
  type MyPayment,
  type MySchedule,
} from '../../lib/api';
import { errorMessage, formatDate, money } from '../../lib/format';
import { Protected } from '../../components/Protected';
import { Money } from '../../components/Money';
import { ScheduleMark } from '../../components/PaymentStatus';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';

/* ------------------------------------------------------------------ */
/* Grouping                                                            */
/* ------------------------------------------------------------------ */

interface ContractGroup {
  id: string;
  unitName: string;
  propertyName: string | null;
  rows: MySchedule[];
}

/** One ledger per tenancy, rows in due-date order, groups in first-seen order. */
function groupByContract(items: MySchedule[], unitFallback: string): ContractGroup[] {
  const groups = new Map<string, ContractGroup>();
  for (const s of items) {
    const id = s.contract?.id ?? 'unknown';
    let g = groups.get(id);
    if (!g) {
      g = {
        id,
        unitName: s.contract?.unit_name ?? unitFallback,
        propertyName: s.contract?.property_name ?? null,
        rows: [],
      };
      groups.set(id, g);
    }
    g.rows.push(s);
  }
  for (const g of groups.values()) {
    g.rows.sort((a, b) => a.due_date.localeCompare(b.due_date));
  }
  return [...groups.values()];
}

/* ------------------------------------------------------------------ */
/* How to pay                                                          */
/* ------------------------------------------------------------------ */

function CopyButton({ value, label }: { value: string; label: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard is blocked (insecure origin, older browser). The number is
      // on screen in full, so this is a convenience, never the only route.
      setCopied(false);
    }
  }

  return (
    <button
      type="button"
      className="btn btn-quiet"
      onClick={() => void copy()}
      aria-label={copied ? t('common.copiedValue', { label }) : t('common.copyValue', { label })}
      style={{ width: 'auto', paddingInline: 'var(--sp-2)', gap: 'var(--sp-2)' }}
    >
      <Icon icon={copied ? 'solar:check-read-linear' : 'solar:copy-linear'} width={18} aria-hidden />
      {copied ? t('common.copied') : t('common.copy')}
    </button>
  );
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'baseline',
        justifyContent: 'space-between',
        gap: 'var(--sp-3)',
        minHeight: 'var(--touch-min)',
        borderBottom: '1px solid var(--rule)',
      }}
    >
      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{label}</span>
      <span style={{ textAlign: 'right' }}>{value}</span>
    </div>
  );
}

function HowToPay({ account, reference }: { account: BankAccount | null; reference: string }) {
  const t = useT();
  return (
    <section id="how-to-pay" className="sheet" style={{ padding: 'var(--sp-4)' }}>
      <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('payments.howToPay')}</h2>

      {account ? (
        <div style={{ display: 'grid', gap: 'var(--sp-1)', paddingTop: 'var(--sp-3)' }}>
          <DetailRow label={t('payments.bank')} value={account.bank_name} />
          <DetailRow label={t('payments.accountName')} value={account.account_name} />

          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 'var(--sp-3)',
              minHeight: 'var(--touch-min)',
              borderBottom: '1px solid var(--rule)',
            }}
          >
            <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('payments.accountNumber')}
            </span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
              <span className="num" style={{ fontWeight: 600, letterSpacing: '0.03em' }}>
                {account.account_number}
              </span>
              <CopyButton
                value={account.account_number}
                label={t('payments.accountNumberLabel')}
              />
            </span>
          </div>

          {account.instructions && (
            <p
              style={{
                margin: 0,
                paddingTop: 'var(--sp-3)',
                color: 'var(--ink-soft)',
                fontSize: 'var(--text-sm)',
                whiteSpace: 'pre-wrap',
              }}
            >
              {account.instructions}
            </p>
          )}

          <p
            style={{
              display: 'flex',
              alignItems: 'flex-start',
              gap: 'var(--sp-2)',
              margin: 0,
              paddingTop: 'var(--sp-3)',
              fontSize: 'var(--text-sm)',
            }}
          >
            <Icon icon="solar:info-circle-linear" width={18} aria-hidden />
            <span>
              {t('payments.reference')}
              {reference ? ' — ' : ''}
              {reference && <strong>{reference}</strong>}.
            </span>
          </p>

          <p
            style={{
              margin: 0,
              paddingTop: 'var(--sp-3)',
              color: 'var(--ink-soft)',
              fontSize: 'var(--text-sm)',
            }}
          >
            {t('payments.howMoney')}
          </p>
        </div>
      ) : (
        <p className="pencil" style={{ paddingTop: 'var(--sp-3)' }}>
          {t('payments.noAccount')}
        </p>
      )}
    </section>
  );
}

/* ------------------------------------------------------------------ */
/* History                                                             */
/* ------------------------------------------------------------------ */

function PaymentRow({ payment }: { payment: MyPayment }) {
  const t = useT();
  const locale = useLocale();
  const reversed = payment.status === 'reversed';
  return (
    <tr>
      <td>
        <span style={{ textDecoration: reversed ? 'line-through' : 'none' }}>
          {formatDate(locale, payment.paid_at)}
        </span>
        <br />
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {payment.unit_name ? `${payment.unit_name} · ` : ''}
          {paymentMethodLabel(t, payment.method)}
          {payment.reference ? ` · ${payment.reference}` : ''}
        </span>
        {reversed && payment.reversal_reason && (
          <>
            <br />
            <span style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>
              {payment.reversal_reason}
            </span>
          </>
        )}
      </td>
      <td className="num">
        <Money amount={payment.amount} struck={reversed} />
        <br />
        {reversed ? (
          <span className="stamp stamp-overdue">{t('payments.reversed')}</span>
        ) : (
          <span className="stamp stamp-paid">{t('payments.received')}</span>
        )}
      </td>
    </tr>
  );
}

/* ------------------------------------------------------------------ */
/* Screen                                                              */
/* ------------------------------------------------------------------ */

function PaymentsContent() {
  const t = useT();
  const locale = useLocale();
  const [schedules, setSchedules] = useState<MySchedule[]>([]);
  const [nextDue, setNextDue] = useState<MySchedule | null>(null);
  const [overdueTotal, setOverdueTotal] = useState(0);
  const [bankAccount, setBankAccount] = useState<BankAccount | null>(null);
  const [payments, setPayments] = useState<MyPayment[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    // History is an addition to the ledger, not the ledger itself.
    void renterApi
      .payments(signal)
      .then((res) => setPayments(res.items ?? []))
      .catch(() => setPayments([]));

    try {
      const res = await renterApi.schedules(signal);
      setSchedules(res.items ?? []);
      setNextDue(res.next_due ?? null);
      setOverdueTotal(res.overdue_total ?? 0);
      setBankAccount(res.bank_account ?? null);
      setError(null);
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      if (err instanceof ApiError && err.status === 404) setSchedules([]);
      else setError(errorMessage(t, err));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const unitFallback = t('common.yourUnit');
  const groups = useMemo(
    () => groupByContract(schedules, unitFallback),
    [schedules, unitFallback],
  );
  const reference = nextDue?.contract?.unit_name ?? groups[0]?.unitName ?? '';

  return (
    <Screen bottomBar>
      <ScreenHeader eyebrow={t('home.eyebrow')} title={t('payments.title')} />

      {error && <Notice tone="error">{error}</Notice>}

      {overdueTotal > 0 && (
        <Notice tone="error">
          {t('payments.overdue', { amount: money(overdueTotal) })}
        </Notice>
      )}

      <section className="sheet" style={{ padding: 'var(--sp-4)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('payment.next.title')}</h2>
        <table className="ledger" style={{ marginTop: 'var(--sp-3)' }}>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={2} className="pencil">
                  {t('common.loading')}
                </td>
              </tr>
            ) : nextDue ? (
              <tr>
                <td>
                  {formatDate(locale, nextDue.due_date)}
                  <br />
                  <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    {nextDue.contract?.unit_name ?? unitFallback}
                    {nextDue.contract?.property_name
                      ? ` · ${nextDue.contract.property_name}`
                      : ''}
                  </span>
                </td>
                <td className="num">
                  <Money amount={scheduleOutstanding(nextDue)} />
                  <br />
                  <ScheduleMark schedule={nextDue} />
                </td>
              </tr>
            ) : (
              <tr>
                <td style={{ color: 'var(--ink-soft)' }}>{t('common.nothingToPayYet')}</td>
                <td className="num">
                  <span className="pencil">—</span>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </section>

      <HowToPay account={bankAccount} reference={reference} />

      {groups.map((group) => (
        <section key={group.id} style={{ display: 'grid', gap: 'var(--sp-2)' }}>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>
            {group.unitName}
            {group.propertyName && (
              <>
                {' '}
                <span
                  style={{
                    color: 'var(--ink-soft)',
                    fontSize: 'var(--text-sm)',
                    fontWeight: 400,
                  }}
                >
                  · {group.propertyName}
                </span>
              </>
            )}
          </h2>
          <table className="ledger">
            <thead>
              <tr>
                <th scope="col">{t('common.due')}</th>
                <th scope="col" className="num">
                  {t('common.amount')}
                </th>
              </tr>
            </thead>
            <tbody>
              {group.rows.map((row) => (
                <tr key={row.id}>
                  <td>
                    {formatDate(locale, row.due_date)}
                    <br />
                    <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                      {formatDate(locale, row.period_start)} –{' '}
                      {formatDate(locale, row.period_end)}
                    </span>
                  </td>
                  <td className="num">
                    <Money amount={row.amount} />
                    <br />
                    <ScheduleMark schedule={row} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}

      {!loading && groups.length === 0 && (
        <p className="pencil">{t('payments.noSchedule')}</p>
      )}

      <section style={{ display: 'grid', gap: 'var(--sp-2)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('payments.history')}</h2>
        {payments.length === 0 ? (
          <p className="pencil">{t('payments.noHistory')}</p>
        ) : (
          <table className="ledger">
            <tbody>
              {payments.map((p) => (
                <PaymentRow key={p.id} payment={p} />
              ))}
            </tbody>
          </table>
        )}
      </section>

      <Link className="btn btn-quiet" href="/">
        {t('common.backToRentBook')}
      </Link>
    </Screen>
  );
}

export default function PaymentsPage() {
  return (
    <Protected>
      <PaymentsContent />
    </Protected>
  );
}
