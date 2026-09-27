'use client';

/**
 * Phase 27 — the landlord buys SMS credits with mobile money (Snippe).
 *
 * Pick a bundle, confirm the phone the payment prompt goes to, approve it on
 * the handset. Nothing here decides whether a payment went through: the order
 * is polled and the server settles it (Snippe's webhook, or its own status
 * check). Credits appear only when the server says the order is `completed`.
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { TableScroll, useT, type Translator } from '@tms/ui';
import { Field, Note, ProblemNote } from './FormBits';
import { SmsCreditsCard, useSmsCredits } from './NotificationBits';
import {
  ApiError,
  smsPurchaseApi,
  toApiError,
  type SmsOrder,
  type SmsOrderStatus,
  type SmsPackagesResponse,
} from '../lib/api';
import { fmtDateTime, fmtTZS } from '../lib/format';

/** How often the waiting screen asks about the order, and for how long. */
const POLL_MS = 4000;
const POLL_FOR_MS = 10 * 60 * 1000;

const STATUS_KEYS: Record<SmsOrderStatus, string> = {
  pending: 'buycredits.status.pending',
  completed: 'buycredits.status.completed',
  failed: 'buycredits.status.failed',
  expired: 'buycredits.status.expired',
  mismatch: 'buycredits.status.mismatch',
};

const STATUS_COLOURS: Record<SmsOrderStatus, string> = {
  pending: 'var(--ink-soft)',
  completed: 'var(--stamp-paid)',
  failed: 'var(--stamp-overdue)',
  expired: 'var(--ink-soft)',
  mismatch: 'var(--stamp-overdue)',
};

function statusLabel(t: Translator, status: SmsOrderStatus | string): string {
  const key = STATUS_KEYS[status as SmsOrderStatus];
  return key ? t(key) : status;
}

function OrderStamp({ status }: { status: SmsOrderStatus }) {
  const t = useT();
  return (
    <span className="pencil" style={{ color: STATUS_COLOURS[status] ?? 'var(--ink-soft)' }}>
      {statusLabel(t, status)}
    </span>
  );
}

/** The waiting-for-approval panel: polls one order until it settles. */
function WaitingForApproval({ order, onSettled }: { order: SmsOrder; onSettled: (o: SmsOrder) => void }) {
  const t = useT();
  const [current, setCurrent] = useState(order);
  const [gaveUp, setGaveUp] = useState(false);
  const settledRef = useRef(onSettled);
  settledRef.current = onSettled;

  useEffect(() => {
    setCurrent(order);
    setGaveUp(false);
    if (order.status !== 'pending') return;
    const started = Date.now();
    const ac = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      try {
        const res = await smsPurchaseApi.get(order.id, ac.signal);
        setCurrent(res.order);
        if (res.order.status !== 'pending') {
          settledRef.current(res.order);
          return;
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        // A failed poll is not a failed payment; keep asking.
      }
      if (Date.now() - started >= POLL_FOR_MS) {
        setGaveUp(true);
        return;
      }
      timer = setTimeout(() => void tick(), POLL_MS);
    };
    timer = setTimeout(() => void tick(), POLL_MS);
    return () => {
      ac.abort();
      if (timer) clearTimeout(timer);
    };
  }, [order]);

  if (current.status === 'completed') {
    return <Note>{t('buycredits.done', { credits: current.credits })}</Note>;
  }
  if (current.status !== 'pending') {
    return (
      <p
        role="alert"
        style={{
          color: 'var(--stamp-overdue)',
          fontSize: 'var(--text-sm)',
          border: '1px solid var(--stamp-overdue)',
          borderRadius: 'var(--radius-sm)',
          padding: 'var(--sp-2) var(--sp-3)',
          maxWidth: 'none',
        }}
      >
        {t(current.status === 'expired' ? 'buycredits.expired' : 'buycredits.failed')}
        {current.failure_reason ? ` (${current.failure_reason})` : null}
      </p>
    );
  }
  return (
    <section
      role="status"
      aria-live="polite"
      className="sheet"
      style={{ padding: 'var(--sp-4) var(--sp-5)', display: 'grid', gap: 'var(--sp-2)' }}
    >
      <strong style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
        <Icon icon="solar:smartphone-2-linear" width={20} /> {t('buycredits.waiting.title')}
      </strong>
      <p style={{ margin: 0 }}>
        {t('buycredits.waiting.body', { phone: current.payer_phone, amount: fmtTZS(current.amount) })}
      </p>
      <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        {gaveUp ? t('buycredits.waiting.long') : t('buycredits.waiting.hint')}{' '}
        <span className="pencil">{current.order_code}</span>
      </p>
    </section>
  );
}

function OrderHistory({ items }: { items: SmsOrder[] | null }) {
  const t = useT();
  return (
    <TableScroll label={t('buycredits.history.title')}>
      <table className="ledger">
        <thead>
          <tr>
            <th>{t('buycredits.history.when')}</th>
            <th>{t('buycredits.history.package')}</th>
            <th className="num">{t('buycredits.history.credits')}</th>
            <th className="num">{t('buycredits.history.amount')}</th>
            <th>{t('buycredits.history.phone')}</th>
            <th>{t('common.status')}</th>
          </tr>
        </thead>
        <tbody>
          {items === null ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                {t('common.loading')}
              </td>
            </tr>
          ) : items.length === 0 ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                {t('buycredits.history.empty')}
              </td>
            </tr>
          ) : (
            items.map((o) => (
              <tr key={o.id}>
                <td style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(o.created_at)}</td>
                <td>
                  {o.package_name}
                  <div style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-xs)' }}>{o.order_code}</div>
                </td>
                <td className="num">{o.credits}</td>
                <td className="num">{fmtTZS(o.amount)}</td>
                <td>{o.payer_phone}</td>
                <td>
                  <OrderStamp status={o.status} />
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </TableScroll>
  );
}

