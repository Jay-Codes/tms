'use client';

/**
 * SMS sales and stock (Phase 27): what landlords buy through Snippe, what the
 * platform buys from Beem, and whether the second covers the first.
 */

import { useState } from 'react';
import { PageHead } from '../../../components/PageHead';
import { OrdersPanel, PackagesPanel, PurchasesPanel, StockPanel } from '../../../components/SmsSales';

type Tab = 'stock' | 'packages' | 'orders' | 'purchases';

const TABS: { key: Tab; label: string }[] = [
  { key: 'stock', label: 'Stock & margin' },
  { key: 'packages', label: 'Packages' },
  { key: 'orders', label: 'Orders' },
  { key: 'purchases', label: 'Beem purchases' },
];

function SmsBody() {
  const [tab, setTab] = useState<Tab>('stock');
  return (
    <>
      <PageHead
        title="SMS sales"
        lead="Credits landlords buy with mobile money, the Beem stock behind them, and the margin in between."
      />
      <div
        className="tabs"
        role="tablist"
        aria-label="SMS sales sections"
        style={{ gridAutoFlow: 'column', justifyContent: 'start', marginBottom: 'var(--sp-5)', overflowX: 'auto' }}
      >
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            className="tab"
            aria-selected={tab === t.key}
            aria-current={tab === t.key ? 'page' : undefined}
            onClick={() => setTab(t.key)}
          >
            {t.label}
          </button>
        ))}
      </div>
      {tab === 'stock' ? <StockPanel /> : null}
      {tab === 'packages' ? <PackagesPanel /> : null}
      {tab === 'orders' ? <OrdersPanel /> : null}
      {tab === 'purchases' ? <PurchasesPanel /> : null}
    </>
  );
}

export default function SmsPage() {
  return (
    <>
      <SmsBody />
    </>
  );
}
