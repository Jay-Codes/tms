'use client';

/**
 * KYC — FLOWS.md flow 2 step 4.
 *
 * Full name, NIDA number, next of kin (name + phone) and email go to
 * `PUT /me/profile`; the optional ID photo goes to MinIO through a presigned
 * PUT the backend issues (`POST /me/profile/kyc-upload` → the signed URL →
 * `.../complete`). The browser never touches a bucket directly and never sees
 * a stored NIDA: the API returns it masked, and leaving the field blank keeps
 * whatever is already on file.
 *
 * Used twice: standalone on the profile screen, and inline in the connect
 * flow when a renter with `kyc_status: "none"` reaches step 5.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Icon } from '@iconify/react';
import {
  ApiError,
  renterApi,
  uploadToPresignedUrl,
  type KycStatus,
  type RenterProfile,
} from '../../lib/api';
import { errorMessage, isValidPhone, normalizePhone } from '../../lib/format';
import { Notice } from '../../components/Screen';

/** API.md: `size_bytes(≤5MiB)`. */
const MAX_PHOTO_BYTES = 5 * 1024 * 1024;
const PHOTO_TYPES = ['image/jpeg', 'image/png'];
const NIDA_DIGITS = 20;

export function KycStatusStamp({ status }: { status: KycStatus }) {
  if (status === 'verified') {
    return (
      <span className="stamp stamp-paid" aria-label="KYC verified">
        Verified
      </span>
    );
  }
  if (status === 'submitted') {
    return (
      <span className="stamp" aria-label="KYC submitted">
        Submitted
      </span>
    );
  }
  return <span className="pencil">Not submitted</span>;
}

export interface KycFormProps {
  /**
   * `undefined` — fetch the profile here. Otherwise the caller has already
   * asked (`null` = the renter has no profile row yet) and no second
   * round-trip is made.
   */
  initialProfile?: RenterProfile | null;
  /** Prefilled from the session when the profile has no name yet. */
  fallbackName?: string;
  onSaved?: (profile: RenterProfile) => void;
  submitLabel?: string;
  heading?: string;
  lead?: string;
}

