'use client';

/**
 * Portal language (SPEC §3.2, Phase 13).
 *
 * Resolution order, strongest first:
 *   1. the signed-in org user's `locale` from `GET /auth/me`;
 *   2. the last choice this browser made, in `localStorage['tms.tenant.locale']`;
 *   3. `guessLocale()` — the device languages, falling back to Swahili.
 *
 * Changing it writes the choice to localStorage immediately (so the auth pages
 * remember it before there is an account to hang it on) and, when there is a
 * session, sends `PATCH /org/members/me {locale}` so the same landlord gets the
 * same language on their next device — and so their own SMS come in it.
 *
 * `<html lang>` follows the resolved locale on every change.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { I18nProvider, guessLocale, isLocale, type Locale } from '@tms/ui';
import en from '../i18n/en';
import sw from '../i18n/sw';
import { membersApi } from './api';
import { useMe } from './auth';
import { setFormatLocale } from './format';

export const LOCALE_STORAGE_KEY = 'tms.tenant.locale';

const MESSAGES: Record<Locale, typeof en> = { en, sw };

export function readStoredLocale(): Locale | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(LOCALE_STORAGE_KEY);
    return isLocale(raw) ? raw : null;
  } catch {
    return null;
  }
}

function storeLocale(locale: Locale) {
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, locale);
  } catch {
    /* private mode — the session still has the right language in memory */
  }
}

interface LocaleState {
  locale: Locale;
  setLocale: (l: Locale) => void;
  /** True while the user's stored preference is still being written. */
  saving: boolean;
}

const LocaleContext = createContext<LocaleState | null>(null);

export function LocaleProvider({ children }: { children: ReactNode }) {
  const { user } = useMe();
  // The first paint is server-rendered, so it must not read localStorage or
  // navigator: start on the default and settle on the real choice in an effect.
  const [locale, setLocaleState] = useState<Locale>('sw');
  const [chosen, setChosen] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const stored = readStoredLocale();
    setLocaleState(stored ?? guessLocale());
  }, []);

  // The account wins over the browser — but only until the landlord picks a
  // language in this tab, which is a deliberate choice we must not undo.
  const userLocale = (user as { locale?: unknown } | null)?.locale;
  useEffect(() => {
    if (chosen) return;
    if (isLocale(userLocale)) {
      setLocaleState(userLocale);
      storeLocale(userLocale);
    }
  }, [userLocale, chosen]);

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const signedIn = Boolean(user);

  const setLocale = useCallback(
    (next: Locale) => {
      setChosen(true);
      setLocaleState(next);
      storeLocale(next);
      if (!signedIn) return;
      setSaving(true);
      void membersApi
        .updateMe({ locale: next })
        // A 404 is expected until the backend lane lands `PATCH
        // /org/members/me`; the language still works from localStorage.
        .catch(() => undefined)
        .finally(() => setSaving(false));
    },
    [signedIn],
  );

  // The pure formatters in lib/format.tsx read this slot; set it before any
  // child renders so `fmtDate()` never prints a stale language.
  setFormatLocale(locale);

  const value = useMemo<LocaleState>(() => ({ locale, setLocale, saving }), [locale, setLocale, saving]);

  return (
    <LocaleContext.Provider value={value}>
      <I18nProvider locale={locale} messages={MESSAGES} onChange={setLocale}>
        {children}
      </I18nProvider>
    </LocaleContext.Provider>
  );
}

export function useLocaleState(): LocaleState {
  const ctx = useContext(LocaleContext);
  if (!ctx) throw new Error('useLocaleState must be used inside <LocaleProvider>');
  return ctx;
}
