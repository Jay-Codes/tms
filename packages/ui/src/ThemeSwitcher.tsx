'use client';

import { useState } from 'react';
import { applyOrgTheme, DEFAULT_THEME, FONT_IDS, FONT_LABELS, type OrgTheme } from './theme';

/**
 * Preview-only control showing the two knobs a landlord can turn.
 * Real apps set the theme from org branding settings, not from here.
 */

const PRESET_COLORS: Array<[string, string]> = [
  ['Ballpoint blue (default)', '#2b4fd0'],
  ['Kanga red', '#c2262e'],
  ['Mangrove green', '#1f6b3a'],
  ['Ochre', '#b3611a'],
  ['Indigo', '#3b2f8f'],
];

export function ThemeSwitcher() {
  const [theme, setTheme] = useState<OrgTheme>(DEFAULT_THEME);

  const update = (next: OrgTheme) => {
    setTheme(next);
    applyOrgTheme(next);
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <div className="field">
        <label>Brand colour</label>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
          {PRESET_COLORS.map(([label, hex]) => (
            <button
              key={hex}
              type="button"
              onClick={() => update({ ...theme, primaryColor: hex })}
              title={label}
              aria-label={label}
              aria-pressed={theme.primaryColor === hex}
              style={{
                width: 'var(--touch-min)',
                height: 'var(--touch-min)',
                borderRadius: 'var(--radius-md)',
                background: hex,
                cursor: 'pointer',
                border: 0,
                outline: theme.primaryColor === hex ? '2px solid var(--ink)' : '1px solid var(--rule)',
                outlineOffset: 2,
              }}
            />
          ))}
        </div>
        <span className="hint">One colour. Pressed, tinted and text-on-colour variants are worked out automatically.</span>
      </div>
      <div className="field">
        <label>Typeface</label>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
          {FONT_IDS.map((id) => (
            <button
              key={id}
              type="button"
              onClick={() => update({ ...theme, font: id })}
              aria-pressed={theme.font === id}
              className={theme.font === id ? 'btn btn-secondary' : 'btn btn-quiet'}
              style={{ fontFamily: `var(--font-${id}), system-ui, sans-serif`, textDecoration: 'none' }}
            >
              {FONT_LABELS[id]}
            </button>
          ))}
        </div>
        <span className="hint">Four faces, all self-hosted so the app works offline.</span>
      </div>
    </div>
  );
}
