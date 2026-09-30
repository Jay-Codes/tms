'use client';

/**
 * Phase 31 — the renter declines a change to their contract, with a reason.
 * The running contract carries on unchanged; the landlord sees the reason.
 * Same bottom-sheet shell as the notice sheet.
 */

import { useEffect, useRef, useState } from 'react';
import { useT } from '@tms/ui';
import { contractApi, type Contract } from '../lib/api';
import { errorMessage } from '../lib/format';
import { Notice } from './Screen';

export function DeclineSheet({
  contract,
  onClose,
  onDone,
}: {
  contract: Contract;
  onClose: () => void;
  onDone: (c: Contract) => void;
}) {
  const t = useT();
  const [reason, setReason] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const dialog = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    dialog.current?.querySelector<HTMLElement>('textarea, button')?.focus();
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onDone((await contractApi.declineAmendment(contract.id, reason.trim())).contract);
    } catch (err) {
      setError(errorMessage(t, err));
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
        aria-label={t('decline.title')}
        className="sheet sheet-raised proof-sheet"
      >
        <header style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 'var(--sp-3)' }}>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('decline.title')}</h2>
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
          <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('decline.lead')}</p>
          {error && <Notice tone="error">{error}</Notice>}
          <div className="field">
            <label htmlFor="decline-reason">{t('decline.reason')}</label>
            <textarea
              id="decline-reason"
              className="input"
              rows={3}
              maxLength={200}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
          <button type="submit" className="btn btn-primary" disabled={busy || !reason.trim()}>
            {busy ? t('decline.sending') : t('decline.submit')}
          </button>
        </form>
      </div>
    </div>
  );
}
