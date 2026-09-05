'use client';

/**
 * Session context for the landlord portal.
 *
 * `AuthProvider` calls `GET /auth/me?audience=org` once on mount and shares the
 * result. It never redirects on its own — /login, /signup, /verify and /invite
 * are public. Protected pages wrap their body in <RequireAuth>, which sends a
 * 401 to /login (via next/navigation, so basePath '/tenant' is added for us).
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useT } from '@tms/ui';
import { ApiError, authApi, type OrgRef, type Session, type User } from './api';

export interface AuthState {
  user: User | null;
  org: OrgRef | null;
  /** The initial /auth/me call has not settled yet. */
  loading: boolean;
  /** Non-401 failure (e.g. the API is down). */
  error: ApiError | null;
  /** True once we know there is no valid org session. */
  unauthenticated: boolean;
  refresh: () => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);
  const [unauthenticated, setUnauthenticated] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const s = await authApi.me(signal);
      setSession(s);
      setUnauthenticated(false);
      setError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setSession(null);
      if (e instanceof ApiError && e.isUnauthorized) {
        setUnauthenticated(true);
        setError(null);
      } else {
        setUnauthenticated(false);
        setError(e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const refresh = useCallback(async () => {
    await load();
  }, [load]);

  const logout = useCallback(async () => {
    try {
      await authApi.logout();
    } catch {
      /* clearing the cookie is best-effort; leave either way */
    }
    setSession(null);
    setUnauthenticated(true);
    router.replace('/login');
  }, [router]);

  const value = useMemo<AuthState>(
    () => ({
      user: session?.user ?? null,
      org: session?.org ?? null,
      loading,
      error,
      unauthenticated,
      refresh,
      logout,
    }),
    [session, loading, error, unauthenticated, refresh, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useMe(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useMe must be used inside <AuthProvider>');
  return ctx;
}

/** Gate for protected pages: redirects to /login on 401. */
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading, error, unauthenticated } = useMe();
  const router = useRouter();
  const t = useT();

  useEffect(() => {
    if (unauthenticated) router.replace('/login');
  }, [unauthenticated, router]);

  if (loading) {
    return (
      <main style={{ padding: 'var(--sp-7)' }}>
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
      </main>
    );
  }

  if (error) {
    return (
      <main style={{ padding: 'var(--sp-7)', maxWidth: 640 }}>
        <h1 style={{ fontSize: 'var(--text-xl)' }}>{t('common.server_silent')}</h1>
        <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)' }}>{error.detail}</p>
      </main>
    );
  }

  if (!user) {
    return (
      <main style={{ padding: 'var(--sp-7)' }}>
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.redirecting_signin')}</p>
      </main>
    );
  }

  return <>{children}</>;
}
