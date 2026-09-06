'use client';

/**
 * Theme picker for the Branding screen (PLAN2 Phase 12, SPEC §2.0).
 *
 * Two layers, exactly as the spec describes them:
 *
 *  1. A gallery of the eight platform presets. Each card renders a *live* mini
 *     ledger — two rows, a PAID stamp, a primary button — with that preset's
 *     derived variables set inline on the card, so the preview is the real
 *     design system rather than a swatch strip. Nothing is written to the
 *     document until the landlord picks one.
 *  2. An "Advanced" panel: the seven themable colours and the font, with a live
 *     contrast badge per validated pair. `validateTheme` is the same rule table
 *     the backend enforces, so Save is disabled before a doomed request is made
 *     — and if the server disagrees anyway, its `failures` are shown verbatim.
 *
 * The candidate theme is applied to the whole page while editing (that is the
 * point of a theme picker) and reverted when the panel unmounts; whatever is
 * saved re-applies from the branding record on the next navigation.
 */

import { Icon } from '@iconify/react';
import {
  FONT_IDS,
  FONT_LABELS,
  LEDGER_THEME,
  PRESETS,
  luminance,
  normalizeHex,
  themeContrastReport,
  themeStyle,
  useT,
  validateTheme,
  type FontId,
  type Translator,
  type ResolvedTheme,
  type ThemePreset,
  type ThemeTokens,
} from '@tms/ui';
import { useEffect, useRef, useState } from 'react';
import { brandingApi, type ThemeContrastFailure } from '../lib/api';
import { previewTheme, restoreSavedTheme } from '../lib/branding';

/** What the form holds while the landlord is deciding. */
export interface ThemeDraft {
  /** The preset the draft is based on; `null` once nothing recognisable is left. */
  preset_id: string | null;
  tokens: ThemeTokens;
  font_id: FontId;
  dark: boolean;
  /** True once a token was hand-edited — decides whether `tokens` is sent. */
  customised: boolean;
}

const TOKEN_FIELDS: Array<{ key: keyof ThemeTokens; labelKey: string; hintKey: string }> = [
  { key: 'paper', labelKey: 'theme.token.paper', hintKey: 'theme.token.paper.hint' },
  { key: 'surface', labelKey: 'theme.token.surface', hintKey: 'theme.token.surface.hint' },
  { key: 'ink', labelKey: 'theme.token.ink', hintKey: 'theme.token.ink.hint' },
  { key: 'ink_muted', labelKey: 'theme.token.ink_muted', hintKey: 'theme.token.ink_muted.hint' },
  { key: 'rule', labelKey: 'theme.token.rule', hintKey: 'theme.token.rule.hint' },
  { key: 'primary', labelKey: 'theme.token.primary', hintKey: 'theme.token.primary.hint' },
  { key: 'accent', labelKey: 'theme.token.accent', hintKey: 'theme.token.accent.hint' },
];

/**
 * The platform's own names for its presets and contrast pairs arrive in English
 * (from `@tms/ui` or `GET /themes/presets`). Everything the platform ships is
 * keyed here; anything unrecognised — a preset added server-side before this
 * build knew about it — prints the name it came with.
 */
const PRESET_NAME_KEYS: Record<string, string> = {
  ledger: 'theme.preset.ledger',
  night_ledger: 'theme.preset.night_ledger',
  warm_paper: 'theme.preset.warm_paper',
  cool_slate: 'theme.preset.cool_slate',
  forest: 'theme.preset.forest',
  ocean: 'theme.preset.ocean',
  high_contrast: 'theme.preset.high_contrast',
  minimal_white: 'theme.preset.minimal_white',
};

const CONTRAST_PAIR_KEYS: Record<string, string> = {
  'ink/paper': 'theme.contrast.pair.ink_paper',
  'ink/surface': 'theme.contrast.pair.ink_surface',
  'ink_muted/paper': 'theme.contrast.pair.ink_muted_paper',
  'ink_muted/surface': 'theme.contrast.pair.ink_muted_surface',
  'on_primary/primary': 'theme.contrast.pair.on_primary_primary',
  'primary/paper': 'theme.contrast.pair.primary_paper',
  'rule/paper': 'theme.contrast.pair.rule_paper',
};

function presetName(t: Translator, preset: { id: string; name: string } | null | undefined): string {
  if (!preset) return t('theme.preset.ledger');
  const key = PRESET_NAME_KEYS[preset.id];
  return key ? t(key) : preset.name;
}

function pairLabel(t: Translator, pair: string, fallback: string): string {
  const key = CONTRAST_PAIR_KEYS[pair];
  return key ? t(key) : fallback;
}

