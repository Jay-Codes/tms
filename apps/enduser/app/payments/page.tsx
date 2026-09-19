'use client';

/**
 * Payments — FLOWS.md flow 7, the renter half.
 *
 * Five things, in the order a renter needs them:
 *   1. How to pay — pinned at the top since Phase 16 (§16.4), because a
 *      renter who cannot find the account number cannot pay at all.
 *   2. What is due next (date, amount, status) and whether anything is late.
 *   3. The full schedule, one ledger per tenancy, with an "awaiting
 *      confirmation" chip on any instalment a proof has been sent for.
 *   4. What has already been received, reversals included.
 *   5. The proofs the renter sent, and whether they were answered.
 *
 * Everything comes from `GET /me/schedules`, `GET /me/payments` and
 * `GET /me/proofs`; nothing is computed here that the server also computes.
 * The history lists are best-effort — a rent book that cannot reach them
 * still shows what is owed.
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
  type MobileMoney,
  type MyPayment,
  type MySchedule,
  type Proof,
} from '../../lib/api';
import { errorMessage, formatDate, money, proofErrorMessage } from '../../lib/format';
import { Protected } from '../../components/Protected';
import { Money } from '../../components/Money';
import { PayDetails } from '../../components/PayDetails';
import { CountdownChip, ProofChip, ScheduleMark } from '../../components/PaymentStatus';
import { ProofSheet, type ProofTarget } from '../../components/ProofSheet';
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

/**
 * Newest proof per instalment. `GET /me/proofs` is newest-first, so the first
 * one seen for a schedule is the one that still speaks for it — the join the
 * API deliberately leaves to the client for every row but `next_due`.
 */
function newestProofBySchedule(proofs: Proof[]): Map<string, Proof> {
  const map = new Map<string, Proof>();
  for (const p of proofs) {
    if (!p.schedule_id || map.has(p.schedule_id)) continue;
    map.set(p.schedule_id, p);
  }
  return map;
}

/* ------------------------------------------------------------------ */
/* How to pay — the pinned card                                        */
/* ------------------------------------------------------------------ */

const COLLAPSED_KEY = 'tms.enduser.how-to-pay-collapsed';

function readCollapsed(): boolean {
  try {
    return window.localStorage.getItem(COLLAPSED_KEY) === '1';
  } catch {
    // Private mode / storage blocked: the card simply opens every time.
    return false;
  }
}

function rememberCollapsed(collapsed: boolean): void {
  try {
    window.localStorage.setItem(COLLAPSED_KEY, collapsed ? '1' : '0');
  } catch {
    // Nothing to do — the preference is a convenience, not a record.
  }
}

