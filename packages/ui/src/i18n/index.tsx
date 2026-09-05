'use client';

/**
 * Minimal i18n runtime shared by the renter and landlord apps (SPEC §3.2).
 *
 * Each app owns its dictionaries (`apps/<app>/i18n/en.ts` and `sw.ts`) as
 * flat `Record<string, string>` maps with identical key sets — the
 * `scripts/i18n-check.mjs` script (run by `make lint`) fails when a key is
 * missing in either language. Messages interpolate `{name}` placeholders;
 * plural forms use the `key.one` / `key.other` convention with `{count}`.
 */

import { createContext, useCallback, useContext, useMemo, type ReactNode } from 'react';

export type Locale = 'sw' | 'en';
export const LOCALES: readonly Locale[] = ['sw', 'en'] as const;
export const DEFAULT_LOCALE: Locale = 'sw';
export const LOCALE_LABELS: Record<Locale, string> = { sw: 'Kiswahili', en: 'English' };

export type Messages = Record<string, string>;
export type Vars = Record<string, string | number | null | undefined>;

export function isLocale(raw: unknown): raw is Locale {
  return raw === 'sw' || raw === 'en';
}

/** Best-effort locale from the device when nothing better is known. */
export function guessLocale(): Locale {
  if (typeof navigator === 'undefined') return DEFAULT_LOCALE;
  const langs = navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const l of langs) {
    const base = (l ?? '').toLowerCase().split('-')[0];
    if (base === 'sw') return 'sw';
    if (base === 'en') return 'en';
  }
  return DEFAULT_LOCALE;
}

export function interpolate(template: string, vars?: Vars): string {
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (m, k: string) => {
    const v = vars[k];
    return v === null || v === undefined ? m : String(v);
  });
}

export interface Translator {
  (key: string, vars?: Vars): string;
  /** Plural helper: picks `key.one` when count === 1 else `key.other`. */
  n: (key: string, count: number, vars?: Vars) => string;
  locale: Locale;
}

export function makeTranslator(locale: Locale, messages: Messages, fallback?: Messages): Translator {
  const lookup = (key: string): string | undefined => messages[key] ?? fallback?.[key];
  const t = ((key: string, vars?: Vars) => {
    const msg = lookup(key);
    if (msg === undefined) {
      if (process.env.NODE_ENV !== 'production') console.warn(`[i18n] missing key "${key}" (${locale})`);
      return key;
    }
    return interpolate(msg, vars);
  }) as Translator;
  t.n = (key, count, vars) => t(count === 1 ? `${key}.one` : `${key}.other`, { count, ...vars });
  t.locale = locale;
  return t;
}

interface I18nContextValue {
  locale: Locale;
  t: Translator;
  setLocale?: (l: Locale) => void;
}

const I18nContext = createContext<I18nContextValue | null>(null);

export interface I18nProviderProps {
  locale: Locale;
  /** Dictionaries keyed by locale; the other language is the fallback. */
  messages: Record<Locale, Messages>;
  /** Optional setter so a switcher can change the language in place. */
  onChange?: (l: Locale) => void;
  children: ReactNode;
}

export function I18nProvider({ locale, messages, onChange, children }: I18nProviderProps) {
  const fallback = locale === 'sw' ? 'en' : 'sw';
  const t = useMemo(() => makeTranslator(locale, messages[locale], messages[fallback]), [locale, messages, fallback]);
  const setLocale = useCallback((l: Locale) => onChange?.(l), [onChange]);
  const value = useMemo(() => ({ locale, t, setLocale: onChange ? setLocale : undefined }), [locale, t, setLocale, onChange]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error('useI18n must be used inside <I18nProvider>');
  return ctx;
}

export function useT(): Translator {
  return useI18n().t;
}

export function useLocale(): Locale {
  return useI18n().locale;
}

/** Intl tag for dates/numbers; Swahili in Tanzania, English otherwise. */
export function intlTag(locale: Locale): string {
  return locale === 'sw' ? 'sw-TZ' : 'en-TZ';
}

export function formatDateIntl(locale: Locale, iso: string, opts?: Intl.DateTimeFormatOptions): string {
  const d = new Date(iso.length === 10 ? `${iso}T00:00:00+03:00` : iso);
  return new Intl.DateTimeFormat(intlTag(locale), { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'Africa/Dar_es_Salaam', ...opts }).format(d);
}

export function formatNumberIntl(locale: Locale, n: number): string {
  return new Intl.NumberFormat(intlTag(locale), { maximumFractionDigits: 0 }).format(n);
}