/** A dark preset is one whose paper is dark; presets carry the flag themselves. */
export const isDarkPaper = (paper: string) => luminance(paper) < 0.5;

const sameTokens = (a: ThemeTokens, b: ThemeTokens) =>
  (Object.keys(a) as Array<keyof ThemeTokens>).every((k) => a[k] === b[k]);

/**
 * Branding record → draft. `source` is the server's own verdict (`preset` vs
 * `custom`) and wins when it is present; without it we compare the tokens
 * against the preset list we have.
 */
export function draftFromTheme(
  theme: ResolvedTheme,
  opts: { presets?: ThemePreset[]; source?: string | null } = {},
): ThemeDraft {
  const presets = opts.presets?.length ? opts.presets : PRESETS;
  const preset = presets.find((p) => p.id === theme.preset_id);
  const customised =
    opts.source === 'custom' || opts.source === 'legacy'
      ? true
      : opts.source === 'preset'
        ? false
        : !preset || !sameTokens(theme.tokens, preset.tokens);
  return {
    preset_id: theme.preset_id ?? null,
    tokens: { ...theme.tokens },
    font_id: theme.font_id,
    dark: theme.dark,
    customised,
  };
}

export function draftToTheme(draft: ThemeDraft): ResolvedTheme {
  return {
    preset_id: draft.preset_id,
    tokens: draft.tokens,
    font_id: draft.font_id,
    dark: draft.customised ? isDarkPaper(draft.tokens.paper) : draft.dark,
  };
}

/* --------------------------- the mini preview ---------------------------- */

/**
 * One preset drawn at postage-stamp size. The variables are set on the card,
 * never on the document, so eight of these can sit side by side.
 */
export function ThemeMiniature({
  tokens,
  font,
  dark,
  compact = false,
}: {
  tokens: ThemeTokens;
  font: FontId;
  dark?: boolean;
  compact?: boolean;
}) {
  const t = useT();
  return (
    <div
      aria-hidden
      data-theme={dark ? 'dark' : undefined}
      style={{
        ...themeStyle(tokens, font),
        fontFamily: 'var(--font-sans)',
        background: 'var(--paper)',
        color: 'var(--ink)',
        border: '1px solid var(--rule)',
        borderRadius: 'var(--radius-sm)',
        padding: compact ? 'var(--sp-2)' : 'var(--sp-3)',
        display: 'grid',
        gap: 6,
        overflow: 'hidden',
      }}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, color: 'var(--ink-soft)' }}>
        <span>{t('theme.sample.rent_book')}</span>
        <span>{t('theme.sample.month')}</span>
      </div>
      <div style={{ borderTop: '1px solid var(--ink)' }} />
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 8,
          fontSize: 12,
          borderBottom: '1px solid var(--rule)',
          paddingBottom: 4,
        }}
      >
        <span>{t('theme.sample.unit_a1')}</span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span className="num" style={{ fontSize: 12 }}>
            450,000
          </span>
          <span className="stamp stamp-paid" style={{ fontSize: 8 }}>
            {t('theme.sample.paid')}
          </span>
        </span>
      </div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 8,
          fontSize: 12,
          borderBottom: '1px solid var(--rule)',
          paddingBottom: 4,
        }}
      >
        <span>{t('theme.sample.unit_a2')}</span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span className="num" style={{ fontSize: 12 }}>
            300,000
          </span>
          <span className="pencil" style={{ fontSize: 10 }}>
            {t('theme.sample.due')}
          </span>
        </span>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span
          style={{
            background: 'var(--primary)',
            color: 'var(--on-primary)',
            borderRadius: 'var(--radius-md)',
            fontSize: 11,
            fontWeight: 600,
            padding: '4px 10px',
          }}
        >
          {t('theme.sample.record')}
        </span>
        <span
          style={{
            fontSize: 11,
            textDecoration: 'underline',
            textDecorationColor: 'var(--accent)',
            textUnderlineOffset: 2,
            color: 'var(--ink)',
          }}
        >
          {t('theme.sample.receipt')}
        </span>
      </div>
    </div>
  );
}

/* ------------------------------- the panel -------------------------------- */

