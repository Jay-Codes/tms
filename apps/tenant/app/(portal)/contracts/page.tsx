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
import { useCallback, useEffect, useState } from 'react';
import { ContractsTable, isReadyToCountersign } from '../../../components/ContractBits';
import { ProblemNote } from '../../../components/FormBits';
import { NewContractForm } from '../../../components/NewContractForm';
import { PageHead } from '../../../components/PageHead';
import { Sheet } from '../../../components/Sheet';
import { ApiError, contractsApi, toApiError, type Contract, type ContractStatus } from '../../../lib/api';
import { useT } from '@tms/ui';

type TabValue = '' | ContractStatus | 'ready';

/** Tab value → the dictionary keys for its label and its empty state. */
const TABS: { value: TabValue; label: string; empty: string }[] = [
  { value: '', label: 'common.all', empty: 'contracts.empty.all' },
  { value: 'pending_signature', label: 'contracts.tab.pending', empty: 'contracts.empty.pending' },
  { value: 'ready', label: 'contracts.tab.ready', empty: 'contracts.empty.ready' },
  { value: 'active', label: 'contracts.tab.active', empty: 'contracts.empty.active' },
  { value: 'expiring', label: 'contracts.tab.expiring', empty: 'contracts.empty.expiring' },
  { value: 'ended', label: 'contracts.tab.ended', empty: 'contracts.empty.ended' },
  { value: 'terminated', label: 'contracts.tab.terminated', empty: 'contracts.empty.terminated' },
];

function ContractsBody() {
  const t = useT();
  const router = useRouter();
  const [tab, setTab] = useState<TabValue>('');
  const [items, setItems] = useState<Contract[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [open, setOpen] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setItems(null);
      // "ready" has no server-side status of its own — read the pending page
      // and keep the ones the renter has already signed.
      const status = tab === 'ready' ? 'pending_signature' : tab;
      try {
        const res = await contractsApi.list({ status, limit: 200 }, signal);
        const rows = res.items ?? [];
        setItems(tab === 'ready' ? rows.filter(isReadyToCountersign) : rows);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [tab],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

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
