'use client';

/**
 * Keep the landlord's colour and font on the signed-in screens too.
 *
 * Pre-auth the branding rides along with `GET /public/units/{code}` (see
 * OrgHeader/useOrgTheme). After sign-in there is no such payload:
 * `GET /me/contracts` carries the unit and the parties but no org slug, so it
 * cannot be the source. `GET /me/link-requests` does expose `org.slug`, so
 * that is what resolves the org here; the branding itself still comes from the
 * public endpoint (`GET /public/orgs/{slug}/branding`).
 *
 * The resolved branding is remembered in localStorage so a returning renter
 * gets their landlord's theme on the first paint instead of a flash of the
 * platform default, and refreshed once per browsing session.
 */

import { useEffect } from 'react';
import { applyOrgTheme, DEFAULT_THEME, FONT_IDS, type FontId } from '@tms/ui';
import { publicApi, renterApi, type PublicBranding } from '../lib/api';

const CACHE_KEY = 'tms.enduser.org-theme';
const SYNCED_KEY = 'tms.enduser.org-theme-synced';

interface CachedTheme {
  slug: string;
  primary_color: string;
  font_id: string;
}

function toFontId(raw: string | undefined): FontId {
  return (FONT_IDS as readonly string[]).includes(raw ?? '')
    ? (raw as FontId)
    : DEFAULT_THEME.font;
}

function readCache(): CachedTheme | null {
  try {
    const raw = window.localStorage.getItem(CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<CachedTheme>;
    return parsed?.primary_color ? (parsed as CachedTheme) : null;
  } catch {
    return null;
  }
}

function writeCache(value: CachedTheme): void {
  try {
    window.localStorage.setItem(CACHE_KEY, JSON.stringify(value));
  } catch {
    // Storage blocked — the theme still applies for this page view.
  }
}

function apply(primaryColor: string, fontId: string | undefined): void {
  applyOrgTheme({ primaryColor, font: toFontId(fontId) });
}

export function OrgThemeSync() {
  useEffect(() => {
    const cached = readCache();
    if (cached) apply(cached.primary_color, cached.font_id);

    // One refresh per session: branding changes rarely and this is a paint
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
        if (cached?.slug === slug) {
          markSynced();
          return;
        }

        const branding: PublicBranding = await publicApi.branding(slug, ac.signal);
        const color = branding.theme?.primary_color;
        if (!color) return;
        apply(color, branding.theme?.font_id);
        writeCache({ slug, primary_color: color, font_id: branding.theme?.font_id ?? '' });
        markSynced();
      } catch {
        // Theming is never worth an error on screen; the defaults are valid.
      }
    })();

    return () => ac.abort();
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
