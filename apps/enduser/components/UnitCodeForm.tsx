'use client';

/**
 * "Have a unit code?" — PLAN2 §18.2.
 *
 * A renter who never scanned anything (they registered from a bare `/register`,
 * or the landlord read the code out over the phone during an in-person
 * onboarding) used to land on a home screen whose only advice was "scan your
 * unit's QR code" — with no way to act on a code already in their hand.
 *
 * This is the way forward: type the 10 characters, go to `/u/{code}`, which is
 * exactly where the sticker would have taken them. Nothing is validated against
 * the server here — the unit page already answers a bad code with
 * `unit.notFound`, and it answers it better than a guess could.
 */

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { Icon } from '@iconify/react';
import { useT } from '@tms/ui';

/**
 * Crockford's base-32 alphabet, as the backend mints unit codes with
 * (`crockfordAlphabet` in `qr.go`): no I, L, O or U, so nothing can be
 * confused with 1, 0 or a swear word.
 */
const CROCKFORD = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

export const UNIT_CODE_LENGTH = 10;

/**
 * What the renter typed, as a code: upper-cased, spaces and dashes dropped,
 * and the three letters Crockford deliberately left out folded onto the digits
 * they look like — a code read aloud comes back as "oh" and "ell" more often
 * than not. Anything still outside the alphabet is dropped rather than
 * silently changed.
 */
export function normalizeUnitCode(raw: string): string {
  return raw
    .toUpperCase()
    .replace(/[IL]/g, '1')
    .replace(/O/g, '0')
    .split('')
    .filter((ch) => CROCKFORD.includes(ch))
    .join('')
    .slice(0, UNIT_CODE_LENGTH);
}

export function UnitCodeForm() {
  const t = useT();
  const router = useRouter();
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const clean = normalizeUnitCode(code);
    if (clean.length !== UNIT_CODE_LENGTH) {
      setError(t('home.unitCode.error'));
      return;
    }
    setError(null);
    router.push(`/u/${encodeURIComponent(clean)}`);
  }

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-3)' }} noValidate>
      <h3
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--sp-2)',
          fontSize: 'var(--text-md)',
          margin: 0,
        }}
      >
        <Icon icon="solar:keyboard-linear" width={20} aria-hidden />
        {t('home.unitCode.title')}
      </h3>
      <div className={`field${error ? ' invalid' : ''}`}>
        <label htmlFor="home-unit-code">{t('home.unitCode.label')}</label>
        <input
          id="home-unit-code"
          className="input"
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          inputMode="text"
          maxLength={UNIT_CODE_LENGTH + 4}
          placeholder={t('home.unitCode.placeholder')}
          style={{ letterSpacing: '0.12em', textTransform: 'uppercase' }}
          value={code}
          onChange={(e) => {
            setCode(normalizeUnitCode(e.target.value));
            setError(null);
          }}
        />
        <span className="hint">{t('home.unitCode.hint')}</span>
        {error && <span className="error">{error}</span>}
      </div>
      <button className="btn btn-secondary" type="submit">
        {t('home.unitCode.submit')}
      </button>
    </form>
  );
}
