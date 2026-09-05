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
import { ContractsTable, isReadyToCountersign } from '../../components/ContractBits';
import { ProblemNote } from '../../components/FormBits';
import { NewContractForm } from '../../components/NewContractForm';
import { PageHead, Shell } from '../../components/Shell';
import { Sheet } from '../../components/Sheet';
import { ApiError, contractsApi, toApiError, type Contract, type ContractStatus } from '../../lib/api';

type TabValue = '' | ContractStatus | 'ready';

const TABS: { value: TabValue; label: string }[] = [
  { value: '', label: 'All' },
  { value: 'pending_signature', label: 'Awaiting signature' },
  { value: 'ready', label: 'Ready to countersign' },
  { value: 'active', label: 'Active' },
  { value: 'expiring', label: 'Expiring' },
  { value: 'ended', label: 'Ended' },
  { value: 'terminated', label: 'Terminated' },
];

const EMPTY: Record<string, string> = {
  '': 'No contracts yet. Approve a link request, or write one here.',
  pending_signature: 'Nothing is waiting for a renter signature.',
  ready: 'Nothing is waiting for your countersignature.',
  active: 'No active contracts.',
  expiring: 'Nothing is close to its end date.',
  ended: 'No contracts have run their course yet.',
  terminated: 'Nothing has been terminated.',
};

function ContractsBody() {
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
        title="Contracts"
        lead="Every tenancy this business has issued, and what each one is waiting for."
        actions={
          <>
            <Link href="/contracts/templates" className="btn btn-quiet">
              <Icon icon="solar:documents-linear" width={20} /> Templates
            </Link>
            <button type="button" className="btn btn-primary" onClick={() => setOpen(true)}>
              <Icon icon="solar:add-square-linear" width={20} /> New contract
            </button>
          </>
        }
      />

      <div role="tablist" style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginBottom: 'var(--sp-4)' }}>
        {TABS.map((t) => {
          const active = tab === t.value;
          return (
            <button
              key={t.value || 'all'}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setTab(t.value)}
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
              {t.label}
            </button>
          );
        })}
      </div>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
        <ContractsTable items={items} emptyText={error ? 'Nothing to show.' : EMPTY[tab]} />
      </div>

      <Sheet open={open} title="New contract" onClose={() => setOpen(false)} width={640}>
        <NewContractForm onCreated={(c) => router.push(`/contracts/${c.id}`)} />
      </Sheet>
    </>
  );
}

export default function ContractsPage() {
  return (
    <Shell>
      <ContractsBody />
    </Shell>
  );
}
