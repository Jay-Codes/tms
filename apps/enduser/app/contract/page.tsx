'use client';

/**
 * Your agreements — FLOWS.md flow 2 step 7b onwards.
 *
 * `GET /me/contracts` in a ledger: one ruled row per tenancy, pencilled while
 * a signature is still missing and stamped once the contract is real. Tapping
 * a row opens the document itself.
 *
 * Phase 18.2 splits the ledger in two. "Current" is what the renter is living
 * under (`pending_signature`, `active`, `expiring`); "Past" is what has
 * finished (`ended`, `terminated`), folded away behind a count because an old
 * tenancy is a record, not a task — and every past row still opens its
 * document, which is never edited after the end and still verifies.
 *
 * The endpoint returns 50 rows a page with a `next_cursor`; "Show older"
 * appends the page before them, so a renter with years of tenancies keeps all
 * of them rather than silently losing the oldest.
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { Icon } from '@iconify/react';
import { useLocale, useT } from '@tms/ui';
import {
  ApiError,
  contractApi,
  isLiveContractStatus,
  needsRenterSignature,
  type Contract,
} from '../../lib/api';
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

function ContractTable({ contracts }: { contracts: Contract[] }) {
  return (
    <table className="ledger">
      <tbody>
        {contracts.map((c) => (
          <ContractRow key={c.id} contract={c} />
        ))}
      </tbody>
    </table>
  );
}

function ContractListContent() {
  const t = useT();
  const locale = useLocale();
  const [contracts, setContracts] = useState<Contract[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pastOpen, setPastOpen] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await contractApi.mine(signal);
      setContracts(res.items ?? []);
      setCursor(res.next_cursor ?? null);
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

  /* Older pages are appended, never replace what is on screen: a renter who
     tapped "Show older" is reading down the list, not starting again. Rows are
     de-duplicated by id in case a page boundary shifts between calls. */
  async function showOlder() {
    if (!cursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const res = await contractApi.mine(undefined, cursor);
      const older = res.items ?? [];
      setContracts((prev) => {
        const seen = new Set(prev.map((c) => c.id));
        return [...prev, ...older.filter((c) => !seen.has(c.id))];
      });
      setCursor(res.next_cursor ?? null);
      setError(null);
    } catch (err) {
      setError(errorMessage(t, err));
    } finally {
      setLoadingMore(false);
    }
  }

  const toSign = contracts.filter(needsRenterSignature);
  /* §18.2: a finished tenancy is history. `draft` should never reach a renter,
     but if one ever does it belongs with the live rows, not the archive. */
  const current = useMemo(
    () => contracts.filter((c) => isLiveContractStatus(c.status) || c.status === 'draft'),
    [contracts],
  );
  const past = useMemo(
    () => contracts.filter((c) => c.status === 'ended' || c.status === 'terminated'),
    [contracts],
  );

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
        <>
          {current.length > 0 && (
            <section style={{ display: 'grid', gap: 'var(--sp-2)' }}>
              <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('contract.list.current')}</h2>
              <ContractTable contracts={current} />
            </section>
          )}

          {past.length > 0 && (
            <section style={{ display: 'grid', gap: 'var(--sp-2)' }}>
              <button
                type="button"
                onClick={() => setPastOpen((v) => !v)}
                aria-expanded={pastOpen}
                aria-controls="past-contracts"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: 'var(--sp-3)',
                  width: '100%',
                  minHeight: 'var(--touch-min)',
                  padding: 0,
                  background: 'transparent',
                  border: 0,
                  color: 'inherit',
                  cursor: 'pointer',
                  textAlign: 'left',
                }}
              >
                <span>
                  <span style={{ fontSize: 'var(--text-lg)', fontWeight: 600 }}>
                    {t('contract.list.past')}
                  </span>
                  <br />
                  <span className="pencil">{t.n('contract.list.pastCount', past.length)}</span>
                </span>
                <Icon
                  icon={pastOpen ? 'solar:alt-arrow-up-linear' : 'solar:alt-arrow-down-linear'}
                  width={22}
                  aria-hidden
                />
              </button>
              {pastOpen && (
                <div id="past-contracts">
                  <ContractTable contracts={past} />
                </div>
              )}
            </section>
          )}

          {cursor && (
            <button
              type="button"
              className="btn btn-secondary"
              disabled={loadingMore}
              onClick={() => void showOlder()}
            >
              {loadingMore ? t('common.loadingOlder') : t('common.showOlder')}
            </button>
          )}
        </>
      )}

      {/* §18.2: the next payment is a live tenancy's business — an ended one's
          leftover `next_due_date` would be a date nobody is owed on. */}
      {!loading && current.some((c) => c.schedules_summary?.next_due_date) && (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {(() => {
            const summary = current.find((c) => c.schedules_summary?.next_due_date)
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
