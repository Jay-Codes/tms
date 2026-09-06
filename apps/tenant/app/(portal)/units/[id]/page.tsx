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
import { Field, Note, ProblemNote } from '../../../../components/FormBits';
import { PeriodPicker } from '../../../../components/PeriodPicker';
import { PageHead } from '../../../../components/PageHead';
import { StatusMark } from '../../../../components/UnitStatus';
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
} from '../../../../lib/api';
import { Amount, fmtDate, fmtPrice, todayISO } from '../../../../lib/format';
import { TableScroll, useT } from '@tms/ui';

/** Override choices as dictionary keys — translated where they are drawn. */
const OVERRIDES: { value: UnitStatusOverride; labelKey: string; hintKey: string }[] = [
  { value: 'vacant', labelKey: 'units.status.vacant', hintKey: 'units.override.vacant_hint' },
  { value: 'maintenance', labelKey: 'units.status.maintenance', hintKey: 'units.override.maintenance_hint' },
  { value: 'unlisted', labelKey: 'units.status.unlisted', hintKey: 'units.override.unlisted_hint' },
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
  const t = useT();
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
      <dl
        className="stack-sm"
        style={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: 'var(--sp-2) var(--sp-4)', margin: 0 }}
      >
        <dt style={{ color: 'var(--ink-soft)' }}>{t('qr.unit_code')}</dt>
        <dd className="num" style={{ margin: 0, textAlign: 'left', letterSpacing: '0.14em', fontWeight: 600 }}>
          {unit.unit_code}
        </dd>
        <dt style={{ color: 'var(--ink-soft)' }}>{t('qr.scan_url')}</dt>
        <dd style={{ margin: 0, wordBreak: 'break-all' }}>{qr?.scan_url ?? unit.scan_url ?? '—'}</dd>
      </dl>

      {qr ? (
        <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-start' }}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={qr.png_url}
            alt={t('qr.code_alt', { name: unit.name })}
            width={180}
            height={180}
            style={{ border: '1px solid var(--rule)', background: '#fff' }}
          />
          <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
            <a className="btn btn-secondary" href={qr.png_url} download={`${unit.unit_code}.png`} target="_blank" rel="noreferrer">
              <Icon icon="solar:download-linear" width={20} /> {t('qr.download_png')}
            </a>
            <button type="button" className="btn btn-quiet" onClick={() => void generate()} disabled={busy}>
              {busy ? t('qr.working') : t('qr.regenerate')}
            </button>
            <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
              {t('qr.link_expiry')}
            </span>
          </div>
        </div>
      ) : (
        <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center' }}>
          <button type="button" className="btn btn-primary" onClick={() => void generate()} disabled={busy}>
            <Icon icon="solar:qr-code-linear" width={20} /> {busy ? t('qr.working') : t('qr.show')}
          </button>
          <Link href={`/properties/${unit.property_id}/qr`} className="btn btn-quiet">
            {t('qr.print_property')}
          </Link>
        </div>
      )}
    </div>
  );
}

/* -------------------------------- prices --------------------------------- */

function Prices({ unit, onPriceAdded }: { unit: Unit; onPriceAdded: () => void }) {
  const t = useT();
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
      setNote(t('units.price.note'));
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

      <TableScroll label={t('units.price.table_label')}>
<table className="ledger">
        <thead>
          <tr>
            <th>{t('units.effective_from')}</th>
            <th className="num">{t('common.amount')}</th>
            <th className="num">{t('units.per_days_label')}</th>
            <th>{t('units.price.th.set_by')}</th>
          </tr>
        </thead>
        <tbody>
          {items === null ? (
            <tr>
              <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                {t('common.loading')}
              </td>
            </tr>
          ) : items.length === 0 ? (
            <tr>
              <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                {t('units.price.empty')}
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
</TableScroll>

      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <h3 style={{ fontSize: 'var(--text-md)', fontWeight: 600 }}>{t('units.price.new')}</h3>
        <ProblemNote error={formError} />
        {note ? <Note>{note}</Note> : null}
        <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 140px 180px', gap: 'var(--sp-4)' }}>
          <Field id="np_amount" label={t('units.price.amount_label')} error={formError?.errors.amount}>
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
          <Field id="np_days" label={t('units.per_days_label')} error={formError?.errors.period_days}>
            <input
              id="np_days"
              className="input num"
              type="number"
              min={1}
              value={periodDays}
              onChange={(e) => setPeriodDays(e.target.value)}
            />
          </Field>
          <Field id="np_from" label={t('units.effective_from')} error={formError?.errors.effective_from}>
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
            {busy ? t('common.saving') : t('units.price.record')}
          </button>
        </div>
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('units.price.footnote')}
        </p>
      </form>
    </div>
  );
}

