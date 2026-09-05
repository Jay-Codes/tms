/**
 * Renter-app theming (Phase 12, theming v2).
 *
 * The branding endpoints serve a *resolved* token set:
 *
 *   theme: { preset_id, tokens: {paper, surface, ink, ink_muted, rule,
 *            primary, accent}, font_id, dark, primary_color }
 *
 * `primary_color` is the v1 field the API keeps for older clients. This module
 * is the single place the renter app turns that JSON — or a cache entry
 * written by an older build — into a `ResolvedTheme` and paints it, so every
 * screen (QR landing, connect, contract, rent book, profile) themes
 * identically.
 *
 * The token → CSS variable expansion itself belongs to `@tms/ui`
 * (`applyOrgTheme` / `resetOrgTheme`); what is added here is the app-level
 * chrome that the design system does not own: the PWA `theme-color`, the
 * document `color-scheme`, and the sheet shadow, all of which follow the
 * paper. `data-theme="dark"` is stamped by `applyOrgTheme`, and the app's own
 * CSS (enduser.css, contract document.css) keys off it.
 */

import {
  applyOrgTheme,
  LEDGER_THEME,
  luminance,
  resetOrgTheme,
  toResolvedTheme as uiToResolvedTheme,
  type FontId,
  type ResolvedTheme,
  type ThemeTokens,
} from '@tms/ui';

export { LEDGER_THEME };
export type { ResolvedTheme, ThemeTokens };

/** Anything the branding endpoints may hand us — v2, or the legacy v1 shape. */
export interface ThemeInput {
  preset_id?: string | null;
  tokens?: Partial<Record<keyof ThemeTokens, string>> | null;
  font_id?: string | null;
  dark?: boolean | null;
  /** v1 field, still served alongside the token set. */
  primary_color?: string | null;
}

const HEX = /^#[0-9a-f]{6}$/i;

/**
 * Normalise any branding `theme` into a full `ResolvedTheme`.
 *
 * - v2 payload → its tokens (`@tms/ui` fills and validates each one against
 *   the Ledger preset, so a half-served theme cannot half-paint a screen).
 * - legacy `{primary_color, font_id}` → Ledger tokens with `primary` **and**
 *   `accent` replaced by that colour, exactly what v1 `applyOrgTheme` did.
 * - null / undefined / garbage → Ledger.
 */
export function toResolvedTheme(input: ThemeInput | null | undefined): ResolvedTheme {
  if (!input || typeof input !== 'object') return LEDGER_THEME;

  const tokens = input.tokens;
  const hasTokens =
    !!tokens && typeof tokens === 'object' && Object.values(tokens).some((v) => HEX.test(String(v)));

  if (hasTokens) {
    const paper = HEX.test(String(tokens.paper)) ? String(tokens.paper) : LEDGER_THEME.tokens.paper;
    return uiToResolvedTheme({
      preset_id: input.preset_id ?? null,
      tokens: tokens as ThemeTokens,
      font_id: (input.font_id ?? LEDGER_THEME.font_id) as FontId,
      // Trust the server's flag when it sends one, otherwise read the paper.
      dark: typeof input.dark === 'boolean' ? input.dark : luminance(paper) < 0.4,
    });
  }

  // v1: one colour and one font.
  return uiToResolvedTheme({
    primaryColor: input.primary_color ?? LEDGER_THEME.tokens.primary,
    font: (input.font_id ?? LEDGER_THEME.font_id) as FontId,
  });
}

/* -------------------------------- applying -------------------------------- */

/** Keep the PWA / status-bar colour in step with the paper being rendered. */
function setThemeColorMeta(color: string): void {
  let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  if (!meta) {
    meta = document.createElement('meta');
    meta.name = 'theme-color';
    document.head.appendChild(meta);
  }
  meta.content = color;
}

/** Paint a resolved theme on <html>. Safe to call repeatedly. */
export function applyTheme(theme: ResolvedTheme): void {
  if (typeof document === 'undefined') return;
  applyOrgTheme(theme);

  const root = document.documentElement;
  // Native controls, scrollbars and form widgets follow the paper too; a dark
  // ledger with a white date picker on top of it reads as a bug.
  root.style.colorScheme = theme.dark ? 'dark' : 'light';
  // The ink-tinted drop shadow disappears on dark paper.
  root.style.setProperty(
    '--shadow-sheet',
    theme.dark ? '0 12px 32px rgb(0 0 0 / 0.5)' : '0 12px 32px rgb(28 43 90 / 0.14)',
  );
  setThemeColorMeta(theme.tokens.paper);
}

/** Back to the platform default (Ledger) — used before an org is known. */
export function resetTheme(): void {
  if (typeof document === 'undefined') return;
  resetOrgTheme();
  const root = document.documentElement;
  root.style.colorScheme = 'light';
  root.style.removeProperty('--shadow-sheet');
  setThemeColorMeta(LEDGER_THEME.tokens.paper);
}

/* --------------------------------- cache --------------------------------- */

/**
 * The last org theme, so a returning renter opens on their landlord's paper
 * instead of a flash of the platform default. The key is unchanged from v1;
 * the shape is versioned and a v1 entry is migrated through
 * `toResolvedTheme` rather than thrown away.
 */
const CACHE_KEY = 'tms.enduser.org-theme';

export interface CachedTheme {
  slug: string;
  theme: ResolvedTheme;
}

export function readCachedTheme(): CachedTheme | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const slug = typeof parsed?.slug === 'string' ? parsed.slug : '';
    if (parsed && typeof parsed.theme === 'object' && parsed.theme) {
      return { slug, theme: toResolvedTheme(parsed.theme as ThemeInput) };
    }
    // v1 entry: {slug, primary_color, font_id}.
    if (typeof parsed?.primary_color === 'string') {
      return {
        slug,
        theme: toResolvedTheme({
          primary_color: parsed.primary_color as string,
          font_id: typeof parsed.font_id === 'string' ? parsed.font_id : null,
        }),
      };
    }
    return null;
  } catch {
    return null;
  }
}

export function writeCachedTheme(slug: string, theme: ResolvedTheme): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(CACHE_KEY, JSON.stringify({ v: 2, slug, theme }));
  } catch {
    // Storage blocked — the theme still applies for this page view.
  }
}
