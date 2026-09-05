'use client';

import { Protected } from './Protected';
import { Screen, ScreenHeader } from './Screen';

/** Placeholder for tabs whose data lands in later phases. */
export function ComingSoon({ title, lead }: { title: string; lead: string }) {
  return (
    <Protected>
      <Screen bottomBar>
        <ScreenHeader eyebrow="Coming soon" title={title} lead={lead} />
        <hr className="rule rule-strong" />
        <p className="pencil">Nothing here yet.</p>
      </Screen>
    </Protected>
  );
}
