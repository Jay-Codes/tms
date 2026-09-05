'use client';

/**
 * One contract (FLOWS flow 3 steps 5–6).
 *
 * The page is the document: the org's letterhead, the terms exactly as they
 * were snapshotted, the payment schedule, the signature block and the hash that
 * proves none of it moved. "Print / Save as PDF" is the browser's own — there
 * is no server-rendered PDF (SPEC §5.5).
 *
 * The two decisions a landlord can take live above the paper: **Activate**,
 * which countersigns and generates the schedule rows, and **Terminate**.
 * Activate is disabled until the renter has signed; the only way past that is
 * the landlord-recorded escape hatch (FLOWS 3.6), which is deliberately a
 * separate, reasoned sheet because it lands in the audit log flagged.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import {
  ContractStatusStamp,
  Facts,
  ScheduleStatusStamp,
  landlordSignature,
  renterSignature,
} from '../../../components/ContractBits';
import { DocumentPaper, PrintStyles } from '../../../components/DocumentPaper';
import { Field, Note, ProblemNote } from '../../../components/FormBits';
import { PageHead, Shell } from '../../../components/Shell';
import { Sheet } from '../../../components/Sheet';
import {
  ApiError,
  contractsApi,
  toApiError,
  unwrapContract,
  type Contract,
  type ContractDocument,
  type ContractSignature,
  type ContractVerification,
  type ScheduleRow,
} from '../../../lib/api';
import { Amount, fmtDate, fmtTZS, todayISO } from '../../../lib/format';

/* ----------------------------- signature block ---------------------------- */

function SignatureCard({ label, signature }: { label: string; signature: ContractSignature | null }) {
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-2)', alignContent: 'start' }}>
      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</span>
      {signature ? (
        <>
          {signature.signature_image_url ? (
            /* eslint-disable-next-line @next/next/no-img-element -- presigned MinIO URL */
            <img
              src={signature.signature_image_url}
              alt={`${signature.name}'s signature`}
              style={{ height: 64, width: 'auto', objectFit: 'contain', objectPosition: 'left' }}
            />
          ) : null}
          <span style={{ fontWeight: 600 }}>{signature.name}</span>
          <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            Signed {fmtDate(signature.signed_at)}
            {signature.phone_masked ? ` via phone ${signature.phone_masked}` : ''}
            {signature.method ? ` · ${signature.method.replace('_', ' ')}` : ''}
          </span>
        </>
      ) : (
        <span className="pencil">not signed yet</span>
      )}
      <hr className="rule" style={{ marginTop: 'var(--sp-2)' }} />
    </div>
  );
}

/* -------------------------------- sheets --------------------------------- */

function RecordOnBehalfForm({
  renterName,
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  renterName: string;
  busy: boolean;
  error: ApiError | null;
  onSubmit: (reason: string) => void;
  onCancel: () => void;
}) {
  const [reason, setReason] = useState('');
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(reason.trim());
      }}
      style={{ display: 'grid', gap: 'var(--sp-4)' }}
      noValidate
    >
      <ProblemNote error={error} />
      <p
        style={{
          display: 'flex',
          gap: 'var(--sp-3)',
          padding: 'var(--sp-3) var(--sp-4)',
          border: '1px solid var(--stamp-overdue)',
          borderRadius: 'var(--radius-sm)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:danger-triangle-linear" width={20} />
        <span>
          This activates the contract without {renterName}&apos;s signature. No renter signature is
          recorded, and the action is flagged in the audit log as landlord-recorded. Use it only when the
          renter has no phone.
        </span>
      </p>
      <Field id="lr_reason" label="Why are you recording this yourself?" hint={`${reason.length}/200`}>
        <textarea
          id="lr_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="e.g. The renter has no mobile phone; the agreement was signed on paper."
        />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? 'Activating…' : 'Record and activate'}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function TerminateForm({
  busy,
  error,
  onSubmit,
  onCancel,
}: {
  busy: boolean;
  error: ApiError | null;
  onSubmit: (reason: string, effectiveDate: string) => void;
  onCancel: () => void;
}) {
  const [reason, setReason] = useState('');
  const [date, setDate] = useState(todayISO());
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(reason.trim(), date);
      }}
      style={{ display: 'grid', gap: 'var(--sp-4)' }}
      noValidate
    >
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>
        Payments still outstanding after the effective date are waived, the unit goes back on the vacancy
        board, and the renter is told by SMS. The contract itself is kept, never edited.
      </p>
      <Field id="t_reason" label="Reason" hint={`${reason.length}/200 — the renter is sent this.`} error={error?.errors.reason}>
        <textarea
          id="t_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="e.g. The renter is moving out early by mutual agreement."
        />
      </Field>
      <Field id="t_date" label="Effective date" error={error?.errors.effective_date}>
        <input id="t_date" className="input" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? 'Terminating…' : 'Terminate contract'}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}

