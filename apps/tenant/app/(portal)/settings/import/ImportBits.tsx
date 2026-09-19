'use client';

/**
 * The pieces of Settings → Import data (PLAN2 Part 2 §16.2).
 *
 * Nothing here parses a CSV. The browser checks one thing — that the file is
 * not bigger than the server will take — and then hands it over; every column
 * rule, every row error and every "this will create property Mbezi" line is
 * the server's own preview, printed as it came. That is the whole reason the
 * preview endpoint exists: the landlord must see exactly what the commit will
 * do, and only one side of the wire may decide that.
 */

import { Icon } from '@iconify/react';
import { useRef, useState, type DragEvent } from 'react';
import { TableScroll, type Translator } from '@tms/ui';
import {
  IMPORT_KINDS,
  IMPORT_MAX_BYTES,
  type ImportBatch,
  type ImportKind,
  type ImportRow,
  type ImportTemplateColumn,
} from '../../../../lib/api';
import { fmtDateTime } from '../../../../lib/format';

/* ------------------------------ vocabulary ------------------------------- */

export function kindLabel(t: Translator, kind: string): string {
  return IMPORT_KINDS.includes(kind as ImportKind) ? t(`import.kind.${kind}.title`) : kind;
}

export function statusLabel(t: Translator, status: string): string {
  switch (status) {
    case 'previewed':
    case 'committed':
    case 'undone':
      return t(`import.status.${status}`);
    default:
      return status;
  }
}

/**
 * The `resolved` block of a row, said in words. It is a hint, not a record —
 * the point is that a landlord can read down the column and see that line 7
 * makes a new property while line 8 joins one that is already there.
 */
export function resolvedHint(
  t: Translator,
  kind: ImportKind,
  resolved: Record<string, unknown> | null,
): string[] {
  if (!resolved) return [];
  const str = (k: string): string => (typeof resolved[k] === 'string' ? (resolved[k] as string) : '');
  const out: string[] = [];

  if (kind === 'units') {
    const property = str('property');
    if (property) {
      out.push(
        resolved.property_create === true || resolved.property_created === true
          ? t('import.resolved.property_create', { name: property })
          : t('import.resolved.property_match', { name: property }),
      );
    }
    if (str('unit')) out.push(t('import.resolved.unit_create', { name: str('unit') }));
  } else if (kind === 'renters') {
    const name = str('full_name');
    if (name) {
      out.push(
        resolved.renter_create === true || resolved.renter_created === true
          ? t('import.resolved.renter_create', { name })
          : t('import.resolved.renter_match', { name }),
      );
    }
    if (str('unit')) out.push(t('import.resolved.unit_match', { name: str('unit') }));
    if (resolved.contract) out.push(t('import.resolved.contract'));
  } else {
    if (str('renter_name')) out.push(t('import.resolved.renter_match', { name: str('renter_name') }));
    if (str('unit')) out.push(t('import.resolved.unit_match', { name: str('unit') }));
    if (str('contract_status')) {
      out.push(t('import.resolved.contract_status', { status: str('contract_status') }));
    }
  }
  return out;
}

/* ------------------------------ kind picker ------------------------------ */

export function KindPicker({
  value,
  onChange,
  t,
}: {
  value: ImportKind | null;
  onChange: (k: ImportKind) => void;
  t: Translator;
}) {
  return (
    <div
      role="group"
      aria-label={t('import.kind.heading')}
      style={{
        display: 'grid',
        gap: 'var(--sp-3)',
        gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))',
      }}
    >
      {IMPORT_KINDS.map((k) => {
        const chosen = value === k;
        return (
          <button
            key={k}
            type="button"
            aria-pressed={chosen}
            onClick={() => onChange(k)}
            className="sheet"
            style={{
              textAlign: 'left',
              cursor: 'pointer',
              padding: 'var(--sp-4)',
              minHeight: 'var(--touch-min)',
              borderWidth: chosen ? 2 : 1,
              borderStyle: 'solid',
              borderColor: chosen ? 'var(--ink)' : 'var(--rule)',
              background: 'var(--paper)',
              color: 'inherit',
              font: 'inherit',
            }}
          >
            <span style={{ display: 'block', fontWeight: 600 }}>{t(`import.kind.${k}.title`)}</span>
            <span
              style={{
                display: 'block',
                marginTop: 'var(--sp-2)',
                color: 'var(--ink-soft)',
                fontSize: 'var(--text-sm)',
              }}
            >
              {t(`import.kind.${k}.desc`)}
            </span>
            <span
              style={{
                display: 'block',
                marginTop: 'var(--sp-2)',
                color: 'var(--ink-soft)',
                fontSize: 'var(--text-xs)',
              }}
            >
              {t(`import.kind.${k}.creates`)}
            </span>
          </button>
        );
      })}
    </div>
  );
}

