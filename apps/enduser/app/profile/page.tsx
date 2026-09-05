'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useT } from '@tms/ui';
import { authApi, renterApi, type RenterProfile } from '../../lib/api';
import { useMe } from '../../lib/auth';
import { displayPhone, errorMessage } from '../../lib/format';
import { Protected } from '../../components/Protected';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';
import { LanguageToggle } from '../../components/LanguageToggle';
import { KycForm } from './KycForm';

function ProfileContent() {
  const t = useT();
  const { user, clearSession } = useMe();
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [profile, setProfile] = useState<RenterProfile | null>(null);
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
