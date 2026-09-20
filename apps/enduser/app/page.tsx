'use client';

/**
 * Renter home — the rent book.
 *
 * Phase 3 fills the top of it with `GET /me/link-requests`: the units the
 * renter has asked to connect to, pencilled while a landlord is still
 * thinking and stamped once they decide.
 *
 * Phase 4 adds the two things that matter more than any of that: a contract
 * waiting for a signature (`GET /me/contracts`) and the next payment due
 * (`GET /me/schedules`). Both are best-effort — a rent book that cannot reach
 * one endpoint still shows the rest.
 *
 * Phase 18.2 splits the hero. A renter may rent two units at once (there is no
 * renter-side uniqueness — only one live contract per *unit*), and a single
 * "next due" hero made the second unit's rent invisible until the first was
 * paid. With more than one live tenancy the screen shows one due card per
 * tenancy, each with its own countdown and its own proof target; with one, the
 * hero is exactly what it was. It also offers a way in for a renter holding a
 * unit code they never scanned.
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { Icon } from '@iconify/react';
import {
  ApiError,
  contractApi,
  isLiveContractStatus,
  needsRenterSignature,
  renterApi,
  scheduleOutstanding,
  type BankAccount,
  type Contract,
  type LinkRequest,
  type MobileMoney,
  type MySchedule,
} from '../lib/api';
import { useLocale, useT, type Translator } from '@tms/ui';
import { useMe } from '../lib/auth';
import { errorMessage, formatDate, money } from '../lib/format';
import { forgetScannedUnit, readScannedUnit } from '../lib/scan';
import { Protected } from '../components/Protected';
import { InstallPrompt } from '../components/InstallPrompt';
import { Money } from '../components/Money';
import { CountdownChip, SourceChip } from '../components/PaymentStatus';
import { UnitCodeForm } from '../components/UnitCodeForm';
import { ProofSheet, type ProofTarget } from '../components/ProofSheet';
import { Notice, Screen, ScreenHeader } from '../components/Screen';

function firstName(fullName: string): string {
  return fullName.trim().split(/\s+/)[0] || '';
}

/** One live tenancy's place in the rent book: what it owes next, and how late. */
interface TenancyDue {
  contractId: string;
  /** "A2 · Mikocheni Flats" — the label every proof sheet and card repeats. */
  label: string;
  unitName: string;
  propertyName: string | null;
  /** Earliest instalment still owed; `null` once the whole book is settled. */
  row: MySchedule | null;
  /** Outstanding on this tenancy's overdue rows only. */
  overdue: number;
}

/**
 * Per-tenancy next due, derived from the `/me/schedules` rows the screen has
 * already loaded (PLAN2 §18.2 — no new endpoint). Only live contracts count:
 * an ended tenancy's unpaid tail is history, not something to pay today.
 */
function tenancyDues(items: MySchedule[], unitFallback: string): TenancyDue[] {
  const byContract = new Map<string, TenancyDue>();
  const sorted = [...items].sort((a, b) => a.due_date.localeCompare(b.due_date));
  for (const row of sorted) {
    const id = row.contract?.id;
    if (!id || !isLiveContractStatus(row.contract?.status)) continue;
    let due = byContract.get(id);
    if (!due) {
      const unitName = row.contract?.unit_name || unitFallback;
      const propertyName = row.contract?.property_name ?? null;
      due = {
        contractId: id,
        label: [unitName, propertyName].filter(Boolean).join(' · '),
        unitName,
        propertyName,
        row: null,
        overdue: 0,
      };
      byContract.set(id, due);
    }
    const unsettled =
      row.status === 'pending' || row.status === 'partial' || row.status === 'overdue';
    if (unsettled && !due.row) due.row = row;
    if (row.status === 'overdue') due.overdue += scheduleOutstanding(row);
  }
  return [...byContract.values()];
}

function statusMark(t: Translator, request: LinkRequest) {
  switch (request.status) {
    case 'approved':
      return <span className="stamp stamp-paid">{t('home.request.approved')}</span>;
    case 'rejected':
      return <span className="stamp stamp-overdue">{t('home.request.rejected')}</span>;
    case 'cancelled':
      return <span className="pencil">{t('home.request.cancelled')}</span>;
    default:
      return <span className="pencil">{t('home.request.pending')}</span>;
  }
}

