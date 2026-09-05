'use client';

import type { ReactNode } from 'react';
import { useRequireAuth } from '../lib/auth';
import { BottomBar } from './BottomBar';
import { Notice, Screen } from './Screen';

/**
 * Wrapper for signed-in screens: holds the render until `GET /auth/me` has
 * answered, bounces 401s to `/login?next=…`, and paints the tab bar.
 */
export function Protected({ children }: { children: ReactNode }) {
  const { status, error, refresh } = useRequireAuth();

  if (status === 'loading') {
    return (
      <Screen bottomBar>
        <p className="pencil">Loading…</p>
      </Screen>
    );
  }

  if (status === 'anonymous') {
    // Either redirecting to login, or the API is unreachable.
    return (
      <Screen>
        {error ? (
          <>
            <Notice tone="error">{error}</Notice>
            <button className="btn btn-secondary" onClick={() => void refresh()}>
              Try again
            </button>
          </>
        ) : (
          <p className="pencil">Taking you to sign in…</p>
        )}
      </Screen>
    );
  }

  return (
    <>
      {children}
      <BottomBar />
    </>
  );
}