export function ThemePanel({
  draft,
  onChange,
  serverFailures,
}: {
  draft: ThemeDraft;
  onChange: (next: ThemeDraft) => void;
  serverFailures?: ThemeContrastFailure[];
}) {
  const t = useT();
  const [presets, setPresets] = useState<ThemePreset[]>(PRESETS);
  /* Refs so the one-shot presets fetch never re-runs when the draft changes. */
  const draftRef = useRef(draft);
  const onChangeRef = useRef(onChange);
  draftRef.current = draft;
  onChangeRef.current = onChange;
  const [advanced, setAdvanced] = useState(false);
  /* Free text while typing a hex: "#1c2" is not a colour yet but must be typeable. */
  const [typing, setTyping] = useState<Partial<Record<keyof ThemeTokens, string>>>({});

  /* The platform presets are public and identical for every org. A failed fetch
     falls back to the copy bundled in @tms/ui. */
  useEffect(() => {
    const ac = new AbortController();
    brandingApi
      .presets(ac.signal)
      .then((res) => {
        if (!Array.isArray(res?.presets) || !res.presets.length) return;
        setPresets(res.presets);
        /* The server's copy is authoritative: a draft that looked "customised"
           only because our bundled tokens drifted is really an untouched preset. */
        const match = res.presets.find((p) => p.id === draftRef.current.preset_id);
        if (match && draftRef.current.customised && sameTokens(draftRef.current.tokens, match.tokens)) {
          onChangeRef.current({ ...draftRef.current, customised: false, dark: match.dark });
        }
      })
      .catch(() => {
        /* bundled fallback */
      });
    return () => ac.abort();
  }, []);

  /* Live preview: the candidate is the page. Leaving the screen puts the saved
     theme back, so an abandoned experiment never sticks. */
  const previewKey = JSON.stringify([draft.tokens, draft.font_id, draft.dark, draft.customised]);
  useEffect(() => {
    previewTheme(draftToTheme(draft));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the serialised draft
  }, [previewKey]);
  useEffect(() => () => restoreSavedTheme(), []);

  const report = themeContrastReport(draft.tokens);
  const failing = validateTheme(draft.tokens);
  const basePreset = presets.find((p) => p.id === draft.preset_id);

  const pickPreset = (p: ThemePreset) => {
    setTyping({});
    onChange({
      preset_id: p.id,
      tokens: { ...p.tokens },
      font_id: p.font_id,
      dark: p.dark,
      customised: false,
    });
  };

  const setToken = (key: keyof ThemeTokens, raw: string) => {
    setTyping((t) => ({ ...t, [key]: raw }));
    const hex = normalizeHex(raw);
    if (!hex) return;
    const tokens = { ...draft.tokens, [key]: hex };
    onChange({ ...draft, tokens, customised: true, dark: isDarkPaper(tokens.paper) });
  };

  const resetToPreset = () => {
    setTyping({});
    const p = basePreset ?? presets.find((x) => x.id === 'ledger');
    if (!p) return;
    pickPreset(p);
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      {/* ------------------------------ gallery ------------------------------ */}
      <div className="field">
        <label id="theme_gallery_label">{t('theme.gallery.label')}</label>
        <span className="hint">{t('theme.gallery.hint')}</span>
        <div
          role="radiogroup"
          aria-labelledby="theme_gallery_label"
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))',
            gap: 'var(--sp-3)',
            marginTop: 'var(--sp-2)',
          }}
        >
          {presets.map((p) => {
            const selected = !draft.customised && draft.preset_id === p.id;
            return (
              <button
                key={p.id}
                type="button"
                role="radio"
                aria-checked={selected}
                onClick={() => pickPreset(p)}
                style={{
                  display: 'grid',
                  gap: 'var(--sp-2)',
                  textAlign: 'left',
                  padding: 'var(--sp-2)',
                  background: 'var(--sheet)',
                  border: selected ? '2px solid var(--ink)' : '1px solid var(--rule)',
                  borderRadius: 'var(--radius-lg)',
                  cursor: 'pointer',
                }}
              >
                <ThemeMiniature tokens={p.tokens} font={p.font_id} dark={p.dark} />
                <span
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: 'var(--sp-2)',
                    fontSize: 'var(--text-sm)',
                    fontWeight: selected ? 600 : 500,
                  }}
                >
                  {presetName(t, p)}
                  {selected ? <Icon icon="solar:check-circle-bold" width={18} /> : null}
                </span>
              </button>
            );
          })}
        </div>
        {draft.customised ? (
          <span className="hint" style={{ marginTop: 'var(--sp-2)' }}>
            {basePreset
              ? t('theme.customised_from', { preset: presetName(t, basePreset) })
              : t('theme.customised')}
          </span>
        ) : null}
      </div>

      {/* ----------------------------- advanced ------------------------------ */}
      <div style={{ border: '1px solid var(--rule)', borderRadius: 'var(--radius-lg)' }}>
        <button
          type="button"
          onClick={() => setAdvanced((v) => !v)}
          aria-expanded={advanced}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--sp-2)',
            width: '100%',
            minHeight: 'var(--touch-min)',
            padding: '0 var(--sp-4)',
            background: 'transparent',
            border: 0,
            cursor: 'pointer',
            font: 'inherit',
            fontWeight: 600,
            textAlign: 'left',
          }}
        >
          <Icon icon={advanced ? 'solar:alt-arrow-down-linear' : 'solar:alt-arrow-right-linear'} width={20} />
          {t('theme.advanced')}
        </button>

        {advanced ? (
          <div style={{ display: 'grid', gap: 'var(--sp-4)', padding: 'var(--sp-4)', borderTop: '1px solid var(--rule)' }}>
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))',
                gap: 'var(--sp-4)',
              }}
            >
              {TOKEN_FIELDS.map((f) => {
                const raw = typing[f.key] ?? draft.tokens[f.key];
                const valid = normalizeHex(raw) !== null;
                return (
                  <div className={valid ? 'field' : 'field invalid'} key={f.key}>
                    <label htmlFor={`token_${f.key}`}>{t(f.labelKey)}</label>
                    <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center' }}>
                      <input
                        aria-label={t('theme.token.picker_aria', { label: t(f.labelKey) })}
                        type="color"
                        value={draft.tokens[f.key]}
                        onChange={(e) => setToken(f.key, e.target.value)}
                        style={{
                          width: 52,
                          height: 44,
                          padding: 0,
                          border: '1px solid var(--rule)',
                          borderRadius: 'var(--radius-sm)',
                          background: 'var(--sheet)',
                        }}
                      />
                      <input
                        id={`token_${f.key}`}
                        className="input num"
                        value={raw}
                        maxLength={7}
                        spellCheck={false}
                        onChange={(e) => setToken(f.key, e.target.value.trim())}
                      />
                    </div>
                    <span className="hint">{t(f.hintKey)}</span>
                    {valid ? null : <span className="error">{t('theme.token.bad_hex')}</span>}
                  </div>
                );
              })}

              <div className="field">
                <label htmlFor="font_id">{t('theme.font.label')}</label>
                <select
                  id="font_id"
                  className="input"
                  value={draft.font_id}
                  onChange={(e) => onChange({ ...draft, font_id: e.target.value as FontId })}
                >
                  {FONT_IDS.map((f) => (
                    <option key={f} value={f}>
                      {FONT_LABELS[f]}
                    </option>
                  ))}
                </select>
                <span className="hint">{t('theme.font.hint')}</span>
              </div>
            </div>

            {/* Contrast badges — the same pairs the API re-checks on save. */}
            <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
              <strong style={{ fontSize: 'var(--text-sm)' }}>{t('theme.contrast.heading')}</strong>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
                {report.map((r) => (
                  <span
                    key={r.pair}
                    title={t('theme.contrast.badge_title', {
                      label: pairLabel(t, r.pair, r.label),
                      minimum: r.minimum,
                    })}
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      gap: 6,
                      padding: '2px var(--sp-2)',
                      border: `1px solid ${r.ok ? 'var(--rule)' : 'var(--stamp-overdue)'}`,
                      borderRadius: 'var(--radius-sm)',
                      fontSize: 'var(--text-xs)',
                      color: r.ok ? 'var(--ink-soft)' : 'var(--stamp-overdue)',
                    }}
                  >
                    <Icon
                      icon={r.ok ? 'solar:check-circle-linear' : 'solar:close-circle-linear'}
                      width={14}
                    />
                    {r.pair} {r.ratio.toFixed(2)}:1
                    {r.ok ? '' : ` ${t('theme.contrast.needs', { minimum: r.minimum })}`}
                  </span>
                ))}
              </div>
              {failing.length ? (
                <span role="alert" className="error">
                  {t.n('theme.contrast.failing', failing.length)}
                </span>
              ) : null}
            </div>

            <div style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
              <button type="button" className="btn btn-secondary" onClick={resetToPreset}>
                {t('theme.reset_to', { preset: presetName(t, basePreset) })}
              </button>
            </div>
          </div>
        ) : null}
      </div>

      {/* The server's own verdict, when it disagrees with ours. */}
      {serverFailures?.length ? (
        <div role="alert" className="error">
          {t('theme.server_rejected', {
            failures: serverFailures
              .map((f) =>
                t('theme.server_rejected.pair', {
                  pair: f.pair,
                  ratio: Number(f.ratio).toFixed(2),
                  minimum: f.minimum,
                }),
              )
              .join('; '),
          })}
        </div>
      ) : null}
    </div>
  );
}

export const LEDGER_DRAFT: ThemeDraft = {
  preset_id: 'ledger',
  tokens: { ...LEDGER_THEME.tokens },
  font_id: LEDGER_THEME.font_id,
  dark: false,
  customised: false,
};
