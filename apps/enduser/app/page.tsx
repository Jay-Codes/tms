'use client';

/**
 * Renter home — the rent book.
 *
 * Phase 3 fills the top of it with `GET /me/link-requests`: the units the
 * renter has asked to connect to, pencilled while a landlord is still
 * thinking and stamped once they decide. Payments land in Phase 5, so "Next
 * payment" keeps its empty state.
 */

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { Icon } from '@iconify/react';
import { ApiError, renterApi, type LinkRequest } from '../lib/api';
import { useMe } from '../lib/auth';
import { errorMessage, formatDate, money } from '../lib/format';
import { forgetScannedUnit, readScannedUnit } from '../lib/scan';
import { Protected } from '../components/Protected';
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
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState<string | null>(null);
  const [scanned, setScanned] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
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

  return (
    <Screen bottomBar>
      <ScreenHeader
        eyebrow="Your rent book"
        title={`Habari, ${user ? firstName(user.full_name) : 'there'}.`}
      />

      {error && <Notice tone="error">{error}</Notice>}

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
        <h2 style={{ fontSize: 'var(--text-lg)' }}>Next payment</h2>

        <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
          <tbody>
            <tr>
              <td colSpan={2} style={{ color: 'var(--ink-soft)' }}>
                Nothing to pay yet
              </td>
              <td className="num">
                <span className="pencil">—</span>
              </td>
            </tr>
          </tbody>
        </table>

        <div style={{ display: 'grid', gap: 'var(--sp-3)', paddingTop: 'var(--sp-4)' }}>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {open.some((r) => r.status === 'approved')
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
