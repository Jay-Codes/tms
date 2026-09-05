'use client';

/**
 * Portal layout — the one place <Shell> is rendered.
 *
 * Every authenticated landlord screen lives in this route group, so the nav,
 * the org branding and the badge counts are fetched once and survive every
 * navigation (PLAN2 #7). The group name is in parentheses, so it adds nothing
 * to any URL: /properties is still /properties (with the /tenant basePath).
 *
 * Auth screens (login, signup, verify, invite) sit outside the group and get
 * no chrome.
 */

import type { ReactNode } from 'react';
import { Shell } from '../../components/Shell';

export default function PortalLayout({ children }: { children: ReactNode }) {
  return <Shell>{children}</Shell>;
}
