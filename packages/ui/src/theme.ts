/**
 * Org theming — the only design-system knobs a landlord can turn.
 *
 * A landlord configures exactly two things:
 *   1. `primaryColor` — one hex color. Strong/soft/on-primary variants
 *      are derived automatically so contrast never breaks.
 *   2. `font` — one of a curated whitelist. Arbitrary fonts are not
 *      allowed (PWA offline caching + guaranteed legibility).
 *
 * Everything else (neutrals, status colors, type scale, spacing,
 * radii, touch targets) is CORE and fixed platform-wide.
 *
 * Used by the enduser and tenant apps. The admin app never calls
 * applyOrgTheme and keeps the defaults.
 */

export const FONT_IDS = ['bricolage', 'archivo', 'instrument', 'hanken'] as const;
export type FontId = (typeof FONT_IDS)[number];

/** Font CSS variables are registered by each app's next/font setup. */
export const FONT_LABELS: Record<FontId, string> = {
  bricolage: 'Bricolage Grotesque',
  archivo: 'Archivo',
  instrument: 'Instrument Sans',
  hanken: 'Hanken Grotesk',
};

export interface OrgTheme {
  /** Hex color, e.g. "#2b4fd0". Comes from org branding settings. */
  primaryColor: string;
  font: FontId;
}

export const DEFAULT_THEME: OrgTheme = {
  primaryColor: '#2b4fd0',
  font: 'bricolage',
};

/* ----- color math (no deps) ----- */

function hexToRgb(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function rgbToHex(r: number, g: number, b: number): string {
  const c = (v: number) => Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, '0');
  return `#${c(r)}${c(g)}${c(b)}`;
}

function mix(hex: string, target: number, amount: number): string {
  const rgb = hexToRgb(hex);
  if (!rgb) return hex;
  const [r, g, b] = rgb;
  return rgbToHex(r + (target - r) * amount, g + (target - g) * amount, b + (target - b) * amount);
}

/** WCAG relative luminance — decides black vs white text on primary. */
function luminance(hex: string): number {
  const rgb = hexToRgb(hex);
  if (!rgb) return 0;
  const [r, g, b] = rgb.map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** Resolve one configured color into the four primary tokens. */
export function derivePrimaryTokens(primaryColor: string) {
  return {
    '--primary': primaryColor,
    '--primary-strong': mix(primaryColor, 0, 0.25), // 25% toward black
    '--primary-soft': mix(primaryColor, 255, 0.85), // 85% toward white
    '--on-primary': luminance(primaryColor) > 0.4 ? '#1c1917' : '#ffffff',
  };
}

/**
 * Apply an org's theme at runtime by setting CSS variables on <html>.
 * Call after fetching org branding (or from the public unit_code
 * endpoint pre-auth). Pass DEFAULT_THEME to reset.
 */
export function applyOrgTheme(theme: OrgTheme): void {
  const root = document.documentElement;
  const tokens = derivePrimaryTokens(theme.primaryColor);
  for (const [k, v] of Object.entries(tokens)) root.style.setProperty(k, v);
  root.style.setProperty(
    '--font-sans',
    `var(--font-${theme.font}), system-ui, -apple-system, sans-serif`,
  );
}
