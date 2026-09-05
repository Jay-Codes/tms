'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { useT } from '@tms/ui';
import { AuthCard } from '../../components/AuthCard';
import { LanguageToggle } from '../../components/LanguageToggle';
import { Field, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

export default function LoginPage() {
  const router = useRouter();
  const { refresh } = useMe();
  const t = useT();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await authApi.login(email.trim(), password);
      await refresh();
      router.replace('/');
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, { detail: String(err) }));
      setBusy(false);
    }
  };

  return (
    <AuthCard
      title={t('auth.login.title')}
      lead={t('auth.login.lead')}
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
            {t('auth.login.new_here')} <Link href="/signup">{t('auth.login.register')}</Link>.
          </span>
          {/* Before there is a session the language lives in localStorage —
              and it is prefilled into signup (Phase 13). */}
          <LanguageToggle />
        </div>
      }
    >
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <ProblemNote error={error} />
        <Field id="email" label={t('auth.login.email')} error={error?.errors.email}>
          <input
            id="email"
            className="input"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
        </Field>
        <Field id="password" label={t('auth.login.password')} error={error?.errors.password}>
          <input
            id="password"
            className="input"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('auth.login.submitting') : t('auth.login.submit')}
        </button>
      </form>
    </AuthCard>
  );
}
