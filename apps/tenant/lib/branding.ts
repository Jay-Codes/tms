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
  v: 2;
  theme: ResolvedTheme;
  vars: Record<string, string>;
}

export function readCachedTheme(): ResolvedTheme | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(THEME_CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as ThemeCacheEntry;
    return parsed?.theme?.tokens ? toResolvedTheme(parsed.theme) : null;
  } catch {
    return null;
  }
}

export function cacheTheme(theme: ResolvedTheme): void {
  if (typeof window === 'undefined') return;
  try {
    const entry: ThemeCacheEntry = {
      v: 2,
      theme,
      vars: deriveTokens(theme.tokens, theme.font_id),
    };
    window.localStorage.setItem(THEME_CACHE_KEY, JSON.stringify(entry));
  } catch {
    /* private mode / quota — the theme still applies for this page load */
  }
}

/** Paint the cached theme, if any. Called before the branding fetch resolves. */
export function applyCachedTheme(): void {
  const cached = readCachedTheme();
  if (cached) applyOrgTheme(cached);
}

/* -------------------------------- applying ------------------------------- */

/** Apply a branding record and remember it for the next page load. */
export function applyBranding(branding: OrgBranding | null | undefined): void {
  if (typeof document === 'undefined') return;
  const theme = resolveTheme(branding);
  applyOrgTheme(theme);
  cacheTheme(theme);
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
