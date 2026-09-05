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
import { useT } from '@tms/ui';

type TabId = 'overdue' | 'due_soon' | 'partial' | 'all' | 'history';

/** Tab order is the order a landlord's morning goes; the labels are keys. */
const TABS: { value: TabId; labelKey: string }[] = [
  { value: 'overdue', labelKey: 'payments.tab.overdue' },
  { value: 'due_soon', labelKey: 'payments.tab.due_soon' },
  { value: 'partial', labelKey: 'payments.tab.partial' },
  { value: 'all', labelKey: 'payments.tab.all' },
  { value: 'history', labelKey: 'payments.tab.history' },
];

const EMPTY_KEY: Record<TabId, string> = {
  overdue: 'payments.empty.overdue',
  due_soon: 'payments.empty.due_soon',
  partial: 'payments.empty.partial',
  all: 'payments.empty.all',
  history: 'payments.empty.history',
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
  const t = useT();
  const initial = (useSearchParams().get('tab') ?? '') as TabId;
  const [tab, setTab] = useState<TabId>(TABS.some((x) => x.value === initial) ? initial : 'overdue');

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
      : schedules.reduce((sum, s) => sum + Math.max(0, (s.amount ?? 0) - (s.paid_amount ?? 0)), 0);

  return (
    <>
      <PageHead title={t('payments.title')} lead={t('payments.lead')} />

      <div
        role="tablist"
        aria-label={t('payments.tablist_label')}
        style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginBottom: 'var(--sp-4)' }}
      >
        {TABS.map((x) => {
          const active = tab === x.value;
          return (
            <button
              key={x.value}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setTab(x.value)}
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
              {t(x.labelKey)}
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
              {t.n('payments.schedule_count', schedules?.length ?? 0)} ·{' '}
              <strong style={{ color: 'var(--ink)' }}>{fmtTZS(outstanding)}</strong>{' '}
              {t('payments.still_owing')}
            </span>
          </p>
        ) : null}

        {tab === 'history' ? (
          <PaymentsTable
            items={payments}
            emptyText={error ? t('common.no_results') : t(EMPTY_KEY.history)}
            onReverse={(p) => {
              setReverseError(null);
              setReversing(p);
            }}
          />
        ) : (
          <SchedulesTable
            items={schedules}
            emptyText={error ? t('common.no_results') : t(EMPTY_KEY[tab])}
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
