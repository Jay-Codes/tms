'use client';

/**
 * Settings → Import data (PLAN2 Part 2 §16.2, API.md §16.2).
 *
 * An import is two movements, and the screen is built in that shape: a
 * **preview** that writes nothing but a record of the file and what each line
 * would do, and a **commit** that runs the ok rows in one transaction. The
 * landlord reads the middle step — line by line, error under the offending
 * cell — before anything is written, and has 24 hours to take it back.
 *
 * No CSV is parsed here. The browser checks the size ceiling so an oversized
 * file is not uploaded for nothing, and everything else comes back from the
 * server: the column reference, the row errors, the resolved names, the
 * created counts. The frontend holds no import logic at all.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { ProblemNote } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import {
  ApiError,
  downloadBlob,
  importHeaderMismatch,
  importRowFailure,
  importsApi,
  toApiError,
  type ImportBatch,
  type ImportCreatedCounts,
  type ImportKind,
  type ImportPreview,
  type ImportTemplateColumn,
} from '../../../../lib/api';
import {
  ColumnReference,
  DropZone,
  HeaderMismatch,
  HistoryTable,
  KindPicker,
  PreviewTable,
  SummaryStrip,
  kindLabel,
} from './ImportBits';

/** Problem `type`s this screen has words of its own for (API.md §16.2). */
const CODE_KEYS: Record<string, string> = {
  empty_file: 'import.err.empty_file',
  not_utf8: 'import.err.not_utf8',
  too_many_rows: 'import.err.too_many_rows',
  file_too_large: 'import.err.file_too_large',
  unreadable_csv: 'import.err.unreadable_csv',
  bad_upload: 'import.err.bad_upload',
  batch_has_errors: 'import.err.batch_has_errors',
  already_committed: 'import.err.already_committed',
  already_undone: 'import.err.already_undone',
  not_committed: 'import.err.not_committed',
  undo_window_closed: 'import.err.undo_window_closed',
};

/** Where each kind lands, so the result can offer the way on. */
const KIND_HREF: Record<ImportKind, string> = {
  units: '/units',
  renters: '/renters',
  payments: '/payments',
};

const COUNT_KEYS = ['properties', 'units', 'renters', 'contracts', 'payments'] as const;

