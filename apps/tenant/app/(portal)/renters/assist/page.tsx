'use client';

/**
 * Onboard in person — FLOWS 2b, PLAN2 Phase 25 (the Phase 18.2 screen).
 *
 * The renter's OTP text never came, and they are standing in front of the
 * landlord. `POST /assist` writes a code into the very slot the SMS would
 * have filled and hands it back; this page shows it large, with a QR of the
 * renter's link, and follows the renter by polling `GET /assist/{id}` every
 * five seconds: waiting → registered → request received → approved.
 *
 * Entry: `?unit=` (unit page), `?renter=` (renter page, "Help log in" — the
 * phone is read from the renter record, never carried in the URL), `?id=`
 * (reopening a session from the list below). A reopened session has no code
 * on screen: the code is never readable again, only replaceable.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { mmss, ShownCode, useSecondsLeft } from '../../../../components/AssistBits';
import { Field, ProblemNote } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import {
  ApiError,
  assistApi,
  assistOpenSessionId,
  rentersApi,
  toApiError,
  unitsApi,
  type AssistCode,
  type AssistDetail,
  type AssistStatusDetail,
  type Unit,
} from '../../../../lib/api';
import { fmtDateTime } from '../../../../lib/format';
import { TableScroll, useT } from '@tms/ui';

/** FLOWS 2b step 5 — the landlord's screen flips as the renter progresses. */
const STEPS: Exclude<AssistStatusDetail, 'closed'>[] = ['waiting', 'registered', 'requested', 'approved'];
const POLL_MS = 5000;

/* ------------------------------- start ------------------------------------ */

function StartForm({
  unitParam,
  renterParam,
  onOpened,
  onReopen,
}: {
  unitParam: string;
  renterParam: string;
  onOpened: (id: string, code: AssistCode) => void;
  onReopen: (id: string) => void;
}) {
  const t = useT();
  const [units, setUnits] = useState<Unit[] | null>(null);
  const [unitId, setUnitId] = useState(unitParam);
  const [phone, setPhone] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    unitsApi
      .list({ limit: 200 }, ac.signal)
      .then((res) => setUnits(res.items ?? []))
      .catch(() => setUnits([]));
    return () => ac.abort();
  }, []);

  /* "Help log in" from a renter's page: their number, not typed again. */
  useEffect(() => {
    if (!renterParam) return;
    const ac = new AbortController();
    rentersApi
      .get(renterParam, ac.signal)
      .then((res) => {
        if (res.renter?.phone) setPhone(res.renter.phone);
        const unit = res.contracts?.find((c) => c.status === 'pending_signature' || c.status === 'active')?.unit?.id;
        if (unit && !unitParam) setUnitId(unit);
      })
      .catch(() => undefined);
    return () => ac.abort();
  }, [renterParam, unitParam]);

  /* A renter is onboarded onto a unit they can still take — an occupied
     unit is left out unless the page was opened from it. */
  const choices = useMemo(
    () => (units ?? []).filter((u) => u.status !== 'occupied' || u.id === unitId),
    [units, unitId],
  );

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setOpenId(null);
    try {
      const res = await assistApi.open({ phone: phone.trim(), unit_id: unitId });
      onOpened(res.session.id, res);
    } catch (err) {
      const apiErr = toApiError(err);
      setOpenId(assistOpenSessionId(apiErr));
      setError(apiErr);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={(e) => void submit(e)} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 520 }} noValidate>
      <p style={{ color: 'var(--ink-soft)' }}>{t('assist.start.lead')}</p>
      <Field id="as_phone" label={t('assist.start.phone')} hint={t('assist.start.phone_hint')} error={error?.errors.phone}>
        <input
          id="as_phone"
          className="input"
          type="tel"
          inputMode="tel"
          autoComplete="off"
          placeholder="07XX XXX XXX"
          value={phone}
          onChange={(e) => setPhone(e.target.value)}
        />
      </Field>
      <Field id="as_unit" label={t('common.unit')} error={error?.errors.unit_id}>
        <select id="as_unit" className="input" value={unitId} onChange={(e) => setUnitId(e.target.value)}>
          <option value="">{units === null ? t('common.loading') : t('assist.start.pick_unit')}</option>
          {choices.map((u) => (
            <option key={u.id} value={u.id}>
              {u.property_name} · {u.name}
            </option>
          ))}
        </select>
      </Field>
      <ProblemNote error={error} />
      {openId ? (
        <div>
          <button type="button" className="btn btn-secondary" onClick={() => onReopen(openId)}>
            {t('assist.start.open_existing')}
          </button>
        </div>
      ) : null}
      <div>
        <button type="submit" className="btn btn-primary" disabled={busy || !phone.trim() || !unitId}>
          <Icon icon="solar:users-group-rounded-linear" width={20} />{' '}
          {busy ? t('assist.start.busy') : t('assist.start.submit')}
        </button>
      </div>
    </form>
  );
}

