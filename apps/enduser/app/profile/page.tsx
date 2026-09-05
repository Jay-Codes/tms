'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { authApi, renterApi, type RenterProfile } from '../../lib/api';
import { useMe } from '../../lib/auth';
import { displayPhone, errorMessage } from '../../lib/format';
import { Protected } from '../../components/Protected';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';
import { KycForm } from './KycForm';

function ProfileContent() {
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
        setLoadError(errorMessage(err));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [attempt]);

  async function logout() {
    setBusy(true);
    setError(null);
    try {
      await authApi.logout();
      clearSession();
      router.replace('/login');
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  }

  return (
    <Screen bottomBar>
      <ScreenHeader eyebrow="Profile" title={profile?.full_name || user?.full_name || ''} />

      {error && <Notice tone="error">{error}</Notice>}
      {loadError && (
        <>
          <Notice tone="error">{loadError}</Notice>
          <button
            className="btn btn-secondary"
            onClick={() => setAttempt((n) => n + 1)}
            disabled={loading}
          >
            Try again
          </button>
        </>
      )}

      <table className="ledger ledger-kv">
        <tbody>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>Name</td>
            <td className="num">{profile?.full_name || user?.full_name}</td>
          </tr>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>Phone</td>
            <td className="num">{user ? displayPhone(user.phone) : ''}</td>
          </tr>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>NIDA</td>
            <td className="num">
              {profile?.nida_masked ?? <span className="pencil">Not given</span>}
            </td>
          </tr>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>Next of kin</td>
            <td className="num">
              {profile?.next_of_kin_name ? (
                `${profile.next_of_kin_name} · ${displayPhone(profile.next_of_kin_phone)}`
              ) : (
                <span className="pencil">Not given</span>
              )}
            </td>
          </tr>
        </tbody>
      </table>

      {!loading && !loadError && (
        <KycForm
          initialProfile={profile}
          fallbackName={user?.full_name}
          onSaved={setProfile}
          lead="Your landlord sees these when you ask to connect to a unit."
        />
      )}
      {loading && <p className="pencil">Loading your details…</p>}

      <button className="btn btn-danger" onClick={() => void logout()} disabled={busy}>
        {busy ? 'Signing out…' : 'Log out'}
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
