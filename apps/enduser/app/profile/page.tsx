'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { authApi } from '../../lib/api';
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
      <ScreenHeader eyebrow="Profile" title={user?.full_name ?? ''} />

      {error && <Notice tone="error">{error}</Notice>}

      <table className="ledger">
        <tbody>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>Name</td>
            <td className="num">{user?.full_name}</td>
          </tr>
          <tr>
            <td style={{ color: 'var(--ink-soft)' }}>Phone</td>
            <td className="num">{user ? displayPhone(user.phone) : ''}</td>
          </tr>
        </tbody>
      </table>

      <KycForm />

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