function RequestRow({
  request,
  onCancel,
  busy,
}: {
  request: LinkRequest;
  onCancel: (id: string) => void;
  busy: boolean;
}) {
  const t = useT();
  const locale = useLocale();
  return (
    <tr>
      <td>
        <span>
          {request.unit.name} · {request.unit.property_name}
        </span>
        <br />
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('home.request.from', {
            label: request.payment_period.label,
            date: formatDate(locale, request.start_date),
          })}
        </span>
        {request.status === 'rejected' && request.rejection_reason && (
          <>
            <br />
            <span style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>
              {t('home.request.reason', { reason: request.rejection_reason })}
            </span>
          </>
        )}
        {request.status === 'pending' && (
          <>
            <br />
            <button
              type="button"
              className="btn btn-quiet"
              style={{ width: 'auto', height: 'var(--touch-min)', paddingInline: 0 }}
              disabled={busy}
              onClick={() => onCancel(request.id)}
            >
              {busy ? t('home.request.cancelling') : t('home.request.cancel')}
            </button>
          </>
        )}
      </td>
      <td className="num">
        {statusMark(t, request)}
        <br />
        <span style={{ fontSize: 'var(--text-sm)' }}>{money(request.payment_period.amount)}</span>
      </td>
    </tr>
  );
}

