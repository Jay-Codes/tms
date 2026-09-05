'use client';

/**
 * Session context for the platform admin console.
 *
 * `AuthProvider` calls `GET /auth/me?audience=admin` once on mount and shares
 * the result. Only `/login` is public; every other page wraps its body in
 * <RequireAuth>, which sends a 401 to /login (via next/navigation, so basePath
 * '/admin' is added for us).
 *
 * This console is for platform admins only. A session of any other kind
 * (a landlord who logged in on this host, say) is refused here rather than
 * shown an empty console — see `wrongAudience`.
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/navigation';
import { ApiError, authApi, type Session, type User } from './api';

export interface AuthState {
  user: User | null;
  /** The initial /auth/me call has not settled yet. */
  loading: boolean;
  /** Non-401 failure (e.g. the API is down). */
  error: ApiError | null;
  /** True once we know there is no valid admin session. */
  unauthenticated: boolean;
  /** A session exists but it does not belong to a platform admin. */
  wrongAudience: boolean;
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
      loading,
      error,
      unauthenticated,
      wrongAudience: Boolean(session?.user) && session?.user.kind !== 'platform_admin',
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

function Message({ title, children }: { title: string; children?: React.ReactNode }) {
  return (
    <main style={{ padding: 'var(--sp-7)', maxWidth: 640 }}>
      <h1 style={{ fontSize: 'var(--text-xl)' }}>{title}</h1>
      {children ? <div style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)' }}>{children}</div> : null}
    </main>
  );
}

/** Gate for protected pages: 401 → /login, non-admin session → refused. */
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading, error, unauthenticated, wrongAudience, logout } = useMe();
  const router = useRouter();

  useEffect(() => {
    if (unauthenticated) router.replace('/login');
  }, [unauthenticated, router]);

  if (loading) {
    return (
      <main style={{ padding: 'var(--sp-7)' }}>
        <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>
      </main>
    );
  }

  if (error) {
    return <Message title="The server is not answering">{error.detail}</Message>;
  }

  if (wrongAudience) {
    return (
      <Message title="Not a platform admin">
        <p>This console is for platform administrators only. Sign in with an admin account.</p>
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => void logout()}
          style={{ marginTop: 'var(--sp-4)' }}
        >
          Sign out
        </button>
      </Message>
    );
  }

  if (!user) {
    return (
      <main style={{ padding: 'var(--sp-7)' }}>
        <p style={{ color: 'var(--ink-soft)' }}>Redirecting to sign in…</p>
      </main>
    );
  }

  return <>{children}</>;
}
