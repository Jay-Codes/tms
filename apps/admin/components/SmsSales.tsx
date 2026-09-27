'use client';

/**
 * Phase 27 — SMS credit sales through Snippe, and the platform's own SMS
 * stock (API.md "Phase 27"). Four panels on one page:
 *
 *  - Stock: Beem SMS bought − sent, set against the credits every org still
 *    holds (the liability), with the low-stock flag, plus the margin report
 *    (sales − Snippe's 2.5% − the Beem cost of the credits sold).
 *  - Packages: the bundles landlords can buy. No delete — taking one off sale
 *    keeps it for the orders that name it; orders keep the price they paid.
 *  - Orders: every org's purchases, and a "check now" for the reconciliation.
 *  - Beem purchases: the bundles the platform bought (an opening balance is a
 *    purchase with cost 0).
 *
 * Every figure is the backend's; this screen only lays them out.
 */

import { useCallback, useEffect, useState } from 'react';
import { TableScroll } from '@tms/ui';
import { Field, Note, ProblemNote } from './FormBits';
import {
  ApiError,
  adminSmsApi,
  toApiError,
  type AdminPlatformSmsPurchase,
  type AdminReconcileResult,
  type AdminSmsMargin,
  type AdminSmsOrder,
  type AdminSmsOrderStatus,
  type AdminSmsPackage,
  type AdminSmsStock,
} from '../lib/api';
import { fmtDate, fmtDateTime, fmtNum, fmtTZS } from '../lib/format';

function Figure({ label, value, sub, alert }: { label: string; value: string; sub?: string; alert?: boolean }) {
  return (
    <div style={{ borderTop: '1px solid var(--rule-strong)', paddingTop: 'var(--sp-3)' }}>
      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</div>
      <div
        className="amount"
        style={{
          fontSize: 'var(--text-2xl)',
          marginTop: 'var(--sp-1)',
          color: alert ? 'var(--stamp-overdue)' : 'var(--ink)',
        }}
      >
        {value}
      </div>
      {sub ? (
        <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', marginTop: 'var(--sp-1)' }}>{sub}</div>
      ) : null}
    </div>
  );
}

const grid: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))',
  gap: 'var(--sp-4)',
};