function ImportBody() {
  const t = useT();

  const [kind, setKind] = useState<ImportKind | null>(null);
  const [columns, setColumns] = useState<ImportTemplateColumn[] | null>(null);
  const [downloading, setDownloading] = useState(false);

  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [committing, setCommitting] = useState(false);

  /** The committed batch and what it made — the result state. */
  const [created, setCreated] = useState<ImportCreatedCounts | null>(null);
  const [undone, setUndone] = useState<ImportCreatedCounts | null>(null);

  const [history, setHistory] = useState<ImportBatch[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [undoBusy, setUndoBusy] = useState<string | null>(null);

  /** A batch reopened from the history, read-only. */
  const [viewing, setViewing] = useState<ImportPreview | null>(null);

  const [error, setError] = useState<ApiError | null>(null);
  const [mismatch, setMismatch] = useState<{ missing: string[]; unknown: string[] } | null>(null);

  /** Problem documents said in the reader's language; anything else as it came. */
  const localize = useCallback(
    (e: unknown): ApiError => {
      const err = toApiError(e);
      if (err.status === 429) return new ApiError(429, { type: 'rate_limited', detail: t('import.err.rate_limited') });
      const failed = importRowFailure(err);
      if (failed) {
        return new ApiError(409, { type: 'row_failed', detail: t('import.err.row_failed', failed) });
      }
      const key = CODE_KEYS[err.code];
      return key ? new ApiError(err.status, { type: err.code, detail: t(key) }) : err;
    },
    [t],
  );

  const clearProblems = () => {
    setError(null);
    setMismatch(null);
  };

  const loadHistory = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await importsApi.list({ limit: 20 }, signal);
      setHistory(res.items ?? []);
      setCursor(res.next_cursor ?? null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setHistory([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void loadHistory(ac.signal);
    return () => ac.abort();
  }, [loadHistory]);

  useEffect(() => {
    if (!kind) return;
    const ac = new AbortController();
    setColumns(null);
    importsApi
      .template(kind, ac.signal)
      .then((res) => setColumns(res.columns ?? []))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setColumns([]);
      });
    return () => ac.abort();
  }, [kind]);

  const chooseKind = (k: ImportKind) => {
    setKind(k);
    setPreview(null);
    setCreated(null);
    setUndone(null);
    setErrorsOnly(false);
    clearProblems();
  };

  const startOver = () => {
    setPreview(null);
    setCreated(null);
    setUndone(null);
    setErrorsOnly(false);
    clearProblems();
  };

  const downloadTemplate = async () => {
    if (!kind) return;
    setDownloading(true);
    clearProblems();
    try {
      const { blob, filename } = await importsApi.templateCsv(kind);
      downloadBlob(blob, filename);
    } catch (e) {
      setError(localize(e));
    } finally {
      setDownloading(false);
    }
  };

  const upload = async (file: File) => {
    if (!kind) return;
    setUploading(true);
    clearProblems();
    setPreview(null);
    setCreated(null);
    setUndone(null);
    try {
      const res = await importsApi.preview(kind, file);
      setPreview(res);
      setErrorsOnly(res.batch.error_count > 0);
      void loadHistory();
    } catch (e) {
      const err = localize(e);
      const heads = importHeaderMismatch(toApiError(e));
      if (heads) setMismatch(heads);
      else setError(err);
    } finally {
      setUploading(false);
    }
  };

  const commit = async (skipErrors: boolean) => {
    if (!preview) return;
    const ok = preview.batch.ok_count;
    const bad = preview.batch.error_count;
    const question = skipErrors
      ? t('import.skip.confirm', { errors: bad, ok })
      : t('import.commit.confirm', { count: ok });
    if (!window.confirm(question)) return;
    setCommitting(true);
    clearProblems();
    try {
      const res = await importsApi.commit(preview.batch.id, skipErrors);
      setCreated(res.created);
      setPreview((p) => (p ? { ...p, batch: res.batch } : p));
      void loadHistory();
    } catch (e) {
      setError(localize(e));
    } finally {
      setCommitting(false);
    }
  };

  const undo = async (batch: ImportBatch) => {
    if (!window.confirm(t('import.undo.confirm', { filename: batch.filename }))) return;
    setUndoBusy(batch.id);
    clearProblems();
    try {
      const res = await importsApi.undo(batch.id);
      setUndone(res.undone);
      setCreated(null);
      if (preview?.batch.id === batch.id) setPreview((p) => (p ? { ...p, batch: res.batch } : p));
      if (viewing?.batch.id === batch.id) setViewing((v) => (v ? { ...v, batch: res.batch } : v));
      void loadHistory();
    } catch (e) {
      setError(localize(e));
    } finally {
      setUndoBusy(null);
    }
  };

  const openBatch = async (batch: ImportBatch) => {
    clearProblems();
    setViewing(null);
    try {
      setViewing(await importsApi.get(batch.id));
    } catch (e) {
      setError(localize(e));
    }
  };

  const loadMore = async () => {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const res = await importsApi.list({ cursor, limit: 20 });
      setHistory((prev) => [...(prev ?? []), ...(res.items ?? [])]);
      setCursor(res.next_cursor ?? null);
    } catch (e) {
      setError(localize(e));
    } finally {
      setLoadingMore(false);
    }
  };

  const counts = (label: string, c: ImportCreatedCounts) => (
    <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
      <h3 style={{ fontSize: 'var(--text-lg)' }}>{label}</h3>
      <ul style={{ display: 'flex', gap: 'var(--sp-4)', flexWrap: 'wrap', listStyle: 'none', padding: 0 }}>
        {COUNT_KEYS.filter((k) => (c[k] ?? 0) > 0).map((k) => (
          <li key={k} style={{ fontSize: 'var(--text-sm)' }}>
            {t(`import.count.${k}`, { count: c[k] })}
          </li>
        ))}
      </ul>
      {COUNT_KEYS.every((k) => (c[k] ?? 0) === 0) ? (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('import.result.nothing')}</p>
      ) : null}
    </div>
  );

  const batch = preview?.batch ?? null;

  return (
    <>
      <PageHead
        title={t('import.title')}
        lead={t('import.lead')}
        actions={
          <Link href="/settings" className="btn btn-quiet">
            {t('nav.settings')}
          </Link>
        }
      />
      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-6)' }}>
        <ProblemNote error={error} />
        {mismatch ? <HeaderMismatch t={t} missing={mismatch.missing} unknown={mismatch.unknown} columns={columns} /> : null}
        {/* An undo can be run from the history as well as from the result, so
            what it took back is said here, above everything. */}
        {undone ? counts(t('import.undone.heading'), undone) : null}

        <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('import.kind.heading')}</h2>
          <KindPicker value={kind} onChange={chooseKind} t={t} />
        </section>

        {kind ? (
          <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>
              {t('import.columns.heading', { kind: kindLabel(t, kind) })}
            </h2>
            <ColumnReference columns={columns} t={t} onDownload={() => void downloadTemplate()} downloading={downloading} />
          </section>
        ) : null}

        {kind && !preview ? (
          <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('import.upload.heading')}</h2>
            <DropZone
              t={t}
              busy={uploading}
              onFile={(f) => void upload(f)}
              onTooLarge={(name) =>
                setError(new ApiError(0, { type: 'file_too_large', detail: t('import.upload.too_large', { name }) }))
              }
            />
          </section>
        ) : null}

        {batch && preview ? (
          <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <div
              style={{
                display: 'flex',
                gap: 'var(--sp-3)',
                alignItems: 'baseline',
                justifyContent: 'space-between',
                flexWrap: 'wrap',
              }}
            >
              <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('import.preview.heading')}</h2>
              <button type="button" className="btn btn-quiet" onClick={startOver}>
                {t('import.preview.discard')}
              </button>
            </div>
            <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{batch.filename}</p>

            <SummaryStrip batch={batch} t={t} />

            <div className="segmented" role="group" aria-label={t('common.status')} style={{ alignSelf: 'start' }}>
              <button type="button" aria-pressed={!errorsOnly} onClick={() => setErrorsOnly(false)}>
                {t('import.filter.all')}
              </button>
              <button type="button" aria-pressed={errorsOnly} onClick={() => setErrorsOnly(true)}>
                {t('import.filter.errors')}
              </button>
            </div>

            <PreviewTable t={t} kind={batch.kind} rows={preview.rows ?? []} columns={columns} errorsOnly={errorsOnly} />

            {created ? null : batch.status === 'previewed' ? (
              <div style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={committing || batch.error_count > 0 || batch.ok_count === 0}
                  onClick={() => void commit(false)}
                >
                  <Icon icon="solar:check-circle-linear" width={20} />{' '}
                  {committing ? t('import.commit.busy') : t('import.commit.action', { count: batch.ok_count })}
                </button>
                {batch.error_count > 0 && batch.ok_count > 0 ? (
                  <button
                    type="button"
                    className="btn btn-secondary"
                    disabled={committing}
                    onClick={() => void commit(true)}
                  >
                    {t('import.skip.action', { errors: batch.error_count, ok: batch.ok_count })}
                  </button>
                ) : null}
                {batch.ok_count === 0 ? (
                  <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('import.commit.none')}</p>
                ) : null}
              </div>
            ) : null}

            {created ? (
              <div style={{ display: 'grid', gap: 'var(--sp-3)', borderTop: '1px solid var(--rule)', paddingTop: 'var(--sp-4)' }}>
                {counts(t('import.result.heading'), created)}
                <div style={{ display: 'flex', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
                  <Link href={KIND_HREF[batch.kind]} className="btn btn-secondary">
                    {t(`import.result.link.${batch.kind}`)}
                  </Link>
                  {batch.can_undo ? (
                    <button
                      type="button"
                      className="btn btn-quiet"
                      disabled={undoBusy === batch.id}
                      onClick={() => void undo(batch)}
                    >
                      {undoBusy === batch.id ? t('import.undo.busy') : t('import.undo.action')}
                    </button>
                  ) : null}
                </div>
                {batch.can_undo ? (
                  <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('import.undo.window')}</p>
                ) : null}
              </div>
            ) : null}

          </section>
        ) : null}

        <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <hr className="rule rule-strong" />
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('import.history.heading')}</h2>
          <HistoryTable
            t={t}
            items={history}
            onOpen={(b) => void openBatch(b)}
            onUndo={(b) => void undo(b)}
            busyId={undoBusy}
          />
          {cursor ? (
            <div>
              <button type="button" className="btn btn-secondary" onClick={() => void loadMore()} disabled={loadingMore}>
                {loadingMore ? t('common.loading_more') : t('common.load_more')}
              </button>
            </div>
          ) : null}
        </section>

        {viewing ? (
          <section style={{ display: 'grid', gap: 'var(--sp-4)' }}>
            <hr className="rule" />
            <div
              style={{
                display: 'flex',
                gap: 'var(--sp-3)',
                alignItems: 'baseline',
                justifyContent: 'space-between',
                flexWrap: 'wrap',
              }}
            >
              <h2 style={{ fontSize: 'var(--text-lg)' }}>{viewing.batch.filename}</h2>
              <button type="button" className="btn btn-quiet" onClick={() => setViewing(null)}>
                {t('import.view.close')}
              </button>
            </div>
            <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('import.view.readonly')}</p>
            <SummaryStrip batch={viewing.batch} t={t} />
            <PreviewTable t={t} kind={viewing.batch.kind} rows={viewing.rows ?? []} columns={null} errorsOnly={false} />
          </section>
        ) : null}
      </div>
    </>
  );
}

export default function ImportPage() {
  return (
    <>
      <ImportBody />
    </>
  );
}
