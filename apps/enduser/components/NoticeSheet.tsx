'use client';

/**
 * Phase 22.5 — the renter gives notice to leave before the contract's end
 * date. The date picker starts at the earliest day the contract allows
 * (today + its notice days); the backend is still the judge and answers
 * `422 notice_too_short` with the real earliest date, which the sheet offers.
 * Same bottom-sheet shell as the proof sheet.
 */

import { useEffect, useRef, useState } from 'react';
import { useLocale, useT } from '@tms/ui';
import { ApiError, contractApi, type Contract } from '../lib/api';
import { addDays, errorMessage, formatDate, todayIso } from '../lib/format';
import { Notice } from './Screen';

export function NoticeSheet({
  contract,
  onClose,
  onDone,
}: {
  contract: Contract;
  onClose: () => void;
  onDone: (c: Contract) => void;
}) {
  const t = useT();
  const locale = useLocale();
  const minDate = addDays(todayIso(), contract.policy?.tenant_notice_days ?? 0);
  // The day before the end date: leaving on the end date needs no notice.
  const maxDate = addDays(contract.end_date, -1);
  const [leaveOn, setLeaveOn] = useState(minDate);
  const [reason, setReason] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [earliest, setEarliest] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const dialog = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    dialog.current?.querySelector<HTMLElement>('input, textarea, button')?.focus();
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setEarliest(null);
    try {
      const res = await contractApi.giveNotice(contract.id, {
        leave_on: leaveOn,
        reason: reason.trim() || undefined,
      });
      onDone(res.contract);
    } catch (err) {
      if (err instanceof ApiError && err.is('notice_too_short') && err.earliest) {
        setEarliest(err.earliest);
        setError(t('notice.tooShort', { date: formatDate(locale, err.earliest) }));
      } else {
        setError(errorMessage(t, err));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="proof-backdrop"
      role="presentation"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-label={t('notice.title')}
        className="sheet sheet-raised proof-sheet"
      >
        <header style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 'var(--sp-3)' }}>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('notice.title')}</h2>
          <button
            type="button"
            className="btn btn-quiet"
            onClick={onClose}
            style={{ width: 'auto', paddingInline: 'var(--sp-2)' }}
          >
            {t('proof.close')}
          </button>
        </header>

        <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {contract.policy?.tenant_notice_days
              ? t('notice.leadDays', { days: contract.policy.tenant_notice_days })
              : t('notice.lead')}
          </p>

          {error && <Notice tone="error">{error}</Notice>}

          <div className="field">
            <label htmlFor="notice-date">{t('notice.leaveOn')}</label>
            <input
              id="notice-date"
              className="input"
              type="date"
              min={minDate}
              max={maxDate}
              value={leaveOn}
              onChange={(e) => setLeaveOn(e.target.value)}
            />
            <span className="hint">{t('notice.beforeEnd', { date: formatDate(locale, contract.end_date) })}</span>
          </div>
          {earliest && (
            <button type="button" className="btn btn-secondary" onClick={() => setLeaveOn(earliest)}>
              {t('notice.useEarliest', { date: formatDate(locale, earliest) })}
            </button>
          )}

          <div className="field">
            <label htmlFor="notice-reason">{t('notice.reason')}</label>
            <textarea
              id="notice-reason"
              className="input"
              rows={2}
              maxLength={200}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>

          <button type="submit" className="btn btn-primary" disabled={busy || !leaveOn}>
            {busy ? t('notice.sending') : t('notice.submit')}
          </button>
        </form>
      </div>
    </div>
  );
}
