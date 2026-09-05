'use client';

/**
 * Accept & sign — FLOWS.md flow 2 step 7b, SPEC §5.5 "Digital signing (MVP)".
 *
 * Three steps: read the summary and ask for a code, type the 6-digit code,
 * then optionally draw a signature. The OTP is only verified by
 * `POST /contracts/{id}/sign`, so a wrong code surfaces at the end and drops
 * the renter back on the code step.
 *
 * The drawn signature is uploaded before signing: presigned ticket → PUT the
 * PNG → `sign` with the returned `signature_object_key`. Skipping the drawing
 * signs with the OTP alone (method `otp_accept`), which is a complete
 * signature in its own right.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { Icon } from '@iconify/react';
import {
  ApiError,
  contractApi,
  hasRenterSignature,
  uploadToPresignedUrl,
  type Contract,
  type ContractDocument,
} from '../../../../lib/api';
import { useMe } from '../../../../lib/auth';
import {
  countdown,
  displayPhone,
  errorMessage,
  formatDate,
  money,
} from '../../../../lib/format';
import { Protected } from '../../../../components/Protected';
import { RentValue } from '../../../../components/RentValue';
import { Notice, Screen, ScreenHeader } from '../../../../components/Screen';
import { SignaturePad, type SignaturePadHandle } from '../../../../components/SignaturePad';

type Step = 'summary' | 'code' | 'draw' | 'done';

/** Bucket limit from API.md — a canvas PNG is far smaller, but check anyway. */
const MAX_SIGNATURE_BYTES = 512 * 1024;

