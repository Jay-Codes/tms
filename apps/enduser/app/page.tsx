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
 */

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { Icon } from '@iconify/react';
import {
  ApiError,
  contractApi,
  needsRenterSignature,
  renterApi,
  scheduleOutstanding,
  type Contract,
  type LinkRequest,
  type MySchedule,
} from '../lib/api';
import { useLocale, useT, type Translator } from '@tms/ui';
import { useMe } from '../lib/auth';
import { errorMessage, formatDate, money } from '../lib/format';
import { forgetScannedUnit, readScannedUnit } from '../lib/scan';
import { Protected } from '../components/Protected';
import { InstallPrompt } from '../components/InstallPrompt';
import { Money } from '../components/Money';
import { NextDueChip } from '../components/PaymentStatus';
import { Notice, Screen, ScreenHeader } from '../components/Screen';

function firstName(fullName: string): string {
  return fullName.trim().split(/\s+/)[0] || '';
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
  const [nextDue, setNextDue] = useState<MySchedule | null>(null);
  const [overdueTotal, setOverdueTotal] = useState(0);
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
        setNextDue(res.next_due ?? null);
        setOverdueTotal(res.overdue_total ?? 0);
      })
      .catch(() => {
        setNextDue(null);
        setOverdueTotal(0);
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

      <section className="sheet" style={{ padding: 'var(--sp-4)' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'baseline',
            justifyContent: 'space-between',
            gap: 'var(--sp-3)',
          }}
        >
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('payment.next.title')}</h2>
          <Link
            href="/payments"
            style={{ color: 'var(--primary)', fontSize: 'var(--text-sm)', fontWeight: 600 }}
          >
            {t('payment.next.howToPay')}
          </Link>
        </div>

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

        <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
          <tbody>
            {nextDue ? (
              <tr>
                <td>
                  {formatDate(locale, nextDue.due_date)}
                  <br />
                  <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    {nextDue.contract?.unit_name ?? t('common.yourUnit')}
                  </span>
                </td>
                <td className="num">
                  <Money amount={scheduleOutstanding(nextDue)} />
                  <br />
                  <NextDueChip schedule={nextDue} />
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

        <div style={{ display: 'grid', gap: 'var(--sp-3)', paddingTop: 'var(--sp-4)' }}>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {nextDue
              ? t('home.hint.pay')
              : toSign.length > 0
                ? t('home.hint.sign')
                : signedWaiting
                  ? t('home.hint.signedWaiting')
                  : open.some((r) => r.status === 'approved')
                    ? t('home.hint.approved')
                    : t('home.hint.none')}
          </p>
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
        </div>
      </section>
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
