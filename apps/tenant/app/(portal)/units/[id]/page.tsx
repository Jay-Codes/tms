'use client';

/**
 * One unit: name, status override, offered payment periods, its QR code and
 * the full price history (SPEC §5.3, FLOWS flows 4 and 5).
 *
 * A new price applies to future contracts only — active contracts keep the
 * rent snapshotted at signing, which the backend enforces.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { Field, Note, ProblemNote } from '../../../components/FormBits';
import { PeriodPicker } from '../../../components/PeriodPicker';
import { PageHead, Shell } from '../../../components/Shell';
import { StatusMark } from '../../../components/UnitStatus';
import {
  ApiError,
  periodsApi,
  toApiError,
  unitsApi,
  unwrapUnit,
  type PaymentPeriod,
  type Price,
  type Unit,
  type UnitQr,
  type UnitStatusOverride,
} from '../../../lib/api';
import { Amount, fmtDate, fmtPrice, todayISO } from '../../../lib/format';

const OVERRIDES: { value: UnitStatusOverride; label: string; hint: string }[] = [
  { value: 'vacant', label: 'Vacant', hint: 'Available. Shows on the vacancy board and to anyone who scans.' },
  { value: 'maintenance', label: 'Maintenance', hint: 'Out of service while you fix it.' },
  { value: 'unlisted', label: 'Unlisted', hint: 'Hidden from renters. A scan answers "not found".' },
];

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section style={{ paddingTop: 'var(--sp-6)' }}>
      <hr className="rule rule-strong" />
      <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{title}</h2>
      {children}
    </section>
  );
}

/* ------------------------------- QR block -------------------------------- */

function QrBlock({ unit }: { unit: Unit }) {
  const [qr, setQr] = useState<UnitQr | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const generate = async () => {
    setBusy(true);
    setError(null);
    try {
      setQr(await unitsApi.qr(unit.id));
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
      <ProblemNote error={error} />
      <dl style={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: 'var(--sp-2) var(--sp-4)', margin: 0 }}>
        <dt style={{ color: 'var(--ink-soft)' }}>Unit code</dt>
        <dd className="num" style={{ margin: 0, textAlign: 'left', letterSpacing: '0.14em', fontWeight: 600 }}>
          {unit.unit_code}
        </dd>
        <dt style={{ color: 'var(--ink-soft)' }}>Scan URL</dt>
        <dd style={{ margin: 0, wordBreak: 'break-all' }}>{qr?.scan_url ?? unit.scan_url ?? '—'}</dd>
      </dl>

      {qr ? (
        <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-start' }}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={qr.png_url}
            alt={`QR code for ${unit.name}`}
            width={180}
            height={180}
            style={{ border: '1px solid var(--rule)', background: '#fff' }}
          />
          <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
            <a className="btn btn-secondary" href={qr.png_url} download={`${unit.unit_code}.png`} target="_blank" rel="noreferrer">
              <Icon icon="solar:download-linear" width={20} /> Download PNG
            </a>
            <button type="button" className="btn btn-quiet" onClick={() => void generate()} disabled={busy}>
              {busy ? 'Working…' : 'Regenerate'}
            </button>
            <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
              The link expires in 15 minutes. The code itself never changes.
            </span>
          </div>
        </div>
      ) : (
        <div style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center' }}>
          <button type="button" className="btn btn-primary" onClick={() => void generate()} disabled={busy}>
            <Icon icon="solar:qr-code-linear" width={20} /> {busy ? 'Working…' : 'Show QR code'}
          </button>
          <Link href={`/properties/${unit.property_id}/qr`} className="btn btn-quiet">
            Print the whole property
          </Link>
        </div>
      )}
    </div>
  );
}

/* -------------------------------- prices --------------------------------- */