/* --------------------------- column reference ---------------------------- */

export function ColumnReference({
  columns,
  t,
  onDownload,
  downloading,
}: {
  columns: ImportTemplateColumn[] | null;
  t: Translator;
  onDownload: () => void;
  downloading: boolean;
}) {
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('import.columns.hint')}</p>
      <div>
        <button type="button" className="btn btn-secondary" onClick={onDownload} disabled={downloading || !columns}>
          <Icon icon="solar:download-minimalistic-linear" width={20} />{' '}
          {downloading ? t('import.template.busy') : t('import.template.download')}
        </button>
      </div>
      <TableScroll label={t('import.columns.table_label')}>
        <table className="ledger">
          <thead>
            <tr>
              <th>{t('import.columns.col.name')}</th>
              <th>{t('import.columns.col.required')}</th>
              <th>{t('import.columns.col.example')}</th>
              <th>{t('import.columns.col.help')}</th>
            </tr>
          </thead>
          <tbody>
            {columns === null ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  {t('common.loading')}
                </td>
              </tr>
            ) : (
              columns.map((c) => (
                <tr key={c.name}>
                  <td style={{ fontFamily: 'var(--font-mono, monospace)', fontWeight: 500 }}>{c.name}</td>
                  <td style={{ fontSize: 'var(--text-sm)' }}>
                    {c.required ? t('import.columns.required_yes') : t('import.columns.required_no')}
                  </td>
                  <td style={{ fontSize: 'var(--text-sm)', whiteSpace: 'nowrap' }}>{c.example}</td>
                  <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{c.help}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableScroll>
    </div>
  );
}

/* -------------------------------- dropzone -------------------------------- */

export function DropZone({
  t,
  busy,
  onFile,
  onTooLarge,
}: {
  t: Translator;
  busy: boolean;
  onFile: (f: File) => void;
  onTooLarge: (name: string) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);

  // The only thing the browser judges about the file: whether the server will
  // even accept the upload. Everything past that is the preview's business.
  const take = (file: File | null | undefined) => {
    if (!file) return;
    if (file.size > IMPORT_MAX_BYTES) {
      onTooLarge(file.name);
      return;
    }
    onFile(file);
  };

  const drop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setOver(false);
    take(e.dataTransfer?.files?.[0]);
  };

  return (
    <div
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={drop}
      style={{
        border: `2px dashed ${over ? 'var(--ink)' : 'var(--rule-strong)'}`,
        borderRadius: 'var(--radius-sm)',
        padding: 'var(--sp-6) var(--sp-4)',
        textAlign: 'center',
        display: 'grid',
        gap: 'var(--sp-3)',
        justifyItems: 'center',
      }}
    >
      <Icon icon="solar:file-text-linear" width={28} />
      <p style={{ color: 'var(--ink-soft)' }}>{t('import.upload.drop')}</p>
      <input
        ref={input}
        type="file"
        accept=".csv,text/csv"
        style={{ display: 'none' }}
        onChange={(e) => {
          take(e.target.files?.[0]);
          // Cleared so choosing the same file twice still fires a change.
          e.target.value = '';
        }}
      />
      <button type="button" className="btn btn-primary" disabled={busy} onClick={() => input.current?.click()}>
        {busy ? t('import.upload.busy') : t('import.upload.choose')}
      </button>
      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('import.upload.hint')}</p>
    </div>
  );
}

/* ------------------------------ header mismatch --------------------------- */