export function KycForm({
  initialProfile,
  fallbackName,
  onSaved,
  submitLabel = 'Save details',
  heading = 'Your details',
  lead,
}: KycFormProps) {
  const [profile, setProfile] = useState<RenterProfile | null>(initialProfile ?? null);
  const [loading, setLoading] = useState(initialProfile === undefined);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [fullName, setFullName] = useState('');
  const [nida, setNida] = useState('');
  const [kinName, setKinName] = useState('');
  const [kinPhone, setKinPhone] = useState('');
  const [email, setEmail] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);

  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const adopt = useCallback(
    (p: RenterProfile, fallback?: string) => {
      setProfile(p);
      setFullName(p.full_name || fallback || '');
      setKinName(p.next_of_kin_name || '');
      setKinPhone(p.next_of_kin_phone || '');
      setEmail(p.email || '');
      setNida('');
    },
    [],
  );

  /* Load the profile unless the caller already asked on our behalf. */
  useEffect(() => {
    if (initialProfile !== undefined) {
      if (initialProfile) adopt(initialProfile, fallbackName);
      else setFullName((n) => n || fallbackName || '');
      setLoading(false);
      return;
    }
    const ac = new AbortController();
    let live = true;
    (async () => {
      try {
        const res = await renterApi.profile(ac.signal);
        if (!live) return;
        adopt(res.profile, fallbackName ?? res.user?.full_name);
        setLoadError(null);
      } catch (err) {
        if (!live || (err instanceof DOMException && err.name === 'AbortError')) return;
        setLoadError(errorMessage(err));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [initialProfile, fallbackName, adopt]);

  const status: KycStatus = profile?.kyc_status ?? 'none';

  const nidaHint = useMemo(() => {
    if (profile?.nida_masked) return `On file: ${profile.nida_masked}. Leave blank to keep it.`;
    return `${NIDA_DIGITS} digits, from your national ID card.`;
  }, [profile?.nida_masked]);

  function pickFile(chosen: File | null) {
    setFileError(null);
    if (!chosen) {
      setFile(null);
      return;
    }
    if (!PHOTO_TYPES.includes(chosen.type)) {
      setFile(null);
      setFileError('Choose a JPEG or PNG photo.');
      return;
    }
    if (chosen.size > MAX_PHOTO_BYTES) {
      setFile(null);
      setFileError('That photo is larger than 5 MB. Choose a smaller one.');
      return;
    }
    setFile(chosen);
  }

  /** UX-side checks only — the API validates all of this again. */
  function validate(): Record<string, string> {
    const errs: Record<string, string> = {};
    if (fullName.trim().length < 2) errs.full_name = 'Enter your full name.';
    if (nida && !new RegExp(`^\\d{${NIDA_DIGITS}}$`).test(nida)) {
      errs.nida_number = `A NIDA number is ${NIDA_DIGITS} digits.`;
    }
    if (!nida && !profile?.nida_masked) errs.nida_number = 'Enter your NIDA number.';
    if (kinName.trim().length < 2) errs.next_of_kin_name = "Enter your next of kin's name.";
    if (!isValidPhone(kinPhone)) {
      errs.next_of_kin_phone = 'Enter a Tanzanian mobile number, e.g. 0712 345 678.';
    }
    if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) {
      errs.email = 'Check the email address.';
    }
    return errs;
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const errs = validate();
    setFieldErrors(errs);
    setError(null);
    setSaved(false);
    if (Object.keys(errs).length > 0) return;

    setBusy(true);
    try {
      setStep('Saving your details…');
      const res = await renterApi.saveProfile({
        full_name: fullName.trim(),
        ...(nida ? { nida_number: nida } : {}),
        next_of_kin_name: kinName.trim(),
        next_of_kin_phone: normalizePhone(kinPhone) ?? kinPhone.trim(),
        ...(email.trim() ? { email: email.trim() } : {}),
      });
      let latest = res.profile;

      if (file) {
        setStep('Uploading your ID photo…');
        const ticket = await renterApi.kycUploadTicket(file.type, file.size);
        await uploadToPresignedUrl(ticket, file);
        const done = await renterApi.kycUploadComplete(ticket.object_key);
        latest = done.profile;
        setFile(null);
        if (fileInput.current) fileInput.current.value = '';
      }

      adopt(latest, fallbackName);
      setSaved(true);
      onSaved?.(latest);
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.errors).length > 0) {
        setFieldErrors(err.errors);
      }
      setError(errorMessage(err));
    } finally {
      setBusy(false);
      setStep(null);
    }
  }

  if (loading) {
    return (
      <section className="sheet" style={{ padding: 'var(--sp-4)' }}>
        <p className="pencil">Loading your details…</p>
      </section>
    );
  }

  return (
    <section
      className="sheet"
      style={{ padding: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}
      aria-labelledby="kyc-heading"
    >
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--sp-3)',
        }}
      >
        <h2 id="kyc-heading" style={{ fontSize: 'var(--text-lg)' }}>
          {heading}
        </h2>
        <KycStatusStamp status={status} />
      </header>

      {lead && (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', margin: 0 }}>{lead}</p>
      )}

      {loadError && <Notice tone="error">{loadError}</Notice>}
      {error && <Notice tone="error">{error}</Notice>}
      {saved && !error && <Notice>Details saved.</Notice>}

      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <div className={`field${fieldErrors.full_name ? ' invalid' : ''}`}>
          <label htmlFor="kyc-name">Full name</label>
          <input
            id="kyc-name"
            className="input"
            autoComplete="name"
            value={fullName}
            onChange={(e) => setFullName(e.target.value)}
          />
          {fieldErrors.full_name && <span className="error">{fieldErrors.full_name}</span>}
        </div>

        <div className={`field${fieldErrors.nida_number ? ' invalid' : ''}`}>
          <label htmlFor="kyc-nida">NIDA number</label>
          <input
            id="kyc-nida"
            className="input"
            inputMode="numeric"
            autoComplete="off"
            maxLength={NIDA_DIGITS}
            placeholder={profile?.nida_masked ?? '00000000000000000000'}
            value={nida}
            onChange={(e) => setNida(e.target.value.replace(/\D/g, '').slice(0, NIDA_DIGITS))}
            style={{ fontVariantNumeric: 'tabular-nums lining-nums', letterSpacing: '0.05em' }}
          />
          <span className="hint">{nidaHint}</span>
          {fieldErrors.nida_number && <span className="error">{fieldErrors.nida_number}</span>}
        </div>

        <div className={`field${fieldErrors.next_of_kin_name ? ' invalid' : ''}`}>
          <label htmlFor="kyc-kin">Next of kin</label>
          <input
            id="kyc-kin"
            className="input"
            value={kinName}
            onChange={(e) => setKinName(e.target.value)}
          />
          {fieldErrors.next_of_kin_name && (
            <span className="error">{fieldErrors.next_of_kin_name}</span>
          )}
        </div>

        <div className={`field${fieldErrors.next_of_kin_phone ? ' invalid' : ''}`}>
          <label htmlFor="kyc-kin-phone">Next of kin phone</label>
          <input
            id="kyc-kin-phone"
            className="input"
            type="tel"
            inputMode="tel"
            placeholder="0712 345 678"
            value={kinPhone}
            onChange={(e) => setKinPhone(e.target.value)}
          />
          {fieldErrors.next_of_kin_phone && (
            <span className="error">{fieldErrors.next_of_kin_phone}</span>
          )}
        </div>

        <div className={`field${fieldErrors.email ? ' invalid' : ''}`}>
          <label htmlFor="kyc-email">Email (optional)</label>
          <input
            id="kyc-email"
            className="input"
            type="email"
            inputMode="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          {fieldErrors.email && <span className="error">{fieldErrors.email}</span>}
        </div>

        <div className={`field${fileError ? ' invalid' : ''}`}>
          <label htmlFor="kyc-photo">ID photo (optional)</label>
          <input
            id="kyc-photo"
            ref={fileInput}
            className="input"
            type="file"
            accept="image/*"
            onChange={(e) => pickFile(e.target.files?.[0] ?? null)}
          />
          <span className="hint">
            {profile?.kyc_doc_uploaded && !file
              ? 'A photo is already on file. Choosing another replaces it.'
              : 'A clear photo of your national ID. JPEG or PNG, up to 5 MB.'}
          </span>
          {fileError && <span className="error">{fileError}</span>}
        </div>

        <button className="btn btn-primary" type="submit" disabled={busy}>
          {busy ? (step ?? 'Saving…') : submitLabel}
        </button>

        <p
          style={{
            display: 'flex',
            gap: 'var(--sp-2)',
            alignItems: 'flex-start',
            margin: 0,
            color: 'var(--ink-soft)',
            fontSize: 'var(--text-xs)',
          }}
        >
          <Icon icon="solar:lock-keyhole-minimalistic-linear" width={16} aria-hidden />
          Your NIDA number is stored encrypted and only ever shown to you and your landlord in
          masked form.
        </p>
      </form>
    </section>
  );
}
