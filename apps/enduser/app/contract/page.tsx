'use client';

/**
 * Your agreements — FLOWS.md flow 2 step 7b onwards.
 *
 * `GET /me/contracts` in a ledger: one ruled row per tenancy, pencilled while
 * a signature is still missing and stamped once the contract is real. Tapping
 * a row opens the document itself.
 */

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { Icon } from '@iconify/react';
import { ApiError, contractApi, needsRenterSignature, type Contract } from '../../lib/api';
import { errorMessage, formatDate, money, priceLine } from '../../lib/format';
import { Protected } from '../../components/Protected';
import { ContractStatusMark } from '../../components/ContractStatus';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';

function ContractRow({ contract }: { contract: Contract }) {
  const sign = needsRenterSignature(contract);
  return (
    <tr>
      <td>
        <Link
          href={`/contract/${encodeURIComponent(contract.id)}`}
          style={{ color: 'inherit', textDecoration: 'none', display: 'block' }}
        >
          <span style={{ fontWeight: 600 }}>
            {contract.unit.name} · {contract.unit.property_name}
          </span>
          <br />
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {priceLine(contract.rent_amount, contract.rent_period_days)}
          </span>
          <br />
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {formatDate(contract.start_date)} – {formatDate(contract.end_date)}
          </span>
        </Link>
      </td>
      <td className="num">
        <Link
          href={`/contract/${encodeURIComponent(contract.id)}`}
          aria-label={`Open the agreement for ${contract.unit.name}`}
          style={{ color: 'inherit', textDecoration: 'none', display: 'block' }}
        >
          <ContractStatusMark contract={contract} />
          <br />
          <span style={{ fontSize: 'var(--text-sm)' }}>{contract.payment_period.label}</span>
          {sign && (
            <>
              <br />
              <span
                style={{
                  fontSize: 'var(--text-sm)',
                  fontWeight: 600,
                  color: 'var(--primary)',
                  whiteSpace: 'nowrap',
                }}
              >
                Sign now →
              </span>
            </>
          )}
        </Link>
      </td>
    </tr>
  );
}

function ContractListContent() {
  const [contracts, setContracts] = useState<Contract[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await contractApi.mine(signal);
      setContracts(res.items ?? []);
      setError(null);
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      // Nothing on file reads as an empty ledger, not as a failure.
      if (err instanceof ApiError && err.status === 404) setContracts([]);
      else setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const toSign = contracts.filter(needsRenterSignature);

  return (
    <Screen bottomBar>
      <ScreenHeader
        eyebrow="Your agreements"
        title="Contracts"
        lead="Read, sign and print your tenancy agreement."
      />

      {error && <Notice tone="error">{error}</Notice>}

      {toSign.length > 0 && (
        <Notice>
          {toSign.length === 1
            ? 'One contract is ready for your signature.'
            : `${toSign.length} contracts are ready for your signature.`}
        </Notice>
      )}

      {loading ? (
        <p className="pencil">Loading…</p>
      ) : contracts.length === 0 ? (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <hr className="rule rule-strong" />
          <p className="pencil">No agreement yet.</p>
          <p
            style={{
              display: 'flex',
              gap: 'var(--sp-2)',
              alignItems: 'center',
              color: 'var(--ink-soft)',
              fontSize: 'var(--text-sm)',
            }}
          >
            <Icon icon="solar:document-text-linear" width={20} aria-hidden />
            Once a landlord approves your unit, the contract appears here to sign.
          </p>
          <Link className="btn btn-secondary" href="/">
            Back to your rent book
          </Link>
        </section>
      ) : (
        <table className="ledger">
          <tbody>
            {contracts.map((c) => (
              <ContractRow key={c.id} contract={c} />
            ))}
          </tbody>
        </table>
      )}

      {!loading && contracts.some((c) => c.schedules_summary?.next_due_date) && (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          Next payment{' '}
          {formatDate(
            contracts.find((c) => c.schedules_summary?.next_due_date)?.schedules_summary
              ?.next_due_date,
          )}
          {(() => {
            const amount = contracts.find((c) => c.schedules_summary?.next_due_date)
              ?.schedules_summary?.next_due_amount;
            return typeof amount === 'number' ? ` · ${money(amount)}` : '';
          })()}
          .
        </p>
      )}
    </Screen>
  );
}

export default function ContractPage() {
  return (
    <Protected>
      <ContractListContent />
    </Protected>
  );
}
