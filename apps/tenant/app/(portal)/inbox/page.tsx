'use client';

/**
 * The landlord's notices (Phase 24): payments reversed or corrected, a
 * duplicate recorded anyway, a proof submitted, a renter's notice to leave.
 * Newest first, a page at a time. Read state is per user and kept by the
 * backend; opening a notice marks it read, and the bell follows.
 */

import { useRouter } from 'next/navigation';
import { useCallback, useState } from 'react';
import { useT } from '@tms/ui';
import { Note, ProblemNote } from '../../../components/FormBits';
import { refreshNavBadges } from '../../../components/NavBadges';
import { PageHead } from '../../../components/PageHead';
import { LoadMore, usePagedList } from '../../../components/Paging';
import { ApiError, inboxApi, toApiError, type InboxItem } from '../../../lib/api';
import { fmtDateTime } from '../../../lib/format';

function InboxBody() {
  const t = useT();
  const router = useRouter();
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const fetchPage = useCallback(
    (cursor: string | undefined, signal?: AbortSignal) => inboxApi.list({ cursor, limit: 50 }, signal),
    [],
  );
  const { items, setItems, error, cursor, loadingMore, loadMore } = usePagedList<InboxItem>(fetchPage);

  const markRead = (ids: string[]) =>
    setItems((prev) => (prev ?? []).map((n) => (ids.includes(n.id) ? { ...n, read: true } : n)));

  const open = async (n: InboxItem) => {
    if (!n.read) {
      markRead([n.id]);
      try {
        await inboxApi.read({ ids: [n.id] });
        refreshNavBadges();
      } catch (e) {
        setActionError(toApiError(e));
      }
    }
    if (n.link) router.push(n.link);
  };

  const readAll = async () => {
    setBusy(true);
    setActionError(null);
    try {
      await inboxApi.read({ all: true });
      setItems((prev) => (prev ?? []).map((n) => ({ ...n, read: true })));
      setNote(t('inbox.all_read'));
      refreshNavBadges();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const unread = (items ?? []).some((n) => !n.read);

  return (
    <>
      <PageHead
        title={t('inbox.title')}
        lead={t('inbox.lead')}
        actions={
          <button type="button" className="btn btn-secondary" onClick={() => void readAll()} disabled={busy || !unread}>
            {t('inbox.mark_all')}
          </button>
        }
      />
      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
        <ProblemNote error={error} />
        <ProblemNote error={actionError} />
        {note ? <Note>{note}</Note> : null}

        {items === null ? (
          <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
        ) : items.length === 0 ? (
          <p style={{ color: 'var(--ink-soft)' }}>{error ? t('common.no_results') : t('inbox.empty')}</p>
        ) : (
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid' }}>
            {items.map((n) => (
              <li key={n.id} style={{ borderBottom: '1px solid var(--rule)' }}>
                <button
                  type="button"
                  onClick={() => void open(n)}
                  style={{
                    display: 'flex',
                    gap: 'var(--sp-3)',
                    alignItems: 'flex-start',
                    width: '100%',
                    padding: 'var(--sp-3) 0',
                    background: 'transparent',
                    border: 0,
                    color: 'var(--ink)',
                    textAlign: 'left',
                    cursor: 'pointer',
                    font: 'inherit',
                  }}
                >
                  <span
                    aria-label={n.read ? undefined : t('inbox.unread')}
                    style={{
                      width: 8,
                      height: 8,
                      marginTop: 8,
                      flex: '0 0 auto',
                      borderRadius: '50%',
                      background: n.read ? 'transparent' : 'var(--primary)',
                    }}
                  />
                  <span style={{ display: 'grid', gap: 'var(--sp-1)', minWidth: 0 }}>
                    <span style={{ fontWeight: n.read ? 400 : 600 }}>{n.title}</span>
                    <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{n.body}</span>
                    <span style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-xs)' }}>
                      {fmtDateTime(n.created_at)}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}

        <LoadMore cursor={cursor} loading={loadingMore} onLoad={() => void loadMore()} />
      </div>
    </>
  );
}

export default function InboxPage() {
  return (
    <>
      <InboxBody />
    </>
  );
}
