'use client';

/**
 * Org theming for the landlord portal (Phase 12).
 *
 * `GET /org/branding` carries the resolved theme — a preset id, the seven
 * themable colours, a font and a dark flag (SPEC §2.0). `applyOrgTheme` from
 * `@tms/ui` expands those into every CSS variable the design system reads and
 * stamps `data-theme="dark"` so the chart palette re-steps with the shell.
 *
 * The resolved theme is cached in localStorage under `tms.tenant.theme` and
 * applied synchronously on mount, so a landlord's own colours are on screen
 * before the branding request comes back — no flash of platform blue. The
 * fetch then overwrites the cache.
 *
 * Backwards compatible: an org still on the v1 record (`{primary_color,
 * font_id}`) resolves through `toResolvedTheme`, which maps one colour onto the
 * Ledger preset.
 */

import { useEffect, useState } from 'react';
import {
  applyOrgTheme,
  deriveTokens,
  FONT_IDS,
  LEDGER_THEME,
  toResolvedTheme,
  type FontId,
  type ResolvedTheme,
} from '@tms/ui';
import { brandingApi, unwrapBranding, type BrandingTheme, type OrgBranding } from './api';

export const THEME_CACHE_KEY = 'tms.tenant.theme';

/**
 * How long a cached `logo_url` is trusted. `GET /org/branding` presigns its
 * URLs for an hour (API.md Branding) and does not say when they expire, so the
 * cache stamps its own read time and treats the URL as stale well inside that
 * hour — a stale one only costs a re-fetch, an expired one costs a broken tile.
 */
const LOGO_TTL_MS = 45 * 60 * 1000;

export function fontId(value: string | null | undefined): FontId {
  return (FONT_IDS as readonly string[]).includes(String(value))
    ? (value as FontId)
    : LEDGER_THEME.font_id;
}

/**
 * Branding record → ResolvedTheme. A v2 record already carries `tokens`; a v1
 * one only has `primary_color`, and is mapped onto Ledger.
 */
export function resolveTheme(branding: OrgBranding | null | undefined): ResolvedTheme {
  const theme = branding?.theme as BrandingTheme | undefined;
  if (theme?.tokens) {
    return toResolvedTheme({
      preset_id: theme.preset_id ?? null,
      tokens: theme.tokens,
      font_id: fontId(theme.font_id),
      dark: Boolean(theme.dark),
    });
  }
  if (theme?.primary_color) {
    return toResolvedTheme({ primaryColor: theme.primary_color, font: fontId(theme?.font_id) });
  }
  return LEDGER_THEME;
}

/* ------------------------------- the cache ------------------------------- */

/**
 * Cache entry. `vars` is the derived variable map, stored alongside the theme
 * so the blocking script in `app/layout.tsx` can paint it without pulling in
 * any of the colour maths.
 */
interface ThemeCacheEntry {
  v: 2 | 3;
  theme: ResolvedTheme;
  vars: Record<string, string>;
  /** v3 (Phase 20.2): the presigned logo, so the mark paints on first frame. */
  logo_url?: string | null;
  display_name?: string | null;
  /** Epoch ms after which `logo_url` is not to be trusted. */
  logo_expires_at?: number;
}

function readCacheEntry(): ThemeCacheEntry | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(THEME_CACHE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as ThemeCacheEntry;
  } catch {
    return null;
  }
}

export function readCachedTheme(): ResolvedTheme | null {
  const parsed = readCacheEntry();
  return parsed?.theme?.tokens ? toResolvedTheme(parsed.theme) : null;
}

export function cacheTheme(theme: ResolvedTheme, mark?: OrgMark): void {
  if (typeof window === 'undefined') return;
  try {
    const entry: ThemeCacheEntry = {
      v: 3,
      theme,
      vars: deriveTokens(theme.tokens, theme.font_id),
      logo_url: mark?.logo_url ?? null,
      display_name: mark?.display_name ?? null,
      logo_expires_at: mark?.logo_url ? Date.now() + LOGO_TTL_MS : undefined,
    };
    window.localStorage.setItem(THEME_CACHE_KEY, JSON.stringify(entry));
  } catch {
    /* private mode / quota — the theme still applies for this page load */
  }
}