function monthStart(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`;
}

function today(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/* --------------------------------------------------------------- stock -- */

export function StockPanel() {
  const [stock, setStock] = useState<AdminSmsStock | null>(null);
  const [margin, setMargin] = useState<AdminSmsMargin | null>(null);
  const [from, setFrom] = useState(monthStart());
  const [to, setTo] = useState(today());
  const [error, setError] = useState<ApiError | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setError(null);
      try {
        const [s, m] = await Promise.all([adminSmsApi.stock(signal), adminSmsApi.margin({ from, to }, signal)]);
        setStock(s);
        setMargin(m);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
    },
    [from, to],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-6)' }}>
      <ProblemNote error={error} />
      {stock ? (
        <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          {stock.low_stock ? (
            <p
              role="alert"
              style={{
                margin: 0,
                color: 'var(--stamp-overdue)',
                border: '1px solid var(--stamp-overdue)',
                borderRadius: 'var(--radius-sm)',
                padding: 'var(--sp-2) var(--sp-3)',
                fontSize: 'var(--text-sm)',
              }}
            >
              Low stock: {fmtNum(stock.stock)} SMS left against {fmtNum(stock.liability)} credits held by orgs (buffer{' '}
              {fmtNum(stock.buffer)}). Buy a Beem bundle and record it under Beem purchases.
            </p>
          ) : null}
          <div style={grid}>
            <Figure
              label="Platform stock (SMS)"
              value={fmtNum(stock.stock)}
              sub={`${fmtNum(stock.sms_bought)} bought − ${fmtNum(stock.sms_sent)} sent`}
              alert={stock.low_stock}
            />
            <Figure
              label="Liability (credits held)"
              value={fmtNum(stock.liability)}
              sub={`${fmtNum(stock.credits_purchased)} bought by orgs · ${fmtNum(stock.credits_granted)} granted`}
            />
            <Figure
              label="Headroom"
              value={fmtNum(stock.headroom)}
              sub={`stock − liability; flag under ${fmtNum(stock.buffer)}`}
              alert={stock.headroom < stock.buffer}
            />
            <Figure
              label="Beem cost"
              value={fmtTZS(stock.beem_cost)}
              sub={`TZS ${stock.avg_cost_per_sms.toFixed(2)} per SMS on average`}
            />
          </div>
          <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            Sent = credits debited ({fmtNum(stock.credits_debited)}) + messages that cost the org nothing, such as OTPs (
            {fmtNum(stock.uncharged_sent)}, one SMS each).
          </p>
        </section>
      ) : null}

      <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>Margin</h2>
        <div style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <Field id="m_from" label="From">
            <input id="m_from" className="input" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </Field>
          <Field id="m_to" label="To (inclusive)">
            <input id="m_to" className="input" type="date" value={to} onChange={(e) => setTo(e.target.value)} />
          </Field>
        </div>
        {margin ? (
          <>
            <div style={grid}>
              <Figure label="Sales" value={fmtTZS(margin.sales)} sub={`${fmtNum(margin.orders)} orders · ${fmtNum(margin.credits_sold)} credits`} />
              <Figure label={`Snippe fee (${margin.fee_percent}%)`} value={fmtTZS(margin.snippe_fee)} />
              <Figure
                label="Beem cost of credits sold"
                value={fmtTZS(margin.beem_cost)}
                sub={`at TZS ${margin.avg_cost_per_sms.toFixed(2)} per SMS`}
              />
              <Figure label="Margin" value={fmtTZS(margin.margin)} alert={margin.margin < 0} />
            </div>
            {margin.by_package.length > 0 ? (
              <TableScroll label="Sales by package">
                <table className="ledger">
                  <thead>
                    <tr>
                      <th>Package</th>
                      <th className="num">Orders</th>
                      <th className="num">Credits</th>
                      <th className="num">Sales</th>
                    </tr>
                  </thead>
                  <tbody>
                    {margin.by_package.map((p) => (
                      <tr key={p.package_name}>
                        <td>{p.package_name}</td>
                        <td className="num">{fmtNum(p.orders)}</td>
                        <td className="num">{fmtNum(p.credits_sold)}</td>
                        <td className="num">{fmtTZS(p.sales)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableScroll>
            ) : (
              <p style={{ color: 'var(--ink-soft)', margin: 0 }}>No completed orders in this window.</p>
            )}
          </>
        ) : null}
      </section>
    </div>
  );
}

/* ------------------------------------------------------------ packages -- */

type PackageForm = { name: string; credits: string; price: string; sort_order: string; active: boolean };
const emptyForm: PackageForm = { name: '', credits: '', price: '', sort_order: '0', active: true };

export function PackagesPanel() {
  const [items, setItems] = useState<AdminSmsPackage[] | null>(null);
  const [enabled, setEnabled] = useState(true);
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState<PackageForm>(emptyForm);
  const [error, setError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await adminSmsApi.packages(signal);
      setItems(res.items ?? []);
      setEnabled(res.purchases_enabled);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const startEdit = (p: AdminSmsPackage) => {
    setEditing(p.id);
    setNote(null);
    setError(null);
    setForm({
      name: p.name,
      credits: String(p.credits),
      price: String(p.price),
      sort_order: String(p.sort_order),
      active: p.active,
    });
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setNote(null);
    const body = {
      name: form.name.trim(),
      credits: Number(form.credits),
      price: Number(form.price),
      sort_order: Number(form.sort_order || 0),
      active: form.active,
    };
    try {
      if (editing) {
        await adminSmsApi.updatePackage(editing, body);
        setNote('Package saved. Orders already placed keep the price they were placed at.');
      } else {
        await adminSmsApi.createPackage(body);
        setNote('Package added.');
      }
      setEditing(null);
      setForm(emptyForm);
      await load();
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  const toggle = async (p: AdminSmsPackage) => {
    setError(null);
    try {
      await adminSmsApi.updatePackage(p.id, { active: !p.active });
      await load();
    } catch (err) {
      setError(toApiError(err));
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      {!enabled ? (
        <p style={{ margin: 0, fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          Purchases are switched off: SNIPPE_API_KEY and SNIPPE_WEBHOOK_SECRET are not both set on the API. Landlords
          see “Buying credits is not switched on yet”. Packages can be prepared in the meantime.
        </p>
      ) : null}
      <ProblemNote error={error} />
      {note ? <Note>{note}</Note> : null}
      <TableScroll label="SMS credit packages">
        <table className="ledger">
          <thead>
            <tr>
              <th>Name</th>
              <th className="num">Credits</th>
              <th className="num">Price</th>
              <th className="num">Per credit</th>
              <th className="num">Order</th>
              <th>On sale</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  No packages yet. Add the bundles the client has priced.
                </td>
              </tr>
            ) : (
              items.map((p) => (
                <tr key={p.id} style={p.active ? undefined : { color: 'var(--ink-soft)' }}>
                  <td style={{ fontWeight: 500 }}>{p.name}</td>
                  <td className="num">{fmtNum(p.credits)}</td>
                  <td className="num">{fmtTZS(p.price)}</td>
                  <td className="num">{(p.price / p.credits).toFixed(2)}</td>
                  <td className="num">{p.sort_order}</td>
                  <td>{p.active ? 'Yes' : 'No'}</td>
                  <td className="num">
                    <span style={{ display: 'inline-flex', gap: 'var(--sp-2)' }}>
                      <button type="button" className="btn btn-quiet" style={{ minHeight: 32 }} onClick={() => startEdit(p)}>
                        Edit
                      </button>
                      <button type="button" className="btn btn-quiet" style={{ minHeight: 32 }} onClick={() => void toggle(p)}>
                        {p.active ? 'Take off sale' : 'Put on sale'}
                      </button>
                    </span>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableScroll>

      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }} noValidate>
        <h3 style={{ fontSize: 'var(--text-lg)' }}>{editing ? 'Edit package' : 'Add a package'}</h3>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
          <Field id="p_name" label="Name" error={error?.errors.name}>
            <input
              id="p_name"
              className="input"
              maxLength={60}
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </Field>
          <Field id="p_credits" label="Credits" error={error?.errors.credits}>
            <input
              id="p_credits"
              className="input num"
              type="number"
              min={1}
              value={form.credits}
              onChange={(e) => setForm((f) => ({ ...f, credits: e.target.value }))}
            />
          </Field>
          <Field id="p_price" label="Price (TZS)" hint="Whole shillings, at least 500." error={error?.errors.price}>
            <input
              id="p_price"
              className="input num"
              type="number"
              min={500}
              value={form.price}
              onChange={(e) => setForm((f) => ({ ...f, price: e.target.value }))}
            />
          </Field>
          <Field id="p_sort" label="Display order" hint="Lower first." error={error?.errors.sort_order}>
            <input
              id="p_sort"
              className="input num"
              type="number"
              value={form.sort_order}
              onChange={(e) => setForm((f) => ({ ...f, sort_order: e.target.value }))}
            />
          </Field>
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
          <input
            type="checkbox"
            checked={form.active}
            onChange={(e) => setForm((f) => ({ ...f, active: e.target.checked }))}
          />
          On sale
        </label>
        <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? 'Saving…' : editing ? 'Save package' : 'Add package'}
          </button>
          {editing ? (
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => {
                setEditing(null);
                setForm(emptyForm);
              }}
            >
              Cancel
            </button>
          ) : null}
        </div>
      </form>
    </div>
  );
}

/* -------------------------------------------------------------- orders -- */

const ORDER_STATUSES: AdminSmsOrderStatus[] = ['pending', 'completed', 'failed', 'expired', 'mismatch'];

function OrderStatus({ status }: { status: AdminSmsOrderStatus }) {
  const cls =
    status === 'completed' ? 'stamp stamp-paid' : status === 'mismatch' || status === 'failed' ? 'stamp stamp-overdue' : 'stamp';
  const label =
    status === 'mismatch' ? 'Amount mismatch' : status.charAt(0).toUpperCase() + status.slice(1);
  return <span className={cls}>{label}</span>;
}

export function OrdersPanel() {
  const [items, setItems] = useState<AdminSmsOrder[] | null>(null);
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [status, setStatus] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<AdminReconcileResult | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const res = await adminSmsApi.orders({ status: status || undefined, limit: 200 }, signal);
        setItems(res.items ?? []);
        setCounts(res.counts ?? {});
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [status],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const reconcile = async () => {
    setRunning(true);
    setError(null);
    try {
      setResult(await adminSmsApi.reconcile());
      await load();
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setRunning(false);
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <div style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <Field id="o_status" label="Status">
          <select id="o_status" className="input" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">All</option>
            {ORDER_STATUSES.map((s) => (
              <option key={s} value={s}>
                {s} ({fmtNum(counts[s] ?? 0)})
              </option>
            ))}
          </select>
        </Field>
        <button type="button" className="btn btn-secondary" disabled={running} onClick={() => void reconcile()}>
          {running ? 'Checking…' : 'Check pending orders now'}
        </button>
      </div>
      <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        The webhook settles orders as Snippe reports them; the API also asks Snippe about any order still pending after
        5 minutes and expires it at 4 hours. An amount mismatch is never credited automatically — check it in the Snippe
        dashboard and top the org up by hand if the money arrived.
      </p>
      {result ? (
        <Note>
          Checked {result.checked}: {result.completed} completed, {result.failed} failed, {result.expired} expired,{' '}
          {result.mismatch} mismatched, {result.pending} still pending, {result.errors} errors.
        </Note>
      ) : null}
      <ProblemNote error={error} />
      <TableScroll label="SMS credit orders">
        <table className="ledger">
          <thead>
            <tr>
              <th>Placed</th>
              <th>Organization</th>
              <th>Package</th>
              <th className="num">Credits</th>
              <th className="num">Amount</th>
              <th>Payer</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={7} style={{ color: 'var(--ink-soft)' }}>
                  No orders.
                </td>
              </tr>
            ) : (
              items.map((o) => (
                <tr key={o.id}>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    {fmtDateTime(o.created_at)}
                    <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>{o.order_code}</div>
                  </td>
                  <td>{o.org_name}</td>
                  <td>{o.package_name}</td>
                  <td className="num">{fmtNum(o.credits)}</td>
                  <td className="num">{fmtTZS(o.amount)}</td>
                  <td>{o.payer_phone}</td>
                  <td>
                    <OrderStatus status={o.status} />
                    {o.failure_reason ? (
                      <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>{o.failure_reason}</div>
                    ) : null}
                    {o.reference ? (
                      <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>Snippe {o.reference}</div>
                    ) : null}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableScroll>
    </div>
  );
}

/* ------------------------------------------------------ Beem purchases -- */

export function PurchasesPanel() {
  const [items, setItems] = useState<AdminPlatformSmsPurchase[] | null>(null);
  const [form, setForm] = useState({ sms_count: '', cost: '', purchased_on: today(), reference: '', note: '' });
  const [error, setError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await adminSmsApi.purchases(signal);
      setItems(res.items ?? []);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      await adminSmsApi.recordPurchase({
        sms_count: Number(form.sms_count),
        cost: Number(form.cost),
        purchased_on: form.purchased_on || undefined,
        reference: form.reference.trim() || undefined,
        note: form.note.trim() || undefined,
      });
      setNote('Beem bundle recorded.');
      setForm({ sms_count: '', cost: '', purchased_on: today(), reference: '', note: '' });
      await load();
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }} noValidate>
        <h3 style={{ fontSize: 'var(--text-lg)' }}>Record a Beem bundle</h3>
        <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          Enter every bundle bought from Beem. For SMS already on the Beem account before this screen existed, record one
          opening balance with cost 0.
        </p>
        <ProblemNote error={error} />
        {note ? <Note>{note}</Note> : null}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
          <Field id="b_count" label="SMS bought" error={error?.errors.sms_count}>
            <input
              id="b_count"
              className="input num"
              type="number"
              min={1}
              value={form.sms_count}
              onChange={(e) => setForm((f) => ({ ...f, sms_count: e.target.value }))}
            />
          </Field>
          <Field id="b_cost" label="Cost (TZS)" error={error?.errors.cost}>
            <input
              id="b_cost"
              className="input num"
              type="number"
              min={0}
              value={form.cost}
              onChange={(e) => setForm((f) => ({ ...f, cost: e.target.value }))}
            />
          </Field>
          <Field id="b_on" label="Bought on" error={error?.errors.purchased_on}>
            <input
              id="b_on"
              className="input"
              type="date"
              value={form.purchased_on}
              onChange={(e) => setForm((f) => ({ ...f, purchased_on: e.target.value }))}
            />
          </Field>
          <Field id="b_ref" label="Invoice / reference" error={error?.errors.reference}>
            <input
              id="b_ref"
              className="input"
              maxLength={120}
              value={form.reference}
              onChange={(e) => setForm((f) => ({ ...f, reference: e.target.value }))}
            />
          </Field>
        </div>
        <Field id="b_note" label="Note" error={error?.errors.note}>
          <input
            id="b_note"
            className="input"
            maxLength={500}
            value={form.note}
            onChange={(e) => setForm((f) => ({ ...f, note: e.target.value }))}
          />
        </Field>
        <div>
          <button type="submit" className="btn btn-primary" disabled={busy || !form.sms_count || form.cost === ''}>
            {busy ? 'Saving…' : 'Record purchase'}
          </button>
        </div>
      </form>

      <TableScroll label="Beem purchases">
        <table className="ledger">
          <thead>
            <tr>
              <th>Bought on</th>
              <th className="num">SMS</th>
              <th className="num">Cost</th>
              <th className="num">Per SMS</th>
              <th>Reference</th>
              <th>Recorded by</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                  Nothing recorded yet.
                </td>
              </tr>
            ) : (
              items.map((p) => (
                <tr key={p.id}>
                  <td>{fmtDate(p.purchased_on)}</td>
                  <td className="num">{fmtNum(p.sms_count)}</td>
                  <td className="num">{fmtTZS(p.cost)}</td>
                  <td className="num">{(p.cost / p.sms_count).toFixed(2)}</td>
                  <td>
                    {p.reference ?? '—'}
                    {p.note ? <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>{p.note}</div> : null}
                  </td>
                  <td>{p.admin_name || '—'}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableScroll>
    </div>
  );
}