function SignContent() {
  const params = useParams<{ id: string }>();
  const id = typeof params?.id === 'string' ? params.id : '';
  const router = useRouter();
  const { user } = useMe();

  const [contract, setContract] = useState<Contract | null>(null);
  const [doc, setDoc] = useState<ContractDocument | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [step, setStep] = useState<Step>('summary');
  const [code, setCode] = useState('');
  const [cooldown, setCooldown] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [alreadySigned, setAlreadySigned] = useState(false);
  const [padEmpty, setPadEmpty] = useState(true);

  const padRef = useRef<SignaturePadHandle | null>(null);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    let live = true;
    (async () => {
      try {
        const [c, d] = await Promise.all([
          contractApi.get(id, ac.signal),
          contractApi.document(id, ac.signal).catch(() => null),
        ]);
        if (!live) return;
        setContract(c.contract);
        setDoc(d);
        // Already signed, or no longer signable — send them back to read it.
        if (c.contract.status !== 'pending_signature' || hasRenterSignature(c.contract)) {
          setAlreadySigned(true);
        }
        setLoadError(null);
      } catch (err) {
        if (!live || (err instanceof DOMException && err.name === 'AbortError')) return;
        setLoadError(
          err instanceof ApiError && err.status === 404
            ? 'This agreement is not available on your account.'
            : errorMessage(err),
        );
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [id]);

  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((s) => s - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  const sendOtp = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await contractApi.sendSignOtp(id);
      setCooldown(res?.resend_after_seconds ?? 60);
      setStep('code');
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setAlreadySigned(true);
      } else {
        setError(errorMessage(err, 5));
      }
    } finally {
      setBusy(false);
    }
  }, [id]);

  /** Upload the drawn PNG, if there is one, and return its object key. */
  const uploadSignature = useCallback(async (): Promise<string | undefined> => {
    const blob = await padRef.current?.toBlob();
    if (!blob) return undefined;
    if (blob.size > MAX_SIGNATURE_BYTES) {
      throw new ApiError(400, {
        title: 'Signature too large',
        detail: 'That signature is too big to upload. Clear it and try a simpler one.',
      });
    }
    const ticket = await contractApi.signatureUploadTicket(id, blob.size);
    await uploadToPresignedUrl(ticket, blob);
    return ticket.object_key;
  }, [id]);

  const submit = useCallback(
    async (withDrawing: boolean) => {
      if (!/^\d{6}$/.test(code)) {
        setError('Enter the 6-digit code from the SMS.');
        setStep('code');
        return;
      }
      setBusy(true);
      setError(null);
      try {
        const key = withDrawing ? await uploadSignature() : undefined;
        const res = await contractApi.sign(id, {
          otp_code: code,
          ...(key ? { signature_object_key: key } : {}),
        });
        setContract(res.contract);
        setStep('done');
      } catch (err) {
        if (err instanceof ApiError && err.status === 400) {
          setError(
            err.fieldError('otp_code') ??
              (err.detail || 'That code is not right. Check the SMS and try again.'),
          );
          setStep('code');
          setCode('');
        } else if (err instanceof ApiError && err.status === 409) {
          setAlreadySigned(true);
        } else if (err instanceof ApiError && err.status === 429) {
          setError(errorMessage(err, 5));
          setStep('code');
        } else {
          setError(errorMessage(err));
        }
      } finally {
        setBusy(false);
      }
    },
    [code, id, uploadSignature],
  );

  const docHref = `/contract/${encodeURIComponent(id)}`;
  const orgName = doc?.org.display_name ?? 'your landlord';

  if (loading) {
    return (
      <Screen bottomBar>
        <p className="pencil">Loading…</p>
      </Screen>
    );
  }

  if (loadError) {
    return (
      <Screen bottomBar>
        <Notice tone="error">{loadError}</Notice>
        <Link className="btn btn-secondary" href="/contract">
          Back to your contracts
        </Link>
      </Screen>
    );
  }

  if (alreadySigned && step !== 'done') {
    return (
      <Screen bottomBar>
        <ScreenHeader
          eyebrow="Already signed"
          title="Nothing left to sign"
          lead={`This agreement no longer needs your signature. ${orgName} countersigns next.`}
        />
        <Link className="btn btn-primary" href={docHref}>
          Open the agreement
        </Link>
      </Screen>
    );
  }

  if (step === 'done') {
    return (
      <Screen bottomBar>
        <ScreenHeader
          eyebrow="Signed"
          title="Signed."
          lead={`Waiting for ${orgName} to countersign. You will get an SMS when your tenancy starts.`}
        />
        <hr className="rule rule-strong" />
        <p
          style={{
            display: 'flex',
            gap: 'var(--sp-2)',
            alignItems: 'center',
            color: 'var(--ink-soft)',
            fontSize: 'var(--text-sm)',
          }}
        >
          <Icon icon="solar:check-circle-linear" width={20} aria-hidden />
          Your signature is on the document, with the date and your phone number.
        </p>
        <Link className="btn btn-primary" href={docHref}>
          Back to the agreement
        </Link>
      </Screen>
    );
  }

  return (
    <Screen bottomBar>
      <header className="no-print" style={{ display: 'grid', gap: 'var(--sp-2)' }}>
        <button
          type="button"
          className="btn btn-quiet"
          style={{ justifySelf: 'start', paddingInline: 0 }}
          onClick={() => (step === 'summary' ? router.push(docHref) : setStep('summary'))}
        >
          <Icon icon="solar:alt-arrow-left-linear" width={18} aria-hidden />
          {step === 'summary' ? 'Back to the agreement' : 'Back'}
        </button>
        <h1 style={{ fontSize: 'var(--text-xl)' }}>
          {step === 'summary'
            ? 'Accept & sign'
            : step === 'code'
              ? 'Enter the code'
              : 'Draw your signature'}
        </h1>
      </header>

      {error && <Notice tone="error">{error}</Notice>}

      {/* ---- step 1: what you are agreeing to ---- */}
      {step === 'summary' && (
        <>
          <p style={{ color: 'var(--ink-soft)' }}>
            Signing means you accept the agreement as it is written. We will send a one-time code
            to {user ? displayPhone(user.phone) : 'your phone'}.
          </p>
          {contract && (
            <table className="ledger ledger-kv">
              <tbody>
                <tr>
                  <td>Unit</td>
                  <td className="num">
                    {contract.unit.name} · {contract.unit.property_name}
                  </td>
                </tr>
                <tr>
                  <td>Rent</td>
                  <td className="num">
                    <RentValue contract={contract} />
                  </td>
                </tr>
                <tr>
                  <td>Paid</td>
                  <td className="num">{contract.payment_period.label}</td>
                </tr>
                <tr>
                  <td>Starts</td>
                  <td className="num">{formatDate(contract.start_date)}</td>
                </tr>
                <tr>
                  <td>Ends</td>
                  <td className="num">{formatDate(contract.end_date)}</td>
                </tr>
                {contract.schedules_summary && (
                  <tr className="total">
                    <td>Total over the term</td>
                    <td className="num amount">{money(contract.schedules_summary.total)}</td>
                  </tr>
                )}
              </tbody>
            </table>
          )}
          <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void sendOtp()}
            >
              {busy ? 'Sending…' : 'Send me the code'}
            </button>
            <Link className="btn btn-quiet" href={docHref}>
              Read the agreement again
            </Link>
          </div>
        </>
      )}

      {/* ---- step 2: the code ---- */}
      {step === 'code' && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!/^\d{6}$/.test(code)) {
              setError('Enter the 6-digit code from the SMS.');
              return;
            }
            setError(null);
            setStep('draw');
          }}
          style={{ display: 'grid', gap: 'var(--sp-4)' }}
          noValidate
        >
          <p style={{ color: 'var(--ink-soft)' }}>
            Sent to {user ? displayPhone(user.phone) : 'your phone'}.
          </p>
          <div className="field">
            <label htmlFor="sign-code">6-digit code</label>
            <input
              id="sign-code"
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              autoFocus
              placeholder="••••••"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
              style={{
                fontSize: 'var(--text-xl)',
                letterSpacing: '0.4em',
                fontVariantNumeric: 'tabular-nums lining-nums',
              }}
            />
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy || code.length < 6}>
            Continue
          </button>
          <button
            type="button"
            className="btn btn-quiet"
            disabled={busy || cooldown > 0}
            onClick={() => void sendOtp()}
          >
            {cooldown > 0 ? `Resend code in ${countdown(cooldown)}` : 'Resend code'}
          </button>
        </form>
      )}

      {/* ---- step 3: optional drawn signature ---- */}
      {step === 'draw' && (
        <>
          <p style={{ color: 'var(--ink-soft)' }}>
            Add a handwritten signature if you want one on the document. It is optional — the code
            you entered is already your signature.
          </p>
          <SignaturePad ref={padRef} onChange={setPadEmpty} />
          <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy || padEmpty}
              onClick={() => void submit(true)}
            >
              {busy ? 'Signing…' : 'Sign with this signature'}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy}
              onClick={() => void submit(false)}
            >
              {busy ? 'Signing…' : 'Skip — sign with the code only'}
            </button>
          </div>
        </>
      )}
    </Screen>
  );
}

export default function SignContractPage() {
  return (
    <Protected>
      <SignContent />
    </Protected>
  );
}