/** The org's open sessions — a landlord who navigated away can come back. */
function OpenSessions({ onReopen }: { onReopen: (id: string) => void }) {
  const t = useT();
  const [items, setItems] = useState<AssistDetail[] | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    const load = () =>
      assistApi
        .list(ac.signal)
        .then((res) => setItems(res.items ?? []))
        .catch(() => undefined);
    void load();
    const timer = setInterval(() => void load(), POLL_MS);
    return () => {
      clearInterval(timer);
      ac.abort();
    };
  }, []);

  if (!items || items.length === 0) return null;
  return (
    <section style={{ paddingTop: 'var(--sp-6)' }}>
      <hr className="rule rule-strong" />
      <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('assist.open_sessions')}</h2>
      <TableScroll label={t('assist.open_sessions')}>
        <table className="ledger">
          <thead>
            <tr>
              <th>{t('common.phone')}</th>
              <th>{t('common.unit')}</th>
              <th>{t('common.status')}</th>
              <th>{t('assist.col.started')}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((d) => (
              <tr key={d.session.id}>
                <td style={{ fontWeight: 600 }}>{d.session.phone}</td>
                <td>{d.session.unit_name}</td>
                <td>{t(`assist.status.${d.status_detail}`)}</td>
                <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                  {fmtDateTime(d.session.created_at)}
                </td>
                <td className="num">
                  <button type="button" className="btn btn-quiet" onClick={() => onReopen(d.session.id)}>
                    {t('assist.reopen')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>
    </section>
  );
}

/* ------------------------------- live ------------------------------------- */

function StatusTrail({ detail }: { detail: AssistDetail }) {
  const t = useT();
  const at = STEPS.indexOf(detail.status_detail as (typeof STEPS)[number]);
  return (
    <ol
      aria-label={t('common.status')}
      style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 'var(--sp-2)' }}
    >
      {STEPS.map((step, i) => {
        const done = at >= i;
        const current = at === i;
        return (
          <li
            key={step}
            aria-current={current ? 'step' : undefined}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-2)',
              color: done ? 'var(--ink)' : 'var(--ink-faint)',
              fontWeight: current ? 600 : 400,
            }}
          >
            <Icon
              icon={done ? 'solar:check-circle-bold' : 'solar:record-linear'}
              width={20}
              style={{ color: done ? 'var(--stamp-paid)' : 'var(--ink-faint)' }}
              aria-hidden
            />
            {step === 'waiting' && !current
              ? t('assist.status.waiting_done')
              : step === 'waiting'
                ? t(detail.session.purpose === 'login' ? 'assist.status.waiting_login' : 'assist.status.waiting')
                : t(`assist.status.${step}`)}
          </li>
        );
      })}
    </ol>
  );
}

