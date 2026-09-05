'use client';

/**
 * Renter login. Phone + PIN is the primary route (SPEC §3); OTP is the
 * fallback for a forgotten PIN — `POST /auth/otp/send {purpose:"login"}`
 * then `POST /auth/otp/verify {purpose:"login"}`, which sets `tms_r` itself.
 */

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useT } from '@tms/ui';
import { authApi } from '../../lib/api';
import { readNextParam, useMe, useNextParam } from '../../lib/auth';
import { countdown, displayPhone, errorMessage, isValidPhone, normalizePhone } from '../../lib/format';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';
import { PlatformTheme } from '../../components/OrgThemeSync';
import { LanguageToggle } from '../../components/LanguageToggle';

type Mode = 'pin' | 'otp-send' | 'otp-verify';

export default function LoginPage() {
  const t = useT();
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
      setError(t('error.phone'));
      return;
    }
    if (!pin) {
      setError(t('error.pinRequired'));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.loginWithPin(e164, pin);
      finish(res.user, res.org ?? null);
    } catch (err) {
      setError(errorMessage(t, err));
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
      setError(errorMessage(t, err));
    } finally {
      setBusy(false);
    }
  }, [t]);

  function submitOtpSend(e: React.FormEvent) {
    e.preventDefault();
    const e164 = normalizePhone(phoneInput);
    if (!e164) {
      setError(t('error.phone'));
      return;
    }
    void sendOtp(e164);
  }

  async function submitOtpVerify(e: React.FormEvent) {
    e.preventDefault();
    if (!/^\d{6}$/.test(code)) {
      setError(t('error.code'));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.otpVerifyLogin(phone, code);
      finish(res.user, res.org ?? null);
    } catch (err) {
      setError(errorMessage(t, err, 5));
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
        eyebrow={t('login.eyebrow')}
        title={mode === 'otp-verify' ? t('otp.title') : t('login.title')}
        lead={
          mode === 'pin'
            ? t('login.lead.pin')
            : mode === 'otp-send'
              ? t('login.lead.otp')
              : t('otp.sentTo', { phone: displayPhone(phone) })
        }
      />

      {error && <Notice tone="error">{error}</Notice>}

      {mode === 'pin' && (
        <form onSubmit={submitPin} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className={`field${phoneInput && !isValidPhone(phoneInput) ? ' invalid' : ''}`}>
            <label htmlFor="phone">{t('field.phone')}</label>
            <input
              id="phone"
              className="input"
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              autoFocus
              placeholder={t('field.phonePlaceholder')}
              value={phoneInput}
              onChange={(e) => setPhoneInput(e.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor="pin">{t('field.pin')}</label>
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
            {busy ? t('login.submitting') : t('login.submit')}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            onClick={() => {
              setMode('otp-send');
              setError(null);
            }}
          >
            {t('login.useOtp')}
          </button>
          <Link className="btn btn-quiet" href={registerHref}>
            {t('login.createAccount')}
          </Link>
        </form>
      )}

      {mode === 'otp-send' && (
        <form onSubmit={submitOtpSend} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className={`field${phoneInput && !isValidPhone(phoneInput) ? ' invalid' : ''}`}>
            <label htmlFor="otp-phone">{t('field.phone')}</label>
            <input
              id="otp-phone"
              className="input"
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              autoFocus
              placeholder={t('field.phonePlaceholder')}
              value={phoneInput}
              onChange={(e) => setPhoneInput(e.target.value)}
            />
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? t('common.sending') : t('login.sendCode')}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            onClick={() => {
              setMode('pin');
              setError(null);
            }}
          >
            {t('login.usePin')}
          </button>
        </form>
      )}

      {mode === 'otp-verify' && (
        <form onSubmit={submitOtpVerify} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className="field">
            <label htmlFor="code">{t('field.code')}</label>
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
            {busy ? t('common.checking') : t('login.submit')}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            disabled={busy || cooldown > 0}
            onClick={() => void sendOtp(phone)}
          >
            {cooldown > 0 ? t('otp.resendIn', { time: countdown(cooldown) }) : t('otp.resend')}
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
            {t('login.usePin')}
          </button>
        </form>
      )}

      <footer style={{ marginTop: 'auto', paddingTop: 'var(--sp-5)' }}>
        <LanguageToggle />
      </footer>
    </Screen>
  );
}
