'use client';

/**
 * Payment periods editor (FLOWS flow 5 step 2a, SPEC §4 "payment periods are
 * landlord-defined, in days, unlimited"). Used by Settings and by the setup
 * wizard, so it takes no props beyond a compact flag.
 *
 * All state lives on the server: every edit is a request to
 * `/org/payment-periods` and the list is re-read afterwards.
 */

import { Icon } from '@iconify/react';
import { useT } from '@tms/ui';
import { useCallback, useEffect, useState } from 'react';
import {
  ApiError,
  periodsApi,
  toApiError,
  type PaymentPeriod,
} from '../lib/api';
import { Field, Note, ProblemNote } from './FormBits';
import { TableScroll } from '@tms/ui';

function Recommended() {
  const t = useT();
  return (
    <span
      style={{
        display: 'inline-block',
        padding: '0.1em 0.5em',
        border: '1px solid var(--rule)',
        borderRadius: 'var(--radius-sm)',
        fontSize: 'var(--text-xs)',
        color: 'var(--ink-soft)',
        background: 'var(--sheet-tint)',
        whiteSpace: 'nowrap',
      }}
    >
      {t('periods.recommended')}
    </span>
  );
}

function Row({
  period,
  first,
  last,
  onChanged,
  onError,
}: {
  period: PaymentPeriod;
  first: boolean;
  last: boolean;
  onChanged: () => Promise<void>;
  onError: (e: ApiError | null) => void;
}) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [label, setLabel] = useState(period.label);
  const [days, setDays] = useState(String(period.days));
  const [busy, setBusy] = useState(false);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    onError(null);
    try {
      await fn();
      await onChanged();
      setEditing(false);
    } catch (e) {
      onError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  if (editing) {
    return (
      <tr>
        <td colSpan={4}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void run(() =>
                periodsApi.update(period.id, { label: label.trim(), days: Number(days) }),
              );
            }}
            style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'flex-end', flexWrap: 'wrap' }}
          >
            <Field id={`lbl-${period.id}`} label={t('periods.col.label')}>
              <input
                id={`lbl-${period.id}`}
                className="input"
                value={label}
                maxLength={40}
                onChange={(e) => setLabel(e.target.value)}
                style={{ width: 220 }}
              />
            </Field>
            <Field id={`days-${period.id}`} label={t('periods.col.days')}>
              <input
                id={`days-${period.id}`}
                className="input num"
                type="number"
                min={1}
                value={days}
                onChange={(e) => setDays(e.target.value)}
                style={{ width: 110 }}
              />
            </Field>
            <button type="submit" className="btn btn-primary" disabled={busy} style={{ minHeight: 40 }}>
              {busy ? t('common.saving') : t('common.save')}
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => {
                setEditing(false);
                setLabel(period.label);
                setDays(String(period.days));
              }}
              style={{ minHeight: 40 }}
            >
              {t('common.cancel')}
            </button>
          </form>
        </td>
      </tr>
    );
  }

  return (
    <tr style={period.active ? undefined : { opacity: 0.55 }}>
      <td style={{ fontWeight: 500 }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          {period.label}
          {period.is_recommended ? <Recommended /> : null}
          {period.active ? null : <span className="pencil">{t('periods.inactive')}</span>}
        </span>
      </td>
      <td className="num">{period.days}</td>
      <td className="num" style={{ whiteSpace: 'nowrap' }}>
        {period.active ? (
          <>
            <button
              type="button"
              className="btn btn-quiet"
              aria-label={t('periods.move_up', { label: period.label })}
              disabled={first || busy}
              onClick={() => void run(() => periodsApi.update(period.id, { sort_order: period.sort_order - 1 }))}
              style={{ minHeight: 32, padding: '0 var(--sp-2)' }}
            >
              <Icon icon="solar:alt-arrow-up-linear" width={18} />
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              aria-label={t('periods.move_down', { label: period.label })}
              disabled={last || busy}
              onClick={() => void run(() => periodsApi.update(period.id, { sort_order: period.sort_order + 1 }))}
              style={{ minHeight: 32, padding: '0 var(--sp-2)' }}
            >
              <Icon icon="solar:alt-arrow-down-linear" width={18} />
            </button>
          </>
        ) : null}
      </td>
      <td className="num" style={{ whiteSpace: 'nowrap' }}>
        {period.active ? (
          <>
            {period.is_recommended ? null : (
              <button
                type="button"
                className="btn btn-quiet"
                onClick={() => void run(() => periodsApi.recommend(period.id))}
                disabled={busy}
                style={{ minHeight: 32 }}
              >
                {t('periods.set_recommended')}
              </button>
            )}
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => setEditing(true)}
              disabled={busy}
              style={{ minHeight: 32 }}
            >
              {t('common.edit')}
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => void run(() => periodsApi.deactivate(period.id))}
              disabled={busy}
              style={{ minHeight: 32 }}
            >
              {t('periods.deactivate')}
            </button>
          </>
        ) : (
          <button
            type="button"
            className="btn btn-quiet"
            onClick={() => void run(() => periodsApi.update(period.id, { active: true }))}
            disabled={busy}
            style={{ minHeight: 32 }}
          >
            {t('periods.restore')}
          </button>
        )}
      </td>
    </tr>
  );
}

