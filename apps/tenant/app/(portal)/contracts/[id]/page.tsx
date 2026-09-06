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
} from '../../../../components/ContractBits';
import { DocumentPaper, PrintStyles } from '../../../../components/DocumentPaper';
import { Field, Note, ProblemNote } from '../../../../components/FormBits';
import { DaysOverdue, PaymentsTable, ReverseSheet } from '../../../../components/PaymentBits';
import { RecordPaymentSheet, type RecordPaymentTarget } from '../../../../components/RecordPaymentSheet';
import { PageHead } from '../../../../components/PageHead';
import { Sheet } from '../../../../components/Sheet';
import {
  ApiError,
  contractsApi,
  isUnsettled,
  paymentsApi,
  remainingOn,
  toApiError,
  unwrapContract,
  type Contract,
  type ContractDocument,
  type ContractSignature,
  type ContractVerification,
  type Payment,
  type Schedule,
  type ScheduleRow,
} from '../../../../lib/api';
import { Amount, fmtDate, fmtTZS, todayISO } from '../../../../lib/format';
import { LOCALE_LABELS, TableScroll, isLocale, useT } from '@tms/ui';

/* ----------------------------- signature block ---------------------------- */

function SignatureCard({ label, signature }: { label: string; signature: ContractSignature | null }) {
  const t = useT();
  return (
    <div style={{ display: 'grid', gap: 'var(--sp-2)', alignContent: 'start' }}>
      <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</span>
      {signature ? (
        <>
          {signature.signature_image_url ? (
            /* eslint-disable-next-line @next/next/no-img-element -- presigned MinIO URL */
            <img
              src={signature.signature_image_url}
              alt={t('contracts.sig.alt', { name: signature.name })}
              style={{ height: 64, width: 'auto', objectFit: 'contain', objectPosition: 'left' }}
            />
          ) : null}
          <span style={{ fontWeight: 600 }}>{signature.name}</span>
          <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {t('contracts.sig.signed', { date: fmtDate(signature.signed_at) })}
            {signature.phone_masked ? ` ${t('contracts.sig.via_phone', { phone: signature.phone_masked })}` : ''}
            {signature.method ? ` · ${signature.method.replace('_', ' ')}` : ''}
          </span>
        </>
      ) : (
        <span className="pencil">{t('contracts.sig.not_signed')}</span>
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
  const t = useT();
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
        <span>{t('contracts.behalf.warning', { name: renterName })}</span>
      </p>
      <Field
        id="lr_reason"
        label={t('contracts.behalf.reason')}
        hint={t('contracts.chars_max', { n: reason.length })}
      >
        <textarea
          id="lr_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('contracts.behalf.reason_placeholder')}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? t('contracts.behalf.submitting') : t('contracts.behalf.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
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
  const t = useT();
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
      <p style={{ color: 'var(--ink-soft)' }}>{t('contracts.terminate.lead')}</p>
      <Field
        id="t_reason"
        label={t('contracts.terminate.reason')}
        hint={`${t('contracts.chars_max', { n: reason.length })} — ${t('contracts.terminate.reason_note')}`}
        error={error?.errors.reason}
      >
        <textarea
          id="t_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('contracts.terminate.reason_placeholder')}
        />
      </Field>
      <Field id="t_date" label={t('contracts.terminate.effective_date')} error={error?.errors.effective_date}>
        <input id="t_date" className="input" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? t('contracts.terminate.submitting') : t('contracts.terminate.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/* --------------------------------- page ---------------------------------- */

function ContractBody({ id }: { id: string }) {
  const t = useT();
  const [contract, setContract] = useState<Contract | null>(null);
  const [doc, setDoc] = useState<ContractDocument | null>(null);
  const [schedules, setSchedules] = useState<ScheduleRow[] | null>(null);
  const [payments, setPayments] = useState<Payment[] | null>(null);
  const [verification, setVerification] = useState<ContractVerification | null>(null);

  const [recordTarget, setRecordTarget] = useState<RecordPaymentTarget | null>(null);
  const [reversing, setReversing] = useState<Payment | null>(null);
  const [reverseBusy, setReverseBusy] = useState(false);
  const [reverseError, setReverseError] = useState<ApiError | null>(null);

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
      // Payment history is a Phase 5 read; a contract written before the
      // payments API existed must still render, so this failure is swallowed
      // into an empty ledger like the two above it.
      try {
        const res = await paymentsApi.list({ contract_id: id, limit: 200 }, signal);
        setPayments(res.items ?? []);
      } catch (e) {
        if (!(e instanceof DOMException)) setPayments([]);
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

  const reverse = async (reason: string) => {
    if (!reversing) return;
    setReverseBusy(true);
    setReverseError(null);
    try {
      await paymentsApi.reverse(reversing.id, reason);
      setReversing(null);
      setNote(t('contracts.payments.reversed'));
      await load();
    } catch (e) {
      setReverseError(toApiError(e));
    } finally {
      setReverseBusy(false);
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
        <PageHead title={t('contracts.detail.title')} />
        <ProblemNote error={error} />
        <p style={{ marginTop: 'var(--sp-4)' }}>
          <Link href="/contracts" className="btn btn-quiet">
            {t('contracts.detail.back')}
          </Link>
        </p>
      </>
    );
  }

  if (!contract) {
    return (
      <>
        <PageHead title={t('contracts.detail.title')} />
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
      </>
    );
  }

  const signedByRenter = renterSignature(contract);
  const signedByLandlord = landlordSignature(contract);
  const canActivate = contract.status === 'pending_signature' && signedByRenter !== null;
  const canTerminate = ['pending_signature', 'active', 'expiring'].includes(contract.status);
  const rows = schedules ?? [];
  const org = doc?.org;
  const summary = contract.schedules_summary;
  /** Money can only be recorded against a live tenancy (API.md Phase 5). */
  const canRecord = contract.status === 'active' || contract.status === 'expiring';
  const contractLabel = `${contract.unit?.name ?? ''} · ${contract.unit?.property_name ?? ''} — ${
    contract.renter?.full_name ?? ''
  }`;
  const renterName = contract.renter?.full_name ?? t('contracts.the_renter');
  const docLanguage = isLocale(contract.language) ? LOCALE_LABELS[contract.language] : null;
  const openRecord = (scheduleId?: string) =>
    setRecordTarget({ contractId: id, scheduleId, label: contractLabel });

  return (
    <>
      <PrintStyles />

      <div className="no-print">
        <PageHead
          title={`${contract.unit?.name ?? t('contracts.detail.title')} · ${contract.renter?.full_name ?? ''}`}
          lead={`${contract.unit?.property_name ?? ''} · ${fmtDate(contract.start_date)} → ${fmtDate(contract.end_date)}`}
          actions={
            <>
              <Link href="/contracts" className="btn btn-quiet">
                <Icon icon="solar:arrow-left-linear" width={20} /> {t('nav.contracts')}
              </Link>
              <button type="button" className="btn btn-secondary" onClick={() => window.print()}>
                <Icon icon="solar:printer-linear" width={20} /> {t('contracts.detail.print')}
              </button>
            </>
          }
        />

        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
          <ContractStatusStamp status={contract.status} />
          <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {t('contracts.meta.written', { date: fmtDate(contract.created_at) })}
            {contract.activated_at
              ? ` · ${t('contracts.meta.activated', { date: fmtDate(contract.activated_at) })}`
              : ''}
            {contract.terminated_at
              ? ` · ${t('contracts.meta.terminated', { date: fmtDate(contract.terminated_at) })}`
              : ''}
          </span>
        </div>

        {/* The money at a glance (API.md `schedules_summary`), so the state of
            the tenancy is readable without scrolling to the ledger. */}
        {summary ? (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sp-5)',
              flexWrap: 'wrap',
              marginTop: 'var(--sp-4)',
              paddingTop: 'var(--sp-3)',
              borderTop: '1px solid var(--rule)',
              fontSize: 'var(--text-sm)',
            }}
          >
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.paid')} </span>
              <strong className="num">
                {summary.paid_count}/{summary.count}
              </strong>
            </span>
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.overdue')} </span>
              {summary.overdue_count > 0 ? (
                <strong className="num" style={{ color: 'var(--stamp-overdue)' }}>
                  {summary.overdue_count}
                </strong>
              ) : (
                <span className="pencil">{t('contracts.summary.overdue_none')}</span>
              )}
            </span>
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.next_due')} </span>
              {summary.next_due_date ? (
                <strong>
                  {fmtDate(summary.next_due_date)} · {fmtTZS(summary.next_due_amount ?? 0)}
                </strong>
              ) : (
                <span className="pencil">{t('contracts.summary.nothing_outstanding')}</span>
              )}
            </span>
            <span>
              <span style={{ color: 'var(--ink-soft)' }}>{t('contracts.summary.total')} </span>
              <strong>{fmtTZS(summary.total)}</strong>
            </span>
          </div>
        ) : null}

        {contract.termination_reason ? (
          <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)' }}>
            {t('contracts.reason_given', { reason: contract.termination_reason })}
          </p>
        ) : null}

        <div style={{ display: 'grid', gap: 'var(--sp-3)', marginTop: 'var(--sp-4)' }}>
          {note ? <Note>{note}</Note> : null}
          <ProblemNote error={actionError} />
        </div>

        {/* ------------------------------ decisions ----------------------------- */}
        {contract.status === 'pending_signature' || canTerminate ? (
          <section style={{ marginTop: 'var(--sp-5)' }}>
            <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap', alignItems: 'center' }}>
              {contract.status === 'pending_signature' ? (
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={!canActivate || busy}
                  title={canActivate ? undefined : t('contracts.activate.blocked')}
                  onClick={() =>
                    void act(
                      () => contractsApi.activate(id),
                      t('contracts.activate.done'),
                    )
                  }
                >
                  <Icon icon="solar:check-circle-linear" width={20} /> {t('contracts.activate')}
                </button>
              ) : null}
              {contract.status === 'pending_signature' && !canActivate ? (
                <button type="button" className="btn btn-quiet" onClick={() => setBehalfOpen(true)} disabled={busy}>
                  {t('contracts.behalf.open')}
                </button>
              ) : null}
              {canTerminate ? (
                <button type="button" className="btn btn-danger" onClick={() => setTerminateOpen(true)} disabled={busy}>
                  <Icon icon="solar:close-circle-linear" width={20} /> {t('contracts.terminate')}
                </button>
              ) : null}
            </div>
            {contract.status === 'pending_signature' && !canActivate ? (
              <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {t('contracts.activate.waiting', { name: renterName })}
              </p>
            ) : null}
          </section>
        ) : null}

        {/* -------------------------------- facts ------------------------------- */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('contracts.terms.title')}</h2>
          <div style={{ maxWidth: 640 }}>
            <Facts
              rows={[
                [t('common.unit'), `${contract.unit?.name ?? '—'} · ${contract.unit?.property_name ?? ''}`],
                [
                  t('common.renter'),
                  `${contract.renter?.full_name ?? '—'}${contract.renter?.phone ? ` · ${contract.renter.phone}` : ''}`,
                ],
                [
                  t('contracts.col.rent'),
                  <Amount key="rent" value={contract.rent_amount} per={contract.rent_period_days} />,
                ],
                [
                  t('contracts.fact.payment_period'),
                  contract.payment_period
                    ? t('contracts.fact.period_days', {
                        label: contract.payment_period.label,
                        days: contract.payment_period.days,
                      })
                    : '—',
                ],
                [
                  t('contracts.fact.term_length'),
                  contract.term_days ? t.n('common.day', contract.term_days) : '—',
                ],
                [t('contracts.fact.starts'), fmtDate(contract.start_date)],
                [t('contracts.fact.ends'), fmtDate(contract.end_date)],
                [
                  t('contracts.fact.due_day'),
                  contract.due_day ? String(contract.due_day) : t('contracts.fact.due_day_default'),
                ],
                /* Phase 13: which language the document itself was written in. */
                [t('contracts.language'), docLanguage ?? <span key="lang" className="pencil">{t('contracts.language.unknown')}</span>],
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
              <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('contracts.schedule.title')}</h2>
              <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {t('contracts.schedule.lead')}
              </p>
            </div>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={!canRecord || rows.length === 0}
              title={
                canRecord ? t('contracts.schedule.record_hint') : t('contracts.schedule.record_blocked')
              }
              onClick={() => openRecord()}
            >
              <Icon icon="solar:wallet-money-linear" width={20} /> {t('contracts.schedule.record')}
            </button>
          </div>

          <TableScroll label={t('contracts.schedule.table_label')}>
          <table className="ledger">
            <thead>
              <tr>
                <th>{t('contracts.schedule.col.period')}</th>
                <th>{t('contracts.schedule.col.due')}</th>
                <th className="num">{t('common.amount')}</th>
                <th className="num">{t('contracts.schedule.col.paid')}</th>
                <th>{t('common.status')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {schedules === null ? (
                <tr>
                  <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                    {t('common.loading')}
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                    {t('contracts.schedule.empty')}
                  </td>
                </tr>
              ) : (
                <>
                  {rows.map((s) => (
                    <tr key={s.id}>
                      <td style={{ fontSize: 'var(--text-sm)' }}>
                        {fmtDate(s.period_start)} → {fmtDate(s.period_end)}
                      </td>
                      <td>
                        {fmtDate(s.due_date)}
                        <DaysOverdue days={(s as Schedule).days_overdue} />
                      </td>
                      <td className="num">{fmtTZS(s.amount)}</td>
                      <td className="num">{s.paid_amount ? fmtTZS(s.paid_amount) : <span className="pencil">—</span>}</td>
                      <td>
                        <ScheduleStatusStamp status={s.status} />
                        {s.status === 'partial' ? (
                          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>
                            {t('contracts.schedule.still_owing', { amount: fmtTZS(remainingOn(s)) })}
                          </div>
                        ) : null}
                      </td>
                      <td>
                        {canRecord && isUnsettled(s) ? (
                          <button
                            type="button"
                            className="btn btn-secondary"
                            style={{ minHeight: 36 }}
                            onClick={() => openRecord(s.id)}
                          >
                            {t('contracts.schedule.record')}
                          </button>
                        ) : null}
                      </td>
                    </tr>
                  ))}
                  <tr className="total">
                    <td colSpan={2}>{t('common.total')}</td>
                    <td className="num">{fmtTZS(rows.reduce((t, s) => t + (s.amount ?? 0), 0))}</td>
                    <td className="num">{fmtTZS(rows.reduce((t, s) => t + (s.paid_amount ?? 0), 0))}</td>
                    <td colSpan={2} />
                  </tr>
                </>
              )}
            </tbody>
          </table>
          </TableScroll>
        </section>

        {/* ------------------------------ payments ------------------------------ */}
        <section style={{ marginTop: 'var(--sp-6)' }}>
          <hr className="rule rule-strong" />
          <div style={{ margin: 'var(--sp-4) 0' }}>
            <h2 style={{ fontSize: 'var(--text-lg)' }}>{t('contracts.payments.title')}</h2>
            <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {t('contracts.payments.lead')}
            </p>
          </div>
          <PaymentsTable
            items={payments}
            showRenter={false}
            showUnit={false}
            emptyText={t('contracts.payments.empty')}
            onReverse={(p) => {
              setReverseError(null);
              setReversing(p);
            }}
          />
        </section>

        <hr className="rule rule-strong" style={{ marginTop: 'var(--sp-6)' }} />
        <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('contracts.doc.title')}</h2>
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
            <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-3)' }}>{t('contracts.doc.parties')}</h3>
            <Facts
              rows={[
                [t('contracts.doc.landlord'), doc.parties?.landlord?.name ?? '—'],
                [
                  t('common.renter'),
                  `${doc.parties?.renter?.name ?? '—'}${
                    doc.parties?.renter?.phone_masked ? ` · ${doc.parties.renter.phone_masked}` : ''
                  }`,
                ],
              ]}
            />
          </section>

          {(doc.schedule ?? []).length > 0 ? (
            <section style={{ marginTop: 'var(--sp-5)' }}>
              <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-3)' }}>{t('contracts.doc.payments')}</h3>
              <div className="doc-scroll-x">
              <table className="ledger">
                <thead>
                  <tr>
                    <th>{t('contracts.schedule.col.period')}</th>
                    <th>{t('contracts.schedule.col.due')}</th>
                    <th className="num">{t('common.amount')}</th>
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
                    <td colSpan={2}>{t('common.total')}</td>
                    <td className="num">{fmtTZS(doc.schedule.reduce((t, s) => t + (s.amount ?? 0), 0))}</td>
                  </tr>
                </tbody>
              </table>
              </div>
            </section>
          ) : null}

          <section style={{ marginTop: 'var(--sp-6)' }}>
            <h3 style={{ fontSize: 'var(--text-md)', marginBottom: 'var(--sp-4)' }}>{t('contracts.doc.signatures')}</h3>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-6)' }}>
              <SignatureCard
                label={t('common.renter')}
                signature={(doc.signatures ?? []).find((s) => s.party === 'renter') ?? signedByRenter}
              />
              <SignatureCard
                label={t('contracts.doc.landlord')}
                signature={(doc.signatures ?? []).find((s) => s.party === 'landlord') ?? signedByLandlord}
              />
            </div>
          </section>

          <section style={{ marginTop: 'var(--sp-6)' }}>
            <hr className="rule" />
            <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
              {t('contracts.verify.hash')}{' '}
              <span className="num" style={{ wordBreak: 'break-all', whiteSpace: 'normal' }}>
                {doc.snapshot_hash ?? contract.snapshot_hash}
              </span>
            </p>
            <div className="no-print" style={{ marginTop: 'var(--sp-3)', display: 'flex', gap: 'var(--sp-3)', alignItems: 'center', flexWrap: 'wrap' }}>
              <button type="button" className="btn btn-quiet" onClick={() => void verify()} disabled={verifying}>
                <Icon icon="solar:shield-check-linear" width={20} /> {verifying ? t('contracts.verify.checking') : t('contracts.verify.action')}
              </button>
              {verification ? (
                verification.valid ? (
                  <span className="stamp stamp-paid">{t('contracts.verify.valid')}</span>
                ) : (
                  <span className="stamp stamp-overdue">{t('contracts.verify.invalid')}</span>
                )
              ) : null}
            </div>
          </section>
        </DocumentPaper>
      ) : (
        <p className="no-print" style={{ color: 'var(--ink-soft)' }}>
          {t('contracts.doc.unavailable')}
        </p>
      )}

      <RecordPaymentSheet
        open={recordTarget !== null}
        target={recordTarget}
        onClose={() => setRecordTarget(null)}
        onRecorded={() => void load()}
      />

      <ReverseSheet
        payment={reversing}
        busy={reverseBusy}
        error={reverseError}
        onClose={() => setReversing(null)}
        onSubmit={(reason) => void reverse(reason)}
      />

      <Sheet
        open={behalfOpen}
        title={t('contracts.behalf.title')}
        onClose={() => setBehalfOpen(false)}
        width={520}
      >
        <RecordOnBehalfForm
          renterName={renterName}
          busy={busy}
          error={actionError}
          onCancel={() => setBehalfOpen(false)}
          onSubmit={(reason) =>
            void act(
              () => contractsApi.activate(id, { landlord_recorded: true, reason }),
              t('contracts.behalf.done'),
            )
          }
        />
      </Sheet>

      <Sheet open={terminateOpen} title={t('contracts.terminate.title')} onClose={() => setTerminateOpen(false)} width={520}>
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
    <>
      <ContractBody id={id} />
    </>
  );
}
