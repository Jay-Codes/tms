'use client';

/**
 * Connect to a unit — FLOWS.md flow 2, steps 4–7.
 *
 * KYC (when the renter has none yet), then the three choices the link request
 * carries: payment period, tenancy length in days, start date. The schedule
 * table under them is a client-side preview so the numbers move as the renter
 * does (`lib/schedule.ts`); the moment `POST /units/{code}/link` answers, the
 * screen switches to the server's `schedule_preview`, which is authoritative.
 *
 * Signing is Phase 4 — this flow ends at "Waiting for landlord approval".
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { Icon } from '@iconify/react';
import { useLocale, useT, type Locale, type Translator } from '@tms/ui';
import {
  ApiError,
  publicApi,
  renterApi,
  type LinkRequest,
  type OfferedPeriod,
  type PublicUnit,
  type RenterProfile,
} from '../../../../lib/api';
import { useMe } from '../../../../lib/auth';
import {
  addDays,
  days as dayLabel,
  errorMessage,
  formatDate,
  money,
  todayIso,
} from '../../../../lib/format';
import { buildPreview, deriveEndDate, termQuickPicks } from '../../../../lib/schedule';
import { forgetScannedUnit, rememberScannedUnit } from '../../../../lib/scan';
import { Protected } from '../../../../components/Protected';
import { OrgHeader, useOrgTheme } from '../../../../components/OrgHeader';
import { Notice, Screen } from '../../../../components/Screen';
import { KycForm } from '../../../profile/KycForm';

/** Recommended periods first, then the landlord's custom ones. */
function sortPeriods(periods: OfferedPeriod[]): OfferedPeriod[] {
  return [...periods].sort((a, b) => {
    if (a.is_recommended !== b.is_recommended) return a.is_recommended ? -1 : 1;
    return a.days - b.days;
  });
}