function HomeContent() {
  const { user } = useMe();
  const t = useT();
  const locale = useLocale();

  const [requests, setRequests] = useState<LinkRequest[]>([]);
  const [contracts, setContracts] = useState<Contract[]>([]);
  const [schedules, setSchedules] = useState<MySchedule[]>([]);
  const [nextDue, setNextDue] = useState<MySchedule | null>(null);
  const [bankAccount, setBankAccount] = useState<BankAccount | null>(null);
  const [mobileMoney, setMobileMoney] = useState<MobileMoney | null>(null);
  const [proofOpen, setProofOpen] = useState(false);
  const [proofTarget, setProofTarget] = useState<ProofTarget | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState<string | null>(null);
  const [scanned, setScanned] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    // Contracts and schedules are additions to the rent book, not the book
    // itself: if either endpoint is unhappy the screen still renders.
    void contractApi
      .mine(signal)
      .then((res) => setContracts(res.items ?? []))
      .catch(() => setContracts([]));
    void renterApi
      .schedules(signal)
      .then((res) => {
        setSchedules(res.items ?? []);
        setNextDue(res.next_due ?? null);
        setBankAccount(res.bank_account ?? null);
        setMobileMoney(res.mobile_money ?? res.bank_account?.mobile_money ?? null);
      })
      .catch(() => {
        setSchedules([]);
        setNextDue(null);
        setBankAccount(null);
        setMobileMoney(null);
      });

    try {
      const res = await renterApi.linkRequests(signal);
      setRequests(res.items ?? []);
      setError(null);
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      // A 404 here means the renter simply has nothing on file yet as far as
      // this screen is concerned — no reason to alarm them.
      if (err instanceof ApiError && err.status === 404) setRequests([]);
      else setError(errorMessage(t, err));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    setScanned(readScannedUnit());
    return () => ac.abort();
  }, [load]);

  async function cancel(id: string) {
    setCancelling(id);
    setError(null);
    try {
      await renterApi.cancelLinkRequest(id);
      await load();
    } catch (err) {
      setError(errorMessage(t, err));
    } finally {
      setCancelling(null);
    }
  }

  const open = requests.filter((r) => r.status !== 'cancelled');
  const toSign = contracts.filter(needsRenterSignature);
  const signedWaiting = contracts.some(
    (c) => c.status === 'pending_signature' && !needsRenterSignature(c),
  );

  const unitFallback = t('common.yourUnit');
  const dues = useMemo(() => tenancyDues(schedules, unitFallback), [schedules, unitFallback]);
  /* §18.2: what is owed *now*. `overdue_total` from the API counts every
     overdue row, an ended tenancy's tail included; the hero is about money the
     renter can still act on, so the live rows are summed here instead. */
  const overdueTotal = useMemo(
    () => dues.reduce((sum, d) => sum + d.overdue, 0),
    [dues],
  );
  /* Two or more live tenancies get a card each; one keeps the Phase 16 hero. */
  const perTenancy = dues.length > 1;

  /* Proof is only offered against a running tenancy — without a `next_due`
     there is no contract to attach a claim to (API.md 409 `contract_not_active`). */
  const heroTarget: ProofTarget | null = nextDue?.contract?.id
    ? {
        contractId: nextDue.contract.id,
        contractLabel: [nextDue.contract.unit_name, nextDue.contract.property_name]
          .filter(Boolean)
          .join(' · '),
        scheduleId: nextDue.id,
        amount: scheduleOutstanding(nextDue),
      }
    : null;
  const payReference = nextDue?.contract?.unit_name ?? '';

  function openProof(target: ProofTarget | null) {
    if (!target) return;
    setProofTarget(target);
    setProofOpen(true);
  }

  function dueTarget(due: TenancyDue): ProofTarget {
    return {
      contractId: due.contractId,
      contractLabel: due.label,
      ...(due.row ? { scheduleId: due.row.id, amount: scheduleOutstanding(due.row) } : {}),
    };
  }

  /* The one line under the hero that tells a renter what happens next. A
     renter with no tenancy at all also gets a way in: the unit code from the
     sticker, typed, when the camera was never involved (§18.2). */
  const hintKey = nextDue
    ? 'home.hint.pay'
    : toSign.length > 0
      ? 'home.hint.sign'
      : signedWaiting
        ? 'home.hint.signedWaiting'
        : open.some((r) => r.status === 'approved')
          ? 'home.hint.approved'
          : 'home.hint.none';

  const hints = (
    <div style={{ display: 'grid', gap: 'var(--sp-3)', paddingTop: 'var(--sp-4)' }}>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t(hintKey)}</p>
      <p
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--sp-2)',
          color: 'var(--ink-soft)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:qr-code-linear" width={20} aria-hidden />
        {t('home.hint.qr')}
      </p>
      {hintKey === 'home.hint.none' && !loading && (
        <>
          <hr className="rule" />
          <UnitCodeForm />
        </>
      )}
    </div>
  );

  return (
    <Screen bottomBar>
      <ScreenHeader
        eyebrow={t('home.eyebrow')}
        title={t('home.greeting', {
          name: (user && firstName(user.full_name)) || t('home.greetingFallback'),
        })}
      />

      <InstallPrompt />

      {error && <Notice tone="error">{error}</Notice>}

      {toSign.length > 0 && (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <table className="ledger">
            <tbody>
              {toSign.map((c) => (
                <tr key={c.id}>
                  <td colSpan={2}>
                    <Link
                      href={`/contract/${encodeURIComponent(c.id)}`}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        gap: 'var(--sp-3)',
                        color: 'var(--primary)',
                        fontWeight: 600,
                        textDecoration: 'none',
                      }}
                    >
                      <span>
                        {t('home.sign.ready')}
                        <br />
                        <span
                          style={{
                            color: 'var(--ink-soft)',
                            fontSize: 'var(--text-sm)',
                            fontWeight: 400,
                          }}
                        >
                          {c.unit.name} · {c.unit.property_name}
                        </span>
                      </span>
                      <Icon icon="solar:alt-arrow-right-linear" width={22} aria-hidden />
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {(loading || open.length > 0) && (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('home.requests.title')}</h2>
          {loading ? (
            <p className="pencil">{t('common.loading')}</p>
          ) : (
            <table className="ledger">
              <tbody>
                {open.map((r) => (
                  <RequestRow
                    key={r.id}
                    request={r}
                    busy={cancelling === r.id}
                    onCancel={(id) => void cancel(id)}
                  />
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}

      {!loading && scanned && !open.some((r) => r.status === 'pending' || r.status === 'approved') && (
        <section style={{ display: 'grid', gap: 'var(--sp-2)' }}>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', margin: 0 }}>
            {t('home.scan.unfinished')}
          </p>
          <Link className="btn btn-secondary" href={`/u/${encodeURIComponent(scanned)}`}>
            {t('home.scan.continue')}
          </Link>
          <button
            type="button"
            className="btn btn-quiet"
            onClick={() => {
              forgetScannedUnit();
              setScanned(null);
            }}
          >
            {t('home.scan.dismiss')}
          </button>
        </section>
      )}

      {/* The hero (Phase 16 §16.3): the date, the amount, how long there is
          left, and the two things a renter can do about it. Phase 18.2: with
          more than one live tenancy it becomes one card per tenancy, because
          the second unit's rent must not wait for the first to be paid. */}
      {perTenancy ? (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <div>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('home.dues.title')}</h2>
            <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('home.dues.lead')}
            </p>
          </div>

          {dues.map((due) => (
            <div key={due.contractId} className="sheet" style={{ padding: 'var(--sp-4)' }}>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'baseline',
                  justifyContent: 'space-between',
                  gap: 'var(--sp-3)',
                  flexWrap: 'wrap',
                }}
              >
                <h3 style={{ fontSize: 'var(--text-md)', margin: 0 }}>
                  {due.unitName}
                  {due.propertyName && (
                    <>
                      {' '}
                      <span
                        style={{
                          color: 'var(--ink-soft)',
                          fontSize: 'var(--text-sm)',
                          fontWeight: 400,
                        }}
                      >
                        · {due.propertyName}
                      </span>
                    </>
                  )}
                </h3>
                {/* No chip on a settled book: "Nothing to pay yet" beside
                    "Nothing due on this unit yet" says the same thing twice. */}
                {due.row && <CountdownChip schedule={due.row} />}
              </div>

              {due.row ? (
                <div style={{ display: 'grid', gap: 'var(--sp-1)', paddingTop: 'var(--sp-3)' }}>
                  <p style={{ margin: 0, fontSize: 'var(--text-md)', fontWeight: 600 }}>
                    {formatDate(locale, due.row.due_date)}
                  </p>
                  <Money
                    amount={scheduleOutstanding(due.row)}
                    style={{ fontSize: 'var(--text-xl)', lineHeight: 1.1 }}
                  />
                  <SourceChip source={due.row.last_payment_source} />
                </div>
              ) : (
                <p style={{ margin: 'var(--sp-3) 0 0', color: 'var(--ink-soft)' }}>
                  {t('home.dues.nothing')}
                </p>
              )}

              {due.overdue > 0 && (
                <p
                  role="alert"
                  style={{
                    margin: 'var(--sp-3) 0 0',
                    color: 'var(--stamp-overdue)',
                    fontSize: 'var(--text-sm)',
                    fontWeight: 600,
                  }}
                >
                  {t('payment.overdueShort', { amount: money(due.overdue) })}
                </p>
              )}

              {due.row && (
                <div style={{ display: 'grid', paddingTop: 'var(--sp-4)' }}>
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={() => openProof(dueTarget(due))}
                  >
                    <Icon icon="solar:camera-linear" width={20} aria-hidden />
                    {t('proof.send')}
                  </button>
                </div>
              )}
            </div>
          ))}

          <div className="sheet" style={{ padding: 'var(--sp-4)' }}>
            <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
              <Link className="btn btn-secondary" href="/payments#how-to-pay">
                {t('home.hero.howToPay')}
              </Link>
            </div>
            {hints}
          </div>
        </section>
      ) : (
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
            <CountdownChip schedule={nextDue} />
          </div>

          {nextDue ? (
            <div style={{ display: 'grid', gap: 'var(--sp-1)', paddingTop: 'var(--sp-3)' }}>
              <p style={{ margin: 0, fontSize: 'var(--text-lg)', fontWeight: 600 }}>
                {formatDate(locale, nextDue.due_date)}
              </p>
              <Money
                amount={scheduleOutstanding(nextDue)}
                style={{ fontSize: 'var(--text-2xl)', lineHeight: 1.1 }}
              />
              <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {nextDue.contract?.unit_name ?? t('common.yourUnit')}
                {nextDue.contract?.property_name ? ` · ${nextDue.contract.property_name}` : ''}
              </p>
              <SourceChip source={nextDue.last_payment_source} />
            </div>
          ) : (
            <p style={{ margin: 'var(--sp-3) 0 0', color: 'var(--ink-soft)' }}>
              {t('common.nothingToPayYet')}
            </p>
          )}

          {overdueTotal > 0 && (
            <p
              role="alert"
              style={{
                margin: 'var(--sp-3) 0 0',
                color: 'var(--stamp-overdue)',
                fontSize: 'var(--text-sm)',
                fontWeight: 600,
              }}
            >
              {t('payment.overdueShort', { amount: money(overdueTotal) })}
            </p>
          )}

          <div style={{ display: 'grid', gap: 'var(--sp-3)', paddingTop: 'var(--sp-4)' }}>
            <Link className="btn btn-secondary" href="/payments#how-to-pay">
              {t('home.hero.howToPay')}
            </Link>
            {heroTarget && (
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => openProof(heroTarget)}
              >
                <Icon icon="solar:camera-linear" width={20} aria-hidden />
                {t('proof.send')}
              </button>
            )}
          </div>

          {hints}
        </section>
      )}

      <ProofSheet
        open={proofOpen}
        target={proofTarget}
        account={bankAccount}
        mobileMoney={mobileMoney}
        reference={payReference}
        onClose={() => setProofOpen(false)}
        onSubmitted={() => void load()}
      />
    </Screen>
  );
}

export default function HomePage() {
  return (
    <Protected>
      <HomeContent />
    </Protected>
  );
}