function Prices({ unit, onPriceAdded }: { unit: Unit; onPriceAdded: () => void }) {
  const [items, setItems] = useState<Price[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [formError, setFormError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [amount, setAmount] = useState('');
  const [periodDays, setPeriodDays] = useState(String(unit.current_price?.period_days ?? 30));
  const [from, setFrom] = useState(todayISO());
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await unitsApi.prices(unit.id, signal);
      setItems(res.items ?? []);
      setError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setItems([]);
    }
  }, [unit.id]);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setFormError(null);
    setNote(null);
    try {
      await unitsApi.addPrice(unit.id, {
        amount: Math.round(Number(amount)),
        period_days: Math.round(Number(periodDays)),
        effective_from: from || undefined,
      });
      setAmount('');
      setNote('New price recorded. It applies to future contracts only.');
      await load();
      onPriceAdded();
    } catch (err) {
      setFormError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  const current = items?.[0] ?? unit.current_price ?? null;

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)', maxWidth: 720 }}>
      <ProblemNote error={error} />
      <p style={{ fontSize: 'var(--text-xl)' }}>
        <Amount value={current?.amount ?? null} per={current?.period_days ?? null} />
      </p>

      <table className="ledger">
        <thead>
          <tr>
            <th>Effective from</th>
            <th className="num">Amount</th>
            <th className="num">Per (days)</th>
            <th>Set by</th>
          </tr>
        </thead>
        <tbody>
          {items === null ? (
            <tr>
              <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                Loading…
              </td>
            </tr>
          ) : items.length === 0 ? (
            <tr>
              <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                No price recorded yet.
              </td>
            </tr>
          ) : (
            items.map((p, i) => (
              <tr key={p.id} className={i === 0 ? 'total' : undefined}>
                <td>{fmtDate(p.effective_from)}</td>
                <td className="num">
                  <Amount value={p.amount} />
                </td>
                <td className="num">{p.period_days}</td>
                <td style={{ color: 'var(--ink-soft)' }}>{p.created_by_name || '—'}</td>
              </tr>
            ))
          )}
        </tbody>
      </table>

      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <h3 style={{ fontSize: 'var(--text-md)', fontWeight: 600 }}>New price</h3>
        <ProblemNote error={formError} />
        {note ? <Note>{note}</Note> : null}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 140px 180px', gap: 'var(--sp-4)' }}>
          <Field id="np_amount" label="Amount (TZS)" error={formError?.errors.amount}>
            <input
              id="np_amount"
              className="input num"
              type="number"
              min={1}
              step={1}
              inputMode="numeric"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              placeholder="250000"
            />
          </Field>
          <Field id="np_days" label="Per (days)" error={formError?.errors.period_days}>
            <input
              id="np_days"
              className="input num"
              type="number"
              min={1}
              value={periodDays}
              onChange={(e) => setPeriodDays(e.target.value)}
            />
          </Field>
          <Field id="np_from" label="Effective from" error={formError?.errors.effective_from}>
            <input
              id="np_from"
              className="input"
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            />
          </Field>
        </div>
        <div>
          <button type="submit" className="btn btn-primary" disabled={busy || !amount}>
            {busy ? 'Saving…' : 'Record new price'}
          </button>
        </div>
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          Active contracts keep the rent they were signed with. This changes what future contracts use.
        </p>
      </form>
    </div>
  );
}

/* --------------------------------- page ---------------------------------- */

