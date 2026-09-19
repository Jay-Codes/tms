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
import { useLocale, useT, type Locale, type Translator } from '@tms/ui';
import {
  ApiError,
  contractApi,
  hasLandlordSignature,
  hasRenterSignature,
  renterApi,
  type Contract,
  type ContractDocument,
  type DocumentSignature,
  type MySchedule,
  type Proof,
  type VerifyResponse,
} from '../../../lib/api';
import { errorMessage, formatDate, money, phoneLast4 } from '../../../lib/format';
import { Protected } from '../../../components/Protected';
import { ContractStamp } from '../../../components/ContractStatus';
import { CountdownChip } from '../../../components/PaymentStatus';
import { RentValue } from '../../../components/RentValue';
import { Notice, Screen } from '../../../components/Screen';
import './document.css';

/** "Signed by {name} on {date} via phone •••1234" (SPEC §5.5). */
function signatureLine(t: Translator, locale: Locale, sig: DocumentSignature): string {
  const last4 = phoneLast4(sig.phone_masked);
  const vars = { name: sig.name, date: formatDate(locale, sig.signed_at), last4 };
  return last4 ? t('doc.signedByVia', vars) : t('doc.signedBy', vars);
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
  const t = useT();
  const locale = useLocale();
  return (
    <div className="doc-sig">
      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
        {t(party === 'landlord' ? 'doc.party.landlord' : 'doc.party.renter')}
      </p>
      {signature ? (
        <>
          {signature.signature_image_url && (
            // Presigned MinIO URL; next/image would need the host allow-listed
            // at build time and this app is CSR-only.
            // eslint-disable-next-line @next/next/no-img-element
            <img
              className="doc-sig-image"
              src={signature.signature_image_url}
              alt={t('doc.signatureAlt', { name: signature.name })}
            />
          )}
          <p style={{ fontWeight: 600 }}>{signatureLine(t, locale, signature)}</p>
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {t(signature.method === 'drawn' ? 'doc.method.drawn' : 'doc.method.otp')}
          </p>
        </>
      ) : (
        <>
          <div className="doc-sig-slot" aria-hidden />
          <p className="pencil">{t('doc.notSigned', { name: fallbackName })}</p>
        </>
      )}
    </div>
  );
}

