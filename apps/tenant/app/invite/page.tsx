'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useState } from 'react';
import { AuthCard } from '../../components/AuthCard';
import { Field, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

function InviteBody() {
  const token = useSearchParams().get('token') ?? '';
  const router = useRouter();
  const { refresh } = useMe();
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  if (!token) {
    return (
      <AuthCard title="Accept your invitation">
        <ProblemNote
          error={new ApiError(400, { detail: 'This link is missing its token. Open the invitation link from your email again.' })}
        />
        <Link href="/login" className="btn btn-secondary">
          Sign in instead
        </Link>
      </AuthCard>
    );
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const local: Record<string, string> = {};
    if (password.length < 8) local.password = 'Use at least 8 characters.';
    if (confirm !== password) local.confirm = 'The two passwords do not match.';
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
    <AuthCard title="Accept your invitation" lead="Choose a password to finish setting up your staff account.">
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <ProblemNote error={error} />
        <Field id="password" label="New password" hint="At least 8 characters." error={fieldErrors.password}>
          <input
            id="password"
            className="input"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
          />
        </Field>
        <Field id="confirm" label="Confirm password" error={fieldErrors.confirm}>
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
          {busy ? 'Saving…' : 'Set password and continue'}
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
