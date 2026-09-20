'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useLocale, useT } from '@tms/ui';
import { authApi, renterApi, type NidaReveal, type RenterProfile } from '../../lib/api';
import { useMe } from '../../lib/auth';
import { displayPhone, errorMessage, formatDate } from '../../lib/format';
import { Protected } from '../../components/Protected';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';
import { LanguageToggle } from '../../components/LanguageToggle';
import { KycForm } from './KycForm';

/**
 * Phase 19.1 — "Who has seen your NIDA".
 *
 * A landlord can ask the API for the renter's full national ID number, and SPEC
 * §8 lets them: a police report or a lease filed at the ward office needs the
 * real digits. What it does not let them do is look unseen. Every reveal is
 * audited, and `GET /me/profile` hands the last ten of them back here, so the
 * renter can see exactly who looked and when. That visibility *is* the control.
 */
function NidaReveals({ reveals }: { reveals: NidaReveal[] }) {
  const t = useT();
  const locale = useLocale();
  return (
    <section style={{ display: 'grid', gap: 'var(--sp-2)' }}>
      <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('profile.reveals.title')}</h2>
      <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        {t('profile.reveals.lead')}
      </p>
      {reveals.length === 0 ? (
        <p className="pencil">{t('profile.reveals.none')}</p>
      ) : (
        <table className="ledger">
          <tbody>
            {reveals.map((r, i) => (
              <tr key={`${r.at}-${i}`}>
                <td>
                  {r.by_kind === 'platform_admin'
                    ? t('profile.reveals.admin')
                    : r.org_name
                      ? t('profile.reveals.landlord', { org: r.org_name })
                      : t('profile.reveals.landlordUnknown')}
                </td>
                <td className="num">
                  <span style={{ fontSize: 'var(--text-sm)' }}>{formatDate(locale, r.at)}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function ProfileContent() {
  const t = useT();
  const { user, clearSession } = useMe();
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [profile, setProfile] = useState<RenterProfile | null>(null);
  const [reveals, setReveals] = useState<NidaReveal[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  /* The page owns the profile fetch so the ledger and the KYC form below it
     always show the same answer from one round-trip. The form stays hidden
     while that fetch is failing: `PUT /me/profile` replaces the whole record,
     and an empty form saved over a profile we never managed to read would
     wipe the renter's next-of-kin details. */
  useEffect(() => {
    const ac = new AbortController();
    let live = true;
    setLoading(true);
    (async () => {
      try {
        const res = await renterApi.profile(ac.signal);
        if (!live) return;
        setProfile(res.profile);
        // Absent on an API older than Phase 19 — then the list is simply empty.
        setReveals(res.nida_reveals ?? []);
        setLoadError(null);
      } catch (err) {
        if (!live || (err instanceof DOMException && err.name === 'AbortError')) return;
        setLoadError(errorMessage(t, err));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [attempt, t]);

  async function logout() {
    setBusy(true);
    setError(null);
    try {
      await authApi.logout();
      clearSession();
      router.replace('/login');
    } catch (err) {
      setError(errorMessage(t, err));
      setBusy(false);
    }
  }

  return (
    <Screen bottomBar>
      <ScreenHeader eyebrow={t('profile.eyebrow')} title={profile?.full_name || user?.full_name || ''} />

      {error && <Notice tone="error">{error}</Notice>}
      {loadError && (
        <>
          <Notice tone="error">{loadError}</Notice>
          <button
            className="btn btn-secondary"
            onClick={() => setAttempt((n) => n + 1)}
            disabled={loading}
          >
            {t('common.tryAgain')}
          </button>
        </>
      )}

      <table className="ledger ledger-kv">
        <tbody>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>{t('profile.name')}</td>
            <td className="num">{profile?.full_name || user?.full_name}</td>
          </tr>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>{t('profile.phone')}</td>
            <td className="num">{user ? displayPhone(user.phone) : ''}</td>
          </tr>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>{t('profile.nida')}</td>
            <td className="num">
              {profile?.nida_masked ?? <span className="pencil">{t('profile.notGiven')}</span>}
            </td>
          </tr>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>{t('profile.nextOfKin')}</td>
            <td className="num">
              {profile?.next_of_kin_name ? (
                `${profile.next_of_kin_name} · ${displayPhone(profile.next_of_kin_phone)}`
              ) : (
                <span className="pencil">{t('profile.notGiven')}</span>
              )}
            </td>
          </tr>
          {/* Phase 13: switching here also saves the choice on the account
              (`PATCH /me {locale}`), so the SMS a renter gets follows the
              language they read the app in. */}
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>{t('lang.label')}</td>
            <td className="num">
              <LanguageToggle align="end" />
            </td>
          </tr>
        </tbody>
      </table>

      {/* §19.1: only worth a section once the renter has a number to look at. */}
      {!loading && !loadError && profile?.nida_masked && <NidaReveals reveals={reveals} />}

      {!loading && !loadError && (
        <KycForm
          initialProfile={profile}
          fallbackName={user?.full_name}
          onSaved={setProfile}
          lead={t('profile.kycLead')}
        />
      )}
      {loading && <p className="pencil">{t('profile.loading')}</p>}

      <button className="btn btn-danger" onClick={() => void logout()} disabled={busy}>
        {busy ? t('profile.loggingOut') : t('profile.logout')}
      </button>
    </Screen>
  );
}

export default function ProfilePage() {
  return (
    <Protected>
      <ProfileContent />
    </Protected>
  );
}