function DocumentContent() {
  const t = useT();
  const locale = useLocale();
  const params = useParams<{ id: string }>();
  const id = typeof params?.id === 'string' ? params.id : '';

  const [doc, setDoc] = useState<ContractDocument | null>(null);
  const [contract, setContract] = useState<Contract | null>(null);
  const [schedules, setSchedules] = useState<MySchedule[]>([]);
  const [proofs, setProofs] = useState<Proof[]>([]);
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
        // The ledger and the renter's own proofs only decorate the document
        // (Phase 16: the countdown chip beside the rent, an "awaiting
        // confirmation" mark on an instalment), so neither may blank it.
        const [d, c, s, pr] = await Promise.all([
          contractApi.document(id, ac.signal),
          contractApi.get(id, ac.signal).catch(() => null),
          renterApi.schedules(ac.signal).catch(() => null),
          renterApi.proofs(ac.signal).catch(() => null),
        ]);
        if (!live) return;
        setDoc(d);
        setContract(c?.contract ?? null);
        setSchedules((s?.items ?? []).filter((row) => row.contract?.id === id));
        setProofs(pr?.items ?? []);
        setError(null);
      } catch (err) {
        if (!live || (err instanceof DOMException && err.name === 'AbortError')) return;
        setError(
          err instanceof ApiError && err.status === 404
            ? t('doc.notAvailable')
            : errorMessage(t, err),
        );
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
      ac.abort();
    };
  }, [id, t]);

  const runVerify = useCallback(async () => {
    setVerifying(true);
    setVerifyError(null);
    try {
      setVerify(await contractApi.verify(id));
    } catch (err) {
      setVerifyError(errorMessage(t, err));
    } finally {
      setVerifying(false);
    }
  }, [id, t]);

  if (loading) {
    return (
      <Screen bottomBar>
        <p className="pencil">{t('doc.loading')}</p>
      </Screen>
    );
  }

  if (error || !doc) {
    return (
      <Screen bottomBar>
        <Notice tone="error">{error ?? t('doc.loadFailed')}</Notice>
        <Link className="btn btn-secondary" href="/contract">
          {t('doc.backToContracts')}
        </Link>
      </Screen>
    );
  }

  const renterSigned = hasRenterSignature({ signatures: doc.signatures });
  const landlordSigned = hasLandlordSignature({ signatures: doc.signatures });
  const needsSignature = doc.status === 'pending_signature' && !renterSigned;
  const scheduleTotal = doc.schedule.reduce((sum, row) => sum + row.amount, 0);

  /* Phase 16: the next instalment still owed on this tenancy drives the
     countdown chip in the header. The document's own schedule rows carry no
     ids — they are a snapshot, not the ledger — so they are matched to the
     ledger by due date, which is unique within one contract. */
  const ledger = [...schedules].sort((a, b) => a.due_date.localeCompare(b.due_date));
  const unsettled = ledger.find((row) => row.status !== 'paid' && row.status !== 'waived') ?? null;
  const scheduleByDue = new Map(ledger.map((row) => [row.due_date, row]));
  const proofBySchedule = new Map<string, Proof>();
  for (const p of proofs) {
    // `GET /me/proofs` is newest first: the first one seen still speaks.
    if (p.schedule_id && !proofBySchedule.has(p.schedule_id)) proofBySchedule.set(p.schedule_id, p);
  }
  /** The renter's own claim against the instalment due on `dueDate`. */
  const proofFor = (dueDate: string): { awaiting: boolean; rejected: Proof | null } => {
    const row = scheduleByDue.get(dueDate);
    if (!row) return { awaiting: false, rejected: null };
    const full = proofBySchedule.get(row.id) ?? null;
    return {
      awaiting: row.proof?.status === 'submitted' || full?.status === 'submitted',
      rejected: full?.status === 'rejected' ? full : null,
    };
  };

  return (
    <Screen bottomBar style={needsSignature ? { paddingBottom: 176 } : undefined}>
      <header className="no-print" style={{ display: 'grid', gap: 'var(--sp-2)' }}>
        <Link
          href="/contract"
          className="btn btn-quiet"
          style={{ justifySelf: 'start', paddingInline: 0 }}
        >
          <Icon icon="solar:alt-arrow-left-linear" width={18} aria-hidden />
          {t('doc.back')}
        </Link>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--sp-3)' }}>
          <h1 style={{ fontSize: 'var(--text-xl)' }}>
            {contract ? `${contract.unit.name} · ${contract.unit.property_name}` : t('doc.title')}
          </h1>
          <ContractStamp status={doc.status} renterSigned={renterSigned} />
        </div>
      </header>

      {needsSignature && (
        <Notice>{t('doc.readThenSign')}</Notice>
      )}
      {doc.status === 'pending_signature' && renterSigned && !landlordSigned && (
        <Notice>{t('doc.signedWaiting', { org: doc.org.display_name })}</Notice>
      )}

      <article className="doc">
        {/* ---- letterhead ---- */}
        {doc.org.letterhead_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            className="doc-letterhead"
            src={doc.org.letterhead_url}
            alt={t('doc.letterheadAlt', { org: doc.org.display_name })}
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
          <h2>{t('doc.parties')}</h2>
          <table className="ledger ledger-kv">
            <tbody>
              <tr>
                <td>{t('doc.party.landlord')}</td>
                <td className="num">{doc.parties.landlord.name}</td>
              </tr>
              <tr>
                <td>{t('doc.party.renter')}</td>
                <td className="num">
                  {doc.parties.renter.name}
                  {doc.parties.renter.phone_masked && (
                    <span className="sub">{doc.parties.renter.phone_masked}</span>
                  )}
                </td>
              </tr>
              {contract && (
                <>
                  <tr>
                    <td>{t('common.unit')}</td>
                    <td className="num">
                      {contract.unit.name} · {contract.unit.property_name}
                    </td>
                  </tr>
                  <tr>
                    <td>{t('common.rent')}</td>
                    <td className="num">
                      <RentValue contract={contract} />
                      {unsettled && (
                        <span className="sub">
                          <CountdownChip schedule={unsettled} />
                        </span>
                      )}
                      <span className="sub no-print">
                        <Link
                          href="/payments#how-to-pay"
                          style={{ color: 'var(--primary)', fontWeight: 600 }}
                        >
                          {t('payment.next.howToPay')}
                        </Link>
                      </span>
                    </td>
                  </tr>
                  <tr>
                    <td>{t('doc.term')}</td>
                    {/* Each date holds together; the range breaks at the
                        en dash when the pair will not fit. */}
                    <td className="num">
                      <span className="nowrap">{formatDate(locale, contract.start_date)}</span> –{' '}
                      <span className="nowrap">{formatDate(locale, contract.end_date)}</span>
                    </td>
                  </tr>
                </>
              )}
            </tbody>
          </table>
        </section>

        {/* ---- terms (server-sanitized snapshot) ---- */}
        <section className="doc-block">
          <h2>{t('doc.terms')}</h2>
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
            <h2>{t('doc.schedule')}</h2>
            <div className="doc-scroll-x">
              <table className="ledger">
                <thead>
                  <tr>
                    <th>{t('common.due')}</th>
                    <th>{t('common.period')}</th>
                    <th className="num">{t('common.amount')}</th>
                  </tr>
                </thead>
                <tbody>
                  {doc.schedule.map((row) => {
                    const { awaiting, rejected } = proofFor(row.due_date);
                    return (
                      <tr key={`${row.due_date}-${row.period_start}`}>
                        <td>
                          {formatDate(locale, row.due_date)}
                          {awaiting && (
                            <span className="sub">
                              <span className="pencil">{t('due.awaiting')}</span>
                            </span>
                          )}
                          {rejected && (
                            <span
                              className="sub"
                              style={{ color: 'var(--stamp-overdue)' }}
                            >
                              {rejected.rejection_reason
                                ? t('proof.rejected.reason', { reason: rejected.rejection_reason })
                                : t('proof.status.rejected')}
                            </span>
                          )}
                        </td>
                        <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                          {formatDate(locale, row.period_start)} –{' '}
                          {formatDate(locale, row.period_end)}
                        </td>
                        <td className="num amount">{money(row.amount)}</td>
                      </tr>
                    );
                  })}
                  <tr className="total">
                    <td colSpan={2}>{t('doc.totalOverTerm')}</td>
                    <td className="num amount">{money(scheduleTotal)}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        )}

        {/* ---- signatures ---- */}
        <section className="doc-block">
          <h2>{t('doc.signatures')}</h2>
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
          <h2>{t('doc.verification')}</h2>
          <p className="doc-hash">{doc.snapshot_hash}</p>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-xs)' }}>
            {t('doc.fingerprint', { date: formatDate(locale, doc.generated_at) })}
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
              {verifying ? t('common.checking') : t('doc.verify')}
            </button>
            {verify &&
              (verify.valid ? (
                <span className="stamp stamp-paid">{t('doc.valid')}</span>
              ) : (
                <span className="stamp stamp-overdue">{t('doc.tampered')}</span>
              ))}
          </div>
          {verify && !verify.valid && (
            <p style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>
              {t('doc.mismatch', { org: doc.org.display_name })}
            </p>
          )}
          {verifyError && <Notice tone="error">{verifyError}</Notice>}
        </section>
      </article>

      <div className="no-print" style={{ display: 'grid', gap: 'var(--sp-3)' }}>
        <button type="button" className="btn btn-secondary" onClick={() => window.print()}>
          <Icon icon="solar:printer-minimalistic-linear" width={20} aria-hidden />
          {t('doc.print')}
        </button>
      </div>

      {needsSignature && (
        <div className="doc-cta">
          <Link className="btn btn-primary" href={`/contract/${encodeURIComponent(id)}/sign`}>
            {t('doc.acceptSign')}
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