/* --------------------------------- page ---------------------------------- */

function ContractBody({ id }: { id: string }) {
  const [contract, setContract] = useState<Contract | null>(null);
  const [doc, setDoc] = useState<ContractDocument | null>(null);
  const [schedules, setSchedules] = useState<ScheduleRow[] | null>(null);
  const [verification, setVerification] = useState<ContractVerification | null>(null);

  const [error, setError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [behalfOpen, setBehalfOpen] = useState(false);
  const [terminateOpen, setTerminateOpen] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const c = unwrapContract(await contractsApi.get(id, signal));
        setContract(c);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        return;
      }
      // The document and the ledger are separate reads; neither should take the
      // page down if it fails on its own.
      try {
        setDoc(await contractsApi.document(id, signal));
      } catch (e) {
        if (!(e instanceof DOMException)) setDoc(null);
      }
      try {
        const res = await contractsApi.schedules(id, signal);
        setSchedules(res.items ?? []);
      } catch (e) {
        if (!(e instanceof DOMException)) setSchedules([]);
      }
    },
    [id],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const act = async (run: () => Promise<unknown>, done: string) => {
    setBusy(true);
    setActionError(null);
    try {
      await run();
      setNote(done);
      setBehalfOpen(false);
      setTerminateOpen(false);
      await load();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const verify = async () => {
    setVerifying(true);
    setActionError(null);
    try {
      setVerification(await contractsApi.verify(id));
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setVerifying(false);
    }
  };

  if (error && !contract) {
    return (
      <>
        <PageHead title="Contract" />
        <ProblemNote error={error} />
        <p style={{ marginTop: 'var(--sp-4)' }}>
          <Link href="/contracts" className="btn btn-quiet">
            Back to contracts
          </Link>
        </p>
      </>
    );
  }

  if (!contract) {
    return (
      <>
        <PageHead title="Contract" />
        <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>
      </>
    );
  }

  const signedByRenter = renterSignature(contract);
  const signedByLandlord = landlordSignature(contract);
  const canActivate = contract.status === 'pending_signature' && signedByRenter !== null;
  const canTerminate = ['pending_signature', 'active', 'expiring'].includes(contract.status);
  const rows = schedules ?? [];
  const org = doc?.org;

  return (
    <>
      <PrintStyles />

      <div className="no-print">
        <PageHead
          title={`${contract.unit?.name ?? 'Contract'} · ${contract.renter?.full_name ?? ''}`}
          lead={`${contract.unit?.property_name ?? ''} · ${fmtDate(contract.start_date)} → ${fmtDate(contract.end_date)}`}
          actions={
            <>
              <Link href="/contracts" className="btn btn-quiet">
                <Icon icon="solar:arrow-left-linear" width={20} /> Contracts
              </Link>
              <button type="button" className="btn btn-secondary" onClick={() => window.print()}>
                <Icon icon="solar:printer-linear" width={20} /> Print / Save as PDF
              </button>
            </>
          }
        />

        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
          <ContractStatusStamp status={contract.status} />
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            written {fmtDate(contract.created_at)}
            {contract.activated_at ? ` · activated ${fmtDate(contract.activated_at)}` : ''}
            {contract.terminated_at ? ` · terminated ${fmtDate(contract.terminated_at)}` : ''}
          </span>
        </div>

        {contract.termination_reason ? (
          <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)' }}>
            Reason given: {contract.termination_reason}
          </p>
        ) : null}

        <div style={{ display: 'grid', gap: 'var(--sp-3)', marginTop: 'var(--sp-4)' }}>
          {note ? <Note>{note}</Note> : null}
          <ProblemNote error={actionError} />
        </div>

        {/* ------------------------------ decisions ----------------------------- */}
        {contract.status === 'pending_signature' || canTerminate ? (
          <section style={{ marginTop: 'var(--sp-5)' }}>
            <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap', alignItems: 'center' }}>
              {contract.status === 'pending_signature' ? (
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={!canActivate || busy}
                  title={canActivate ? undefined : 'The renter has not signed yet.'}
                  onClick={() =>
                    void act(
                      () => contractsApi.activate(id),
                      'Activated. The schedule is generated and the unit is now occupied.',
                    )
                  }
                >
                  <Icon icon="solar:check-circle-linear" width={20} /> Activate
                </button>
              ) : null}
              {contract.status === 'pending_signature' && !canActivate ? (
                <button type="button" className="btn btn-quiet" onClick={() => setBehalfOpen(true)} disabled={busy}>
                  Record on renter&apos;s behalf
                </button>
              ) : null}
              {canTerminate ? (
                <button type="button" className="btn btn-danger" onClick={() => setTerminateOpen(true)} disabled={busy}>
                  <Icon icon="solar:close-circle-linear" width={20} /> Terminate
                </button>
              ) : null}
            </div>
            {contract.status === 'pending_signature' && !canActivate ? (
              <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                Waiting for {contract.renter?.full_name ?? 'the renter'} to sign. Activating countersigns the
                contract, generates every payment in the schedule and marks the unit occupied.
              </p>
            ) : null}
          </section>
        ) : null}

        {/* -------------------------------- facts ------------------------------- */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>Terms</h2>
          <div style={{ maxWidth: 640 }}>
            <Facts
              rows={[
                ['Unit', `${contract.unit?.name ?? '—'} · ${contract.unit?.property_name ?? ''}`],
                ['Renter', `${contract.renter?.full_name ?? '—'}${contract.renter?.phone ? ` · ${contract.renter.phone}` : ''}`],
                ['Rent', <Amount key="rent" value={contract.rent_amount} per={contract.rent_period_days} />],
                [
                  'Payment period',
                  contract.payment_period
                    ? `${contract.payment_period.label} · ${contract.payment_period.days} days`
                    : '—',
                ],
                ['Tenancy length', contract.term_days ? `${contract.term_days} days` : '—'],
                ['Starts', fmtDate(contract.start_date)],
                ['Ends', fmtDate(contract.end_date)],
                ['Due day', contract.due_day ? String(contract.due_day) : 'From the start date'],
              ]}
            />
          </div>
        </section>

        {/* ------------------------------ schedules ----------------------------- */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <div
            style={{
              display: 'flex',
              alignItems: 'flex-end',
              justifyContent: 'space-between',
              gap: 'var(--sp-4)',
              margin: 'var(--sp-4) 0',
            }}
          >
            <div>
              <h2 style={{ fontSize: 'var(--text-lg)' }}>Payment schedule</h2>
              <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                Generated across the whole tenancy when the contract is activated.
              </p>
            </div>
            <button type="button" className="btn btn-secondary" disabled title="Recording payments ships in Phase 5.">
              Record payment
            </button>
          </div>

          <table className="ledger">
            <thead>
              <tr>
                <th>Period</th>
                <th>Due</th>
                <th className="num">Amount</th>
                <th className="num">Paid</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {schedules === null ? (
                <tr>
                  <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                    Loading…
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                    No schedule yet — it is written when the contract is activated.
                  </td>
                </tr>
              ) : (
                <>
                  {rows.map((s) => (
                    <tr key={s.id}>
                      <td style={{ fontSize: 'var(--text-sm)' }}>
                        {fmtDate(s.period_start)} → {fmtDate(s.period_end)}
                      </td>
                      <td>{fmtDate(s.due_date)}</td>
                      <td className="num">{fmtTZS(s.amount)}</td>
                      <td className="num">{s.paid_amount ? fmtTZS(s.paid_amount) : <span className="pencil">—</span>}</td>
                      <td>
                        <ScheduleStatusStamp status={s.status} />
                      </td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td colSpan={2}>Total</td>
                    <td className="num">{fmtTZS(rows.reduce((t, s) => t + (s.amount ?? 0), 0))}</td>
                    <td className="num">{fmtTZS(rows.reduce((t, s) => t + (s.paid_amount ?? 0), 0))}</td>
                    <td />
                  </tr>
                </>
              )}
            </tbody>
          </table>
        </section>

        <hr className="rule rule-strong" style={{ marginTop: 'var(--sp-6)' }} />
        <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>Document</h2>
      </div>

      {/* -------------------------------- paper -------------------------------- */}
      {doc ? (
        <DocumentPaper
          html={doc.terms_html}
          displayName={org?.display_name}
          logoUrl={org?.logo_url}
          letterheadUrl={org?.letterhead_url}
          footerText={org?.footer_text}
        >
          <section style={{ marginTop: 'var(--sp-5)' }}>
            <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-3)' }}>Parties</h3>
            <Facts
              rows={[
                ['Landlord', doc.parties?.landlord?.name ?? '—'],
                [
                  'Renter',
                  `${doc.parties?.renter?.name ?? '—'}${
                    doc.parties?.renter?.phone_masked ? ` · ${doc.parties.renter.phone_masked}` : ''
                  }`,
                ],
              ]}
            />
          </section>

          {(doc.schedule ?? []).length > 0 ? (
            <section style={{ marginTop: 'var(--sp-5)' }}>
              <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-3)' }}>Payments</h3>
              <table className="ledger">
                <thead>
                  <tr>
                    <th>Period</th>
                    <th>Due</th>
                    <th className="num">Amount</th>
                  </tr>
                </thead>
                <tbody>
                  {doc.schedule.map((s, i) => (
                    <tr key={`${s.due_date}-${i}`}>
                      <td style={{ fontSize: 'var(--text-sm)' }}>
                        {fmtDate(s.period_start)} → {fmtDate(s.period_end)}
                      </td>
                      <td>{fmtDate(s.due_date)}</td>
                      <td className="num">{fmtTZS(s.amount)}</td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td colSpan={2}>Total</td>
                    <td className="num">{fmtTZS(doc.schedule.reduce((t, s) => t + (s.amount ?? 0), 0))}</td>
                  </tr>
                </tbody>
              </table>
            </section>
          ) : null}

          <section style={{ marginTop: 'var(--sp-6)' }}>
            <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-4)' }}>Signatures</h3>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-6)' }}>
              <SignatureCard
                label="Renter"
                signature={(doc.signatures ?? []).find((s) => s.party === 'renter') ?? signedByRenter}
              />
              <SignatureCard
                label="Landlord"
                signature={(doc.signatures ?? []).find((s) => s.party === 'landlord') ?? signedByLandlord}
              />
            </div>
          </section>

          <section style={{ marginTop: 'var(--sp-6)' }}>
            <hr className="rule" />
            <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
              Verification hash{' '}
              <span className="num" style={{ wordBreak: 'break-all' }}>
                {doc.snapshot_hash ?? contract.snapshot_hash}
              </span>
            </p>
            <div className="no-print" style={{ marginTop: 'var(--sp-3)', display: 'flex', gap: 'var(--sp-3)', alignItems: 'center', flexWrap: 'wrap' }}>
              <button type="button" className="btn btn-quiet" onClick={() => void verify()} disabled={verifying}>
                <Icon icon="solar:shield-check-linear" width={20} /> {verifying ? 'Checking…' : 'Verify'}
              </button>
              {verification ? (
                verification.valid ? (
                  <span className="stamp stamp-paid">Unchanged since signing</span>
                ) : (
                  <span className="stamp stamp-overdue">Does not match</span>
                )
              ) : null}
            </div>
          </section>
        </DocumentPaper>
      ) : (
        <p className="no-print" style={{ color: 'var(--ink-soft)' }}>
          The document could not be loaded.
        </p>
      )}

      <Sheet
        open={behalfOpen}
        title="Record on the renter's behalf"
        onClose={() => setBehalfOpen(false)}
        width={520}
      >
        <RecordOnBehalfForm
          renterName={contract.renter?.full_name ?? 'the renter'}
          busy={busy}
          error={actionError}
          onCancel={() => setBehalfOpen(false)}
          onSubmit={(reason) =>
            void act(
              () => contractsApi.activate(id, { landlord_recorded: true, reason }),
              'Activated and recorded on the renter’s behalf. The action is flagged in the audit log.',
            )
          }
        />
      </Sheet>

      <Sheet open={terminateOpen} title="Terminate this contract" onClose={() => setTerminateOpen(false)} width={520}>
        <TerminateForm
          busy={busy}
          error={actionError}
          onCancel={() => setTerminateOpen(false)}
          onSubmit={(reason, effective_date) =>
            void act(
              () => contractsApi.terminate(id, { reason, effective_date }),
              'Terminated. The unit is back on the vacancy board and the renter has been notified.',
            )
          }
        />
      </Sheet>
    </>
  );
}

export default function ContractPage() {
  const params = useParams<{ id: string }>();
  const id = String(params?.id ?? '');
  return (
    <Shell>
      <ContractBody id={id} />
    </Shell>
  );
}
