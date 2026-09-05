'use client';

/**
 * The contract document as paper: org letterhead (or logo), the resolved terms
 * HTML, whatever the caller slots underneath (schedule, signature block), and
 * the org's footer line.
 *
 * The HTML is `terms_snapshot_html` — sanitized server-side on write (API.md
 * allows only p/br/h1-h3/lists/inline marks/tables/blockquote, attributes
 * stripped bar `class`), so it is injected as-is. The frontend never sanitizes
 * or edits a snapshot; that is the backend's contract.
 *
 * Printing: `<PrintStyles/>` hides everything outside `.doc-print`, so the
 * browser's "Save as PDF" produces the document alone. No server-side PDF
 * (SPEC §5.5).
 */

import type { ReactNode } from 'react';

export function PrintStyles() {
  return (
    <style>{`
      @media print {
        @page { margin: 16mm; }
        body * { visibility: hidden; }
        .doc-print, .doc-print * { visibility: visible; }
        .doc-print {
          position: absolute;
          left: 0;
          top: 0;
          width: 100%;
          border: 0;
          padding: 0;
          background: #fff;
        }
        .no-print { display: none !important; }
      }
    `}</style>
  );
}

/** Terms body styling — the design system has no prose class of its own. */
const TERMS_STYLE = `
  .doc-terms { color: var(--ink); line-height: var(--leading-body); }
  .doc-terms h1 { font-size: var(--text-xl); margin: var(--sp-5) 0 var(--sp-3); }
  .doc-terms h2 { font-size: var(--text-lg); margin: var(--sp-5) 0 var(--sp-3); }
  .doc-terms h3 { font-size: var(--text-md); margin: var(--sp-4) 0 var(--sp-2); font-weight: 600; }
  .doc-terms p { margin: 0 0 var(--sp-3); max-width: var(--measure); }
  .doc-terms ul, .doc-terms ol { margin: 0 0 var(--sp-3); padding-left: 1.4em; }
  .doc-terms li { margin-bottom: var(--sp-1); }
  .doc-terms blockquote {
    margin: 0 0 var(--sp-3);
    padding-left: var(--sp-4);
    border-left: 2px solid var(--rule);
    color: var(--ink-soft);
  }
  .doc-terms table { border-collapse: collapse; width: 100%; margin: 0 0 var(--sp-4); }
  .doc-terms th, .doc-terms td { border: 1px solid var(--rule); padding: var(--sp-2) var(--sp-3); text-align: left; }
  .doc-terms th { font-weight: 600; background: var(--sheet-tint); }
`;

export interface DocumentPaperProps {
  displayName?: string | null;
  logoUrl?: string | null;
  letterheadUrl?: string | null;
  footerText?: string | null;
  /** Resolved, server-sanitized terms HTML. */
  html: string;
  /** Slotted under the terms: schedule table, signature block, hash. */
  children?: ReactNode;
  /** Small caption above the letterhead, e.g. "Preview — sample values". */
  caption?: ReactNode;
}

export function DocumentPaper({
  displayName,
  logoUrl,
  letterheadUrl,
  footerText,
  html,
  children,
  caption,
}: DocumentPaperProps) {
  return (
    <article
      className="sheet doc-print"
      style={{ padding: 'var(--sp-6)', background: 'var(--sheet)' }}
    >
      <style>{TERMS_STYLE}</style>

      {caption ? (
        <p className="no-print" style={{ margin: '0 0 var(--sp-4)', fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
          {caption}
        </p>
      ) : null}

      <header style={{ marginBottom: 'var(--sp-5)' }}>
        {letterheadUrl ? (
          /* eslint-disable-next-line @next/next/no-img-element -- presigned MinIO URL, not a build-time asset */
          <img
            src={letterheadUrl}
            alt=""
            style={{ display: 'block', width: '100%', maxHeight: 160, objectFit: 'contain', objectPosition: 'left' }}
          />
        ) : (
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
            {logoUrl ? (
              /* eslint-disable-next-line @next/next/no-img-element -- presigned MinIO URL */
              <img src={logoUrl} alt="" style={{ height: 48, width: 'auto' }} />
            ) : null}
            <span style={{ fontSize: 'var(--text-xl)', fontWeight: 600 }}>{displayName ?? ''}</span>
          </div>
        )}
        <hr className="rule rule-strong" style={{ marginTop: 'var(--sp-4)' }} />
      </header>

      <div className="doc-terms" dangerouslySetInnerHTML={{ __html: html }} />

      {children}

      {footerText ? (
        <footer style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule" />
          <p
            style={{
              marginTop: 'var(--sp-3)',
              color: 'var(--ink-soft)',
              fontSize: 'var(--text-sm)',
              whiteSpace: 'pre-line',
            }}
          >
            {footerText}
          </p>
        </footer>
      ) : null}
    </article>
  );
}
