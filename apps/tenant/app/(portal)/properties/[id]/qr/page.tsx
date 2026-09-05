'use client';

/**
 * Printable QR sheet for a whole property (SPEC §3.1, §5.3).
 * `GET /properties/{id}/qr-sheet` returns presigned 15-minute PNG URLs and
 * generates any code that was missing, so this page never invents anything.
 *
 * Print stylesheet: nav chrome is dropped and cards lay out 2 × 3 on A4.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useEffect, useState } from 'react';
import { ProblemNote } from '../../../../../components/FormBits';
import { PageHead } from '../../../../../components/PageHead';
import {
  ApiError,
  propertiesApi,
  toApiError,
  unwrapProperty,
  type Property,
  type QrSheetItem,
} from '../../../../../lib/api';
import { useMe } from '../../../../../lib/auth';

const PRINT_CSS = `
.qr-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
  gap: var(--sp-4);
}
.qr-card {
  border: 1px solid var(--rule);
  border-radius: var(--radius-lg);
  background: var(--sheet);
  padding: var(--sp-4);
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--sp-2);
  break-inside: avoid;
}
.qr-card img { width: 150px; height: 150px; image-rendering: pixelated; }
.qr-card .qr-org { font-size: var(--text-xs); color: var(--ink-soft); letter-spacing: 0.08em; text-transform: uppercase; }
.qr-card .qr-prop { font-size: var(--text-sm); color: var(--ink-soft); }
.qr-card .qr-unit { font-size: var(--text-lg); font-weight: 700; }
.qr-card .qr-code { font-variant-numeric: tabular-nums; letter-spacing: 0.14em; font-weight: 600; }
.qr-card .qr-url { font-size: 10px; color: var(--ink-soft); word-break: break-all; }

@media print {
  @page { size: A4 portrait; margin: 12mm; }
  html, body { background: #fff !important; }
  .shell-grid { display: block !important; min-height: 0 !important; }
  .shell-rail, .shell-topbar, .shell-banner, .no-print { display: none !important; }
  .shell-body { padding: 0 !important; }
  .qr-grid { grid-template-columns: repeat(2, 1fr) !important; gap: 6mm !important; }
  .qr-card { height: 82mm; border: 1px dashed #999 !important; border-radius: 0 !important; box-shadow: none !important; }
  .qr-card img { width: 42mm; height: 42mm; }
}
`;

function QrBody({ id }: { id: string }) {
  const { org } = useMe();
  const [property, setProperty] = useState<Property | null>(null);
  const [items, setItems] = useState<QrSheetItem[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    (async () => {
      try {
        const [p, sheet] = await Promise.all([
          propertiesApi.get(id, ac.signal),
          propertiesApi.qrSheet(id, ac.signal),
        ]);
        setProperty(unwrapProperty(p));
        setItems(sheet.items ?? []);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setItems([]);
      }
    })();
    return () => ac.abort();
  }, [id]);

  return (
    <>
      <style dangerouslySetInnerHTML={{ __html: PRINT_CSS }} />

      <div className="no-print">
        <PageHead
          title="QR sheet"
          lead={
            property
              ? `${property.name} — one sticker per unit. Cut along the dashed lines.`
              : 'Loading the sheet…'
          }
          actions={
            <>
              <Link href={`/properties/${id}`} className="btn btn-quiet">
                Back to property
              </Link>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => window.print()}
                disabled={!items || items.length === 0}
              >
                <Icon icon="solar:printer-linear" width={20} /> Print
              </button>
            </>
          }
        />
        <hr className="rule rule-strong" />
        <div style={{ padding: 'var(--sp-4) 0' }}>
          <ProblemNote error={error} />
          {items && items.length === 0 && !error ? (
            <p style={{ color: 'var(--ink-soft)' }}>
              This property has no units yet, so there is nothing to print.{' '}
              <Link href={`/properties/${id}`}>Add a unit first.</Link>
            </p>
          ) : null}
          <p style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>
            Image links expire after 15 minutes — reload the page if the codes stop showing.
          </p>
        </div>
      </div>

      {items === null ? (
        <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>
      ) : (
        <div className="qr-grid">
          {items.map((it) => (
            <div className="qr-card" key={it.unit_id}>
              <div className="qr-org">{org?.name ?? ''}</div>
              <div className="qr-prop">{property?.name ?? ''}</div>
              <div className="qr-unit">{it.unit_name}</div>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={it.png_url} alt={`QR code for ${it.unit_name}`} />
              <div className="qr-code">{it.unit_code}</div>
              <div className="qr-url">{it.scan_url}</div>
            </div>
          ))}
        </div>
      )}
    </>
  );
}

export default function PropertyQrPage() {
  const params = useParams<{ id: string }>();
  const id = typeof params?.id === 'string' ? params.id : '';
  return (
    <>
      <QrBody id={id} />
    </>
  );
}
