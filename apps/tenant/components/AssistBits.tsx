'use client';

/**
 * Landlord-assisted onboarding pieces (PLAN2 Phase 25, FLOWS 2b).
 *
 * The landlord's screen is the channel a code travels on when the SMS never
 * arrives: the code is shown large, the renter types it on their own phone.
 * Nothing here ever enters a code *for* the renter (DECISIONS 2026-09-20).
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { ApiError, assistApi, toApiError } from '../lib/api';
import { ProblemNote } from './FormBits';
import { Sheet } from './Sheet';

/** Seconds left until `iso`, ticking once a second; 0 once it has passed. */
export function useSecondsLeft(iso: string | null | undefined): number {
  const target = iso ? Date.parse(iso) : NaN;
  const read = useCallback(
    () => (Number.isNaN(target) ? 0 : Math.max(0, Math.round((target - Date.now()) / 1000))),
    [target],
  );
  const [left, setLeft] = useState(read);
  useEffect(() => {
    setLeft(read());
    if (Number.isNaN(target)) return;
    const timer = setInterval(() => setLeft(read()), 1000);
    return () => clearInterval(timer);
  }, [read, target]);
  return left;
}

/** `m:ss` — a code lives five minutes, a session thirty. */
export function mmss(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

/**
 * The code as the renter reads it across the table: six digits in two
 * groups of three, and how long it has left. An expired code is struck
 * through rather than hidden, so nobody reads out a dead one.
 */
export function ShownCode({ code, expiresAt }: { code: string; expiresAt: string }) {
  const t = useT();
  const left = useSecondsLeft(expiresAt);
  const expired = left === 0;
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-2)', justifyItems: 'start' }}>
      <p
        aria-label={t('assist.code_label')}
        className="amount"
        style={{
          margin: 0,
          fontSize: 'clamp(2.5rem, 9vw, 4rem)',
          fontWeight: 700,
          letterSpacing: '0.12em',
          fontVariantNumeric: 'tabular-nums lining-nums',
          lineHeight: 1.1,
          color: expired ? 'var(--ink-faint)' : 'var(--ink)',
          textDecoration: expired ? 'line-through' : 'none',
        }}
      >
        {code.slice(0, 3)}&thinsp;{code.slice(3)}
      </p>
      <p
        role="timer"
        aria-live="off"
        style={{
          margin: 0,
          fontSize: 'var(--text-sm)',
          color: expired ? 'var(--stamp-overdue)' : 'var(--ink-soft)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--sp-1)',
        }}
      >
        <Icon icon="solar:clock-circle-linear" width={16} aria-hidden />
        {expired ? t('assist.code_expired') : t('assist.code_expires_in', { time: mmss(left) })}
      </p>
    </div>
  );
}

/**
 * Witness signing (FLOWS 2b step 6): a signing code is written to the slot
 * the `contract_sign_otp` SMS would fill and shown here; the renter types it
 * into Accept & sign on their own phone, and the signature row records the
 * landlord as witness.
 */
export function WitnessSheet({
  contractId,
  renterName,
  open,
  onClose,
}: {
  contractId: string;
  renterName: string;
  open: boolean;
  onClose: () => void;
}) {
  const t = useT();
  const [code, setCode] = useState<{ code: string; code_expires_at: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const reveal = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      setCode(await assistApi.witnessCode(contractId));
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  }, [contractId]);

  /* One reveal per opening; closing forgets the code so it is never left
     on a screen that has been put down. */
  useEffect(() => {
    if (!open) {
      setCode(null);
      setError(null);
      return;
    }
    void reveal();
  }, [open, reveal]);

  return (
    <Sheet open={open} title={t('assist.witness.title')} onClose={onClose} width={480}>
      <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
        <p style={{ color: 'var(--ink-soft)' }}>{t('assist.witness.lead', { name: renterName })}</p>
        <ProblemNote error={error} />
        {code ? (
          <ShownCode code={code.code} expiresAt={code.code_expires_at} />
        ) : busy ? (
          <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
        ) : null}
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('assist.witness.note')}</p>
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          <button type="button" className="btn btn-secondary" onClick={() => void reveal()} disabled={busy}>
            <Icon icon="solar:refresh-linear" width={20} /> {busy ? t('assist.new_code_busy') : t('assist.new_code')}
          </button>
          <button type="button" className="btn btn-quiet" onClick={onClose}>
            {t('common.done')}
          </button>
        </div>
      </div>
    </Sheet>
  );
}
