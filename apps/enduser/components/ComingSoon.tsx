'use client';

import { useT } from '@tms/ui';
import { Protected } from './Protected';
import { Screen, ScreenHeader } from './Screen';

/** Placeholder for tabs whose data lands in later phases. */
export function ComingSoon({ title, lead }: { title: string; lead: string }) {
  const t = useT();
  return (
    <Protected>
      <Screen bottomBar>
        <ScreenHeader eyebrow={t('comingSoon.eyebrow')} title={title} lead={lead} />
        <hr className="rule rule-strong" />
        <p className="pencil">{t('comingSoon.empty')}</p>
      </Screen>
    </Protected>
  );
}
