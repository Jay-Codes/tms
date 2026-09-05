'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { Suspense, useEffect, useRef, useState } from 'react';
import { AuthCard } from '../../components/AuthCard';
import { Note, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

function VerifyBody() {
  const token = useSearchParams().get('token') ?? '';
  const { refresh } = useMe();
  const [state, setState] = useState<'idle' | 'working' | 'done' | 'failed'>('idle');
  const [error, setError] = useState<ApiError | null>(null);
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return;
    ran.current = true;
    if (!token) {
      setState('failed');
      setError(new ApiError(400, { detail: 'This link is missing its token. Open the link from your email again.' }));
      return;
    }
    setState('working');
    authApi
      .verifyEmail(token)
      .then(async () => {
        setState('done');
        await refresh();
      })
      .catch((e) => {
        setError(e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));
        setState('failed');
      });
  }, [token, refresh]);

  return (
    <AuthCard title="Email verification">
      {state === 'working' || state === 'idle' ? (
        <p style={{ color: 'var(--ink-soft)' }}>Checking your link…</p>
      ) : null}

      {state === 'done' ? (
        <>
          <Note>Your email address is verified.</Note>
          <Link href="/" className="btn btn-primary">
            Go to dashboard
          </Link>
        </>
      ) : null}

      {state === 'failed' ? (
        <>
          <ProblemNote error={error} />
          <p style={{ color: 'var(--ink-soft)' }}>
            You can sign in and send yourself a new link from the banner on the dashboard.
          </p>
          <Link href="/login" className="btn btn-secondary">
            Sign in
          </Link>
        </>
      ) : null}
    </AuthCard>
  );
}

export default function VerifyPage() {
  return (
    <Suspense fallback={null}>
      <VerifyBody />
    </Suspense>
  );
}
