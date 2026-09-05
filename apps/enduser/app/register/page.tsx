'use client';

/**
 * Renter registration — FLOWS.md flow 2, steps 1–3.
 *
 *   1. phone            → POST /auth/otp/send    {purpose:"register"}
 *   2. 6-digit OTP      → POST /auth/otp/verify  {purpose:"register"} → otp_token
 *   3. name + PIN       → POST /auth/register/renter → session cookie
 *
 * `otp_token` lives in component state only (10-minute Redis token; never
 * persisted). Validation here is UX — the API validates again.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { ApiError, authApi } from '../../lib/api';
import { readNextParam, useMe, useNextParam } from '../../lib/auth';
import { countdown, displayPhone, errorMessage, isValidPhone, isValidPin, normalizePhone } from '../../lib/format';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';
import { PlatformTheme } from '../../components/OrgThemeSync';

type Step = 'phone' | 'otp' | 'pin';

export default function RegisterPage() {
  const router = useRouter();
  const { setSession } = useMe();
  const nextPath = useNextParam();

  const [step, setStep] = useState<Step>('phone');
  const [phoneInput, setPhoneInput] = useState('');
  const [phone, setPhone] = useState(''); // normalized E.164
  const [code, setCode] = useState('');
  const [otpToken, setOtpToken] = useState('');
  const [fullName, setFullName] = useState('');
  const [pin, setPin] = useState('');
  const [pinConfirm, setPinConfirm] = useState('');

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [phoneTaken, setPhoneTaken] = useState(false);
  const [cooldown, setCooldown] = useState(0);

  /* resend cooldown ticker */
  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((s) => s - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  const otpRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (step === 'otp') otpRef.current?.focus();
  }, [step]);

  const sendOtp = useCallback(
    async (e164: string) => {
      setBusy(true);
      setError(null);
      setPhoneTaken(false);
      try {
        const res = await authApi.otpSend(e164, 'register');
        setPhone(e164);
        setCooldown(res.resend_after_seconds ?? 60);
        setStep('otp');
      } catch (err) {
        if (err instanceof ApiError && err.status === 409) setPhoneTaken(true);
        setError(errorMessage(err));
      } finally {
        setBusy(false);
      }
    },
    [],
  );

  function submitPhone(e: React.FormEvent) {
    e.preventDefault();
    const e164 = normalizePhone(phoneInput);
    if (!e164) {
      setError('Enter a Tanzanian mobile number, e.g. 0712 345 678.');
      return;
    }
    void sendOtp(e164);
  }

  async function submitOtp(e: React.FormEvent) {
    e.preventDefault();
    if (!/^\d{6}$/.test(code)) {
      setError('Enter the 6-digit code from the SMS.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.otpVerifyRegister(phone, code);
      setOtpToken(res.otp_token);
      setStep('pin');
    } catch (err) {
      setError(errorMessage(err, 5));
    } finally {
      setBusy(false);
    }
  }

  async function submitPin(e: React.FormEvent) {
    e.preventDefault();
    if (!fullName.trim()) {
      setError('Enter your full name.');
      return;
    }
    if (!isValidPin(pin)) {
      setError('Your PIN must be 4 to 6 digits.');
      return;
    }
    if (pin !== pinConfirm) {
      setError('The two PINs do not match.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.registerRenter({
        phone,
        otp_token: otpToken,
        pin,
        full_name: fullName.trim(),
      });
      setSession(res.user, res.org ?? null);
      router.replace(readNextParam() ?? '/');
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) setPhoneTaken(true);
      if (err instanceof ApiError && err.status === 400 && err.fieldError('otp_token')) {
        // Token expired — send them back to re-verify rather than dead-end.
        setStep('otp');
        setCode('');
      }
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const loginHref = nextPath ? `/login?next=${encodeURIComponent(nextPath)}` : '/login';

  return (
    <Screen>
      <PlatformTheme />
      <ScreenHeader
        eyebrow={`Step ${step === 'phone' ? 1 : step === 'otp' ? 2 : 3} of 3`}
        title={
          step === 'phone'
            ? 'Create your account'
            : step === 'otp'
              ? 'Enter the code'
              : 'Your name and PIN'
        }
        lead={
          step === 'phone'
            ? 'We send a one-time code to your phone. No app store, no email needed.'
            : step === 'otp'
              ? `Sent to ${displayPhone(phone)}.`
              : 'The PIN is how you sign in from now on.'
        }
      />

      {error && <Notice tone="error">{error}</Notice>}

      {phoneTaken && (
        <p style={{ margin: 0 }}>
          <Link className="btn btn-secondary" href={loginHref}>
            Log in instead
          </Link>
        </p>
      )}

      {step === 'phone' && (
        <form onSubmit={submitPhone} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className={`field${phoneInput && !isValidPhone(phoneInput) ? ' invalid' : ''}`}>
            <label htmlFor="phone">Phone number</label>
            <input
              id="phone"
              className="input"
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              autoFocus
              placeholder="0712 345 678"
              value={phoneInput}
              onChange={(e) => setPhoneInput(e.target.value)}
            />
            <span className="hint">07…, 255… or +255… all work.</span>
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? 'Sending…' : 'Send code'}
          </button>
          <Link className="btn btn-quiet" href={loginHref}>
            I already have an account
          </Link>
        </form>
      )}

      {step === 'otp' && (
        <form onSubmit={submitOtp} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className="field">
            <label htmlFor="code">6-digit code</label>
            <input
              id="code"
              ref={otpRef}
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
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
            {busy ? 'Checking…' : 'Verify'}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            disabled={busy || cooldown > 0}
            onClick={() => void sendOtp(phone)}
          >
            {cooldown > 0 ? `Resend code in ${countdown(cooldown)}` : 'Resend code'}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            onClick={() => {
              setStep('phone');
              setCode('');
              setError(null);
            }}
          >
            Change number
          </button>
        </form>
      )}

      {step === 'pin' && (
        <form onSubmit={submitPin} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className="field">
            <label htmlFor="full_name">Full name</label>
            <input
              id="full_name"
              className="input"
              type="text"
              autoComplete="name"
              autoFocus
              placeholder="Asha Mwakalinga"
              value={fullName}
              onChange={(e) => setFullName(e.target.value)}
            />
          </div>
          <div className={`field${pin && !isValidPin(pin) ? ' invalid' : ''}`}>
            <label htmlFor="pin">Choose a PIN</label>
            <input
              id="pin"
              className="input"
              type="password"
              inputMode="numeric"
              autoComplete="new-password"
              maxLength={6}
              value={pin}
              onChange={(e) => setPin(e.target.value.replace(/\D/g, '').slice(0, 6))}
            />
            <span className="hint">4 to 6 digits.</span>
          </div>
          <div className={`field${pinConfirm && pin !== pinConfirm ? ' invalid' : ''}`}>
            <label htmlFor="pin_confirm">Confirm PIN</label>
            <input
              id="pin_confirm"
              className="input"
              type="password"
              inputMode="numeric"
              autoComplete="new-password"
              maxLength={6}
              value={pinConfirm}
              onChange={(e) => setPinConfirm(e.target.value.replace(/\D/g, '').slice(0, 6))}
            />
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? 'Creating account…' : 'Create account'}
          </button>
        </form>
      )}
    </Screen>
  );
}