function LiveSession({
  id,
  initialCode,
  onDone,
}: {
  id: string;
  initialCode: AssistCode | null;
  onDone: () => void;
}) {
  const t = useT();
  const [detail, setDetail] = useState<AssistDetail | null>(null);
  const [shown, setShown] = useState<AssistCode | null>(initialCode);
  const [windowEnds, setWindowEnds] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [copied, setCopied] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const res = await assistApi.get(id, signal);
        setDetail(res);
        setWindowEnds(res.session.expires_at);
      } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') return;
        setError(toApiError(err));
      }
    },
    [id],
  );

  const closed = detail?.status_detail === 'closed';

  /* 5 s poll while the session is live (FLOWS 2b step 5). */
  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    if (closed) return () => ac.abort();
    const timer = setInterval(() => void load(ac.signal), POLL_MS);
    return () => {
      clearInterval(timer);
      ac.abort();
    };
  }, [load, closed]);

  const windowLeft = useSecondsLeft(windowEnds);

  const newCode = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await assistApi.newCode(id);
      setShown(res);
      setWindowEnds(res.expires_at);
      void load();
    } catch (err) {
      setError(toApiError(err));
      void load();
    } finally {
      setBusy(false);
    }
  };

  const close = async () => {
    setBusy(true);
    setError(null);
    try {
      await assistApi.close(id);
      setShown(null);
      await load();
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  const copyLink = async () => {
    if (!shown) return;
    try {
      await navigator.clipboard.writeText(shown.link);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard refused — the link is on screen to read out */
    }
  };

  if (!detail) {
    return (
      <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
        {!error ? <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p> : null}
        <div>
          <button type="button" className="btn btn-secondary" onClick={onDone}>
            {t('assist.start_another')}
          </button>
        </div>
      </div>
    );
  }

  const s = detail.session;
  const request = detail.link_request;

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      <p style={{ margin: 0 }}>
        <span style={{ fontWeight: 600 }}>{s.phone}</span>
        <span style={{ color: 'var(--ink-soft)' }}>
          {' '}
          · {s.unit_name} · {t(s.purpose === 'login' ? 'assist.purpose.login' : 'assist.purpose.register')}
        </span>
      </p>

      <ProblemNote error={error} />

      {closed ? (
        <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <p style={{ color: 'var(--ink-soft)' }}>{t('assist.closed_lead')}</p>
          <StatusTrail detail={detail} />
        </div>
      ) : (
        <div
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: 'var(--sp-6)',
            alignItems: 'flex-start',
          }}
        >
          <div style={{ display: 'grid', gap: 'var(--sp-4)', minWidth: 0, flex: '1 1 260px' }}>
            {shown ? (
              <>
                <p style={{ margin: 0, color: 'var(--ink-soft)' }}>{t('assist.code_lead')}</p>
                <ShownCode code={shown.code} expiresAt={shown.code_expires_at} />
              </>
            ) : (
              <p style={{ margin: 0, color: 'var(--ink-soft)' }}>{t('assist.no_code')}</p>
            )}
            <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
              <button
                type="button"
                className={shown ? 'btn btn-secondary' : 'btn btn-primary'}
                onClick={() => void newCode()}
                disabled={busy}
              >
                <Icon icon="solar:refresh-linear" width={20} />{' '}
                {busy ? t('assist.new_code_busy') : shown ? t('assist.new_code') : t('assist.show_code')}
              </button>
              <button type="button" className="btn btn-quiet" onClick={() => void close()} disabled={busy}>
                {t('assist.close')}
              </button>
            </div>
            <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('assist.window', { time: mmss(windowLeft), n: s.code_issued_count })}
            </p>
          </div>

          {shown ? (
            <figure style={{ margin: 0, display: 'grid', gap: 'var(--sp-2)', justifyItems: 'center' }}>
              {shown.link_qr ? (
                // eslint-disable-next-line @next/next/no-img-element -- inline data URL from the API
                <img
                  src={shown.link_qr}
                  alt={t('assist.qr_alt')}
                  width={200}
                  height={200}
                  style={{
                    imageRendering: 'pixelated',
                    border: '1px solid var(--rule)',
                    borderRadius: 'var(--radius-sm)',
                    background: '#fff',
                  }}
                />
              ) : null}
              <figcaption style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', textAlign: 'center' }}>
                {t('assist.qr_caption')}
              </figcaption>
              <button type="button" className="btn btn-quiet" onClick={() => void copyLink()} style={{ minHeight: 32 }}>
                <Icon icon="solar:copy-linear" width={18} /> {copied ? t('assist.link_copied') : t('assist.copy_link')}
              </button>
            </figure>
          ) : null}
        </div>
      )}

      {!closed ? (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <hr className="rule" />
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('assist.progress')}</h2>
          <StatusTrail detail={detail} />
          {detail.renter ? (
            <p style={{ margin: 0, color: 'var(--ink-soft)' }}>
              {t('assist.renter_is', { name: detail.renter.full_name })}
            </p>
          ) : null}
        </section>
      ) : null}

      <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
        {request ? (
          <Link
            href={`/link-requests/${request.id}`}
            className={request.status === 'pending' ? 'btn btn-primary' : 'btn btn-secondary'}
          >
            <Icon icon="solar:clipboard-check-linear" width={20} />{' '}
            {request.status === 'pending' ? t('assist.review') : t('assist.open_request')}
          </Link>
        ) : null}
        {closed ? (
          <button type="button" className="btn btn-secondary" onClick={onDone}>
            {t('assist.start_another')}
          </button>
        ) : null}
      </div>
    </div>
  );
}

/* ------------------------------- page ------------------------------------- */

function AssistBody() {
  const t = useT();
  const router = useRouter();
  const params = useSearchParams();
  const idParam = params.get('id') ?? '';
  const unitParam = params.get('unit') ?? '';
  const renterParam = params.get('renter') ?? '';

  const [sessionId, setSessionId] = useState(idParam);
  /* The code only exists in this state: never in the URL, never stored. */
  const [firstCode, setFirstCode] = useState<AssistCode | null>(null);

  useEffect(() => {
    setSessionId(idParam);
  }, [idParam]);

  const show = (id: string, code: AssistCode | null) => {
    setFirstCode(code);
    setSessionId(id);
    router.replace(`/renters/assist?id=${encodeURIComponent(id)}`);
  };

  return (
    <>
      <PageHead
        title={t('assist.title')}
        lead={t('assist.lead')}
        actions={
          <Link href="/renters" className="btn btn-quiet">
            <Icon icon="solar:arrow-left-linear" width={20} /> {t('renters.directory')}
          </Link>
        }
      />
      <hr className="rule rule-strong" />
      <div style={{ paddingTop: 'var(--sp-5)' }}>
        {sessionId ? (
          <LiveSession
            key={sessionId}
            id={sessionId}
            initialCode={firstCode}
            onDone={() => {
              setFirstCode(null);
              setSessionId('');
              router.replace('/renters/assist');
            }}
          />
        ) : (
          <>
            <StartForm
              unitParam={unitParam}
              renterParam={renterParam}
              onOpened={(id, code) => show(id, code)}
              onReopen={(id) => show(id, null)}
            />
            <OpenSessions onReopen={(id) => show(id, null)} />
          </>
        )}
      </div>
    </>
  );
}

export default function AssistPage() {
  return (
    <>
      <Suspense fallback={null}>
        <AssistBody />
      </Suspense>
    </>
  );
}
