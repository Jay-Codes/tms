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
import { useT } from '@tms/ui';
import { ApiError, publicApi, type PublicUnit } from '../../../lib/api';
import { useMe } from '../../../lib/auth';
import { errorMessage, money, priceLine } from '../../../lib/format';
import { rememberScannedUnit } from '../../../lib/scan';
import { OrgHeader, useOrgTheme } from '../../../components/OrgHeader';
import { Notice, Screen } from '../../../components/Screen';
import { LanguageToggle } from '../../../components/LanguageToggle';

export default function UnitLandingPage() {
  const t = useT();
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
        else setError(errorMessage(t, err));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [unitCode, t]);

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
        <p className="pencil">{t('unit.looking')}</p>
      </Screen>
    );
  }

  if (notFound) {
    return (
      <Screen>
        <header style={{ display: 'grid', gap: 'var(--sp-2)' }}>
          <h1 style={{ fontSize: 'var(--text-xl)' }}>{t('unit.notFound.title')}</h1>
          <p style={{ color: 'var(--ink-soft)' }}>{t('unit.notFound.lead')}</p>
        </header>
        <Link className="btn btn-secondary" href="/">
          {t('common.goToRentBook')}
        </Link>
      </Screen>
    );
  }

  if (error || !unit) {
    return (
      <Screen>
        <Notice tone="error">{error ?? t('unit.loadFailed')}</Notice>
        <button className="btn btn-secondary" onClick={() => router.refresh()}>
          {t('common.tryAgain')}
        </button>
      </Screen>
    );
  }

  const registerHref = `/register?next=${encodeURIComponent(`/u/${unitCode}`)}`;
  const loginHref = `/login?next=${encodeURIComponent(`/u/${unitCode}`)}`;

  return (
    <Screen>
      <OrgHeader branding={unit.branding} />

      {/* Pre-auth: whichever language is picked here is the one the account
          is registered with (lib/locale.tsx → `POST /auth/register/renter`). */}
      <LanguageToggle align="end" />

      <header style={{ display: 'grid', gap: 'var(--sp-1)' }}>
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', margin: 0 }}>
          {unit.property.name}
          {unit.property.location_text ? ` · ${unit.property.location_text}` : ''}
        </p>
        <h1 style={{ fontSize: 'var(--text-2xl)' }}>{unit.unit.name}</h1>
      </header>

      {unit.price ? (
        <p className="amount" style={{ fontSize: 'var(--text-xl)', margin: 0 }}>
          {priceLine(t, unit.price.amount, unit.price.period_days, unit.price.currency)}
        </p>
      ) : (
        <p className="pencil">{t('unit.noPrice')}</p>
      )}

      {unit.occupied ? (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <Notice tone="error">{t('unit.occupied')}</Notice>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', margin: 0 }}>
            {t('unit.occupiedLead', {
              unit: unit.unit.name,
              org: unit.branding.display_name,
            })}
          </p>
        </section>
      ) : (
        <>
          <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('unit.howYouPay')}</h2>
            <table className="ledger">
              <thead>
                <tr>
                  <th>{t('common.period')}</th>
                  <th className="num">{t('unit.eachPayment')}</th>
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
                          {t('unit.recommended')}
                        </span>
                      )}
                      <br />
                      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                        {t('unit.everyDays', { days: p.days })}
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
                      <span className="pencil">{t('unit.noPeriods')}</span>
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
              {t('unit.signAfterApproval')}
            </p>
            {auth.status === 'authenticated' ? (
              <Link
                className="btn btn-primary"
                href={`/u/${encodeURIComponent(unitCode)}/connect`}
              >
                {t('common.continue')}
              </Link>
            ) : (
              <>
                <Link className="btn btn-primary" href={registerHref}>
                  {t('unit.register')}
                </Link>
                <Link className="btn btn-quiet" href={loginHref}>
                  {t('unit.login')}
                </Link>
              </>
            )}
          </section>
        </>
      )}
    </Screen>
  );
}
