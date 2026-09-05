'use client';

import type { ReactNode } from 'react';
import { useT } from '@tms/ui';
import { useRequireAuth } from '../lib/auth';
import { errorMessage } from '../lib/format';
import { BottomBar } from './BottomBar';
import { OrgThemeSync } from './OrgThemeSync';
import { Notice, Screen } from './Screen';

/**
 * Wrapper for signed-in screens: holds the render until `GET /auth/me` has
 * answered, bounces 401s to `/login?next=…`, and paints the tab bar.
 */
export function Protected({ children }: { children: ReactNode }) {
  const { status, failure, refresh } = useRequireAuth();
  const t = useT();

  if (status === 'loading') {
    return (
      <Screen bottomBar>
        <p className="pencil">{t('common.loading')}</p>
      </Screen>
    );
  }

  if (status === 'anonymous') {
    // Either redirecting to login, or the API is unreachable.
    return (
      <Screen>
        {failure ? (
          <>
            <Notice tone="error">{errorMessage(t, failure)}</Notice>
            <button className="btn btn-secondary" onClick={() => void refresh()}>
              {t('common.tryAgain')}
            </button>
          </>
        ) : (
          <p className="pencil">{t('auth.redirecting')}</p>
        )}
      </Screen>
    );
  }

  return (
    <>
      <OrgThemeSync />
      {children}
      <BottomBar />
    </>
  );
}
