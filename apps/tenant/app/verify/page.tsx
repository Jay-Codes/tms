'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { Suspense, useEffect, useRef, useState } from 'react';
import { useT } from '@tms/ui';
import { AuthCard } from '../../components/AuthCard';
import { LanguageToggle } from '../../components/LanguageToggle';
import { Note, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

function VerifyBody() {
  const token = useSearchParams().get('token') ?? '';
  const { refresh } = useMe();
  const t = useT();
  const [state, setState] = useState<'idle' | 'working' | 'done' | 'failed'>('idle');
  const [error, setError] = useState<ApiError | null>(null);
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return;
    ran.current = true;
    if (!token) {
      setState('failed');
      setError(new ApiError(400, { detail: t('auth.verify.missing_token') }));
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
    // `t` is intentionally not a dependency: re-running the verification when
    // the language changes would POST the one-shot token a second time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, refresh]);

  return (
    <AuthCard title={t('auth.verify.title')} footer={<LanguageToggle />}>
      {state === 'working' || state === 'idle' ? (
        <p style={{ color: 'var(--ink-soft)' }}>{t('auth.verify.checking')}</p>
      ) : null}

      {state === 'done' ? (
        <>
          <Note>{t('auth.verify.ok')}</Note>
          <Link href="/" className="btn btn-primary">
            {t('auth.verify.go_dashboard')}
          </Link>
        </>
      ) : null}

      {state === 'failed' ? (
        <>
          <ProblemNote error={error} />
          <p style={{ color: 'var(--ink-soft)' }}>{t('auth.verify.resend_hint')}</p>
          <Link href="/login" className="btn btn-secondary">
            {t('auth.verify.sign_in')}
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
