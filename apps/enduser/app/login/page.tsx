'use client';

/**
 * Renter login. Phone + PIN is the primary route (SPEC §3); OTP is the
 * fallback for a forgotten PIN — `POST /auth/otp/send {purpose:"login"}`
 * then `POST /auth/otp/verify {purpose:"login"}`, which sets `tms_r` itself.
 */

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { authApi } from '../../lib/api';
import { readNextParam, useMe, useNextParam } from '../../lib/auth';
import { countdown, displayPhone, errorMessage, isValidPhone, normalizePhone } from '../../lib/format';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';
import { PlatformTheme } from '../../components/OrgThemeSync';

type Mode = 'pin' | 'otp-send' | 'otp-verify';

export default function LoginPage() {
  const router = useRouter();
  const { setSession } = useMe();
  const nextPath = useNextParam();

  const [mode, setMode] = useState<Mode>('pin');
  const [phoneInput, setPhoneInput] = useState('');
  const [phone, setPhone] = useState('');
  const [pin, setPin] = useState('');
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [cooldown, setCooldown] = useState(0);

  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((s) => s - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  const finish = useCallback(
    (user: Parameters<typeof setSession>[0], org: Parameters<typeof setSession>[1]) => {
      setSession(user, org ?? null);
      router.replace(readNextParam() ?? '/');
    },
    [router, setSession],
  );

  async function submitPin(e: React.FormEvent) {
    e.preventDefault();
    const e164 = normalizePhone(phoneInput);
    if (!e164) {
      setError('Enter a Tanzanian mobile number, e.g. 0712 345 678.');
      return;
    }
    if (!pin) {
      setError('Enter your PIN.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.loginWithPin(e164, pin);
      finish(res.user, res.org ?? null);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const sendOtp = useCallback(async (e164: string) => {
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.otpSend(e164, 'login');
      setPhone(e164);
      setCooldown(res.resend_after_seconds ?? 60);
      setMode('otp-verify');
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }, []);

  function submitOtpSend(e: React.FormEvent) {
    e.preventDefault();
    const e164 = normalizePhone(phoneInput);
    if (!e164) {
      setError('Enter a Tanzanian mobile number, e.g. 0712 345 678.');
      return;
    }
    void sendOtp(e164);
  }

  async function submitOtpVerify(e: React.FormEvent) {
    e.preventDefault();
    if (!/^\d{6}$/.test(code)) {
      setError('Enter the 6-digit code from the SMS.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.otpVerifyLogin(phone, code);
      finish(res.user, res.org ?? null);
    } catch (err) {
      setError(errorMessage(err, 5));
    } finally {
      setBusy(false);
    }
  }

  const registerHref = nextPath
    ? `/register?next=${encodeURIComponent(nextPath)}`
    : '/register';

  return (
    <Screen>
      <PlatformTheme />
      <ScreenHeader
        eyebrow="Welcome back"
        title={mode === 'otp-verify' ? 'Enter the code' : 'Sign in'}
        lead={
          mode === 'pin'
            ? 'Your phone number and the PIN you chose.'
            : mode === 'otp-send'
              ? 'We will send a one-time code instead of asking for your PIN.'
              : `Sent to ${displayPhone(phone)}.`
        }
      />

      {error && <Notice tone="error">{error}</Notice>}

      {mode === 'pin' && (
        <form onSubmit={submitPin} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
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
          </div>
          <div className="field">
            <label htmlFor="pin">PIN</label>
            <input
              id="pin"
              className="input"
              type="password"
              inputMode="numeric"
              autoComplete="current-password"
              maxLength={6}
              value={pin}
              onChange={(e) => setPin(e.target.value.replace(/\D/g, '').slice(0, 6))}
            />
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            onClick={() => {
              setMode('otp-send');
              setError(null);
            }}
          >
            Use OTP instead
          </button>
          <Link className="btn btn-quiet" href={registerHref}>
            Create an account
          </Link>
        </form>
      )}

      {mode === 'otp-send' && (
        <form onSubmit={submitOtpSend} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className={`field${phoneInput && !isValidPhone(phoneInput) ? ' invalid' : ''}`}>
            <label htmlFor="otp-phone">Phone number</label>
            <input
              id="otp-phone"
              className="input"
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              autoFocus
              placeholder="0712 345 678"
              value={phoneInput}
              onChange={(e) => setPhoneInput(e.target.value)}
            />
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? 'Sending…' : 'Send code'}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            onClick={() => {
              setMode('pin');
              setError(null);
            }}
          >
            Use my PIN instead
          </button>
        </form>
      )}

      {mode === 'otp-verify' && (
        <form onSubmit={submitOtpVerify} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className="field">
            <label htmlFor="code">6-digit code</label>
            <input
              id="code"
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
            {busy ? 'Checking…' : 'Sign in'}
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
              setMode('pin');
              setCode('');
              setError(null);
            }}
          >
            Use my PIN instead
          </button>
        </form>
      )}
    </Screen>
  );
}
