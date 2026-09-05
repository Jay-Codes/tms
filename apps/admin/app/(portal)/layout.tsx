'use client';

/**
 * Console layout — the one place <Shell> is rendered. /login sits outside this
 * route group; everything else lives inside it so the nav is mounted once
 * (PLAN2 #7). Route groups add nothing to the URL.
 */

import type { ReactNode } from 'react';
import { Shell } from '../../components/Shell';

export default function PortalLayout({ children }: { children: ReactNode }) {
  return <Shell>{children}</Shell>;
}
