'use client';

/**
 * The NIDA number on a landlord screen (Phase 19.1).
 *
 * The masked value is the resting state and stays the thing on the page. The
 * full number is a **decision**: a confirm sheet says in one line why the
 * reveal is recorded, takes an optional reason, and only then does the browser
 * POST for it. What comes back lives in React state for sixty seconds — never
 * localStorage, never a URL, never a second render after the timer — so a
 * landlord's open laptop does not leak somebody's ID number all afternoon.
 *
 * The reveal itself is the server's audit row; nothing here decides anything.
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useT } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import { ApiError, NIDA_REVEAL_MS, rentersApi, toApiError, type NidaReveal } from '../lib/api';

function Masked({ value }: { value: string | null | undefined }) {
  return (
    <span className="num" style={{ letterSpacing: '0.08em' }}>
      {value ?? '—'}
    </span>
  );
}

/**
 * The cell for the "NIDA" row of a `<Facts>` table: the value, and the way to
 * see it in full. `userId` is the renter; `masked` is whatever the profile
 * carried (`null` when the renter has no NIDA on file, in which case there is
 * nothing to reveal and no button is offered).
 */
export function NidaField({ userId, masked }: { userId: string; masked: string | null | undefined }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [revealed, setRevealed] = useState<NidaReveal | null>(null);
  const [secondsLeft, setSecondsLeft] = useState(0);
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);

  const stop = useCallback(() => {
    if (timer.current) clearInterval(timer.current);
    timer.current = null;
  }, []);

  // Re-mask on unmount as well as on the timer: navigating away is as good a
  // reason to forget the number as running out of time.
  useEffect(() => stop, [stop]);

  const reveal = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await rentersApi.revealNida(userId, reason.trim() || undefined);
      setRevealed(res);
      setCopied(false);
      setOpen(false);
      setReason('');
      const until = Date.now() + NIDA_REVEAL_MS;
      setSecondsLeft(Math.round(NIDA_REVEAL_MS / 1000));
      stop();
      timer.current = setInterval(() => {
        const left = Math.max(0, Math.round((until - Date.now()) / 1000));
        setSecondsLeft(left);
        if (left === 0) {
          stop();
          setRevealed(null);
          setCopied(false);
        }
      }, 1000);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!revealed) return;
    try {
      await navigator.clipboard.writeText(revealed.nida_number);
      setCopied(true);
    } catch {
      /* no clipboard permission — the number is on screen to read */
    }
  };

  const hide = () => {
    stop();
    setRevealed(null);
    setCopied(false);
  };

  if (!masked) return <Masked value={masked} />;

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
      {revealed ? (
        <>
          <strong className="num" style={{ letterSpacing: '0.08em' }}>
            {revealed.nida_number}
          </strong>
          <button type="button" className="btn btn-quiet" onClick={() => void copy()} style={{ minHeight: 32 }}>
            <Icon icon="solar:copy-linear" width={18} /> {copied ? t('common.copied') : t('common.copy')}
          </button>
          <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
            {t('renters.nida.hiding_in', { seconds: secondsLeft })}
          </span>
          <button type="button" className="btn btn-quiet" onClick={hide} style={{ minHeight: 32 }}>
            {t('renters.nida.hide')}
          </button>
        </>
      ) : (
        <>
          <Masked value={masked} />
          <button
            type="button"
            className="btn btn-quiet"
            onClick={() => {
              setError(null);
              setOpen(true);
            }}
            style={{ minHeight: 32 }}
          >
            <Icon icon="solar:eye-linear" width={18} /> {t('renters.nida.show')}
          </button>
        </>
      )}

      <Sheet open={open} title={t('renters.nida.confirm_title')} onClose={() => setOpen(false)} width={480}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void reveal();
          }}
          style={{ display: 'grid', gap: 'var(--sp-4)' }}
          noValidate
        >
          <ProblemNote error={error} />
          <p style={{ color: 'var(--ink-soft)' }}>{t('renters.nida.confirm_why')}</p>
          <Field
            id="nida_reason"
            label={t('renters.nida.reason_label')}
            hint={t('renters.nida.reason_hint', { n: reason.length })}
            error={error?.errors.reason}
          >
            <textarea
              id="nida_reason"
              className="input"
              rows={2}
              maxLength={200}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t('renters.nida.reason_placeholder')}
            />
          </Field>
          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? t('renters.nida.revealing') : t('renters.nida.reveal')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setOpen(false)} disabled={busy}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      </Sheet>
    </span>
  );
}
