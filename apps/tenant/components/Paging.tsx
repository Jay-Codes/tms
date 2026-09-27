'use client';

/**
 * Phase 23 — keyset paging for the list screens. Every list endpoint answers
 * `{items, next_cursor}` (null on the last page); a screen shows the first
 * page and a "Load more" button that follows the cursor and appends. The
 * cursor is opaque — nothing here reads it.
 */

import { useCallback, useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { ApiError, toApiError } from '../lib/api';

export interface Page<T> {
  items?: T[] | null;
  next_cursor?: string | null;
}

/**
 * `fetchPage` must be stable (useCallback) and change only when the filters
 * do: a new one starts the list again from the first page.
 */
export function usePagedList<T>(
  fetchPage: (cursor: string | undefined, signal?: AbortSignal) => Promise<Page<T>>,
) {
  const [items, setItems] = useState<T[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  /** Re-read the first page in place (after a change), keeping rows on screen until it lands. */
  const reload = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const res = await fetchPage(undefined, signal);
        setItems(res.items ?? []);
        setCursor(res.next_cursor ?? null);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
        setCursor(null);
      }
    },
    [fetchPage],
  );

  useEffect(() => {
    setItems(null);
    setCursor(null);
    const ac = new AbortController();
    void reload(ac.signal);
    return () => ac.abort();
  }, [reload]);

  const loadMore = useCallback(async () => {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const res = await fetchPage(cursor);
      setItems((prev) => [...(prev ?? []), ...(res.items ?? [])]);
      setCursor(res.next_cursor ?? null);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setLoadingMore(false);
    }
  }, [cursor, fetchPage]);

  return { items, setItems, error, cursor, loadingMore, loadMore, reload };
}

/** The "Load more" button the expenses and audit pages already use. */
export function LoadMore({
  cursor,
  loading,
  onLoad,
}: {
  cursor: string | null;
  loading: boolean;
  onLoad: () => void;
}) {
  const t = useT();
  if (!cursor) return null;
  return (
    <div>
      <button type="button" className="btn btn-secondary" onClick={onLoad} disabled={loading}>
        {loading ? t('common.loading_more') : t('common.load_more')}
      </button>
    </div>
  );
}
