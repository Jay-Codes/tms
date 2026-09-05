/**
 * Org theming v2 (SPEC §2.0, PLAN2 Phase 12).
 *
 * A landlord picks a **preset** (eight ship with the platform) and may then
 * override the individual tokens in an Advanced panel. The themable set is
 * exactly seven colours plus one font id:
 *
 *   paper · surface · ink · ink_muted · rule · primary · accent   (+ font_id)
 *
 * Everything else is CORE and fixed platform-wide: stamp inks (paid/overdue),
 * the focus ring, the 4px spacing grid, the stationery radii and the type
 * scale. Those are never emitted by this module.
 *
 * The backend is the source of truth for presets (`GET /themes/presets`) and
 * re-validates every save; `PRESETS` below is the offline/fallback copy and
 * `validateTheme` is the *same* rule table the server enforces, so the client
 * can block a bad save before it is attempted.
 *
 * v1 (`{ primaryColor, font }`) still works: `applyOrgTheme` maps it onto the
 * Ledger preset, so orgs that never picked a preset keep exactly what they had.
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

export const PRESET_IDS = [
  'ledger',
  'night_ledger',
  'warm_paper',
  'cool_slate',
  'forest',
  'ocean',
  'high_contrast',
  'minimal_white',
] as const;
export type PresetId = (typeof PRESET_IDS)[number];

/** The seven themable colours. Lower-case `#rrggbb`, as the API stores them. */
export interface ThemeTokens {
  paper: string;
  surface: string;
  ink: string;
  ink_muted: string;
  rule: string;
  primary: string;
  accent: string;
}

/** What `GET /org/branding` resolves to, and what `applyOrgTheme` wants. */
export interface ResolvedTheme {
  preset_id: PresetId | string | null;
  tokens: ThemeTokens;
  font_id: FontId;
  dark: boolean;
}

/** A preset as served by `GET /themes/presets`. */
export interface ThemePreset {
  id: PresetId | string;
  name: string;
  dark: boolean;
  tokens: ThemeTokens;
  font_id: FontId;
}

/** v1 shape — one colour and one font. Still accepted by `applyOrgTheme`. */
export interface OrgTheme {
  primaryColor: string;
  font: FontId;
}

export type LegacyOrgTheme = OrgTheme;

export const DEFAULT_THEME: OrgTheme = {
  primaryColor: '#2b4fd0',
  font: 'bricolage',
};

/** The platform default — identical to the values baked into tokens.css. */
export const LEDGER_THEME: ResolvedTheme = {
  preset_id: 'ledger',
  tokens: {
    paper: '#fbfbf7',
    surface: '#ffffff',
    ink: '#1c2b5a',
    ink_muted: '#4a5680',
    rule: '#cbd3e8',
    primary: '#2b4fd0',
    accent: '#2b4fd0',
  },
  font_id: 'bricolage',
  dark: false,
};

/* ============================ colour math (no deps) ======================== */

function hexToRgb(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{6})$/i.exec(String(hex ?? '').trim());
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function rgbToHex(r: number, g: number, b: number): string {
  const c = (v: number) => Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, '0');
  return `#${c(r)}${c(g)}${c(b)}`;
}

/** True when `value` is a 6-digit hex colour. */
export function isHexColor(value: unknown): boolean {
  return hexToRgb(String(value ?? '')) !== null;
}

/** Normalise to lower-case `#rrggbb`; returns `null` when unparseable. */
export function normalizeHex(value: string): string | null {
  const rgb = hexToRgb(value);
  return rgb ? rgbToHex(rgb[0], rgb[1], rgb[2]) : null;
}

/**
 * Mix `hex` toward `target` by `amount` (0..1). Mixing toward a *colour*
 * rather than toward white is what makes the soft/faint steps work on a dark
 * preset: "85% toward paper" is a tint on Ledger and a shade on Night ledger.
 */
export function mix(hex: string, target: string, amount: number): string {
  const a = hexToRgb(hex);
  const b = hexToRgb(target);
  if (!a || !b) return hex;
  return rgbToHex(
    a[0] + (b[0] - a[0]) * amount,
    a[1] + (b[1] - a[1]) * amount,
    a[2] + (b[2] - a[2]) * amount,
  );
}

