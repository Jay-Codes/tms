'use client';

/**
 * The "Rent" row of a contract, as the renter reads it.
 *
 * Rent is quoted per unit period (`rent_amount` / `rent_period_days`) but
 * *paid* per payment period, so the headline is the amount actually handed
 * over — `TZS 300,000 / Quarterly (90 days)` — with the unit price kept
 * underneath as the basis (SPEC §6, PLAN2 Phase 9).
 *
 * `rent_per_period` is optional on the wire: an older API sends only the
 * basis, and then the basis is the whole line. When the payment period is
 * the unit period (the common monthly case) both lines say the same thing,
 * so only one is drawn.
 */

import { days, money, priceLine } from '../lib/format';
import type { Contract } from '../lib/api';

export function RentValue({ contract }: { contract: Contract }) {
  const basis = priceLine(contract.rent_amount, contract.rent_period_days);
  const perPeriod = contract.rent_per_period;
  const period = contract.payment_period;

  const sameAsBasis =
    perPeriod === contract.rent_amount && period?.days === contract.rent_period_days;

  if (typeof perPeriod !== 'number' || !period || sameAsBasis) {
    return <span className="nowrap">{basis}</span>;
  }

  return (
    <>
      <span className="nowrap">{money(perPeriod)}</span>{' '}
      <span className="nowrap">
        / {period.label ? `${period.label} (${days(period.days)})` : days(period.days)}
      </span>
      <span className="sub">{basis}</span>
    </>
  );
}
