'use client';

/**
 * The one place a contract's state turns into ink.
 *
 * Design system rule (SPEC §2.0): a stamp is only for what has actually
 * happened — active, ended, terminated. Anything still open is pencilled.
 */

import { useT, type Translator } from '@tms/ui';
import { hasRenterSignature, type Contract, type ContractStatus } from '../lib/api';

export function contractStatusLabel(
  t: Translator,
  status: ContractStatus,
  renterSigned: boolean,
): { text: string; stamped: boolean; tone: 'paid' | 'overdue' | 'plain' } {
  switch (status) {
    case 'pending_signature':
      return renterSigned
        ? { text: t('contract.status.awaitingLandlord'), stamped: false, tone: 'plain' }
        : { text: t('contract.status.awaitingYou'), stamped: false, tone: 'plain' };
    case 'active':
      return { text: t('contract.status.active'), stamped: true, tone: 'paid' };
    case 'expiring':
      return { text: t('contract.status.expiring'), stamped: false, tone: 'plain' };
    case 'ended':
      return { text: t('contract.status.ended'), stamped: true, tone: 'plain' };
    case 'terminated':
      return { text: t('contract.status.terminated'), stamped: true, tone: 'overdue' };
    default:
      return { text: t('contract.status.draft'), stamped: false, tone: 'plain' };
  }
}

export function ContractStamp({
  status,
  renterSigned,
}: {
  status: ContractStatus;
  renterSigned: boolean;
}) {
  const t = useT();
  const mark = contractStatusLabel(t, status, renterSigned);
  if (!mark.stamped) return <span className="pencil">{mark.text}</span>;
  const cls =
    mark.tone === 'paid' ? 'stamp stamp-paid' : mark.tone === 'overdue' ? 'stamp stamp-overdue' : 'stamp';
  return <span className={cls}>{mark.text}</span>;
}

/** Convenience wrapper for a whole contract. */
export function ContractStatusMark({ contract }: { contract: Contract }) {
  return <ContractStamp status={contract.status} renterSigned={hasRenterSignature(contract)} />;
}
