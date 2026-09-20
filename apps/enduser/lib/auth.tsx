'use client';

/**
 * Renter session state.
 *
 * The Go API owns the session (httpOnly `tms_r` cookie); the browser can only
 * ask "who am I?" via `GET /auth/me?audience=renter`. This provider does that
 * once on mount and hands the answer to every screen.
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { ApiError, authApi, type Org, type User } from './api';

export { BASE_PATH } from './basePath';
import { BASE_PATH } from './basePath';

export type AuthStatus = 'loading' | 'authenticated' | 'anonymous';

export interface AuthState {
  status: AuthStatus;
  user: User | null;
  org: Org | null;
  /**
   * Non-auth failure (server down, network) — distinct from "not signed in".
   * The thrown error itself, so the screen can put it into words in the
   * renter's own language (`errorMessage(t, failure)`); this provider sits
   * above the i18n provider and has no translator of its own.
   */
  failure: unknown;
  /** Re-ask the API who the caller is. */
  refresh: () => Promise<void>;
  /** Adopt a session just created by register/login without a second round-trip. */
  setSession: (user: User, org?: Org | null) => void;
  /** Forget the local copy after logout. */
  clearSession: () => void;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>('loading');
  const [user, setUser] = useState<User | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [failure, setFailure] = useState<unknown>(null);
  const mounted = useRef(true);

  const load = useCallback(async () => {
    try {
      const me = await authApi.me();
      if (!mounted.current) return;
      setUser(me.user);
      setOrg(me.org ?? null);
      setFailure(null);
      setStatus('authenticated');
    } catch (err) {
      if (!mounted.current) return;
      setUser(null);
      setOrg(null);
      // 401 is the normal "not signed in", not something to report.
      setFailure(err instanceof ApiError && err.status === 401 ? null : err);
      setStatus('anonymous');
    }
  }, []);

  useEffect(() => {
    mounted.current = true;
    void load();
    return () => {
      mounted.current = false;
    };
  }, [load]);

  const value = useMemo<AuthState>(
    () => ({
      status,
      user,
      org,
      failure,
      refresh: load,
      setSession: (u: User, o: Org | null = null) => {
        setUser(u);
        setOrg(o);
        setFailure(null);
        setStatus('authenticated');
      },
      clearSession: () => {
        setUser(null);
        setOrg(null);
        setFailure(null);
        setStatus('anonymous');
      },
    }),
    [status, user, org, failure, load],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useMe(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useMe must be used inside <AuthProvider>');
  return ctx;
}

/* ------------------------------------------------------------------ */
/* return-to (`?next=`) handling                                       */
/* ------------------------------------------------------------------ */

/**
 * Only same-origin, in-app paths are honoured, so a crafted `?next=` can't be
 * used to bounce a renter off-site. The app's basePath is stripped because
 * next/navigation re-adds it.
 */
export function safeNext(raw: string | null | undefined): string | null {
  if (!raw) return null;
  let value = raw.trim();
  // URL parsing normalises a backslash to '/', so '/\evil.test' would resolve
  // to the protocol-relative '//evil.test'; control characters are stripped by
  // the same parser and can hide one. Reject both before the '//' check.
  if (/[\\\u0000-\u001f\u007f]/.test(value)) return null;
  if (!value.startsWith('/') || value.startsWith('//')) return null;
  if (value === BASE_PATH) return '/';
  if (value.startsWith(`${BASE_PATH}/`)) value = value.slice(BASE_PATH.length);
  return value.startsWith('/') ? value : null;
}

/** Read `?next=` from the current URL. Client-only (no `useSearchParams`,
 *  so pages stay statically renderable without a Suspense boundary). */
export function readNextParam(): string | null {
  if (typeof window === 'undefined') return null;
  return safeNext(new URLSearchParams(window.location.search).get('next'));
}

/**
 * `?next=` as reactive state. Pages are prerendered as static HTML, so reading
 * the query string during render would bake the prerendered (empty) value into
 * links; this reads it after mount instead.
 */
export function useNextParam(): string | null {
  const [next, setNext] = useState<string | null>(null);
  useEffect(() => {
    setNext(readNextParam());
  }, []);
  return next;
}

/** Current in-app path, for handing to `?next=` when bouncing to login. */
export function currentPath(): string {
  if (typeof window === 'undefined') return '/';
  const { pathname, search } = window.location;
  return safeNext(pathname + search) ?? '/';
}

/**
 * Guard for protected screens: sends anonymous callers to
 * `/enduser/login?next=<where they were>`.
 * Returns the auth state so the caller can render a skeleton while loading.
 */
export function useRequireAuth(): AuthState {
  const auth = useMe();
  const router = useRouter();

  useEffect(() => {
    // A server/network failure is not a 401 — keep the renter here and let the
    // screen show the error rather than bouncing them to a login they also
    // cannot complete.
    if (auth.status === 'anonymous' && !auth.failure) {
      router.replace(`/login?next=${encodeURIComponent(currentPath())}`);
    }
  }, [auth.status, auth.failure, router]);

  return auth;
}
