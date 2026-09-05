'use client';

/**
 * The one place a contract's state turns into ink.
 *
 * Design system rule (SPEC §2.0): a stamp is only for what has actually
 * happened — active, ended, terminated. Anything still open is pencilled.
 */

import { hasRenterSignature, type Contract, type ContractStatus } from '../lib/api';

export function contractStatusLabel(
  status: ContractStatus,
  renterSigned: boolean,
): { text: string; stamped: boolean; tone: 'paid' | 'overdue' | 'plain' } {
  switch (status) {
    case 'pending_signature':
      return renterSigned
        ? { text: 'Waiting for landlord to countersign', stamped: false, tone: 'plain' }
        : { text: 'Awaiting your signature', stamped: false, tone: 'plain' };
    case 'active':
      return { text: 'Active', stamped: true, tone: 'paid' };
    case 'expiring':
      return { text: 'Ending soon', stamped: false, tone: 'plain' };
    case 'ended':
      return { text: 'Ended', stamped: true, tone: 'plain' };
    case 'terminated':
      return { text: 'Terminated', stamped: true, tone: 'overdue' };
    default:
      return { text: 'Draft', stamped: false, tone: 'plain' };
  }
}

export function ContractStamp({
  status,
  renterSigned,
}: {
  status: ContractStatus;
  renterSigned: boolean;
}) {
  const mark = contractStatusLabel(status, renterSigned);
  if (!mark.stamped) return <span className="pencil">{mark.text}</span>;
  const cls =
    mark.tone === 'paid' ? 'stamp stamp-paid' : mark.tone === 'overdue' ? 'stamp stamp-overdue' : 'stamp';
  return <span className={cls}>{mark.text}</span>;
}

/** Convenience wrapper for a whole contract. */
export function ContractStatusMark({ contract }: { contract: Contract }) {
  return <ContractStamp status={contract.status} renterSigned={hasRenterSignature(contract)} />;
}
