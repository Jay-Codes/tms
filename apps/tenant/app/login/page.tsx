'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { AuthCard } from '../../components/AuthCard';
import { Field, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

export default function LoginPage() {
  const router = useRouter();
  const { refresh } = useMe();
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
      title="Sign in"
      lead="Landlord and staff accounts sign in with email and password."
      footer={
        <>
          New here? <Link href="/signup">Register your business</Link>.
        </>
      }
    >
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <ProblemNote error={error} />
        <Field id="email" label="Email" error={error?.errors.email}>
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
        <Field id="password" label="Password" error={error?.errors.password}>
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
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </AuthCard>
  );
}