/* -------------------------------- the mark ------------------------------- */

/** What `OrgMark` in the shell draws: the logo, and the name beside it. */
export interface OrgMark {
  logo_url: string | null;
  display_name: string | null;
}

/**
 * The mark lives in a module-level slot rather than a context because the shell
 * mounts once for the session and the branding page (a route under it) has to
 * be able to push a new logo into it after an upload or a delete.
 */
let currentMark: OrgMark = { logo_url: null, display_name: null };
let markLoaded = false;
const markListeners = new Set<(m: OrgMark) => void>();

function setMark(next: OrgMark): void {
  currentMark = next;
  markLoaded = true;
  for (const fn of markListeners) fn(next);
}

/** The cached mark, or nothing when the cache is empty or the URL went stale. */
function cachedMark(): OrgMark {
  const entry = readCacheEntry();
  if (!entry) return { logo_url: null, display_name: null };
  const fresh = !entry.logo_expires_at || entry.logo_expires_at > Date.now();
  return {
    logo_url: fresh ? (entry.logo_url ?? null) : null,
    display_name: entry.display_name ?? null,
  };
}

/**
 * Subscribe to the org mark. Seeded from the cache so the logo is on screen
 * before `GET /org/branding` answers, exactly as the colours are.
 */
export function useOrgMark(): OrgMark {
  const [mark, setLocal] = useState<OrgMark>(() => (markLoaded ? currentMark : cachedMark()));
  useEffect(() => {
    if (!markLoaded) {
      const seeded = cachedMark();
      currentMark = seeded;
      setLocal(seeded);
    } else {
      setLocal(currentMark);
    }
    markListeners.add(setLocal);
    return () => {
      markListeners.delete(setLocal);
    };
  }, []);
  return mark;
}

/**
 * Re-read `/org/branding` for a fresh presigned logo. Called once when the
 * `<img>` errors (an expired URL is a 403, not a broken file); the caller falls
 * back to the plain square if this answers with nothing.
 */
export async function refreshOrgMark(): Promise<OrgMark> {
  try {
    const branding = unwrapBranding(await brandingApi.get());
    applyBranding(branding);
    return currentMark;
  } catch {
    setMark({ logo_url: null, display_name: currentMark.display_name });
    return currentMark;
  }
}

/** Paint the cached theme, if any. Called before the branding fetch resolves. */
export function applyCachedTheme(): void {
  const cached = readCachedTheme();
  if (cached) applyOrgTheme(cached);
  if (!markLoaded) setMark(cachedMark());
}

/* -------------------------------- applying ------------------------------- */

/**
 * Apply a branding record and remember it for the next page load — the theme
 * and, since Phase 20.2, the logo the shell's mark draws. The branding page
 * calls this after every save, upload and delete, which is what makes the rail
 * and the mobile bar follow a logo change without a reload.
 */
export function applyBranding(branding: OrgBranding | null | undefined): void {
  if (typeof document === 'undefined') return;
  const theme = resolveTheme(branding);
  applyOrgTheme(theme);
  const mark: OrgMark = {
    logo_url: branding?.logo_url ?? null,
    display_name: branding?.display_name ?? null,
  };
  cacheTheme(theme, mark);
  setMark(mark);
}

/** Apply a candidate while the landlord is editing; nothing is cached. */
export function previewTheme(theme: ResolvedTheme): void {
  applyOrgTheme(theme);
}

/** Undo a preview: back to whatever is saved (cache), else the default. */
export function restoreSavedTheme(): void {
  applyOrgTheme(readCachedTheme() ?? LEDGER_THEME);
}

/**
 * Fetch and apply. Failures are deliberately silent: an unthemed portal is a
 * working portal, and the cached theme is already on screen.
 */
export async function loadAndApplyOrgTheme(signal?: AbortSignal): Promise<void> {
  applyCachedTheme();
  try {
    applyBranding(unwrapBranding(await brandingApi.get(signal)));
  } catch {
    /* keep the cached (or platform default) theme */
  }
}