/* --------------------------------- page ---------------------------------- */

function UnitBody({ id }: { id: string }) {
  const t = useT();
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
      setSavedNote(t('common.saved'));
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!unit) return;
    if (!window.confirm(t('units.delete_confirm', { name: unit.name }))) return;
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
        <PageHead title={t('common.unit')} />
        <hr className="rule rule-strong" />
        <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <ProblemNote error={error} />
          <div>
            <Link href="/units" className="btn btn-secondary">
              {t('units.back_board')}
            </Link>
          </div>
        </div>
      </>
    );
  }

  if (!unit) {
    return (
      <>
        <PageHead title={t('common.unit')} />
        <hr className="rule rule-strong" />
        <p style={{ paddingTop: 'var(--sp-5)', color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
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
              {t('properties.back_to_property')}
            </Link>
            <button type="button" className="btn btn-danger" onClick={() => void remove()}>
              {t('units.delete')}
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
          className="wrap-sm"
          style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'flex-end' }}
          noValidate
        >
          <Field id="u_name" label={t('units.name_label')} error={actionError?.errors.name}>
            <input
              id="u_name"
              className="input"
              value={name}
              maxLength={60}
              onChange={(e) => setName(e.target.value)}
              style={{ width: 320, maxWidth: '100%' }}
            />
          </Field>
          <button
            type="submit"
            className="btn btn-secondary"
            disabled={busy || !name.trim() || name.trim() === unit.name}
            style={{ minHeight: 40 }}
          >
            {t('units.rename')}
          </button>
        </form>
      </div>

      <Section title={t('common.status')}>
        <div style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 640 }}>
          <p>
            {t('units.now')} <StatusMark status={unit.status} override={unit.status_override} />{' '}
            {unit.status_override ? (
              <span style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>
                {t('units.set_by_hand_paren')}
              </span>
            ) : null}
          </p>
          {unit.status === 'occupied' ? (
            <p style={{ color: 'var(--ink-soft)' }}>
              {t('units.occupied_derived')}
            </p>
          ) : (
            <fieldset style={{ border: 0, margin: 0, padding: 0, display: 'grid', gap: 'var(--sp-2)' }}>
              <legend style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', marginBottom: 'var(--sp-2)' }}>
                {t('units.override')}
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
                    <span style={{ fontWeight: 500 }}>{t(o.labelKey)}</span>
                    <br />
                    <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t(o.hintKey)}</span>
                  </span>
                </label>
              ))}
            </fieldset>
          )}
        </div>
      </Section>

      <Section title={t('units.section.periods')}>
        <div style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 720 }}>
          <PeriodPicker periods={periods} value={allowed} onChange={setAllowed} idPrefix="unit" />
          <div>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy}
              onClick={() => void patch({ allowed_period_ids: allowed })}
            >
              {t('units.save_periods')}
            </button>
          </div>
        </div>
      </Section>

      <Section title={t('qr.code')}>
        <QrBlock unit={unit} />
      </Section>

      <Section title={t('units.section.prices')}>
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
    <>
      <UnitBody id={id} />
    </>
  );
}
