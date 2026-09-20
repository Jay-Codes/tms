'use client';

/** Small pieces shared by the user directory and the user detail page. */

import { userKindLabel, type UserKind } from '../lib/api';

/** `Renter` / `Landlord staff` / `Platform admin`, quietly, next to the name. */
export function KindChip({ kind }: { kind: UserKind | string }) {
  return (
    <span className="pencil" style={{ fontSize: 'var(--text-sm)', whiteSpace: 'nowrap' }}>
      {userKindLabel(kind)}
    </span>
  );
}

/** KYC state, stamped when verified and pencilled while it is not. */
export function KycStamp({ status }: { status: string | null | undefined }) {
  if (!status) return <span className="pencil">not submitted</span>;
  if (status === 'verified') return <span className="stamp stamp-paid">Verified</span>;
  if (status === 'rejected') return <span className="stamp stamp-overdue">Rejected</span>;
  return <span className="pencil">{status.replace(/_/g, ' ')}</span>;
}
