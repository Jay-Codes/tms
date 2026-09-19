'use client';

/**
 * "I have paid" — Phase 16 §16.1, the renter half.
 *
 * A proof is a claim, not a payment: this sheet collects what the renter says
 * they paid plus the receipt behind it, and the landlord decides. The file
 * goes straight to MinIO through a presigned PUT (`POST /me/proofs/upload` →
 * the signed URL → `POST /me/proofs`), exactly as the KYC photo does — the
 * browser never touches a bucket on its own and never keeps the file.
 *
 * The org's payment instructions are repeated inside the sheet on purpose:
 * the moment a renter is filling this in is the moment they may realise they
 * paid the wrong account.
 */

import { useEffect, useMemo, useRef, useState } from 'react';
import { Icon } from '@iconify/react';
import { useT } from '@tms/ui';
import {
  ApiError,
  PROOF_MAX_BYTES,
  PROOF_TYPES,
  renterApi,
  uploadToPresignedUrl,
  type BankAccount,
  type MobileMoney,
  type ProofMethod,
} from '../lib/api';
import { proofErrorMessage, todayIso } from '../lib/format';
import { PayDetails } from './PayDetails';
import { Notice } from './Screen';

/** What the sheet was opened against: one tenancy, optionally one instalment. */
export interface ProofTarget {
  contractId: string;
  /** "A2 · Mikocheni Flats", for the sheet's subtitle. */
  contractLabel?: string;
  scheduleId?: string | null;
  /** Prefilled amount — the outstanding balance of the row that opened it. */
  amount?: number;
}

export interface ProofSheetProps {
  open: boolean;
  target: ProofTarget | null;
  account: BankAccount | null;
  mobileMoney?: MobileMoney | null;
  reference: string;
  onClose: () => void;
  /** Reload the screen behind the sheet once a claim has landed. */
  onSubmitted?: () => void;
}

const FILE_ACCEPT = PROOF_TYPES.join(',');

