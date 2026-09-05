'use client';

/**
 * The collections desk (FLOWS flow 7, landlord).
 *
 * Four schedule views and one history. The order of the tabs is the order a
 * landlord's morning goes: what is late, what is about to be, what was only
 * half paid, then everything. Each tab is a plain server-side filter on
 * `GET /schedules` — nothing is computed here, so what is on screen is what the
 * backend believes, including `days_overdue`.
 *
 * The history tab is the correction desk: every payment ever recorded, and the
 * one way to undo one (reverse, with a reason, audited — never a delete).
 */

import { Icon } from '@iconify/react';
import { Suspense, useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import { ProblemNote } from '../../../components/FormBits';
import { PaymentsTable, ReverseSheet, SchedulesTable } from '../../../components/PaymentBits';
import { RecordPaymentSheet, type RecordPaymentTarget } from '../../../components/RecordPaymentSheet';
import { PageHead } from '../../../components/PageHead';
import {
  ApiError,
  paymentsApi,
  schedulesApi,
  toApiError,
  type Payment,
  type Schedule,
} from '../../../lib/api';
import { fmtTZS, isoPlusDays, todayISO } from '../../../lib/format';

type TabId = 'overdue' | 'due_soon' | 'partial' | 'all' | 'history';

const TABS: { value: TabId; label: string }[] = [
  { value: 'overdue', label: 'Overdue' },
  { value: 'due_soon', label: 'Due soon' },
  { value: 'partial', label: 'Partial' },
  { value: 'all', label: 'All schedules' },
  { value: 'history', label: 'Payment history' },
];

const EMPTY: Record<TabId, string> = {
  overdue: 'Nothing is overdue. Every due payment has been settled.',
  due_soon: 'Nothing falls due in the next seven days.',
  partial: 'No part-paid schedules.',
  all: 'No payment schedules yet — they are written when a contract is activated.',
  history: 'No payments recorded yet.',
};

/** Each tab is one query against `GET /schedules`; "due soon" is the 7-day window. */
function scheduleQuery(tab: TabId) {
  if (tab === 'overdue') return { status: 'overdue' as const, limit: 200 };
  if (tab === 'due_soon')
    return { status: 'pending' as const, due_from: todayISO(), due_to: isoPlusDays(7), limit: 200 };
  if (tab === 'partial') return { status: 'partial' as const, limit: 200 };
  return { limit: 200 };
}

function PaymentsBody() {
  const initial = (useSearchParams().get('tab') ?? '') as TabId;
  const [tab, setTab] = useState<TabId>(TABS.some((t) => t.value === initial) ? initial : 'overdue');

  const [schedules, setSchedules] = useState<Schedule[] | null>(null);
  const [payments, setPayments] = useState<Payment[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  const [recordTarget, setRecordTarget] = useState<RecordPaymentTarget | null>(null);
  const [reversing, setReversing] = useState<Payment | null>(null);
  const [reverseBusy, setReverseBusy] = useState(false);
  const [reverseError, setReverseError] = useState<ApiError | null>(null);

  const load = useCallback(
    async (which: TabId, signal?: AbortSignal) => {
      setError(null);
      try {
        if (which === 'history') {
          setPayments(null);
          const res = await paymentsApi.list({ limit: 200 }, signal);
          setPayments(res.items ?? []);
        } else {
          setSchedules(null);
          const res = await schedulesApi.list(scheduleQuery(which), signal);
          setSchedules(res.items ?? []);
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        if (which === 'history') setPayments([]);
        else setSchedules([]);
      }
    },
    [],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(tab, ac.signal);
    return () => ac.abort();
  }, [tab, load]);

  const reverse = async (reason: string) => {
    if (!reversing) return;
    setReverseBusy(true);
    setReverseError(null);
    try {
      await paymentsApi.reverse(reversing.id, reason);
      setReversing(null);
      await load(tab);
    } catch (e) {
      setReverseError(toApiError(e));
    } finally {
      setReverseBusy(false);
    }
  };

  // A running total under the head, so the tab answers "how much?" as well as
  // "how many?" — the overdue figure is the one the dashboard card repeats.
  const outstanding =
    tab === 'history' || schedules === null
      ? null
      : schedules.reduce((t, s) => t + Math.max(0, (s.amount ?? 0) - (s.paid_amount ?? 0)), 0);

  return (
    <>
      <PageHead
        title="Payments"
        lead="Rent is paid outside the system — cash, bank transfer or mobile money. This is where it is written down."
      />

      <div role="tablist" style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginBottom: 'var(--sp-4)' }}>
        {TABS.map((t) => {
          const active = tab === t.value;
          return (
            <button
              key={t.value}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setTab(t.value)}
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
              {t.label}
            </button>
          );
        })}
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />

        {outstanding !== null && (schedules ?? []).length > 0 ? (
          <p style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', color: 'var(--ink-soft)' }}>
            <Icon icon="solar:wallet-money-linear" width={20} />
            <span>
              {schedules?.length} schedule{schedules?.length === 1 ? '' : 's'} ·{' '}
              <strong style={{ color: 'var(--ink)' }}>{fmtTZS(outstanding)}</strong> still owing
            </span>
          </p>
        ) : null}

        {tab === 'history' ? (
          <PaymentsTable
            items={payments}
            emptyText={error ? 'Nothing to show.' : EMPTY.history}
            onReverse={(p) => {
              setReverseError(null);
              setReversing(p);
            }}
          />
        ) : (
          <SchedulesTable
            items={schedules}
            emptyText={error ? 'Nothing to show.' : EMPTY[tab]}
            onRecord={(s) =>
              setRecordTarget({
                contractId: s.contract?.id ?? s.contract_id ?? '',
                scheduleId: s.id,
                label: s.contract
                  ? `${s.contract.unit_name} · ${s.contract.property_name} — ${s.contract.renter_name}`
                  : undefined,
              })
            }
          />
        )}
      </div>

      <RecordPaymentSheet
        open={recordTarget !== null}
        target={recordTarget}
        onClose={() => setRecordTarget(null)}
        onRecorded={() => void load(tab)}
      />

      <ReverseSheet
        payment={reversing}
        busy={reverseBusy}
        error={reverseError}
        onClose={() => setReversing(null)}
        onSubmit={(reason) => void reverse(reason)}
      />
    </>
  );
}

export default function PaymentsPage() {
  return (
    <>
      <Suspense fallback={null}>
        <PaymentsBody />
      </Suspense>
    </>
  );
}
