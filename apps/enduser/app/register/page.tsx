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
import { useLocale, useT } from '@tms/ui';
import { ApiError, authApi } from '../../lib/api';
import { readNextParam, useMe, useNextParam } from '../../lib/auth';
import { countdown, displayPhone, errorMessage, isValidPhone, isValidPin, normalizePhone } from '../../lib/format';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';
import { PlatformTheme } from '../../components/OrgThemeSync';
import { LanguageToggle } from '../../components/LanguageToggle';

type Step = 'phone' | 'otp' | 'pin';

export default function RegisterPage() {
  const t = useT();
  /* Whatever the renter (or their landlord's page) chose is what the account
     is created with — `POST /auth/register/renter {locale}`. */
  const locale = useLocale();
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
        setError(errorMessage(t, err));
      } finally {
        setBusy(false);
      }
    },
    [t],
  );

  function submitPhone(e: React.FormEvent) {
    e.preventDefault();
    const e164 = normalizePhone(phoneInput);
    if (!e164) {
      setError(t('error.phone'));
      return;
    }
    void sendOtp(e164);
  }

  async function submitOtp(e: React.FormEvent) {
    e.preventDefault();
    if (!/^\d{6}$/.test(code)) {
      setError(t('error.code'));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await authApi.otpVerifyRegister(phone, code);
      setOtpToken(res.otp_token);
      setStep('pin');
    } catch (err) {
      setError(errorMessage(t, err, 5));
    } finally {
      setBusy(false);
    }
  }

  async function submitPin(e: React.FormEvent) {
    e.preventDefault();
    if (!fullName.trim()) {
      setError(t('error.nameRequired'));
      return;
    }
    if (!isValidPin(pin)) {
      setError(t('error.pinLength'));
      return;
    }
    if (pin !== pinConfirm) {
      setError(t('error.pinMismatch'));
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
        locale,
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
      setError(errorMessage(t, err));
    } finally {
      setBusy(false);
    }
  }

  const loginHref = nextPath ? `/login?next=${encodeURIComponent(nextPath)}` : '/login';

  return (
    <Screen>
      <PlatformTheme />
      <ScreenHeader
        eyebrow={t('register.step', { n: step === 'phone' ? 1 : step === 'otp' ? 2 : 3 })}
        title={
          step === 'phone'
            ? t('register.title.phone')
            : step === 'otp'
              ? t('otp.title')
              : t('register.title.pin')
        }
        lead={
          step === 'phone'
            ? t('register.lead.phone')
            : step === 'otp'
              ? t('otp.sentTo', { phone: displayPhone(phone) })
              : t('register.lead.pin')
        }
      />

      {error && <Notice tone="error">{error}</Notice>}

      {phoneTaken && (
        <p style={{ margin: 0 }}>
          <Link className="btn btn-secondary" href={loginHref}>
            {t('register.loginInstead')}
          </Link>
        </p>
      )}

      {step === 'phone' && (
        <form onSubmit={submitPhone} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
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
            <span className="hint">{t('field.phoneFormats')}</span>
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? t('common.sending') : t('login.sendCode')}
          </button>
          <Link className="btn btn-quiet" href={loginHref}>
            {t('register.haveAccount')}
          </Link>
        </form>
      )}

      {step === 'otp' && (
        <form onSubmit={submitOtp} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className="field">
            <label htmlFor="code">{t('field.code')}</label>
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
            {busy ? t('common.checking') : t('register.verify')}
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
              setStep('phone');
              setCode('');
              setError(null);
            }}
          >
            {t('register.changeNumber')}
          </button>
        </form>
      )}

      {step === 'pin' && (
        <form onSubmit={submitPin} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
          <div className="field">
            <label htmlFor="full_name">{t('field.fullName')}</label>
            <input
              id="full_name"
              className="input"
              type="text"
              autoComplete="name"
              autoFocus
              placeholder={t('field.namePlaceholder')}
              value={fullName}
              onChange={(e) => setFullName(e.target.value)}
            />
          </div>
          <div className={`field${pin && !isValidPin(pin) ? ' invalid' : ''}`}>
            <label htmlFor="pin">{t('field.choosePin')}</label>
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
            <span className="hint">{t('field.pinHint')}</span>
          </div>
          <div className={`field${pinConfirm && pin !== pinConfirm ? ' invalid' : ''}`}>
            <label htmlFor="pin_confirm">{t('field.confirmPin')}</label>
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
            {busy ? t('register.submitting') : t('register.submit')}
          </button>
        </form>
      )}

      <footer style={{ marginTop: 'auto', paddingTop: 'var(--sp-5)' }}>
        <LanguageToggle />
      </footer>
    </Screen>
  );
}