function UnitBody({ id }: { id: string }) {
  const router = useRouter();
  const [unit, setUnit] = useState<Unit | null>(null);
  const [periods, setPeriods] = useState<PaymentPeriod[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [name, setName] = useState('');
  const [allowed, setAllowed] = useState<string[] | null>(null);
  const [savedNote, setSavedNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const apply = useCallback((u: Unit) => {
    setUnit(u);
    setName(u.name);
    setAllowed(u.allowed_period_ids && u.allowed_period_ids.length ? u.allowed_period_ids : null);
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    (async () => {
      try {
        apply(unwrapUnit(await unitsApi.get(id, ac.signal)));
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
      try {
        const per = await periodsApi.list(false, ac.signal);
        setPeriods((per.items ?? []).filter((p) => p.active));
      } catch {
        /* picker degrades to empty */
      }
    })();
    return () => ac.abort();
  }, [id, apply]);

  const patch = async (body: { name?: string; status?: UnitStatusOverride; allowed_period_ids?: string[] | null }) => {
    setBusy(true);
    setActionError(null);
    setSavedNote(null);
    try {
      apply(unwrapUnit(await unitsApi.update(id, body)));
      setSavedNote('Saved.');
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!unit) return;
    if (!window.confirm(`Delete unit "${unit.name}"? Its QR code stops working.`)) return;
    setActionError(null);
    try {
      await unitsApi.remove(unit.id);
      router.push(`/properties/${unit.property_id}`);
    } catch (e) {
      setActionError(toApiError(e));
    }
  };

  if (error) {
    return (
      <>
        <PageHead title="Unit" />
        <hr className="rule rule-strong" />
        <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <ProblemNote error={error} />
          <div>
            <Link href="/units" className="btn btn-secondary">
              Back to the vacancy board
            </Link>
          </div>
        </div>
      </>
    );
  }

  if (!unit) {
    return (
      <>
        <PageHead title="Unit" />
        <hr className="rule rule-strong" />
        <p style={{ paddingTop: 'var(--sp-5)', color: 'var(--ink-soft)' }}>Loading…</p>
      </>
    );
  }

  return (
    <>
      <PageHead
        title={unit.name}
        lead={`${unit.property_name} · ${fmtPrice(unit.current_price)}`}
        actions={
          <>
            <Link href={`/properties/${unit.property_id}`} className="btn btn-quiet">
              Back to property
            </Link>
            <button type="button" className="btn btn-danger" onClick={() => void remove()}>
              Delete unit
            </button>
          </>
        }
      />
      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
        <ProblemNote error={actionError} />
        {savedNote ? <Note>{savedNote}</Note> : null}

        <form
          onSubmit={(e) => {
            e.preventDefault();
            void patch({ name: name.trim() });
          }}
          style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'flex-end' }}
          noValidate
        >
          <Field id="u_name" label="Unit name" error={actionError?.errors.name}>
            <input
              id="u_name"
              className="input"
              value={name}
              maxLength={60}
              onChange={(e) => setName(e.target.value)}
              style={{ width: 320 }}
            />
          </Field>
          <button
            type="submit"
            className="btn btn-secondary"
            disabled={busy || !name.trim() || name.trim() === unit.name}
            style={{ minHeight: 40 }}
          >
            Rename
          </button>
        </form>
      </div>

      <Section title="Status">
        <div style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 640 }}>
          <p>
            Now: <StatusMark status={unit.status} override={unit.status_override} />{' '}
            {unit.status_override ? (
              <span style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>(set by hand)</span>
            ) : null}
          </p>
          {unit.status === 'occupied' ? (
            <p style={{ color: 'var(--ink-soft)' }}>
              Occupied is derived from the active contract and cannot be set by hand. End or terminate the
              contract and the unit flips back to vacant on its own.
            </p>
          ) : (
            <fieldset style={{ border: 0, margin: 0, padding: 0, display: 'grid', gap: 'var(--sp-2)' }}>
              <legend style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', marginBottom: 'var(--sp-2)' }}>
                Override
              </legend>
              {OVERRIDES.map((o) => (
                <label
                  key={o.value}
                  htmlFor={`st-${o.value}`}
                  style={{
                    display: 'flex',
                    alignItems: 'flex-start',
                    gap: 'var(--sp-3)',
                    minHeight: 'var(--touch-min)',
                    cursor: 'pointer',
                  }}
                >
                  <input
                    id={`st-${o.value}`}
                    type="radio"
                    name="unit_status"
                    value={o.value}
                    checked={unit.status === o.value}
                    disabled={busy}
                    onChange={() => void patch({ status: o.value })}
                    style={{ width: 18, height: 18, marginTop: 4 }}
                  />
                  <span>
                    <span style={{ fontWeight: 500 }}>{o.label}</span>
                    <br />
                    <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{o.hint}</span>
                  </span>
                </label>
              ))}
            </fieldset>
          )}
        </div>
      </Section>

      <Section title="Payment periods">
        <div style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 720 }}>
          <PeriodPicker periods={periods} value={allowed} onChange={setAllowed} idPrefix="unit" />
          <div>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy}
              onClick={() => void patch({ allowed_period_ids: allowed })}
            >
              Save periods
            </button>
          </div>
        </div>
      </Section>

      <Section title="QR code">
        <QrBlock unit={unit} />
      </Section>

      <Section title="Prices">
        <Prices
          unit={unit}
          onPriceAdded={() => {
            // the header prints the current price, so re-read the unit
            void unitsApi
              .get(id)
              .then((u) => apply(unwrapUnit(u)))
              .catch(() => undefined);
          }}
        />
      </Section>
    </>
  );
}

export default function UnitPage() {
  const params = useParams<{ id: string }>();
  const id = typeof params?.id === 'string' ? params.id : '';
  return (
    <Shell>
      <UnitBody id={id} />
    </Shell>
  );
}
