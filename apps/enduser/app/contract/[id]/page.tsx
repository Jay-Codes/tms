'use client';

/**
 * The agreement itself — SPEC §5.5, FLOWS.md flow 2 step 7b/8.
 *
 * `GET /contracts/{id}/document` is the whole document: the org's letterhead,
 * the snapshotted terms, the parties, the schedule and the signature block.
 * Nothing here re-derives any of it — the snapshot is the contract, and the
 * hash under the signatures is what `/verify` checks it against.
 *
 * This is also the deep link the "ready to sign" SMS points at
 * (`{APP_BASE_URL}/enduser/contract/{id}`); `<Protected>` bounces a logged-out
 * renter to `/login?next=…` and back here afterwards.
 */

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { Icon } from '@iconify/react';
import {
  ApiError,
  contractApi,
  hasLandlordSignature,
  hasRenterSignature,
  type Contract,
  type ContractDocument,
  type DocumentSignature,
  type VerifyResponse,
} from '../../../lib/api';
import { errorMessage, formatDate, money, phoneLast4, priceLine } from '../../../lib/format';
import { Protected } from '../../../components/Protected';
import { ContractStamp } from '../../../components/ContractStatus';
import { Notice, Screen } from '../../../components/Screen';
import './document.css';

const PARTY_LABEL: Record<string, string> = { landlord: 'Landlord', renter: 'Renter' };

/** "Signed by {name} on {date} via phone •••1234" (SPEC §5.5). */
function signatureLine(sig: DocumentSignature): string {
  const last4 = phoneLast4(sig.phone_masked);
  const via = last4 ? ` via phone •••${last4}` : '';
  return `Signed by ${sig.name} on ${formatDate(sig.signed_at)}${via}`;
}

function SignatureSlot({
  party,
  signature,
  fallbackName,
}: {
  party: 'landlord' | 'renter';
  signature: DocumentSignature | undefined;
  fallbackName: string;
}) {
  return (
    <div className="doc-sig">
      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{PARTY_LABEL[party]}</p>
      {signature ? (
        <>
          {signature.signature_image_url && (
            // Presigned MinIO URL; next/image would need the host allow-listed
            // at build time and this app is CSR-only.
            // eslint-disable-next-line @next/next/no-img-element
            <img
              className="doc-sig-image"
              src={signature.signature_image_url}
              alt={`${signature.name}'s signature`}
            />
          )}
          <p style={{ fontWeight: 600 }}>{signatureLine(signature)}</p>
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {signature.method === 'drawn' ? 'Drawn signature + one-time code' : 'One-time code'}
          </p>
        </>
      ) : (
        <>
          <div className="doc-sig-slot" aria-hidden />
          <p className="pencil">
            {fallbackName} — not signed yet
          </p>
        </>
      )}
    </div>
  );
}

