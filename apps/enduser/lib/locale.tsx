'use client';

/**
 * Which language the rent book speaks (SPEC §3.2, PLAN2 Phase 13).
 *
 * The order the active locale is decided in, strongest first:
 *
 *   1. the signed-in renter's `user.locale` (the API is the record of it)
 *   2. `localStorage['tms.enduser.locale']` — what they last chose here
 *   3. `?lang=sw|en` on the URL (an SMS or a landlord's link can carry it)
 *   4. the landlord's default language, remembered from a branding payload
 *   5. `guessLocale()` — the device, falling back to Kiswahili
 *
 * `setLocale` is the one way it changes: it re-renders, persists, tells the
 * API (`PATCH /me {locale}`) when there is a session to attach it to, and
 * stamps `<html lang>`. A 404/405 from that PATCH is ignored on purpose — the
 * backend lane may not have landed yet and a renter switching language must
 * not see an error for it.
 *
 * Resolution happens after mount, never during render: these pages are
 * prerendered as static HTML, so reading storage while rendering would
 * hydrate one language into markup built for another.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { usePathname } from 'next/navigation';
import {
  DEFAULT_LOCALE,
  I18nProvider,
  guessLocale,
  isLocale,
  useI18n,
  type Locale,
  type Messages,
} from '@tms/ui';
import en from '../i18n/en';
import sw from '../i18n/sw';
import { renterApi, type PublicBranding } from './api';
import { useMe } from './auth';

const STORE_KEY = 'tms.enduser.locale';
const ORG_KEY = 'tms.enduser.org-locale';

const MESSAGES: Record<Locale, Messages> = { en, sw };

/* ------------------------------------------------------------------ */
/* storage                                                             */
/* ------------------------------------------------------------------ */

function read(key: string): Locale | null {
  try {
    const raw = window.localStorage.getItem(key);
    return isLocale(raw) ? raw : null;
  } catch {
    // Private mode / blocked storage: fall through to the next source.
    return null;
  }
}

function write(key: string, locale: Locale): void {
  try {
    window.localStorage.setItem(key, locale);
  } catch {
    // The choice still holds for this page view.
  }
}

/** The renter's stored choice, if they have made one on this device. */
export function readStoredLocale(): Locale | null {
  if (typeof window === 'undefined') return null;
  return read(STORE_KEY);
}

/** The org's default, as last seen on a branding payload. */
export function readOrgLocale(): Locale | null {
  if (typeof window === 'undefined') return null;
  return read(ORG_KEY);
}

/**
 * Remember the landlord's default language from any branding payload the app
 * happens to fetch. `language` is the Phase 13 field; `sms_language` is the
 * older name for the same setting, and an API serving neither leaves the
 * previous answer alone.
 */
export function rememberOrgLocale(branding: PublicBranding | null | undefined): void {
  if (typeof window === 'undefined' || !branding) return;
  const raw = branding.language ?? branding.sms_language;
  if (isLocale(raw)) write(ORG_KEY, raw);
}

function queryLocale(): Locale | null {
  if (typeof window === 'undefined') return null;
  const raw = new URLSearchParams(window.location.search).get('lang');
  return isLocale(raw) ? raw : null;
}

/** Everything except the signed-in user, who is only known once /auth/me answers. */
function resolveLocale(): Locale {
  return readStoredLocale() ?? queryLocale() ?? readOrgLocale() ?? guessLocale();
}

/* ------------------------------------------------------------------ */
/* provider                                                            */
/* ------------------------------------------------------------------ */

export function LocaleProvider({ children }: { children: React.ReactNode }) {
  const { user, status } = useMe();
  const [locale, setLocaleState] = useState<Locale>(DEFAULT_LOCALE);
  /* Once the renter picks a language in this session it outranks whatever the
     session payload says — the PATCH may still be in flight. */
  const picked = useRef(false);

  useEffect(() => {
    setLocaleState(resolveLocale());
  }, []);

  useEffect(() => {
    if (picked.current) return;
    const theirs = user?.locale;
    if (isLocale(theirs)) {
      setLocaleState(theirs);
      write(STORE_KEY, theirs);
    }
  }, [user]);

  const setLocale = useCallback(
    (next: Locale) => {
      picked.current = true;
      setLocaleState(next);
      write(STORE_KEY, next);
      if (status === 'authenticated') {
        void renterApi.updateLocale(next).catch(() => {
          // Not yet served by this backend — the choice is still local.
        });
      }
    },
    [status],
  );

  return (
    <I18nProvider locale={locale} messages={MESSAGES} onChange={setLocale}>
      <DocumentLanguage />
      {children}
    </I18nProvider>
  );
}

/**
 * Keeps `<html lang>` and the tab title in step with the chosen language.
 *
 * The title needs more than one write. `metadata` in app/layout.tsx is a
 * build-time constant in the platform's default language, and Next re-applies
 * it after every client-side navigation — sometimes a tick *after* this effect
 * has run. So the effect stamps the title, and an observer on the `<title>`
 * node puts the renter's language back whenever something else overwrites it.
 * `pathname` re-runs the whole thing on navigation, and the guard against our
 * own writes keeps the observer from looping.
 */
function DocumentLanguage() {
  const { locale, t } = useI18n();
  const pathname = usePathname();

  useEffect(() => {
    document.documentElement.lang = locale;

    const title = t('app.title');
    const apply = () => {
      if (document.title !== title) document.title = title;
    };
    apply();

    const node = document.querySelector('title');
    if (!node) return;
    const observer = new MutationObserver(apply);
    observer.observe(node, { childList: true, characterData: true, subtree: true });
    return () => observer.disconnect();
  }, [locale, t, pathname]);

  return null;
}

/* ------------------------------------------------------------------ */
/* hooks                                                               */
/* ------------------------------------------------------------------ */

/** The current locale plus the setter, for the SW/EN toggle. */
export function useLocaleChoice(): { locale: Locale; setLocale: (l: Locale) => void } {
  const { locale, setLocale } = useI18n();
  return useMemo(() => ({ locale, setLocale: setLocale ?? (() => undefined) }), [locale, setLocale]);
}
