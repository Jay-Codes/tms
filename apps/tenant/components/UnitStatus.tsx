'use client';

/**
 * Unit status in the ledger's own language (SPEC §2.0):
 *  - occupied   → stamped (it has happened: someone lives there)
 *  - vacant     → pencilled (open, waiting)
 *  - maintenance / unlisted → muted, quiet
 */

import { useT } from '@tms/ui';
import type { UnitStatus } from '../lib/api';

export const UNIT_STATUSES: UnitStatus[] = ['vacant', 'occupied', 'maintenance', 'unlisted'];

export function StatusMark({ status, override }: { status: UnitStatus; override?: boolean }) {
  const t = useT();
  if (status === 'occupied') {
    return (
      <span className="stamp stamp-paid" title={t('units.status.occupied_title')}>
        {t('units.status.occupied')}
      </span>
    );
  }
  if (status === 'vacant') {
    return <span className="pencil">{t('units.status.vacant_pencil')}</span>;
  }
  return (
    <span
      style={{
        color: 'var(--ink-faint)',
        fontSize: 'var(--text-sm)',
      }}
      title={override ? t('units.set_by_hand') : undefined}
    >
      {t(`units.status.${status}`)}
    </span>
  );
}
