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
import { useLocale, useT } from '@tms/ui';
import { ApiError, contractApi, needsRenterSignature, type Contract } from '../../lib/api';
import { errorMessage, formatDate, money, priceLine } from '../../lib/format';
import { Protected } from '../../components/Protected';
import { ContractStatusMark } from '../../components/ContractStatus';
import { Notice, Screen, ScreenHeader } from '../../components/Screen';

function ContractRow({ contract }: { contract: Contract }) {
  const t = useT();
  const locale = useLocale();
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
            {priceLine(t, contract.rent_amount, contract.rent_period_days)}
          </span>
          <br />
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {formatDate(locale, contract.start_date)} – {formatDate(locale, contract.end_date)}
          </span>
        </Link>
      </td>
      <td className="num">
        <Link
          href={`/contract/${encodeURIComponent(contract.id)}`}
          aria-label={t('contract.list.open', { unit: contract.unit.name })}
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
                {t('contract.list.signNow')}
              </span>
            </>
          )}
        </Link>
      </td>
    </tr>
  );
}

function ContractListContent() {
  const t = useT();
  const locale = useLocale();
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
      else setError(errorMessage(t, err));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const toSign = contracts.filter(needsRenterSignature);

  return (
    <Screen bottomBar>
      <ScreenHeader
        eyebrow={t('contract.list.eyebrow')}
        title={t('contract.list.title')}
        lead={t('contract.list.lead')}
      />

      {error && <Notice tone="error">{error}</Notice>}

      {toSign.length > 0 && (
        <Notice>
          {t.n('contract.list.ready', toSign.length)}
        </Notice>
      )}

      {loading ? (
        <p className="pencil">{t('common.loading')}</p>
      ) : contracts.length === 0 ? (
        <section style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <hr className="rule rule-strong" />
          <p className="pencil">{t('contract.list.empty')}</p>
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
            {t('contract.list.emptyHint')}
          </p>
          <Link className="btn btn-secondary" href="/">
            {t('common.backToRentBook')}
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
          {(() => {
            const summary = contracts.find((c) => c.schedules_summary?.next_due_date)
              ?.schedules_summary;
            const date = formatDate(locale, summary?.next_due_date);
            const amount = summary?.next_due_amount;
            return typeof amount === 'number'
              ? t('contract.list.nextWithAmount', { date, amount: money(amount) })
              : t('contract.list.next', { date });
          })()}
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
