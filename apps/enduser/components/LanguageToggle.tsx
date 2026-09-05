'use client';

/**
 * SW / EN — two segments, both a full touch target (SPEC §2.1: 44px).
 *
 * It sits wherever the renter might need it before the app knows who they
 * are (login, register, the QR landing) and once it does (Profile). Choosing
 * here is what registration sends as `locale`, and what `PATCH /me` stores
 * for a signed-in renter — see lib/locale.tsx.
 */

import { LOCALES, useT, type Locale } from '@tms/ui';
import { useLocaleChoice } from '../lib/locale';

export function LanguageToggle({
  align = 'center',
}: {
  /** `center` under a form, `end` in a header row. */
  align?: 'center' | 'start' | 'end';
}) {
  const t = useT();
  const { locale, setLocale } = useLocaleChoice();

  return (
    <div
      role="group"
      aria-label={t('lang.aria')}
      style={{
        display: 'flex',
        gap: 'var(--sp-2)',
        justifyContent: align === 'center' ? 'center' : `flex-${align}`,
      }}
    >
      {LOCALES.map((l: Locale) => {
        const active = l === locale;
        return (
          <button
            key={l}
            type="button"
            className={active ? 'btn btn-secondary' : 'btn btn-quiet'}
            aria-pressed={active}
            lang={l}
            onClick={() => setLocale(l)}
            style={{
              width: 'auto',
              minWidth: 'var(--touch-min)',
              height: 'var(--touch-min)',
              paddingInline: 'var(--sp-3)',
              fontWeight: active ? 600 : 400,
            }}
          >
            {t(`lang.${l}`)}
          </button>
        );
      })}
    </div>
  );
}
