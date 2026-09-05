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
import { useMe } from '../lib/auth';
import { errorMessage, formatDate, money } from '../lib/format';
import { forgetScannedUnit, readScannedUnit } from '../lib/scan';
import { Protected } from '../components/Protected';
import { NextDueChip } from '../components/PaymentStatus';
import { Notice, Screen, ScreenHeader } from '../components/Screen';

function firstName(fullName: string): string {
  return fullName.trim().split(/\s+/)[0] || 'there';
}

function statusMark(request: LinkRequest) {
  switch (request.status) {
    case 'approved':
      return <span className="stamp stamp-paid">Approved</span>;
    case 'rejected':
      return <span className="stamp stamp-overdue">Rejected</span>;
    case 'cancelled':
      return <span className="pencil">Cancelled</span>;
    default:
      return <span className="pencil">Waiting for approval</span>;
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
  return (
    <tr>
      <td>
        <span>
          {request.unit.name} · {request.unit.property_name}
        </span>
        <br />
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {request.payment_period.label} · from {formatDate(request.start_date)}
        </span>
        {request.status === 'rejected' && request.rejection_reason && (
          <>
            <br />
            <span style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>
              Reason: {request.rejection_reason}
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
              {busy ? 'Cancelling…' : 'Cancel request'}
            </button>
          </>
        )}
      </td>
      <td className="num">
        {statusMark(request)}
        <br />
        <span style={{ fontSize: 'var(--text-sm)' }}>{money(request.payment_period.amount)}</span>
      </td>
    </tr>
  );
}

function HomeContent() {
  const { user } = useMe();

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
      else setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

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
      setError(errorMessage(err));
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
        eyebrow="Your rent book"
        title={`Habari, ${user ? firstName(user.full_name) : 'there'}.`}
      />

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
                        Contract ready to sign
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
          <h2 style={{ fontSize: 'var(--text-lg)' }}>Your requests</h2>
          {loading ? (
            <p className="pencil">Loading…</p>
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
            You scanned a unit but didn&rsquo;t finish.
          </p>
          <Link className="btn btn-secondary" href={`/u/${encodeURIComponent(scanned)}`}>
            Continue where you left off
          </Link>
          <button
            type="button"
            className="btn btn-quiet"
            onClick={() => {
              forgetScannedUnit();
              setScanned(null);
            }}
          >
            Dismiss
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
          <h2 style={{ fontSize: 'var(--text-lg)' }}>Next payment</h2>
          <Link
            href="/payments"
            style={{ color: 'var(--primary)', fontSize: 'var(--text-sm)', fontWeight: 600 }}
          >
            How to pay →
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
            {money(overdueTotal)} overdue.
          </p>
        )}

        <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
          <tbody>
            {nextDue ? (
              <tr>
                <td>
                  {formatDate(nextDue.due_date)}
                  <br />
                  <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                    {nextDue.contract?.unit_name ?? 'Your unit'}
                  </span>
                </td>
                <td className="num">
                  <span className="amount">{money(scheduleOutstanding(nextDue))}</span>
                  <br />
                  <NextDueChip schedule={nextDue} />
                </td>
              </tr>
            ) : (
              <tr>
                <td style={{ color: 'var(--ink-soft)' }}>Nothing to pay yet</td>
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
              ? 'Pay your landlord directly, then they record it here.'
              : toSign.length > 0
                ? 'Sign your contract and payments will appear here.'
                : signedWaiting
                  ? 'You have signed — payments start once your landlord countersigns.'
                  : open.some((r) => r.status === 'approved')
                    ? 'Payments start once your contract is signed and activated.'
                    : 'No tenancy yet — scan your unit’s QR code.'}
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
            The sticker on your door opens this app with the unit already filled in.
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