export function BuyCredits() {
  const t = useT();
  const { credits, reload: reloadCredits } = useSmsCredits();
  const [catalogue, setCatalogue] = useState<SmsPackagesResponse | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [history, setHistory] = useState<SmsOrder[] | null>(null);
  const [selected, setSelected] = useState('');
  const [phone, setPhone] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [active, setActive] = useState<SmsOrder | null>(null);
  const [switchedOff, setSwitchedOff] = useState(false);

  const loadHistory = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await smsPurchaseApi.orders(signal);
      setHistory(res.items ?? []);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setHistory([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    smsPurchaseApi
      .packages(ac.signal)
      .then((res) => {
        setCatalogue(res);
        setPhone((p) => p || res.default_phone || '');
        setSelected((s) => s || res.items[0]?.id || '');
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setLoadError(toApiError(e));
      });
    void loadHistory(ac.signal);
    return () => ac.abort();
  }, [loadHistory]);

  const pkg = catalogue?.items.find((p) => p.id === selected) ?? null;
  const enabled = !!catalogue?.enabled && !switchedOff;

  const buy = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!pkg) return;
    setBusy(true);
    setError(null);
    try {
      const res = await smsPurchaseApi.order({ package_id: pkg.id, phone: phone.trim() || undefined });
      setActive(res.order);
      void loadHistory();
    } catch (err) {
      const apiErr = toApiError(err);
      if (apiErr.code === 'purchases_disabled') setSwitchedOff(true);
      else setError(apiErr);
      void loadHistory();
    } finally {
      setBusy(false);
    }
  };

  const settled = useCallback(
    (o: SmsOrder) => {
      setActive(o);
      void loadHistory();
      if (o.status === 'completed') reloadCredits();
    },
    [loadHistory, reloadCredits],
  );

  const waiting = active?.status === 'pending';

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      <SmsCreditsCard credits={credits} />
      <ProblemNote error={loadError} />

      {catalogue && !enabled ? (
        <p
          role="status"
          style={{
            margin: 0,
            fontSize: 'var(--text-sm)',
            border: '1px solid var(--rule-strong)',
            borderRadius: 'var(--radius-sm)',
            padding: 'var(--sp-3)',
          }}
        >
          <strong style={{ fontWeight: 600 }}>{t('buycredits.disabled.title')}</strong>{' '}
          {t('buycredits.disabled.body')}
        </p>
      ) : null}

      {catalogue && enabled && catalogue.items.length === 0 ? (
        <p style={{ color: 'var(--ink-soft)', margin: 0 }}>{t('buycredits.no_packages')}</p>
      ) : null}

      {catalogue && enabled && catalogue.items.length > 0 ? (
        <form onSubmit={buy} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }} noValidate>
          <ProblemNote error={error} />
          <fieldset style={{ border: 0, padding: 0, margin: 0 }}>
            <legend style={{ fontWeight: 600, marginBottom: 'var(--sp-2)' }}>{t('buycredits.pick')}</legend>
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
                gap: 'var(--sp-3)',
              }}
            >
              {catalogue.items.map((p) => {
                const on = p.id === selected;
                return (
                  <label
                    key={p.id}
                    style={{
                      display: 'grid',
                      gap: 'var(--sp-1)',
                      padding: 'var(--sp-3) var(--sp-4)',
                      border: `${on ? 2 : 1}px solid ${on ? 'var(--accent)' : 'var(--rule-strong)'}`,
                      borderRadius: 'var(--radius-sm)',
                      cursor: waiting ? 'default' : 'pointer',
                      minHeight: 'var(--touch-min)',
                    }}
                  >
                    <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
                      <input
                        type="radio"
                        name="sms_package"
                        value={p.id}
                        checked={on}
                        disabled={waiting}
                        onChange={() => setSelected(p.id)}
                      />
                      <span style={{ fontWeight: 600 }}>{p.name}</span>
                    </span>
                    <span className="amount" style={{ fontSize: 'var(--text-lg)' }}>
                      {t('buycredits.credits', { count: p.credits })}
                    </span>
                    <span>{fmtTZS(p.price)}</span>
                    <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-xs)' }}>
                      {t('buycredits.per_credit', { price: fmtTZS(p.price / p.credits) })}
                    </span>
                  </label>
                );
              })}
            </div>
          </fieldset>

          <Field id="pay_phone" label={t('buycredits.phone.label')} hint={t('buycredits.phone.hint')} error={error?.errors.phone}>
            <input
              id="pay_phone"
              className="input"
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              value={phone}
              disabled={waiting}
              onChange={(e) => setPhone(e.target.value)}
              style={{ maxWidth: 320 }}
            />
          </Field>

          <div>
            <button type="submit" className="btn btn-primary" disabled={busy || waiting || !pkg || !phone.trim()}>
              <Icon icon="solar:card-send-linear" width={20} />{' '}
              {busy ? t('buycredits.sending') : t('buycredits.pay', { amount: fmtTZS(pkg?.price ?? 0) })}
            </button>
          </div>
          <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('buycredits.how')}</p>
        </form>
      ) : null}

      {active ? <WaitingForApproval order={active} onSettled={settled} /> : null}

      <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
        <h3 style={{ fontSize: 'var(--text-lg)' }}>{t('buycredits.history.title')}</h3>
        <OrderHistory items={history} />
      </section>
    </div>
  );
}
