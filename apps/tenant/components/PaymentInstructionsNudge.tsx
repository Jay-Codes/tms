'use client';

/**
 * Phase 16 §16.4 — "Add payment instructions so renters know where to pay".
 *
 * A renter who cannot find an account number pays late, or pays the wrong
 * person; the landlord rarely notices the gap because their own screens never
 * print it. So the dashboard says it, once, until it is fixed.
 *
 * The flag is the backend's (`payment_instructions_set` on
 * `GET /org/bank-account`) — the frontend decides nothing about it. Only the
 * *dismissal* is local, keyed by org id so two orgs on one browser do not
 * silence each other, and it is dropped the moment the account is cleared
 * again, which is what makes the banner come back.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { bankAccountApi, readBankAccountView } from '../lib/api';
import { useMe } from '../lib/auth';

const KEY = (orgId: string) => `tms.nudge.pay_instructions.${orgId}`;

/** localStorage is a convenience, never a dependency: a throw means "not dismissed". */
function readDismissed(orgId: string): boolean {
  try {
    return window.localStorage.getItem(KEY(orgId)) === '1';
  } catch {
    return false;
  }
}

function writeDismissed(orgId: string, value: boolean): void {
  try {
    if (value) window.localStorage.setItem(KEY(orgId), '1');
    else window.localStorage.removeItem(KEY(orgId));
  } catch {
    /* private mode, blocked site data — the banner simply returns next load */
  }
}

/**
 * Reads the flag once per mount. `null` while unknown — the banner draws
 * nothing rather than flashing on and off.
 */
export function usePaymentInstructionsSet(): boolean | null {
  const [set, setSet] = useState<boolean | null>(null);
  useEffect(() => {
    const ac = new AbortController();
    bankAccountApi
      .get(ac.signal)
      .then((res) => setSet(readBankAccountView(res).payment_instructions_set))
      .catch((e) => {
        // "Never set" reads as a 404 on some deployments; that is a no, not an
        // outage. Any other failure leaves the nudge silent.
        if (e instanceof DOMException) return;
        const status = (e as { status?: number }).status;
        setSet(status === 404 ? false : null);
      });
    return () => ac.abort();
  }, []);
  return set;
}

export function PaymentInstructionsNudge() {
  const t = useT();
  const { org } = useMe();
  const orgId = org?.id ?? '';
  const isSet = usePaymentInstructionsSet();
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => {
    if (!orgId) return;
    setDismissed(readDismissed(orgId));
  }, [orgId]);

  // Set again → forget the dismissal, so clearing the account later brings the
  // banner back without the landlord having to know why it went quiet.
  useEffect(() => {
    if (orgId && isSet === true) writeDismissed(orgId, false);
  }, [orgId, isSet]);

  if (isSet !== false || dismissed) return null;

  return (
    <p
      role="status"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-2)',
        flexWrap: 'wrap',
        margin: 'var(--sp-4) 0 0',
        padding: 'var(--sp-2) var(--sp-3)',
        border: '1px solid var(--rule-strong)',
        borderRadius: 'var(--radius-sm)',
        fontSize: 'var(--text-sm)',
        color: 'var(--ink)',
      }}
    >
      <Icon icon="solar:card-transfer-linear" width={18} style={{ flexShrink: 0 }} />
      <span style={{ flex: 1, minWidth: 200 }}>{t('nudge.pay_instructions')}</span>
      <Link href="/settings/bank-account" className="btn btn-quiet" style={{ minHeight: 36 }}>
        {t('nudge.pay_instructions.action')}
      </Link>
      <button
        type="button"
        className="btn btn-quiet"
        style={{ minHeight: 36 }}
        onClick={() => {
          if (orgId) writeDismissed(orgId, true);
          setDismissed(true);
        }}
      >
        {t('common.dismiss')}
      </button>
    </p>
  );
}
