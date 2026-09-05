'use client';

/**
 * QR landing — FLOWS.md flow 2, steps 1–2.
 *
 * The sticker on the door points here. `GET /public/units/{unit_code}` needs
 * no session, so the renter sees their landlord's branding, the unit and the
 * price before deciding to register. A renter who already has an account is
 * sent straight on to the connect step (flow 2's "alternate entry").
 */

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { Icon } from '@iconify/react';
import { ApiError, publicApi, type PublicUnit } from '../../../lib/api';
import { useMe } from '../../../lib/auth';
import { errorMessage, money, priceLine } from '../../../lib/format';
import { rememberScannedUnit } from '../../../lib/scan';
import { OrgHeader, useOrgTheme } from '../../../components/OrgHeader';
import { Notice, Screen } from '../../../components/Screen';

export default function UnitLandingPage() {
  const params = useParams<{ unit_code: string }>();
  const unitCode = typeof params?.unit_code === 'string' ? params.unit_code : '';
  const router = useRouter();
  const auth = useMe();

  const [unit, setUnit] = useState<PublicUnit | null>(null);
  const [loading, setLoading] = useState(true);
  const [notFound, setNotFound] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useOrgTheme(unit?.branding, unit?.org?.slug);

  /* Keep the code for the length of the tab: `?next=` covers the normal
     register/login round trip, this covers a renter who wanders off it. */
  useEffect(() => {
    if (unitCode) rememberScannedUnit(unitCode);
  }, [unitCode]);

  useEffect(() => {
    if (!unitCode) return;
    const ac = new AbortController();
    let live = true;
    (async () => {
      try {
        const res = await publicApi.unit(unitCode, ac.signal);
        if (!live) return;
        setUnit(res);
        setNotFound(false);
        setError(null);
      } catch (err) {
        if (!live || (err instanceof DOMException && err.name === 'AbortError')) return;
        if (err instanceof ApiError && err.status === 404) setNotFound(true);
        else setError(errorMessage(err));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [unitCode]);

  /* Already signed in → skip the sales pitch, go to step 5. */
  const canConnect = !!unit && !unit.occupied;
  useEffect(() => {
    if (auth.status === 'authenticated' && canConnect) {
      router.replace(`/u/${encodeURIComponent(unitCode)}/connect`);
    }
  }, [auth.status, canConnect, router, unitCode]);

  if (loading) {
    return (
      <Screen>
        <p className="pencil">Looking up this unit…</p>
      </Screen>
    );
  }

  if (notFound) {
    return (
      <Screen>
        <header style={{ display: 'grid', gap: 'var(--sp-2)' }}>
          <h1 style={{ fontSize: 'var(--text-xl)' }}>This code doesn&rsquo;t open a unit</h1>
          <p style={{ color: 'var(--ink-soft)' }}>
            The QR code may be out of date, or the landlord has taken this unit off the market.
            Check the sticker and try again, or ask the landlord for a new one.
          </p>
        </header>
        <Link className="btn btn-secondary" href="/">
          Go to my rent book
        </Link>
      </Screen>
    );
  }

  if (error || !unit) {
    return (
      <Screen>
        <Notice tone="error">{error ?? 'This unit could not be loaded.'}</Notice>
        <button className="btn btn-secondary" onClick={() => router.refresh()}>
          Try again
        </button>
      </Screen>
    );
  }

  const registerHref = `/register?next=${encodeURIComponent(`/u/${unitCode}`)}`;
  const loginHref = `/login?next=${encodeURIComponent(`/u/${unitCode}`)}`;

  return (
    <Screen>
      <OrgHeader branding={unit.branding} />

      <header style={{ display: 'grid', gap: 'var(--sp-1)' }}>
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', margin: 0 }}>
          {unit.property.name}
          {unit.property.location_text ? ` · ${unit.property.location_text}` : ''}
        </p>
        <h1 style={{ fontSize: 'var(--text-2xl)' }}>{unit.unit.name}</h1>
      </header>

      {unit.price ? (
        <p className="amount" style={{ fontSize: 'var(--text-xl)', margin: 0 }}>
          {priceLine(unit.price.amount, unit.price.period_days, unit.price.currency)}
        </p>
      ) : (
        <p className="pencil">Price not set yet — ask the landlord.</p>
      )}

      {unit.occupied ? (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <Notice tone="error">Unit occupied — contact landlord.</Notice>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', margin: 0 }}>
            Someone is already renting {unit.unit.name}. {unit.branding.display_name} can tell you
            when it frees up, or point you at another unit.
          </p>
        </section>
      ) : (
        <>
          <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>How you can pay</h2>
            <table className="ledger">
              <thead>
                <tr>
                  <th>Period</th>
                  <th className="num">Each payment</th>
                </tr>
              </thead>
              <tbody>
                {unit.periods.map((p) => (
                  <tr key={p.id}>
                    <td>
                      {p.label}
                      {p.is_recommended && (
                        <span
                          className="pencil"
                          style={{ marginLeft: 'var(--sp-2)', fontStyle: 'normal' }}
                        >
                          Recommended
                        </span>
                      )}
                      <br />
                      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                        every {p.days} days
                      </span>
                    </td>
                    <td className="num">
                      {p.amount === null ? <span className="pencil">—</span> : money(p.amount)}
                    </td>
                  </tr>
                ))}
                {unit.periods.length === 0 && (
                  <tr>
                    <td colSpan={2}>
                      <span className="pencil">No payment periods offered yet.</span>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </section>

          <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
            <p
              style={{
                display: 'flex',
                gap: 'var(--sp-2)',
                alignItems: 'flex-start',
                margin: 0,
                color: 'var(--ink-soft)',
                fontSize: 'var(--text-sm)',
              }}
            >
              <Icon icon="solar:document-text-linear" width={18} aria-hidden />
              You will review and sign the full contract after approval.
            </p>
            {auth.status === 'authenticated' ? (
              <Link
                className="btn btn-primary"
                href={`/u/${encodeURIComponent(unitCode)}/connect`}
              >
                Continue
              </Link>
            ) : (
              <>
                <Link className="btn btn-primary" href={registerHref}>
                  Register to connect
                </Link>
                <Link className="btn btn-quiet" href={loginHref}>
                  Log in
                </Link>
              </>
            )}
          </section>
        </>
      )}
    </Screen>
  );
}
