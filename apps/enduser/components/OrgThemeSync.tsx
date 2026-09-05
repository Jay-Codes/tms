'use client';

/**
 * Keep the landlord's theme on the signed-in screens too.
 *
 * Pre-auth the branding rides along with `GET /public/units/{code}` (see
 * OrgHeader/useOrgTheme). After sign-in there is no such payload:
 * `GET /me/contracts` carries the unit and the parties but no org slug, so it
 * cannot be the source. `GET /me/link-requests` does expose `org.slug`, so
 * that is what resolves the org here; the branding itself still comes from the
 * public endpoint (`GET /public/orgs/{slug}/branding`).
 *
 * The resolved theme (Phase 12: the full token set, not just a colour) is
 * remembered in localStorage so a returning renter gets their landlord's paper
 * on the first paint instead of a flash of the platform default, and refreshed
 * once per browsing session.
 *
 * `<PlatformTheme />` is the counterpart for the pages where no org is known
 * yet — register and login: it restores the cached theme if there is one and
 * otherwise resets to the platform default (Ledger).
 */

import { useEffect } from 'react';
import { publicApi, renterApi, type PublicBranding } from '../lib/api';
import { applyTheme, readCachedTheme, resetTheme, toResolvedTheme, writeCachedTheme } from '../lib/theme';

const SYNCED_KEY = 'tms.enduser.org-theme-synced';

export function OrgThemeSync() {
  useEffect(() => {
    const cached = readCachedTheme();
    if (cached) applyTheme(cached.theme);
    else resetTheme();

    // One refresh per session: the theme changes rarely and this is a paint
    // detail, not data the renter acts on.
    try {
      if (window.sessionStorage.getItem(SYNCED_KEY) === '1') return;
    } catch {
      // No sessionStorage: fall through and just resolve once per mount.
    }

    const ac = new AbortController();

    void (async () => {
      try {
        const res = await renterApi.linkRequests(ac.signal);
        const items = res.items ?? [];
        // The tenancy the renter actually has beats one they merely asked for.
        const slug =
          items.find((r) => r.status === 'approved')?.org?.slug ??
          items.find((r) => r.status === 'pending')?.org?.slug ??
          items[0]?.org?.slug;
        if (!slug) return;

        const branding: PublicBranding = await publicApi.branding(slug, ac.signal);
        if (!branding?.theme) return;
        const theme = toResolvedTheme(branding.theme);
        applyTheme(theme);
        writeCachedTheme(slug, theme);
        markSynced();
      } catch {
        // Theming is never worth an error on screen; the defaults are valid.
      }
    })();

    return () => ac.abort();
  }, []);

  return null;
}

/**
 * Pages seen before an org is known (register, login). No fetch: either the
 * renter has a remembered landlord, or they get the platform ledger.
 */
export function PlatformTheme() {
  useEffect(() => {
    const cached = readCachedTheme();
    if (cached) applyTheme(cached.theme);
    else resetTheme();
  }, []);

  return null;
}

function markSynced(): void {
  try {
    window.sessionStorage.setItem(SYNCED_KEY, '1');
  } catch {
    // Ignored — worst case we resolve again on the next mount.
  }
}