export function ProofSheet({
  open,
  target,
  account,
  mobileMoney,
  reference,
  onClose,
  onSubmitted,
}: ProofSheetProps) {
  const t = useT();
  const today = todayIso();

  const [amount, setAmount] = useState('');
  const [paidAt, setPaidAt] = useState(today);
  const [method, setMethod] = useState<ProofMethod>('bank_transfer');
  const [ref, setRef] = useState('');
  const [note, setNote] = useState('');
  const [file, setFile] = useState<File | null>(null);

  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState(false);

  const dialog = useRef<HTMLDivElement | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  /* Fresh sheet every time it opens — a stale amount from the last row would
     be the one mistake this screen must not make. */
  useEffect(() => {
    if (!open) return;
    setAmount(target?.amount ? String(target.amount) : '');
    setPaidAt(todayIso());
    setMethod('bank_transfer');
    setRef('');
    setNote('');
    setFile(null);
    setFieldErrors({});
    setError(null);
    setDone(false);
    setStep(null);
    if (fileInput.current) fileInput.current.value = '';
  }, [open, target]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    dialog.current?.querySelector<HTMLElement>('input, select, textarea, button')?.focus();
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  /* The thumbnail is a browser-local object URL; it is revoked as soon as the
     choice changes so nothing lingers in memory. PDFs get an icon instead. */
  const previewUrl = useMemo(
    () => (file && file.type.startsWith('image/') ? URL.createObjectURL(file) : null),
    [file],
  );
  useEffect(() => {
    return () => {
      if (previewUrl) URL.revokeObjectURL(previewUrl);
    };
  }, [previewUrl]);

  if (!open) return null;

  function pickFile(chosen: File | null) {
    setFieldErrors((e) => ({ ...e, file: '' }));
    if (!chosen) {
      setFile(null);
      return;
    }
    if (!(PROOF_TYPES as readonly string[]).includes(chosen.type)) {
      setFile(null);
      setFieldErrors((e) => ({ ...e, file: t('proof.error.fileType') }));
      return;
    }
    if (chosen.size > PROOF_MAX_BYTES) {
      setFile(null);
      setFieldErrors((e) => ({ ...e, file: t('proof.error.fileSize') }));
      return;
    }
    setFile(chosen);
  }

  /** UX-side checks only — the API validates every one of these again. */
  function validate(): Record<string, string> {
    const errs: Record<string, string> = {};
    const value = Number(amount);
    if (!Number.isFinite(value) || Math.round(value) <= 0) errs.amount = t('proof.error.amount');
    if (!paidAt || paidAt > todayIso()) errs.paid_at = t('proof.error.date');
    if (!file) errs.file = t('proof.error.fileRequired');
    return errs;
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!target) return;
    const errs = validate();
    setFieldErrors(errs);
    setError(null);
    if (Object.keys(errs).length > 0 || !file) return;

    setBusy(true);
    try {
      setStep(t('proof.uploading'));
      const ticket = await renterApi.proofUploadTicket(target.contractId, file.type, file.size);
      await uploadToPresignedUrl(ticket, file);

      setStep(t('proof.submitting'));
      // Midday local keeps the day intact whichever way the server reads the
      // offset, and stays inside the API's "no more than a day ahead" rule.
      await renterApi.createProof({
        contract_id: target.contractId,
        ...(target.scheduleId ? { schedule_id: target.scheduleId } : {}),
        amount: Math.round(Number(amount)),
        paid_at: new Date(`${paidAt}T12:00:00`).toISOString(),
        method,
        ...(ref.trim() ? { reference: ref.trim() } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
        object_key: ticket.object_key,
      });

      setDone(true);
      onSubmitted?.();
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.errors).length > 0) {
        setFieldErrors(err.errors);
      }
      setError(proofErrorMessage(t, err));
    } finally {
      setBusy(false);
      setStep(null);
    }
  }

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
        aria-label={t('proof.title')}
        className="sheet sheet-raised proof-sheet"
      >
        <header
          style={{
            display: 'flex',
            alignItems: 'flex-start',
            justifyContent: 'space-between',
            gap: 'var(--sp-3)',
          }}
        >
          <div>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('proof.title')}</h2>
            {target?.contractLabel && (
              <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {target.contractLabel}
              </p>
            )}
          </div>
          <button
            type="button"
            className="btn btn-quiet"
            onClick={onClose}
            style={{ width: 'auto', paddingInline: 'var(--sp-2)' }}
          >
            {t('proof.close')}
          </button>
        </header>

        {!target ? (
          <p className="pencil">{t('proof.noContract')}</p>
        ) : done ? (
          <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <p style={{ margin: 0 }}>
              <span className="stamp stamp-paid">{t('proof.status.submitted')}</span>
            </p>
            <h3 style={{ fontSize: 'var(--text-lg)', margin: 0 }}>{t('proof.done.title')}</h3>
            <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('proof.done.lead')}
            </p>
            <button type="button" className="btn btn-primary" onClick={onClose}>
              {t('proof.close')}
            </button>
          </div>
        ) : (
          <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
            <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('proof.lead')}
            </p>

            {error && <Notice tone="error">{error}</Notice>}

            <div className={`field${fieldErrors.amount ? ' invalid' : ''}`}>
              <label htmlFor="proof-amount">{t('proof.amount')}</label>
              <input
                id="proof-amount"
                className="input"
                inputMode="numeric"
                autoComplete="off"
                value={amount}
                onChange={(e) => setAmount(e.target.value.replace(/[^\d]/g, ''))}
              />
              {fieldErrors.amount && <span className="error">{fieldErrors.amount}</span>}
            </div>

            <div className={`field${fieldErrors.paid_at ? ' invalid' : ''}`}>
              <label htmlFor="proof-date">{t('proof.paidAt')}</label>
              <input
                id="proof-date"
                className="input"
                type="date"
                max={today}
                value={paidAt}
                onChange={(e) => setPaidAt(e.target.value)}
              />
              {fieldErrors.paid_at && <span className="error">{fieldErrors.paid_at}</span>}
            </div>

            <div className={`field${fieldErrors.method ? ' invalid' : ''}`}>
              <label htmlFor="proof-method">{t('proof.method')}</label>
              <select
                id="proof-method"
                className="input"
                value={method}
                onChange={(e) => setMethod(e.target.value as ProofMethod)}
              >
                <option value="bank_transfer">{t('payments.method.bank_transfer')}</option>
                <option value="mobile_money_manual">
                  {t('payments.method.mobile_money_manual')}
                </option>
              </select>
              {fieldErrors.method && <span className="error">{fieldErrors.method}</span>}
            </div>

            <div className={`field${fieldErrors.reference ? ' invalid' : ''}`}>
              <label htmlFor="proof-ref">{t('proof.reference')}</label>
              <input
                id="proof-ref"
                className="input"
                autoComplete="off"
                maxLength={80}
                value={ref}
                onChange={(e) => setRef(e.target.value)}
              />
              {fieldErrors.reference && <span className="error">{fieldErrors.reference}</span>}
            </div>

            <div className={`field${fieldErrors.note ? ' invalid' : ''}`}>
              <label htmlFor="proof-note">{t('proof.note')}</label>
              <textarea
                id="proof-note"
                className="input"
                rows={2}
                maxLength={500}
                style={{ paddingBlock: 'var(--sp-2)', resize: 'vertical' }}
                value={note}
                onChange={(e) => setNote(e.target.value)}
              />
              {fieldErrors.note && <span className="error">{fieldErrors.note}</span>}
            </div>

            <div
              className={`field${fieldErrors.file || fieldErrors.object_key ? ' invalid' : ''}`}
            >
              <label htmlFor="proof-file">{t('proof.file')}</label>
              <input
                id="proof-file"
                ref={fileInput}
                className="input"
                type="file"
                accept={FILE_ACCEPT}
                capture="environment"
                onChange={(e) => pickFile(e.target.files?.[0] ?? null)}
              />
              <span className="hint">{t('proof.fileHint')}</span>
              {file && (
                <span
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--sp-3)',
                    paddingTop: 'var(--sp-2)',
                  }}
                >
                  {previewUrl ? (
                    // A local object URL; next/image cannot serve one and this
                    // app is CSR-only.
                    // eslint-disable-next-line @next/next/no-img-element
                    <img
                      src={previewUrl}
                      alt={t('proof.filePreviewAlt')}
                      style={{
                        width: 56,
                        height: 56,
                        objectFit: 'cover',
                        borderRadius: 'var(--radius-sm)',
                        border: '1px solid var(--rule)',
                      }}
                    />
                  ) : (
                    <Icon icon="solar:document-linear" width={28} aria-hidden />
                  )}
                  <span
                    style={{
                      color: 'var(--ink-soft)',
                      fontSize: 'var(--text-sm)',
                      overflowWrap: 'anywhere',
                    }}
                  >
                    {file.name}
                  </span>
                </span>
              )}
              {(fieldErrors.file || fieldErrors.object_key) && (
                <span className="error">{fieldErrors.file || fieldErrors.object_key}</span>
              )}
            </div>

            <section
              style={{
                display: 'grid',
                gap: 'var(--sp-2)',
                padding: 'var(--sp-3)',
                borderTop: '1px solid var(--rule)',
              }}
            >
              <h3 style={{ fontSize: 'var(--text-md)', margin: 0 }}>{t('proof.instructions')}</h3>
              <PayDetails
                account={account}
                mobileMoney={mobileMoney}
                reference={reference}
                compact
              />
            </section>

            <button className="btn btn-primary" type="submit" disabled={busy}>
              {busy ? (step ?? t('proof.submitting')) : t('proof.submit')}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
