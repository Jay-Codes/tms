'use client';

/**
 * Renter home. Phase 1 has no tenancy data yet (units and contracts land in
 * Phases 2–4), so the ledger shows its empty state and points at the QR code
 * on the door — flow 2, step 1.
 */

import { Icon } from '@iconify/react';
import { useMe } from '../lib/auth';
import { Protected } from '../components/Protected';
import { Screen, ScreenHeader } from '../components/Screen';

function firstName(fullName: string): string {
  return fullName.trim().split(/\s+/)[0] || 'there';
}

function HomeContent() {
  const { user } = useMe();

  return (
    <Screen bottomBar>
      <ScreenHeader
        eyebrow="Your rent book"
        title={`Habari, ${user ? firstName(user.full_name) : 'there'}.`}
      />

      <section className="sheet" style={{ padding: 'var(--sp-4)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>Next payment</h2>

        <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
          <tbody>
            <tr>
              <td colSpan={2} style={{ color: 'var(--ink-soft)' }}>
                Nothing to pay yet
              </td>
              <td className="num">
                <span className="pencil">—</span>
              </td>
            </tr>
          </tbody>
        </table>

        <div style={{ display: 'grid', gap: 'var(--sp-3)', paddingTop: 'var(--sp-4)' }}>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            No tenancy yet — scan your unit&rsquo;s QR code.
          </p>
          <p
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-2)',
              color: 'var(--ink-soft)',
              fontSize: 'var(--text-sm)',
            }}
          >
            <Icon icon="solar:qr-code-linear" width={20} aria-hidden />
            The sticker on your door opens this app with the unit already filled in.
          </p>
        </div>
      </section>
    </Screen>
  );
}

export default function HomePage() {
  return (
    <Protected>
      <HomeContent />
    </Protected>
  );
}