/** The 400 the screen can actually act on: which columns, and what was wanted. */
export function HeaderMismatch({
  t,
  missing,
  unknown,
  columns,
}: {
  t: Translator;
  missing: string[];
  unknown: string[];
  columns: ImportTemplateColumn[] | null;
}) {
  const mono = { fontFamily: 'var(--font-mono, monospace)' };
  return (
    <div
      role="alert"
      style={{
        border: '1px solid var(--stamp-overdue)',
        borderRadius: 'var(--radius-sm)',
        padding: 'var(--sp-3)',
        display: 'grid',
        gap: 'var(--sp-2)',
      }}
    >
      <p style={{ color: 'var(--stamp-overdue)', fontWeight: 600 }}>{t('import.err.header_mismatch')}</p>
      {missing.length ? (
        <p style={{ fontSize: 'var(--text-sm)' }}>
          {t('import.err.missing')}: <span style={mono}>{missing.join(', ')}</span>
        </p>
      ) : null}
      {unknown.length ? (
        <p style={{ fontSize: 'var(--text-sm)' }}>
          {t('import.err.unknown')}: <span style={mono}>{unknown.join(', ')}</span>
        </p>
      ) : null}
      {columns ? (
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          {t('import.err.expected')}{' '}
          <span style={mono}>{columns.map((c) => c.name).join(',')}</span>
        </p>
      ) : null}
    </div>
  );
}

/* ------------------------------ preview table ----------------------------- */

/** Column order comes from the template when we have it; the file otherwise. */
function rawColumns(rows: ImportRow[], columns: ImportTemplateColumn[] | null): string[] {
  const seen: string[] = [];
  for (const row of rows) {
    for (const k of Object.keys(row.raw ?? {})) if (!seen.includes(k)) seen.push(k);
  }
  if (!columns) return seen;
  const ordered = columns.map((c) => c.name).filter((n) => seen.includes(n));
  return [...ordered, ...seen.filter((n) => !ordered.includes(n))];
}

export function SummaryStrip({ batch, t }: { batch: ImportBatch; t: Translator }) {
  const cell = (label: string, value: number, tone?: string) => (
    <div key={label}>
      <div style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{label}</div>
      <div className="num" style={{ fontSize: 'var(--text-xl)', fontWeight: 600, color: tone }}>
        {value}
      </div>
    </div>
  );
  return (
    <div
      style={{
        display: 'flex',
        gap: 'var(--sp-6)',
        flexWrap: 'wrap',
        padding: 'var(--sp-3) 0',
        borderTop: '1px solid var(--rule)',
        borderBottom: '1px solid var(--rule)',
      }}
    >
      {cell(t('import.summary.rows'), batch.row_count)}
      {cell(t('import.summary.ok'), batch.ok_count)}
      {cell(t('import.summary.errors'), batch.error_count, batch.error_count ? 'var(--stamp-overdue)' : undefined)}
    </div>
  );
}