function HowToPay({
  account,
  mobileMoney,
  reference,
  onSendProof,
}: {
  account: BankAccount | null;
  mobileMoney: MobileMoney | null;
  reference: string;
  onSendProof: (() => void) | null;
}) {
  const t = useT();
  const [collapsed, setCollapsed] = useState(false);

  /* Collapsible only after the first view (PLAN2 §16.4): the choice is this
     device's, so it lives in localStorage and never in the API. A renter who
     arrived on `#how-to-pay` is asking for it open, whatever they chose last. */
  useEffect(() => {
    if (window.location.hash === '#how-to-pay') {
      setCollapsed(false);
      return;
    }
    setCollapsed(readCollapsed());
  }, []);

  function toggle() {
    const next = !collapsed;
    setCollapsed(next);
    rememberCollapsed(next);
  }

  return (
    <section id="how-to-pay" className="sheet" style={{ padding: 'var(--sp-4)' }}>
      <button
        type="button"
        onClick={toggle}
        aria-expanded={!collapsed}
        aria-controls="how-to-pay-body"
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--sp-3)',
          width: '100%',
          minHeight: 'var(--touch-min)',
          padding: 0,
          background: 'transparent',
          border: 0,
          color: 'inherit',
          cursor: 'pointer',
          textAlign: 'left',
        }}
      >
        <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('payments.howToPay')}</h2>
        <Icon
          icon={collapsed ? 'solar:alt-arrow-down-linear' : 'solar:alt-arrow-up-linear'}
          width={22}
          aria-hidden
        />
      </button>

      {!collapsed && (
        <div id="how-to-pay-body" style={{ display: 'grid', gap: 'var(--sp-4)', paddingTop: 'var(--sp-3)' }}>
          <PayDetails account={account} mobileMoney={mobileMoney} reference={reference} />
          {onSendProof && (
            <button type="button" className="btn btn-primary" onClick={onSendProof}>
              <Icon icon="solar:camera-linear" width={20} aria-hidden />
              {t('proof.send')}
            </button>
          )}
        </div>
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

function ProofRow({
  proof,
  busy,
  onWithdraw,
}: {
  proof: Proof;
  busy: boolean;
  onWithdraw: (id: string) => void;
}) {
  const t = useT();
  const locale = useLocale();
  return (
    <tr>
      <td>
        {formatDate(locale, proof.paid_at)}
        <br />
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {proof.contract?.unit_name ? `${proof.contract.unit_name} · ` : ''}
          {paymentMethodLabel(t, proof.method)}
          {proof.reference ? ` · ${proof.reference}` : ''}
        </span>
        {proof.status === 'rejected' && proof.rejection_reason && (
          <>
            <br />
            <span style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>
              {t('proof.rejected.reason', { reason: proof.rejection_reason })}
            </span>
          </>
        )}
        {proof.status === 'submitted' && (
          <>
            <br />
            <button
              type="button"
              className="btn btn-quiet"
              style={{ width: 'auto', height: 'var(--touch-min)', paddingInline: 0 }}
              disabled={busy}
              onClick={() => onWithdraw(proof.id)}
            >
              {busy ? t('proof.withdrawing') : t('proof.withdraw')}
            </button>
          </>
        )}
      </td>
      <td className="num">
        <Money amount={proof.amount} />
        <br />
        <ProofChip status={proof.status} />
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
  const [mobileMoney, setMobileMoney] = useState<MobileMoney | null>(null);
  const [payments, setPayments] = useState<MyPayment[]>([]);
  const [proofs, setProofs] = useState<Proof[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [proofOpen, setProofOpen] = useState(false);
  const [proofTarget, setProofTarget] = useState<ProofTarget | null>(null);
  const [withdrawing, setWithdrawing] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    // History and proofs are additions to the ledger, not the ledger itself.
    void renterApi
      .payments(signal)
      .then((res) => setPayments(res.items ?? []))
      .catch(() => setPayments([]));
    void renterApi
      .proofs(signal)
      .then((res) => setProofs(res.items ?? []))
      .catch(() => setProofs([]));

    try {
      const res = await renterApi.schedules(signal);
      setSchedules(res.items ?? []);
      setNextDue(res.next_due ?? null);
      setOverdueTotal(res.overdue_total ?? 0);
      setBankAccount(res.bank_account ?? null);
      setMobileMoney(res.mobile_money ?? res.bank_account?.mobile_money ?? null);
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
  const proofBySchedule = useMemo(() => newestProofBySchedule(proofs), [proofs]);
  const reference = nextDue?.contract?.unit_name ?? groups[0]?.unitName ?? '';

  /** The tenancy a proof defaults to: the one the next payment belongs to. */
  const defaultTarget: ProofTarget | null = useMemo(() => {
    if (nextDue?.contract?.id) {
      return {
        contractId: nextDue.contract.id,
        contractLabel: [nextDue.contract.unit_name, nextDue.contract.property_name]
          .filter(Boolean)
          .join(' · '),
        scheduleId: nextDue.id,
        amount: scheduleOutstanding(nextDue),
      };
    }
    const g = groups.find((group) => group.id !== 'unknown');
    if (!g) return null;
    return {
      contractId: g.id,
      contractLabel: [g.unitName, g.propertyName].filter(Boolean).join(' · '),
    };
  }, [nextDue, groups]);

  function openProof(target: ProofTarget | null) {
    setProofTarget(target);
    setProofOpen(true);
  }

  async function withdraw(id: string) {
    if (!window.confirm(t('proof.withdrawConfirm'))) return;
    setWithdrawing(id);
    setError(null);
    try {
      await renterApi.withdrawProof(id);
      await load();
    } catch (err) {
      setError(proofErrorMessage(t, err));
    } finally {
      setWithdrawing(null);
    }
  }

  return (
    <Screen bottomBar>
      <ScreenHeader eyebrow={t('home.eyebrow')} title={t('payments.title')} />

      {error && <Notice tone="error">{error}</Notice>}

      {overdueTotal > 0 && (
        <Notice tone="error">
          {t('payments.overdue', { amount: money(overdueTotal) })}
        </Notice>
      )}

      <HowToPay
        account={bankAccount}
        mobileMoney={mobileMoney}
        reference={reference}
        onSendProof={defaultTarget ? () => openProof(defaultTarget) : null}
      />

      <section className="sheet" style={{ padding: 'var(--sp-4)' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'baseline',
            justifyContent: 'space-between',
            gap: 'var(--sp-3)',
            flexWrap: 'wrap',
          }}
        >
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('payment.next.title')}</h2>
          {!loading && <CountdownChip schedule={nextDue} />}
        </div>
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
              {group.rows.map((row) => {
                // `next_due` carries its own proof; every other row is joined
                // client-side from `GET /me/proofs`.
                const proof = proofBySchedule.get(row.id) ?? null;
                const awaiting = row.proof?.status === 'submitted' || proof?.status === 'submitted';
                const rejected = proof?.status === 'rejected' ? proof : null;
                return (
                  <tr key={row.id}>
                    <td>
                      {formatDate(locale, row.due_date)}
                      <br />
                      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                        {formatDate(locale, row.period_start)} –{' '}
                        {formatDate(locale, row.period_end)}
                      </span>
                      {rejected && (
                        <>
                          <br />
                          <span
                            style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}
                          >
                            {rejected.rejection_reason
                              ? t('proof.rejected.reason', { reason: rejected.rejection_reason })
                              : t('proof.status.rejected')}
                          </span>
                          <br />
                          <button
                            type="button"
                            className="btn btn-quiet"
                            style={{
                              width: 'auto',
                              height: 'var(--touch-min)',
                              paddingInline: 0,
                            }}
                            onClick={() =>
                              openProof({
                                contractId: group.id,
                                contractLabel: [group.unitName, group.propertyName]
                                  .filter(Boolean)
                                  .join(' · '),
                                scheduleId: row.id,
                                amount: scheduleOutstanding(row),
                              })
                            }
                          >
                            {t('proof.sendAgain')}
                          </button>
                        </>
                      )}
                    </td>
                    <td className="num">
                      <Money amount={row.amount} />
                      <br />
                      {awaiting ? (
                        <CountdownChip schedule={row} awaiting />
                      ) : (
                        <ScheduleMark schedule={row} />
                      )}
                    </td>
                  </tr>
                );
              })}
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

      <section style={{ display: 'grid', gap: 'var(--sp-2)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('proof.history')}</h2>
        {proofs.length === 0 ? (
          <p className="pencil">{t('proof.none')}</p>
        ) : (
          <table className="ledger">
            <tbody>
              {proofs.map((p) => (
                <ProofRow
                  key={p.id}
                  proof={p}
                  busy={withdrawing === p.id}
                  onWithdraw={(id) => void withdraw(id)}
                />
              ))}
            </tbody>
          </table>
        )}
      </section>

      <Link className="btn btn-quiet" href="/">
        {t('common.backToRentBook')}
      </Link>

      <ProofSheet
        open={proofOpen}
        target={proofTarget}
        account={bankAccount}
        mobileMoney={mobileMoney}
        reference={reference}
        onClose={() => setProofOpen(false)}
        onSubmitted={() => void load()}
      />
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
