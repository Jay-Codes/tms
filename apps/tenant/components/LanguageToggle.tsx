'use client';

/**
 * The SW/EN switch (Phase 13).
 *
 * Two shapes for two places: a pair of pill buttons small enough for the auth
 * footer and the mobile drawer, and a plain <select> for Settings →
 * Preferences where it sits in a normal form row.
 */

import { LOCALE_LABELS, LOCALES, useT, type Locale } from '@tms/ui';
import { useLocaleState } from '../lib/locale';

/** Short caps used on the compact toggle: "SW" / "EN". */
const SHORT: Record<Locale, string> = { sw: 'SW', en: 'EN' };

export function LanguageToggle({ compact = true }: { compact?: boolean }) {
  const { locale, setLocale } = useLocaleState();
  const t = useT();

  if (!compact) {
    return (
      <select
        id="locale"
        value={locale}
        onChange={(e) => setLocale(e.target.value as Locale)}
        aria-label={t('common.language')}
      >
        {LOCALES.map((l) => (
          <option key={l} value={l}>
            {LOCALE_LABELS[l]}
          </option>
        ))}
      </select>
    );
  }

  return (
    <div
      role="group"
      aria-label={t('common.language')}
      className="lang-toggle"
      style={{ display: 'inline-flex', gap: 2, border: '1px solid var(--rule)', borderRadius: 999, padding: 2 }}
    >
      {LOCALES.map((l) => {
        const on = l === locale;
        return (
          <button
            key={l}
            type="button"
            onClick={() => setLocale(l)}
            aria-pressed={on}
            // The full language name is what a screen reader should hear; the
            // two letters are only there because the footer is 40px tall.
            aria-label={LOCALE_LABELS[l]}
            style={{
              minHeight: 28,
              padding: '0 var(--sp-3)',
              borderRadius: 999,
              border: 0,
              font: 'inherit',
              fontSize: 'var(--text-xs)',
              fontWeight: on ? 600 : 400,
              cursor: 'pointer',
              background: on ? 'var(--primary)' : 'transparent',
              color: on ? 'var(--on-primary)' : 'var(--ink-soft)',
            }}
          >
            {SHORT[l]}
          </button>
        );
      })}
    </div>
  );
}