export function PreviewTable({
  t,
  kind,
  rows,
  columns,
  errorsOnly,
}: {
  t: Translator;
  kind: ImportKind;
  rows: ImportRow[];
  columns: ImportTemplateColumn[] | null;
  errorsOnly: boolean;
}) {
  const cols = rawColumns(rows, columns);
  const shown = errorsOnly ? rows.filter((r) => r.errors && Object.keys(r.errors).length > 0) : rows;

  // The line and status columns stay put while the raw cells scroll under the
  // thumb — on a phone the whole point is "which line, and is it wrong?".
  const sticky = (left: number, header: boolean): React.CSSProperties => ({
    position: 'sticky',
    left,
    zIndex: header ? 2 : 1,
    background: 'var(--paper)',
  });

  return (
    <TableScroll label={t('import.preview.heading')}>
      <table className="ledger">
        <thead>
          <tr>
            <th style={{ ...sticky(0, true), minWidth: 56 }}>{t('import.col.line')}</th>
            <th style={{ ...sticky(56, true), minWidth: 92 }}>{t('import.col.status')}</th>
            {cols.map((c) => (
              <th key={c} style={{ whiteSpace: 'nowrap' }}>
                {c}
              </th>
            ))}
            <th style={{ minWidth: 200 }}>{t('import.col.resolved')}</th>
          </tr>
        </thead>
        <tbody>
          {shown.length === 0 ? (
            <tr>
              <td colSpan={cols.length + 3} style={{ color: 'var(--ink-soft)' }}>
                {errorsOnly ? t('import.preview.no_errors') : t('import.preview.empty')}
              </td>
            </tr>
          ) : (
            shown.map((row) => {
              const errors = row.errors ?? {};
              const rowError = errors._row;
              const bad = Object.keys(errors).length > 0;
              const hint = resolvedHint(t, kind, row.resolved);
              return (
                <tr key={row.line}>
                  <td className="num" style={sticky(0, false)}>
                    {row.line}
                  </td>
                  <td style={{ ...sticky(56, false), fontSize: 'var(--text-sm)' }}>
                    {bad ? (
                      <span style={{ color: 'var(--stamp-overdue)', fontWeight: 600 }}>{t('import.row.error')}</span>
                    ) : row.entity_id ? (
                      <span className="pencil">{t('import.row.imported')}</span>
                    ) : (
                      <span className="pencil">{t('import.row.ok')}</span>
                    )}
                  </td>
                  {cols.map((c) => (
                    <td key={c} style={{ verticalAlign: 'top', paddingTop: 'var(--sp-2)' }}>
                      <div style={{ whiteSpace: 'nowrap' }}>{row.raw?.[c] ?? ''}</div>
                      {errors[c] ? (
                        <div
                          style={{
                            color: 'var(--stamp-overdue)',
                            fontSize: 'var(--text-xs)',
                            marginTop: 2,
                            maxWidth: 220,
                            whiteSpace: 'normal',
                          }}
                        >
                          {errors[c]}
                        </div>
                      ) : null}
                    </td>
                  ))}
                  <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', verticalAlign: 'top', paddingTop: 'var(--sp-2)' }}>
                    {rowError ? (
                      <div style={{ color: 'var(--stamp-overdue)', whiteSpace: 'normal', maxWidth: 260 }}>{rowError}</div>
                    ) : hint.length ? (
                      <div style={{ whiteSpace: 'normal', maxWidth: 260 }}>{hint.join(' · ')}</div>
                    ) : (
                      '—'
                    )}
                  </td>
                </tr>
              );
            })
          )}
        </tbody>
      </table>
    </TableScroll>
  );
}

/* -------------------------------- history -------------------------------- */

export function HistoryTable({
  t,
  items,
  onOpen,
  onUndo,
  busyId,
}: {
  t: Translator;
  items: ImportBatch[] | null;
  onOpen: (b: ImportBatch) => void;
  onUndo: (b: ImportBatch) => void;
  busyId: string | null;
}) {
  return (
    <TableScroll label={t('import.history.heading')}>
      <table className="ledger">
        <thead>
          <tr>
            <th>{t('import.history.col.file')}</th>
            <th>{t('import.history.col.kind')}</th>
            <th className="num">{t('import.history.col.rows')}</th>
            <th>{t('common.status')}</th>
            <th>{t('import.history.col.who')}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {items === null ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                {t('common.loading')}
              </td>
            </tr>
          ) : items.length === 0 ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                {t('import.history.empty')}
              </td>
            </tr>
          ) : (
            items.map((b) => (
              <tr key={b.id}>
                <td>
                  <button
                    type="button"
                    className="btn btn-quiet"
                    onClick={() => onOpen(b)}
                    style={{ minHeight: 32, padding: 0, fontWeight: 500 }}
                  >
                    {b.filename}
                  </button>
                </td>
                <td style={{ fontSize: 'var(--text-sm)' }}>{kindLabel(t, b.kind)}</td>
                <td className="num" style={{ fontSize: 'var(--text-sm)', whiteSpace: 'nowrap' }}>
                  {t('import.history.counts', { ok: b.ok_count, errors: b.error_count, rows: b.row_count })}
                </td>
                <td>
                  {b.status === 'committed' ? (
                    <span className="stamp stamp-paid">{statusLabel(t, b.status)}</span>
                  ) : (
                    <span className="pencil">{statusLabel(t, b.status)}</span>
                  )}
                </td>
                <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', whiteSpace: 'nowrap' }}>
                  {t('import.history.by', {
                    who: b.created_by?.name ?? t('import.history.someone'),
                    when: fmtDateTime(b.committed_at ?? b.created_at),
                  })}
                </td>
                <td className="num">
                  {b.can_undo ? (
                    <button
                      type="button"
                      className="btn btn-quiet"
                      style={{ minHeight: 32 }}
                      disabled={busyId === b.id}
                      onClick={() => onUndo(b)}
                    >
                      {busyId === b.id ? t('import.undo.busy') : t('import.undo.action')}
                    </button>
                  ) : null}
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </TableScroll>
  );
}
