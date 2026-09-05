'use client';

/**
 * Unit status in the ledger's own language (SPEC §2.0):
 *  - occupied   → stamped (it has happened: someone lives there)
 *  - vacant     → pencilled (open, waiting)
 *  - maintenance / unlisted → muted, quiet
 */

import type { UnitStatus } from '../lib/api';

export const UNIT_STATUSES: UnitStatus[] = ['vacant', 'occupied', 'maintenance', 'unlisted'];

export function StatusMark({ status, override }: { status: UnitStatus; override?: boolean }) {
  if (status === 'occupied') {
    return (
      <span className="stamp stamp-paid" title="Occupied — derived from an active contract">
        Occupied
      </span>
    );
  }
  if (status === 'vacant') {
    return <span className="pencil">vacant</span>;
  }
  return (
    <span
      style={{
        color: 'var(--ink-faint)',
        fontSize: 'var(--text-sm)',
        textTransform: 'capitalize',
      }}
      title={override ? 'Set by hand' : undefined}
    >
      {status}
    </span>
  );
}