function ConnectContent() {
  const t = useT();
  const locale = useLocale();
  const params = useParams<{ unit_code: string }>();
  const unitCode = typeof params?.unit_code === 'string' ? params.unit_code : '';
  const { user } = useMe();

  const [unit, setUnit] = useState<PublicUnit | null>(null);
  const [profile, setProfile] = useState<RenterProfile | null>(null);
  const [existing, setExisting] = useState<LinkRequest | null>(null);
  const [submitted, setSubmitted] = useState<LinkRequest | null>(null);

  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [occupied, setOccupied] = useState(false);

  const [periodId, setPeriodId] = useState('');
  const [termDays, setTermDays] = useState(0);
  const [startDate, setStartDate] = useState(todayIso());
  const [accepted, setAccepted] = useState(false);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [needsKyc, setNeedsKyc] = useState(false);

  useOrgTheme(unit?.branding, unit?.org?.slug);

  useEffect(() => {
    if (unitCode) rememberScannedUnit(unitCode);
  }, [unitCode]);

  const periods = useMemo(() => sortPeriods(unit?.periods ?? []), [unit?.periods]);
  const period = periods.find((p) => p.id === periodId) ?? null;

  /* Public unit + profile + any request the renter already has for this unit,
     in one pass. A failing link-request list is not fatal: the submit below
     surfaces the real problem (and a duplicate answers 409 anyway). */
  useEffect(() => {
    if (!unitCode) return;
    const ac = new AbortController();
    let live = true;
    (async () => {
      try {
        const [unitRes, profileRes, requestsRes] = await Promise.all([
          publicApi.unit(unitCode, ac.signal),
          renterApi.profile(ac.signal).catch((err: unknown) => {
            if (err instanceof ApiError && err.status === 404) return null;
            throw err;
          }),
          renterApi.linkRequests(ac.signal).catch(() => null),
        ]);
        if (!live) return;

        setUnit(unitRes);
        setProfile(profileRes?.profile ?? null);
        setOccupied(unitRes.occupied);
        setNeedsKyc((profileRes?.profile.kyc_status ?? 'none') === 'none');

        const open = (requestsRes?.items ?? []).find(
          (r) =>
            r.unit.id === unitRes.unit.id && (r.status === 'pending' || r.status === 'approved'),
        );
        setExisting(open ?? null);

        const first = sortPeriods(unitRes.periods)[0];
        if (first) {
          setPeriodId(first.id);
          setTermDays(first.days);
        }
        setLoadError(null);
      } catch (err) {
        if (!live || (err instanceof DOMException && err.name === 'AbortError')) return;
        setLoadError(errorMessage(t, err));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [unitCode, t]);

  const choosePeriod = useCallback(
    (p: OfferedPeriod) => {
      setPeriodId(p.id);
      // A term shorter than one period is not payable — pull it up.
      setTermDays((t) => (t < p.days ? p.days : t));
    },
    [],
  );

  const preview = useMemo(
    () => (unit && period ? buildPreview(unit.price, period, termDays, startDate) : null),
    [unit, period, termDays, startDate],
  );

  const endDate = useMemo(
    () => (termDays > 0 ? deriveEndDate(startDate, termDays) : null),
    [startDate, termDays],
  );

  const onKycSaved = useCallback((p: RenterProfile) => {
    setProfile(p);
    if (p.kyc_status !== 'none') setNeedsKyc(false);
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!period) {
      setError(t('connect.error.period'));
      return;
    }
    if (termDays < period.days) {
      setError(t('connect.error.term', { days: dayLabel(t, period.days) }));
      return;
    }
    if (!accepted) {
      setError(t('connect.error.terms'));
      return;
    }

    setBusy(true);
    try {
      const res = await renterApi.createLinkRequest(unitCode, {
        payment_period_id: period.id,
        term_days: termDays,
        start_date: startDate,
        accepted_terms: true,
      });
      setSubmitted(res.request);
      forgetScannedUnit();
    } catch (err) {
      if (err instanceof ApiError && err.status === 412) {
        setNeedsKyc(true);
        setError(t('connect.error.kyc'));
      } else if (err instanceof ApiError && err.status === 409 && err.is('unit_occupied')) {
        setOccupied(true);
      } else if (err instanceof ApiError && err.status === 409) {
        // Duplicate pending request — show the one already on file.
        const list = await renterApi.linkRequests().catch(() => null);
        const open = (list?.items ?? []).find(
          (r) => r.unit.id === unit?.unit.id && (r.status === 'pending' || r.status === 'approved'),
        );
        if (open) setExisting(open);
        else setError(errorMessage(t, err));
      } else {
        setError(errorMessage(t, err));
      }
    } finally {
      setBusy(false);
    }
  }

  if (loading) {
    return (
      <Screen bottomBar>
        <p className="pencil">{t('common.loading')}</p>
      </Screen>
    );
  }

  if (loadError || !unit) {
    return (
      <Screen bottomBar>
        <Notice tone="error">{loadError ?? t('unit.loadFailed')}</Notice>
        <Link className="btn btn-secondary" href="/">
          {t('common.goToRentBook')}
        </Link>
      </Screen>
    );
  }

  const request = submitted ?? existing;

  /* ---- terminal states ---- */

  if (occupied && !request) {
    return (
      <Screen bottomBar>
        <OrgHeader branding={unit.branding} />
        <h1 style={{ fontSize: 'var(--text-xl)' }}>{unit.unit.name}</h1>
        <Notice tone="error">{t('unit.occupied')}</Notice>
        <Link className="btn btn-secondary" href="/">
          {t('common.goToRentBook')}
        </Link>
      </Screen>
    );
  }

  if (request) {
    const approved = request.status === 'approved';
    return (
      <Screen bottomBar>
        <OrgHeader branding={unit.branding} />

        <header style={{ display: 'grid', gap: 'var(--sp-2)' }}>
          <span className={approved ? 'stamp stamp-paid' : 'pencil'} style={{ justifySelf: 'start' }}>
            {approved ? t('home.request.approved') : t('connect.sent.stamp')}
          </span>
          <h1 style={{ fontSize: 'var(--text-xl)' }}>
            {approved ? t('connect.sent.titleApproved') : t('connect.sent.titleWaiting')}
          </h1>
          <p style={{ color: 'var(--ink-soft)' }}>
            {t(approved ? 'connect.sent.leadApproved' : 'connect.sent.leadWaiting', {
              org: request.org.name,
              unit: request.unit.name,
            })}
          </p>
        </header>

        <RequestLedger request={request} />

        <Link className="btn btn-primary" href="/">
          {t('common.goToRentBook')}
        </Link>
      </Screen>
    );
  }

  /* ---- the form ---- */

  return (
    <Screen bottomBar>
      <OrgHeader branding={unit.branding} />

      <header style={{ display: 'grid', gap: 'var(--sp-1)' }}>
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', margin: 0 }}>
          {unit.property.name}
        </p>
        <h1 style={{ fontSize: 'var(--text-xl)' }}>
          {t('connect.title', { unit: unit.unit.name })}
        </h1>
      </header>

      {needsKyc && (
        <KycForm
          initialProfile={profile}
          fallbackName={user?.full_name}
          onSaved={onKycSaved}
          submitLabel={t('kyc.connect.submit')}
          heading={t('kyc.connect.heading')}
          lead={t('kyc.connect.lead')}
        />
      )}

      {!needsKyc && (
        <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-5)' }} noValidate>
          {error && <Notice tone="error">{error}</Notice>}

          <fieldset style={{ border: 0, padding: 0, margin: 0, display: 'grid', gap: 'var(--sp-3)' }}>
            <legend style={{ fontSize: 'var(--text-lg)', fontWeight: 600, padding: 0 }}>
              {t('connect.howPay')}
            </legend>
            <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
              {periods.map((p) => (
                <label
                  key={p.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--sp-3)',
                    minHeight: 'var(--row-h)',
                    padding: '0 var(--sp-3)',
                    border: `1px solid ${p.id === periodId ? 'var(--primary)' : 'var(--rule)'}`,
                    background: p.id === periodId ? 'var(--primary-soft)' : 'transparent',
                    borderRadius: 'var(--radius-md)',
                    cursor: 'pointer',
                  }}
                >
                  <input
                    type="radio"
                    name="period"
                    value={p.id}
                    checked={p.id === periodId}
                    onChange={() => choosePeriod(p)}
                  />
                  <span style={{ flex: 1 }}>
                    {p.label}
                    {p.is_recommended && (
                      <span
                        className="stamp"
                        style={{
                          marginLeft: 'var(--sp-2)',
                          transform: 'none',
                          border: '1px solid currentColor',
                          outline: 0,
                          color: 'var(--ink-soft)',
                        }}
                      >
                        {t('unit.recommended')}
                      </span>
                    )}
                    <br />
                    <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                      {t('unit.everyDays', { days: p.days })}
                    </span>
                  </span>
                  <span className="num">
                    {p.amount === null ? <span className="pencil">—</span> : money(p.amount)}
                  </span>
                </label>
              ))}
            </div>
          </fieldset>

          <fieldset style={{ border: 0, padding: 0, margin: 0, display: 'grid', gap: 'var(--sp-3)' }}>
            <legend style={{ fontSize: 'var(--text-lg)', fontWeight: 600, padding: 0 }}>
              {t('connect.howLong')}
            </legend>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
              {period &&
                termQuickPicks(period.days).map((d) => (
                  <button
                    key={d}
                    type="button"
                    className={termDays === d ? 'btn btn-secondary' : 'btn btn-quiet'}
                    style={{ width: 'auto', flex: '1 1 42%' }}
                    onClick={() => setTermDays(d)}
                  >
                    {dayLabel(t, d)}
                  </button>
                ))}
            </div>
            <div className="field">
              <label htmlFor="term">{t('connect.termDays')}</label>
              <input
                id="term"
                className="input"
                type="number"
                inputMode="numeric"
                min={period?.days ?? 1}
                step={1}
                value={termDays || ''}
                onChange={(e) => setTermDays(Math.max(0, Math.floor(Number(e.target.value) || 0)))}
              />
              {period && (
                <span className="hint">
                  {t('connect.termHint', { days: dayLabel(t, period.days) })}
                </span>
              )}
            </div>
          </fieldset>

          <div className="field">
            <label htmlFor="start">{t('connect.startDate')}</label>
            <input
              id="start"
              className="input"
              type="date"
              // API.md: `start_date(date ≥ today-7)`.
              min={addDays(todayIso(), -7)}
              value={startDate}
              onChange={(e) => setStartDate(e.target.value || todayIso())}
            />
            <span className="hint">
              {endDate
                ? t('connect.endsOn', { date: formatDate(locale, endDate) })
                : t('connect.pickMoveIn')}
            </span>
          </div>

          {preview && (
            <section style={{ display: 'grid', gap: 'var(--sp-2)' }}>
              <h2 style={{ fontSize: 'var(--text-lg)' }}>
                {t.n('connect.payments', preview.count)}
              </h2>
              <table className="ledger">
                <thead>
                  <tr>
                    <th>{t('common.due')}</th>
                    <th className="num">{t('common.amount')}</th>
                  </tr>
                </thead>
                <tbody>
                  {preview.rows.map((row) => (
                    <tr key={row.n}>
                      <td>
                        {formatDate(locale, row.due)}
                        {row.partial && (
                          <>
                            <br />
                            <span className="pencil">
                              {t('connect.covers', { days: dayLabel(t, row.daysCovered) })}
                            </span>
                          </>
                        )}
                      </td>
                      <td className="num">{money(row.amount)}</td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td>{t('common.total')}</td>
                    <td className="num">{money(preview.total)}</td>
                  </tr>
                </tbody>
              </table>
              <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-xs)', margin: 0 }}>
                {t('connect.estimate', { org: unit.branding.display_name })}
              </p>
            </section>
          )}

          <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('doc.terms')}</h2>
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
            <label
              style={{
                display: 'flex',
                gap: 'var(--sp-3)',
                alignItems: 'flex-start',
                minHeight: 'var(--touch-min)',
                cursor: 'pointer',
              }}
            >
              <input
                type="checkbox"
                checked={accepted}
                onChange={(e) => setAccepted(e.target.checked)}
                style={{ marginTop: '0.35em' }}
              />
              <span>
                {t('connect.accept', {
                  org: unit.branding.display_name,
                  unit: unit.unit.name,
                })}
              </span>
            </label>
          </section>

          <button className="btn btn-primary" type="submit" disabled={busy || !accepted}>
            {busy ? t('common.sending') : t('connect.submit')}
          </button>
        </form>
      )}
    </Screen>
  );
}

/** The server's own numbers, once a request exists. */
function RequestLedger({ request }: { request: LinkRequest }) {
  const t = useT();
  const locale = useLocale();
  const p = request.schedule_preview;
  return (
    <table className="ledger ledger-kv">
      <tbody>
        <tr>
          <td style={{ color: 'var(--ink-soft)' }}>{t('common.unit')}</td>
          <td className="num">
            {request.unit.name} · {request.unit.property_name}
          </td>
        </tr>
        <tr>
          <td style={{ color: 'var(--ink-soft)' }}>{t('connect.ledger.paying')}</td>
          <td className="num">
            {request.payment_period.label} · {money(request.payment_period.amount)}
          </td>
        </tr>
        <tr>
          <td style={{ color: 'var(--ink-soft)' }}>{t('connect.ledger.tenancy')}</td>
          <td className="num">{dayLabel(t, request.term_days)}</td>
        </tr>
        <tr>
          <td style={{ color: 'var(--ink-soft)' }}>{t('connect.ledger.dates')}</td>
          <td className="num">
            <span className="nowrap">{formatDate(locale, request.start_date)}</span> –{' '}
            <span className="nowrap">{formatDate(locale, request.end_date)}</span>
          </td>
        </tr>
        {p && (
          <>
            <tr>
              <td style={{ color: 'var(--ink-soft)' }}>{t('connect.ledger.payments')}</td>
              <td className="num">
                {t('connect.ledger.firstDue', {
                  count: p.count,
                  date: formatDate(locale, p.first_due),
                })}
              </td>
            </tr>
            <tr className="total">
              <td>{t('common.total')}</td>
              <td className="num">{money(p.total)}</td>
            </tr>
          </>
        )}
      </tbody>
    </table>
  );
}

export default function ConnectPage() {
  return (
    <Protected>
      <ConnectContent />
    </Protected>
  );
}
