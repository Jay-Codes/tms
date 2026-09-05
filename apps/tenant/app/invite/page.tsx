'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useState } from 'react';
import { useT } from '@tms/ui';
import { AuthCard } from '../../components/AuthCard';
import { LanguageToggle } from '../../components/LanguageToggle';
import { Field, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

function InviteBody() {
  const token = useSearchParams().get('token') ?? '';
  const router = useRouter();
  const { refresh } = useMe();
  const t = useT();
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  if (!token) {
    return (
      <AuthCard title={t('auth.invite.title')} footer={<LanguageToggle />}>
        <ProblemNote error={new ApiError(400, { detail: t('auth.invite.missing_token') })} />
        <Link href="/login" className="btn btn-secondary">
          {t('auth.invite.sign_in_instead')}
        </Link>
      </AuthCard>
    );
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const local: Record<string, string> = {};
    if (password.length < 8) local.password = t('auth.err.password_short');
    if (confirm !== password) local.confirm = t('auth.err.password_mismatch');
    setFieldErrors(local);
    if (Object.keys(local).length > 0) return;

    setBusy(true);
    setError(null);
    try {
      await authApi.acceptInvite(token, password);
      await refresh();
      router.replace('/');
    } catch (err) {
      const ae = err instanceof ApiError ? err : new ApiError(0, { detail: String(err) });
      setError(ae);
      setFieldErrors(ae.errors);
      setBusy(false);
    }
  };

  return (
    <AuthCard
      title={t('auth.invite.title')}
      lead={t('auth.invite.lead')}
      footer={<LanguageToggle />}
    >
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <ProblemNote error={error} />
        <Field
          id="password"
          label={t('auth.invite.password')}
          hint={t('auth.invite.password_hint')}
          error={fieldErrors.password}
        >
          <input
            id="password"
            className="input"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
          />
        </Field>
        <Field id="confirm" label={t('auth.invite.confirm')} error={fieldErrors.confirm}>
          <input
            id="confirm"
            className="input"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
          />
        </Field>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('auth.invite.submitting') : t('auth.invite.submit')}
        </button>
      </form>
    </AuthCard>
  );
}

export default function InvitePage() {
  return (
    <Suspense fallback={null}>
      <InviteBody />
    </Suspense>
  );
}
