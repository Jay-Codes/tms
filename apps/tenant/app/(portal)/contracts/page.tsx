'use client';

/**
 * The contracts ledger (FLOWS flow 3 steps 5–6, flow 6 step 3).
 *
 * Every tab but one is a plain `?status=` filter. "Ready to countersign" is
 * derived rather than stored — a `pending_signature` contract that already
 * carries the renter's signature — so it is filtered client-side over the
 * pending page, which is the same rule the rail badge counts by.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useCallback, useState } from 'react';
import { ContractsTable, isReadyToCountersign } from '../../../components/ContractBits';
import { ProblemNote } from '../../../components/FormBits';
import { NewContractForm } from '../../../components/NewContractForm';
import { LoadMore, usePagedList } from '../../../components/Paging';
import { PageHead } from '../../../components/PageHead';
import { Sheet } from '../../../components/Sheet';
import { EvictionsCard, HoldoversCard } from '../../../components/UnhappyBits';
import { contractsApi, type Contract, type ContractStatus } from '../../../lib/api';
import { useT } from '@tms/ui';

/** `review` (Phase 31) is the owner's queue: changes submitted for approval. */
type TabValue = '' | ContractStatus | 'ready' | 'review';

/** Tab value → the dictionary keys for its label and its empty state. */
const TABS: { value: TabValue; label: string; empty: string }[] = [
  { value: '', label: 'common.all', empty: 'contracts.empty.all' },
  { value: 'pending_signature', label: 'contracts.tab.pending', empty: 'contracts.empty.pending' },
  { value: 'ready', label: 'contracts.tab.ready', empty: 'contracts.empty.ready' },
  { value: 'review', label: 'contracts.tab.review', empty: 'contracts.empty.review' },
  { value: 'active', label: 'contracts.tab.active', empty: 'contracts.empty.active' },
  { value: 'expiring', label: 'contracts.tab.expiring', empty: 'contracts.empty.expiring' },
  { value: 'ended', label: 'contracts.tab.ended', empty: 'contracts.empty.ended' },
  { value: 'terminated', label: 'contracts.tab.terminated', empty: 'contracts.empty.terminated' },
];

function ContractsBody() {
  const t = useT();
  const router = useRouter();
  const [tab, setTab] = useState<TabValue>('');
  const [open, setOpen] = useState(false);

  // "ready" has no server-side status of its own — read the pending pages and
  // keep the ones the renter has already signed, page by page.
  const fetchPage = useCallback(
    async (cursor: string | undefined, signal?: AbortSignal) => {
      const res =
        tab === 'review'
          ? await contractsApi.list({ amendment_stage: 'submitted', limit: 200, cursor }, signal)
          : await contractsApi.list({ status: tab === 'ready' ? 'pending_signature' : tab, limit: 200, cursor }, signal);
      const rows = res.items ?? [];
      return { items: tab === 'ready' ? rows.filter(isReadyToCountersign) : rows, next_cursor: res.next_cursor };
    },
    [tab],
  );
  const { items, error, cursor, loadingMore, loadMore } = usePagedList<Contract>(fetchPage);

  return (
    <>
      <PageHead
        title={t('nav.contracts')}
        lead={t('contracts.lead')}
        actions={
          <>
            <Link href="/contracts/templates" className="btn btn-quiet">
              <Icon icon="solar:documents-linear" width={20} /> {t('contracts.templates_link')}
            </Link>
            <button type="button" className="btn btn-primary" onClick={() => setOpen(true)}>
              <Icon icon="solar:add-square-linear" width={20} /> {t('contracts.new')}
            </button>
          </>
        }
      />

      {/* Phase 22.5: what needs a decision — tenancies that ran out, open evictions. */}
      <div style={{ display: 'grid', gap: 'var(--sp-4)', marginBottom: 'var(--sp-5)' }}>
        <HoldoversCard onRenewed={(c) => router.push(`/contracts/${c.id}`)} />
        <EvictionsCard />
      </div>

      <div role="tablist" aria-label={t('contracts.filter_tabs')} style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginBottom: 'var(--sp-4)' }}>
        {TABS.map((item) => {
          const active = tab === item.value;
          return (
            <button
              key={item.value || 'all'}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setTab(item.value)}
              style={{
                minHeight: 'var(--touch-min)',
                padding: '0 var(--sp-4)',
                border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
                borderRadius: 'var(--radius-md)',
                background: active ? 'var(--primary-soft)' : 'transparent',
                color: 'var(--ink)',
                fontWeight: active ? 600 : 400,
                fontSize: 'var(--text-sm)',
                cursor: 'pointer',
              }}
            >
              {t(item.label)}
            </button>
          );
        })}
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
        <ContractsTable items={items} emptyText={
            error ? t('common.no_results') : t(TABS.find((x) => x.value === tab)?.empty ?? 'contracts.empty.all')
          } />
        <LoadMore cursor={cursor} loading={loadingMore} onLoad={() => void loadMore()} />
      </div>

      <Sheet open={open} title={t('contracts.new')} onClose={() => setOpen(false)} width={640}>
        <NewContractForm onCreated={(c) => router.push(`/contracts/${c.id}`)} />
      </Sheet>
    </>
  );
}

export default function ContractsPage() {
  return (
    <>
      <ContractsBody />
    </>
  );
}