/** WCAG 2.x relative luminance. */
export function luminance(hex: string): number {
  const rgb = hexToRgb(hex);
  if (!rgb) return 0;
  const [r, g, b] = rgb.map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** WCAG 2.x contrast ratio, 1..21. Order-independent. */
export function contrastRatio(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  const [hi, lo] = la >= lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/** Black or white on top of `primary` — the rule the whole system relies on. */
export function onPrimary(primary: string): string {
  return luminance(primary) > 0.4 ? '#1c1917' : '#ffffff';
}

/* ============================== validation ================================ */

export interface ThemeFailure {
  /** e.g. `"ink/paper"` — matches the server's own pair names. */
  pair: string;
  ratio: number;
  minimum: number;
}

/** The pair table. Must stay identical to the backend's. */
export const CONTRAST_PAIRS: Array<{
  pair: string;
  minimum: number;
  label: string;
  of: (t: ThemeTokens) => [string, string];
}> = [
  { pair: 'ink/paper', minimum: 4.5, label: 'Body text on the page', of: (t) => [t.ink, t.paper] },
  { pair: 'ink/surface', minimum: 4.5, label: 'Body text on a sheet', of: (t) => [t.ink, t.surface] },
  {
    pair: 'ink_muted/paper',
    minimum: 4.5,
    label: 'Secondary text on the page',
    of: (t) => [t.ink_muted, t.paper],
  },
  {
    pair: 'ink_muted/surface',
    minimum: 4.5,
    label: 'Secondary text on a sheet',
    of: (t) => [t.ink_muted, t.surface],
  },
  {
    pair: 'on_primary/primary',
    minimum: 4.5,
    label: 'Button label on the brand colour',
    of: (t) => [onPrimary(t.primary), t.primary],
  },
  {
    pair: 'primary/paper',
    minimum: 3.0,
    label: 'Brand colour against the page',
    of: (t) => [t.primary, t.paper],
  },
  { pair: 'rule/paper', minimum: 1.2, label: 'Ledger rules on the page', of: (t) => [t.rule, t.paper] },
];

/** Every pair with its measured ratio — what the Advanced panel's badges show. */
export function themeContrastReport(
  tokens: ThemeTokens,
): Array<{ pair: string; label: string; ratio: number; minimum: number; ok: boolean }> {
  return CONTRAST_PAIRS.map((p) => {
    const [a, b] = p.of(tokens);
    const ratio = Math.round(contrastRatio(a, b) * 100) / 100;
    return { pair: p.pair, label: p.label, ratio, minimum: p.minimum, ok: ratio >= p.minimum };
  });
}

/** The failing pairs only — same shape the API returns in its 400 body. */
export function validateTheme(tokens: ThemeTokens): ThemeFailure[] {
  return themeContrastReport(tokens)
    .filter((r) => !r.ok)
    .map(({ pair, ratio, minimum }) => ({ pair, ratio, minimum }));
}

/* ================================ presets ================================= */

/**
 * Offline copy of the platform presets. `GET /themes/presets` is authoritative;
 * these are what the design-system page and a failed fetch fall back to.
 */
// Generated from backend/internal/theme/presets.json — the server is the source of truth.
// Regenerate: curl -s localhost:8081/api/v1/themes/presets (see backend/internal/theme/presets_sync_test.go).
export const PRESETS: ThemePreset[] = [
  {
    id: 'ledger',
    name: 'Ledger',
    dark: false,
    font_id: 'bricolage',
    tokens: {
      paper: '#fbfbf7',
      surface: '#ffffff',
      ink: '#1c2b5a',
      ink_muted: '#4a5680',
      rule: '#cbd3e8',
      primary: '#2b4fd0',
      accent: '#2b4fd0',
    },
  },
  {
    id: 'night_ledger',
    name: 'Night ledger',
    dark: true,
    font_id: 'bricolage',
    tokens: {
      paper: '#14161c',
      surface: '#1c1f27',
      ink: '#e8eaf2',
      ink_muted: '#aab1c7',
      rule: '#343a4a',
      primary: '#96b4ff',
      accent: '#96b4ff',
    },
  },
  {
    id: 'warm_paper',
    name: 'Warm paper',
    dark: false,
    font_id: 'instrument',
    tokens: {
      paper: '#faf5ec',
      surface: '#fffdf8',
      ink: '#33291d',
      ink_muted: '#6b5844',
      rule: '#ddd0b8',
      primary: '#9a5423',
      accent: '#9a5423',
    },
  },
  {
    id: 'cool_slate',
    name: 'Cool slate',
    dark: false,
    font_id: 'archivo',
    tokens: {
      paper: '#f3f5f7',
      surface: '#ffffff',
      ink: '#1f2933',
      ink_muted: '#4d5a67',
      rule: '#ccd5dd',
      primary: '#2f6382',
      accent: '#2f6382',
    },
  },
  {
    id: 'forest',
    name: 'Forest',
    dark: false,
    font_id: 'hanken',
    tokens: {
      paper: '#f3f7f2',
      surface: '#ffffff',
      ink: '#1b2e21',
      ink_muted: '#45604d',
      rule: '#c9dbc9',
      primary: '#1f6640',
      accent: '#1f6640',
    },
  },
  {
    id: 'ocean',
    name: 'Ocean',
    dark: false,
    font_id: 'archivo',
    tokens: {
      paper: '#f1f6fa',
      surface: '#ffffff',
      ink: '#14303f',
      ink_muted: '#43606f',
      rule: '#c3d7e3',
      primary: '#12607f',
      accent: '#12607f',
    },
  },
  {
    id: 'high_contrast',
    name: 'High contrast',
    dark: false,
    font_id: 'archivo',
    tokens: {
      paper: '#ffffff',
      surface: '#ffffff',
      ink: '#000000',
      ink_muted: '#1a1a1a',
      rule: '#000000',
      primary: '#0000c8',
      accent: '#0000c8',
    },
  },
  {
    id: 'minimal_white',
    name: 'Minimal white',
    dark: false,
    font_id: 'hanken',
    tokens: {
      paper: '#ffffff',
      surface: '#fafafa',
      ink: '#18181b',
      ink_muted: '#52525b',
      rule: '#dcdce0',
      primary: '#3f3f46',
      accent: '#3f3f46',
    },
  },
];

export function presetById(id: string | null | undefined): ThemePreset | undefined {
  return PRESETS.find((p) => p.id === id);
}

/* ============================== derivation ================================ */

export type CssVars = Record<string, string>;

/**
 * Expand the seven themable colours into every CSS variable the design system
 * reads. Nothing outside this map is ever written to the document.
 */
export function deriveTokens(tokens: ThemeTokens, font: FontId = 'bricolage'): CssVars {
  const t = tokens;
  return {
    '--paper': t.paper,
    '--sheet': t.surface,
    /* Wells and input fills: the sheet nudged 4% toward the ink, so it stays a
       tint on light presets and a lift on dark ones. */
    '--sheet-tint': mix(t.surface, t.ink, 0.04),
    '--ink': t.ink,
    '--ink-soft': t.ink_muted,
    /* Placeholders/disabled only — never body text, hence no AA pair for it. */
    '--ink-faint': mix(t.ink_muted, t.paper, 0.45),
    '--rule': t.rule,
    '--rule-strong': t.ink,
    '--primary': t.primary,
    '--primary-strong': mix(t.primary, '#000000', 0.25),
    /* Toward *paper*, not white: a dark preset needs a shade, not a tint. */
    '--primary-soft': mix(t.primary, t.paper, 0.85),
    '--on-primary': onPrimary(t.primary),
    '--accent': t.accent,
    '--accent-soft': mix(t.accent, t.paper, 0.85),
    '--font-sans': `var(--font-${font}), system-ui, -apple-system, sans-serif`,
  };
}

/** Same map as a `style` object for a React element (inline preview cards). */
export function themeStyle(tokens: ThemeTokens, font?: FontId): Record<string, string> {
  return deriveTokens(tokens, font) as unknown as Record<string, string>;
}

/* ================================ apply =================================== */

const isLegacy = (t: ResolvedTheme | LegacyOrgTheme): t is LegacyOrgTheme =>
  typeof (t as LegacyOrgTheme).primaryColor === 'string';

/** Coerce anything the API (or an old cache) hands us into a ResolvedTheme. */
export function toResolvedTheme(
  theme: ResolvedTheme | LegacyOrgTheme | null | undefined,
): ResolvedTheme {
  if (!theme) return LEDGER_THEME;
  if (isLegacy(theme)) {
    const font = (FONT_IDS as readonly string[]).includes(theme.font)
      ? theme.font
      : LEDGER_THEME.font_id;
    return {
      preset_id: null,
      tokens: {
        ...LEDGER_THEME.tokens,
        primary: normalizeHex(theme.primaryColor) ?? LEDGER_THEME.tokens.primary,
        accent: normalizeHex(theme.primaryColor) ?? LEDGER_THEME.tokens.accent,
      },
      font_id: font,
      dark: false,
    };
  }
  const tokens = { ...LEDGER_THEME.tokens, ...(theme.tokens ?? {}) };
  for (const k of Object.keys(tokens) as Array<keyof ThemeTokens>) {
    tokens[k] = normalizeHex(tokens[k]) ?? LEDGER_THEME.tokens[k];
  }
  return {
    preset_id: theme.preset_id ?? null,
    tokens,
    font_id: (FONT_IDS as readonly string[]).includes(theme.font_id)
      ? theme.font_id
      : LEDGER_THEME.font_id,
    dark: Boolean(theme.dark),
  };
}

/**
 * Apply an org's theme at runtime by setting CSS variables on <html>, and
 * stamp `data-theme="dark"` for dark presets so the chart palette (which ships
 * a re-stepped dark set) and anything else keyed on it follows.
 */
export function applyOrgTheme(theme: ResolvedTheme | LegacyOrgTheme): void {
  if (typeof document === 'undefined') return;
  const resolved = toResolvedTheme(theme);
  const root = document.documentElement;
  for (const [k, v] of Object.entries(deriveTokens(resolved.tokens, resolved.font_id))) {
    root.style.setProperty(k, v);
  }
  if (resolved.dark) root.setAttribute('data-theme', 'dark');
  else root.removeAttribute('data-theme');
}

/** Drop every variable this module set; the stylesheet defaults take over. */
export function resetOrgTheme(): void {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  for (const k of Object.keys(deriveTokens(LEDGER_THEME.tokens))) root.style.removeProperty(k);
  root.removeAttribute('data-theme');
}

/** Back-compat: v1 helper, still used by the enduser pre-auth pages. */
export function derivePrimaryTokens(primaryColor: string) {
  return {
    '--primary': primaryColor,
    '--primary-strong': mix(primaryColor, '#000000', 0.25),
    '--primary-soft': mix(primaryColor, '#ffffff', 0.85),
    '--on-primary': onPrimary(primaryColor),
  };
}