function DocumentContent() {
  const params = useParams<{ id: string }>();
  const id = typeof params?.id === 'string' ? params.id : '';

  const [doc, setDoc] = useState<ContractDocument | null>(null);
  const [contract, setContract] = useState<Contract | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [verify, setVerify] = useState<VerifyResponse | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [verifyError, setVerifyError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    let live = true;
    (async () => {
      try {
        // The document is the screen; the contract record only adds the
        // commercial summary, so a failure there must not blank the page.
        const [d, c] = await Promise.all([
          contractApi.document(id, ac.signal),
          contractApi.get(id, ac.signal).catch(() => null),
        ]);
        if (!live) return;
        setDoc(d);
        setContract(c?.contract ?? null);
        setError(null);
      } catch (err) {
        if (!live || (err instanceof DOMException && err.name === 'AbortError')) return;
        setError(
          err instanceof ApiError && err.status === 404
            ? 'This agreement is not available on your account.'
            : errorMessage(err),
        );
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [id]);

  const runVerify = useCallback(async () => {
    setVerifying(true);
    setVerifyError(null);
    try {
      setVerify(await contractApi.verify(id));
    } catch (err) {
      setVerifyError(errorMessage(err));
    } finally {
      setVerifying(false);
    }
  }, [id]);

  if (loading) {
    return (
      <Screen bottomBar>
        <p className="pencil">Loading your agreement…</p>
      </Screen>
    );
  }

  if (error || !doc) {
    return (
      <Screen bottomBar>
        <Notice tone="error">{error ?? 'This agreement could not be loaded.'}</Notice>
        <Link className="btn btn-secondary" href="/contract">
          Back to your contracts
        </Link>
      </Screen>
    );
  }

  const renterSigned = hasRenterSignature({ signatures: doc.signatures });
  const landlordSigned = hasLandlordSignature({ signatures: doc.signatures });
  const needsSignature = doc.status === 'pending_signature' && !renterSigned;
  const scheduleTotal = doc.schedule.reduce((sum, row) => sum + row.amount, 0);

  return (
    <Screen bottomBar style={needsSignature ? { paddingBottom: 176 } : undefined}>
      <header className="no-print" style={{ display: 'grid', gap: 'var(--sp-2)' }}>
        <Link
          href="/contract"
          className="btn btn-quiet"
          style={{ justifySelf: 'start', paddingInline: 0 }}
        >
          <Icon icon="solar:alt-arrow-left-linear" width={18} aria-hidden />
          All contracts
        </Link>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--sp-3)' }}>
          <h1 style={{ fontSize: 'var(--text-xl)' }}>
            {contract ? `${contract.unit.name} · ${contract.unit.property_name}` : 'Your agreement'}
          </h1>
          <ContractStamp status={doc.status} renterSigned={renterSigned} />
        </div>
      </header>

      {needsSignature && (
        <Notice>Read the agreement, then accept and sign at the bottom of this page.</Notice>
      )}
      {doc.status === 'pending_signature' && renterSigned && !landlordSigned && (
        <Notice>
          You have signed. Waiting for {doc.org.display_name} to countersign — you will get an SMS.
        </Notice>
      )}

      <article className="doc">
        {/* ---- letterhead ---- */}
        {doc.org.letterhead_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            className="doc-letterhead"
            src={doc.org.letterhead_url}
            alt={`${doc.org.display_name} letterhead`}
          />
        ) : (
          <header
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-3)',
              paddingBottom: 'var(--sp-3)',
              borderBottom: '1px solid var(--rule-strong)',
            }}
          >
            {doc.org.logo_url && (
              // eslint-disable-next-line @next/next/no-img-element
              <img
                src={doc.org.logo_url}
                alt=""
                width={44}
                height={44}
                style={{ width: 44, height: 44, objectFit: 'contain' }}
              />
            )}
            <p style={{ margin: 0, fontWeight: 600, fontSize: 'var(--text-lg)' }}>
              {doc.org.display_name}
            </p>
          </header>
        )}

        {/* ---- parties ---- */}
        <section className="doc-block">
          <h2>Parties</h2>
          <table className="ledger">
            <tbody>
              <tr>
                <td>Landlord</td>
                <td className="num">{doc.parties.landlord.name}</td>
              </tr>
              <tr>
                <td>Renter</td>
                <td className="num">
                  {doc.parties.renter.name}
                  {doc.parties.renter.phone_masked && (
                    <>
                      <br />
                      <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                        {doc.parties.renter.phone_masked}
                      </span>
                    </>
                  )}
                </td>
              </tr>
              {contract && (
                <>
                  <tr>
                    <td>Unit</td>
                    <td className="num">
                      {contract.unit.name} · {contract.unit.property_name}
                    </td>
                  </tr>
                  <tr>
                    <td>Rent</td>
                    <td className="num">
                      {priceLine(contract.rent_amount, contract.rent_period_days)}
                    </td>
                  </tr>
                  <tr>
                    <td>Term</td>
                    <td className="num">
                      {formatDate(contract.start_date)} – {formatDate(contract.end_date)}
                    </td>
                  </tr>
                </>
              )}
            </tbody>
          </table>
        </section>

        {/* ---- terms (server-sanitized snapshot) ---- */}
        <section className="doc-block">
          <h2>Terms</h2>
          <div className="doc-scroll-x">
            <div
              className="doc-terms"
              // `terms_html` is the snapshot the backend sanitized on the way
              // in (API.md: allow-list of block tags, all attributes stripped
              // except `class`). It is rendered verbatim so the printed page
              // matches what was hashed.
              dangerouslySetInnerHTML={{ __html: doc.terms_html }}
            />
          </div>
        </section>

        {/* ---- payment schedule ---- */}
        {doc.schedule.length > 0 && (
          <section className="doc-block">
            <h2>Payment schedule</h2>
            <div className="doc-scroll-x">
              <table className="ledger">
                <thead>
                  <tr>
                    <th>Due</th>
                    <th>Period</th>
                    <th className="num">Amount</th>
                  </tr>
                </thead>
                <tbody>
                  {doc.schedule.map((row) => (
                    <tr key={`${row.due_date}-${row.period_start}`}>
                      <td>{formatDate(row.due_date)}</td>
                      <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                        {formatDate(row.period_start)} – {formatDate(row.period_end)}
                      </td>
                      <td className="num amount">{money(row.amount)}</td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td colSpan={2}>Total over the term</td>
                    <td className="num amount">{money(scheduleTotal)}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        )}

        {/* ---- signatures ---- */}
        <section className="doc-block">
          <h2>Signatures</h2>
          <SignatureSlot
            party="renter"
            signature={doc.signatures.find((s) => s.party === 'renter')}
            fallbackName={doc.parties.renter.name}
          />
          <SignatureSlot
            party="landlord"
            signature={doc.signatures.find((s) => s.party === 'landlord')}
            fallbackName={doc.parties.landlord.name}
          />
        </section>

        {/* ---- footer + verification ---- */}
        {doc.org.footer_text && (
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {doc.org.footer_text}
          </p>
        )}

        <section className="doc-block">
          <h2>Verification</h2>
          <p className="doc-hash">{doc.snapshot_hash}</p>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-xs)' }}>
            Document fingerprint · generated {formatDate(doc.generated_at)}
          </p>
          <div
            className="no-print"
            style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}
          >
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => void runVerify()}
              disabled={verifying}
              style={{ width: 'auto' }}
            >
              {verifying ? 'Checking…' : 'Verify'}
            </button>
            {verify &&
              (verify.valid ? (
                <span className="stamp stamp-paid">Valid</span>
              ) : (
                <span className="stamp stamp-overdue">Tampered</span>
              ))}
          </div>
          {verify && !verify.valid && (
            <p style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>
              This document does not match its fingerprint. Contact {doc.org.display_name}.
            </p>
          )}
          {verifyError && <Notice tone="error">{verifyError}</Notice>}
        </section>
      </article>

      <div className="no-print" style={{ display: 'grid', gap: 'var(--sp-3)' }}>
        <button type="button" className="btn btn-secondary" onClick={() => window.print()}>
          <Icon icon="solar:printer-minimalistic-linear" width={20} aria-hidden />
          Print / Save as PDF
        </button>
      </div>

      {needsSignature && (
        <div className="doc-cta">
          <Link className="btn btn-primary" href={`/contract/${encodeURIComponent(id)}/sign`}>
            Accept &amp; sign
          </Link>
        </div>
      )}
    </Screen>
  );
}

export default function ContractDocumentPage() {
  return (
    <Protected>
      <DocumentContent />
    </Protected>
  );
}
