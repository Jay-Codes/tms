'use client';

/**
 * Platform message templates — `GET /admin/templates` (API.md Phase 14).
 *
 * These are the words every landlord's messages fall back to: an org override
 * wins, then this row, then the built-in Go default. Editing one here changes
 * what every org without an override sends, so the list leads with the
 * version and the last editor rather than the body.
 */

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { TableScroll } from '@tms/ui';
import { ProblemNote } from '../../../components/FormBits';
import { PageHead } from '../../../components/PageHead';
import { ApiError, adminApi, toApiError, type AdminTemplate } from '../../../lib/api';
import { fmtDateTime } from '../../../lib/format';
import { kindLabel, smsSegments } from '../../../lib/sms';

function Segments({ body }: { body: string }) {
  const { segments, unicode } = smsSegments(body ?? '');
  return (
    <span
      className="num"
      title={unicode ? 'Unicode body — 70 characters per segment' : '160 characters per segment'}
      style={{ color: segments > 3 ? 'var(--stamp-overdue)' : undefined }}
    >
      {segments}
      {unicode ? ' ·U' : ''}
    </span>
  );
}

export default function TemplatesPage() {
  const [items, setItems] = useState<AdminTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    try {
      const res = await adminApi.templates(signal);
      setItems(res.items ?? []);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  return (
    <>
      <PageHead
        title="Templates"
        lead="The platform wording for every notification kind, in Kiswahili and English."
        actions={
          <button type="button" className="btn btn-secondary" disabled={loading} onClick={() => void load()}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
        }
      />

      <ProblemNote error={error} />

      <p style={{ color: 'var(--ink-soft)', maxWidth: '62ch', marginBottom: 'var(--sp-4)' }}>
        At send time the resolution order is: the organization&apos;s own override, then the platform
        template below, then the built-in default. Locking a kind removes the first step.
      </p>

      <TableScroll label="Platform templates">
        <table className="ledger">
          <thead>
            <tr>
              <th>Kind</th>
              <th className="num">SW segments</th>
              <th className="num">EN segments</th>
              <th className="num">Version</th>
              <th>Last edited</th>
              <th>Override</th>
            </tr>
          </thead>
          <tbody>
            {items.length === 0 && !loading ? (
              <tr>
                <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                  No templates returned.
                </td>
              </tr>
            ) : (
              items.map((t) => (
                <tr key={t.kind}>
                  <td>
                    <Link href={`/templates/${t.kind}`} style={{ fontWeight: 500, color: 'inherit' }}>
                      {kindLabel(t.kind)}
                    </Link>
                    <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      <code>{t.kind}</code>
                    </div>
                  </td>
                  <td className="num">
                    <Segments body={t.sw} />
                  </td>
                  <td className="num">
                    <Segments body={t.en} />
                  </td>
                  <td className="num">{t.version}</td>
                  <td>
                    {t.updated_at ? (
                      <>
                        <div style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(t.updated_at)}</div>
                        <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                          {t.updated_by ?? 'seeded'}
                        </div>
                      </>
                    ) : (
                      <span className="pencil">never edited</span>
                    )}
                  </td>
                  <td>
                    {t.locked ? (
                      <span className="stamp stamp-overdue" title="Landlords cannot override this kind">
                        Locked
                      </span>
                    ) : (
                      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>Allowed</span>
                    )}
                  </td>
                </tr>
              ))
            )}
            {loading ? (
              <tr>
                <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </TableScroll>
    </>
  );
}
