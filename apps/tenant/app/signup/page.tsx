'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { useT, type Translator } from '@tms/ui';
import { AuthCard } from '../../components/AuthCard';
import { LanguageToggle } from '../../components/LanguageToggle';
import { useLocaleState } from '../../lib/locale';
import { Field, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

/** Client-side checks are UX only — the backend is authoritative. */
function localErrors(
  t: Translator,
  f: { org_name: string; owner_name: string; email: string; phone: string; password: string; confirm: string },
) {
  const e: Record<string, string> = {};
  if (!f.org_name.trim()) e.org_name = t('auth.err.org_name');
  if (!f.owner_name.trim()) e.owner_name = t('auth.err.owner_name');
  if (!f.email.trim()) e.email = t('auth.err.email');
  if (!f.phone.trim()) e.phone = t('auth.err.phone');
  if (f.password.length < 8) e.password = t('auth.err.password_short');
  if (f.confirm !== f.password) e.confirm = t('auth.err.password_mismatch');
  return e;
}

export default function SignupPage() {
  const router = useRouter();
  const { refresh } = useMe();
  const t = useT();
  const { locale } = useLocaleState();
  const [form, setForm] = useState({
    org_name: '',
    owner_name: '',
    email: '',
    phone: '',
    password: '',
    confirm: '',
  });
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const local = localErrors(t, form);
    setFieldErrors(local);
    if (Object.keys(local).length > 0) return;

    setBusy(true);
    setError(null);
    try {
      await authApi.signupOrg({
        org_name: form.org_name.trim(),
        owner_name: form.owner_name.trim(),
        email: form.email.trim(),
        phone: form.phone.trim(),
        password: form.password,
        // The toggle in the footer is the new owner's first language choice;
        // it becomes their account locale so their own SMS arrive in it.
        locale,
      });
      await refresh();
      router.replace('/setup');
    } catch (err) {
      const ae = err instanceof ApiError ? err : new ApiError(0, { detail: String(err) });
      setError(ae);
      setFieldErrors(ae.errors);
      setBusy(false);
    }
  };

  return (
    <AuthCard
      title={t('auth.signup.title')}
      lead={t('auth.signup.lead')}
      footer={
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 'var(--sp-4)',
            flexWrap: 'wrap',
          }}
        >
          <span>
            {t('auth.signup.registered')} <Link href="/login">{t('auth.signup.sign_in')}</Link>.
          </span>
          <LanguageToggle />
        </div>
      }
    >
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <ProblemNote error={error} />
        <Field
          id="org_name"
          label={t('auth.signup.org_name')}
          hint={t('auth.signup.org_name_hint')}
          error={fieldErrors.org_name}
        >
          <input id="org_name" className="input" value={form.org_name} onChange={set('org_name')} autoComplete="organization" />
        </Field>
        <Field id="owner_name" label={t('auth.signup.owner_name')} error={fieldErrors.owner_name}>
          <input id="owner_name" className="input" value={form.owner_name} onChange={set('owner_name')} autoComplete="name" />
        </Field>
        <Field
          id="email"
          label={t('auth.signup.email')}
          hint={t('auth.signup.email_hint')}
          error={fieldErrors.email}
        >
          <input id="email" className="input" type="email" value={form.email} onChange={set('email')} autoComplete="email" />
        </Field>
        <Field
          id="phone"
          label={t('auth.signup.phone')}
          hint={t('auth.signup.phone_hint')}
          error={fieldErrors.phone}
        >
          <input id="phone" className="input" type="tel" value={form.phone} onChange={set('phone')} autoComplete="tel" />
        </Field>
        <Field
          id="password"
          label={t('auth.signup.password')}
          hint={t('auth.signup.password_hint')}
          error={fieldErrors.password}
        >
          <input id="password" className="input" type="password" value={form.password} onChange={set('password')} autoComplete="new-password" />
        </Field>
        <Field id="confirm" label={t('auth.signup.confirm')} error={fieldErrors.confirm}>
          <input id="confirm" className="input" type="password" value={form.confirm} onChange={set('confirm')} autoComplete="new-password" />
        </Field>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('auth.signup.submitting') : t('auth.signup.submit')}
        </button>
      </form>
    </AuthCard>
  );
}
