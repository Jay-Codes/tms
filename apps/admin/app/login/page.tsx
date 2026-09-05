'use client';

/**
 * Platform admin sign-in. `POST /auth/login {email,password}` sets `tms_a`
 * only when the account is a platform admin; a landlord's credentials succeed
 * against the same endpoint but set `tms_o`, so the session that comes back is
 * checked here and refused rather than half-admitted.
 */

import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { AuthCard } from '../../components/AuthCard';
import { Field, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi, toApiError } from '../../lib/api';
import { useMe } from '../../lib/auth';

export default function LoginPage() {
  const router = useRouter();
  const { refresh } = useMe();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [notAdmin, setNotAdmin] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setNotAdmin(false);
    try {
      const session = await authApi.login(email.trim(), password);
      if (session?.user?.kind !== 'platform_admin') {
        // Not our audience: drop whatever cookie the login set and say so.
        setNotAdmin(true);
        await authApi.logout().catch(() => undefined);
        setBusy(false);
        return;
      }
      await refresh();
      router.replace('/');
    } catch (err) {
      setError(toApiError(err));
      setBusy(false);
    }
  };

  return (
    <AuthCard title="Sign in" lead="Platform administrators only.">
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <ProblemNote error={error} />
        {notAdmin ? (
          <p
            role="alert"
            style={{
              color: 'var(--stamp-overdue)',
              fontSize: 'var(--text-sm)',
              border: '1px solid var(--stamp-overdue)',
              borderRadius: 'var(--radius-sm)',
              padding: 'var(--sp-2) var(--sp-3)',
              maxWidth: 'none',
            }}
          >
            Not a platform admin. Landlord accounts sign in at the landlord portal.
          </p>
        ) : null}
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
