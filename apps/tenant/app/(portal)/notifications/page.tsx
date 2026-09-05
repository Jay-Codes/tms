'use client';

/**
 * Messages (FLOWS flow 8; API.md Phase 6).
 *
 * Two tabs, because a landlord arrives here for one of two reasons: to check
 * that a reminder actually reached someone, or to tell everybody something.
 * The log is the record of every SMS this org has sent, reminders and custom
 * alike; a failed row is the only one that offers a retry, because the backend
 * has already spent its three attempts on it.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { Suspense, useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import { ProblemNote } from '../../components/FormBits';
import { NotificationLogTable, SendMessageForm } from '../../components/NotificationBits';
import { PageHead, Shell } from '../../components/Shell';
import {
  ApiError,
  KIND_LABELS,
  notificationsApi,
  toApiError,
  type NotificationLogEntry,
} from '../../lib/api';

type TabId = 'log' | 'send';

/** Kinds worth filtering by; the value is what `?kind=` takes (API.md). */
const KIND_OPTIONS = [
  'reminder_7d',
  'reminder_due',
  'overdue_daily',
  'thank_you',
  'unsigned_reminder',
  'custom',
  'link_approved',
  'link_rejected',
  'otp',
];

const STATUS_OPTIONS = ['queued', 'sent', 'failed'];

function LogTab() {
  const [items, setItems] = useState<NotificationLogEntry[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [filters, setFilters] = useState({ kind: '', status: '', from: '', to: '' });
  const [retrying, setRetrying] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setError(null);
      try {
        const res = await notificationsApi.log({ ...filters, limit: 200 }, signal);
        setItems(res.items ?? []);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    },
    [filters],
  );

  useEffect(() => {
    const ac = new AbortController();
    setItems(null);
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const retry = async (entry: NotificationLogEntry) => {
    setRetrying(entry.id);
    setError(null);
    try {
      await notificationsApi.retry(entry.id);
      await load();
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setRetrying(null);
    }
  };

  const set = (k: keyof typeof filters, v: string) => setFilters((f) => ({ ...f, [k]: v }));

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-4)', alignItems: 'flex-end' }}>
        <div className="field" style={{ width: 220 }}>
          <label htmlFor="f_kind">Kind</label>
          <select id="f_kind" className="input" value={filters.kind} onChange={(e) => set('kind', e.target.value)}>
            <option value="">All kinds</option>
            {KIND_OPTIONS.map((k) => (
              <option key={k} value={k}>
                {KIND_LABELS[k] ?? k}
              </option>
            ))}
          </select>
        </div>
        <div className="field" style={{ width: 170 }}>
          <label htmlFor="f_status">Status</label>
          <select id="f_status" className="input" value={filters.status} onChange={(e) => set('status', e.target.value)}>
            <option value="">Any status</option>
            {STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>
                {s[0].toUpperCase() + s.slice(1)}
              </option>
            ))}
          </select>
        </div>
        <div className="field" style={{ width: 170 }}>
          <label htmlFor="f_from">From</label>
          <input id="f_from" className="input" type="date" value={filters.from} onChange={(e) => set('from', e.target.value)} />
        </div>
        <div className="field" style={{ width: 170 }}>
          <label htmlFor="f_to">To</label>
          <input id="f_to" className="input" type="date" value={filters.to} onChange={(e) => set('to', e.target.value)} />
        </div>
        <button type="button" className="btn btn-quiet" onClick={() => void load()} style={{ minHeight: 40 }}>
          <Icon icon="solar:refresh-linear" width={18} /> Refresh
        </button>
      </div>

      <ProblemNote error={error} />

      <NotificationLogTable
        items={items}
        emptyText={error ? 'Nothing to show.' : 'No messages have been sent yet.'}
        onRetry={(n) => void retry(n)}
        retryingId={retrying}
      />
    </div>
  );
}

function NotificationsBody() {
  const initial = (useSearchParams().get('tab') ?? '') as TabId;
  const [tab, setTab] = useState<TabId>(initial === 'send' ? 'send' : 'log');
  // Bumped after a send so the log refetches when the reader flips back to it.
  const [nonce, setNonce] = useState(0);

  return (
    <>
      <PageHead
        title="Messages"
        lead="Every SMS this business has sent, and the one place to send your own."
        actions={
          <Link href="/settings/notifications" className="btn btn-secondary">
            <Icon icon="solar:settings-linear" width={20} /> Notification settings
          </Link>
        }
      />

      <div role="tablist" style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginBottom: 'var(--sp-4)' }}>
        {(
          [
            { value: 'log', label: 'Log' },
            { value: 'send', label: 'Send message' },
          ] as { value: TabId; label: string }[]
        ).map((t) => {
          const active = tab === t.value;
          return (
            <button
              key={t.value}
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

      <div style={{ paddingTop: 'var(--sp-5)' }}>
        {tab === 'log' ? (
          <LogTab key={nonce} />
        ) : (
          <SendMessageForm onSent={() => setNonce((n) => n + 1)} />
        )}
      </div>
    </>
  );
}

export default function NotificationsPage() {
  return (
    <Shell>
      <Suspense fallback={<p style={{ color: 'var(--ink-soft)' }}>Loading…</p>}>
        <NotificationsBody />
      </Suspense>
    </Shell>
  );
}