export function PeriodsManager({ heading }: { heading?: string }) {
  const t = useT();
  const [items, setItems] = useState<PaymentPeriod[] | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [label, setLabel] = useState('');
  const [days, setDays] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await periodsApi.list(true, signal);
      setItems(res.items ?? []);
      setLoadError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setLoadError(toApiError(e));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const reload = useCallback(async () => {
    await load();
  }, [load]);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await periodsApi.create({ label: label.trim(), days: Number(days) });
      setLabel('');
      setDays('');
      setNote(t('periods.added'));
      await reload();
    } catch (err) {
      setActionError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  const restoreRecommended = async () => {
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await periodsApi.restoreRecommended();
      setNote(t('periods.presets_restored'));
      await reload();
    } catch (err) {
      setActionError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  const active = (items ?? []).filter((p) => p.active);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
      {heading ? <h3 style={{ fontSize: 'var(--text-lg)' }}>{heading}</h3> : null}
      <ProblemNote error={loadError} />
      <ProblemNote error={actionError} />
      {note ? <Note>{note}</Note> : null}

      <TableScroll label={t('periods.title')}>
      <table className="ledger">
        <thead>
          <tr>
            <th>{t('periods.col.period')}</th>
            <th className="num">{t('periods.col.days')}</th>
            <th className="num">{t('periods.col.order')}</th>
            <th className="num" />
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
                {t('periods.empty')}
              </td>
            </tr>
          ) : (
            items.map((p) => (
              <Row
                key={p.id}
                period={p}
                first={active.length > 0 && active[0].id === p.id}
                last={active.length > 0 && active[active.length - 1].id === p.id}
                onChanged={reload}
                onError={setActionError}
              />
            ))
          )}
        </tbody>
      </table>
      </TableScroll>

      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('periods.note.recommended')}</p>

      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('periods.note.future_only')}</p>

      <form onSubmit={add} style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'flex-end', flexWrap: 'wrap' }} noValidate>
        <Field
          id="p_label"
          label={t('periods.new.label')}
          hint={t('periods.new.hint')}
          error={actionError?.errors.label}
        >
          <input
            id="p_label"
            className="input"
            value={label}
            maxLength={40}
            onChange={(e) => setLabel(e.target.value)}
            placeholder={t('periods.new.placeholder')}
            style={{ width: 240 }}
          />
        </Field>
        <Field id="p_days" label={t('periods.col.days')} error={actionError?.errors.days}>
          <input
            id="p_days"
            className="input num"
            type="number"
            min={1}
            value={days}
            onChange={(e) => setDays(e.target.value)}
            placeholder="7"
            style={{ width: 110 }}
          />
        </Field>
        <button
          type="submit"
          className="btn btn-primary"
          disabled={busy || !label.trim() || !days}
          style={{ minHeight: 40 }}
        >
          <Icon icon="solar:add-circle-linear" width={20} /> {t('periods.add')}
        </button>
        <button type="button" className="btn btn-secondary" onClick={() => void restoreRecommended()} disabled={busy} style={{ minHeight: 40 }}>
          {t('periods.restore_presets')}
        </button>
      </form>
    </div>
  );
}
