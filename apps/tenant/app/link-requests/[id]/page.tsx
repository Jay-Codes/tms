'use client';

/**
 * One link request (FLOWS flow 3 steps 2–4): the renter's KYC, the terms they
 * asked for, and the decision. Approving creates the contract in Phase 4; here
 * it only flips the request and queues the renter's SMS.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { Field, Note, ProblemNote } from '../../../components/FormBits';
import { Facts, KycStamp, LinkStatusStamp, ViewIdDocButton } from '../../../components/RenterBits';
import { PageHead, Shell } from '../../../components/Shell';
import { Sheet } from '../../../components/Sheet';
import {
  ApiError,
  hasKycDoc,
  linkRequestsApi,
  toApiError,
  unwrapRequest,
  unwrapRequestDetail,
  type LinkRequest,
  type RenterProfile,
} from '../../../lib/api';
import { Amount, fmtDate, fmtTZS } from '../../../lib/format';

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section style={{ paddingTop: 'var(--sp-6)' }}>
      <hr className="rule rule-strong" />
      <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{title}</h2>
      {children}
    </section>
  );
}

/* ------------------------------ reject sheet ------------------------------ */

function RejectForm({
  onSubmit,
  onCancel,
  error,
  busy,
}: {
  onSubmit: (reason: string) => void;
  onCancel: () => void;
  error: ApiError | null;
  busy: boolean;
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
      <p style={{ color: 'var(--ink-soft)' }}>
        The renter is sent this reason by SMS. Keep it short and plain.
      </p>
      <Field
        id="reject_reason"
        label="Reason"
        hint={`${reason.length}/200`}
        error={error?.errors.reason}
      >
        <textarea
          id="reject_reason"
          className="input"
          rows={3}
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="e.g. The unit is already promised to another renter."
        />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? 'Rejecting…' : 'Reject request'}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}

/* --------------------------------- page ---------------------------------- */

function RequestBody({ id }: { id: string }) {
  const [request, setRequest] = useState<LinkRequest | null>(null);
  const [profile, setProfile] = useState<RenterProfile | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [rejectOpen, setRejectOpen] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const detail = unwrapRequestDetail(await linkRequestsApi.get(id, signal));
        setRequest(detail.request ?? null);
        setProfile(detail.renter_profile);
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
    },
    [id],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  /** 409 = someone already decided this request; re-read rather than argue. */
  const decide = async (run: () => Promise<{ request: LinkRequest } | LinkRequest>, done: string) => {
    setBusy(true);
    setActionError(null);
    try {
      setRequest(unwrapRequest(await run()));
      setNote(done);
      setConfirmOpen(false);
      setRejectOpen(false);
    } catch (e) {
      const err = toApiError(e);
      setActionError(err);
      if (err.status === 409) {
        setConfirmOpen(false);
        setRejectOpen(false);
        setNote(null);
        await load();
      }
    } finally {
      setBusy(false);
    }
  };

  if (error && !request) {
    return (
      <>
        <PageHead title="Link request" />
        <ProblemNote error={error} />
        <p style={{ marginTop: 'var(--sp-4)' }}>
          <Link href="/link-requests" className="btn btn-quiet">
            Back to the inbox
          </Link>
        </p>
      </>
    );
  }

  if (!request) {
    return (
      <>
        <PageHead title="Link request" />
        <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>
      </>
    );
  }

  const pending = request.status === 'pending';
  const renterId = request.renter?.user_id;
  const kycStatus = profile?.kyc_status ?? request.renter?.kyc_status;
  const docOnFile = hasKycDoc(profile);
  const preview = request.schedule_preview;

  return (
    <>
      <PageHead
        title={request.renter?.full_name ?? 'Link request'}
        lead={`${request.unit?.name ?? '—'} · ${request.unit?.property_name ?? ''}`}
        actions={
          <Link href="/link-requests" className="btn btn-quiet">
            <Icon icon="solar:arrow-left-linear" width={20} /> Inbox
          </Link>
        }
      />

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', marginBottom: 'var(--sp-4)' }}>
        <LinkStatusStamp status={request.status} />
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          requested {fmtDate(request.created_at)}
          {request.decided_at ? ` · decided ${fmtDate(request.decided_at)}` : ''}
        </span>
      </div>

      {request.rejection_reason ? (
        <p style={{ color: 'var(--ink-soft)' }}>Reason given: {request.rejection_reason}</p>
      ) : null}

      <div style={{ display: 'grid', gap: 'var(--sp-3)', marginTop: 'var(--sp-4)' }}>
        {note ? <Note>{note}</Note> : null}
        {actionError ? <ProblemNote error={actionError} /> : null}
      </div>

      <Section title="Renter">
        <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <Facts
            rows={[
              ['Full name', profile?.full_name ?? request.renter?.full_name ?? '—'],
              ['Phone', request.renter?.phone ?? '—'],
              ['Email', profile?.email ?? '—'],
              [
                'NIDA',
                <span key="nida" className="num" style={{ letterSpacing: '0.08em' }}>
                  {profile?.nida_masked ?? '—'}
                </span>,
              ],
              ['Next of kin', profile?.next_of_kin_name ?? '—'],
              ['Next of kin phone', profile?.next_of_kin_phone ?? '—'],
              ['KYC', <KycStamp key="kyc" status={kycStatus} />],
            ]}
          />
          {renterId ? (
            <ViewIdDocButton
              userId={renterId}
              disabled={!docOnFile}
              disabledReason={docOnFile ? undefined : 'No ID photo was uploaded.'}
            />
          ) : null}
          {renterId ? (
            <div>
              <Link href={`/renters/${renterId}`} className="btn btn-quiet">
                Open renter record
              </Link>
            </div>
          ) : null}
        </div>
      </Section>

      <Section title="Requested terms">
        <div style={{ maxWidth: 640 }}>
          <Facts
            rows={[
              [
                'Unit',
                request.unit?.id ? (
                  <Link key="u" href={`/units/${request.unit.id}`} style={{ color: 'inherit' }}>
                    {request.unit.name} · {request.unit.property_name}
                  </Link>
                ) : (
                  (request.unit?.name ?? '—')
                ),
              ],
              [
                'Payment period',
                `${request.payment_period?.label ?? '—'}${
                  request.payment_period?.days ? ` · ${request.payment_period.days} days` : ''
                }`,
              ],
              [
                'Amount per period',
                <Amount key="amt" value={request.payment_period?.amount ?? null} />,
              ],
              ['Tenancy length', request.term_days ? `${request.term_days} days` : '—'],
              ['Starts', fmtDate(request.start_date)],
              ['Ends', fmtDate(request.end_date)],
            ]}
          />

          {preview ? (
            <div style={{ marginTop: 'var(--sp-5)' }}>
              <h3 style={{ fontSize: 'var(--text-base)', marginBottom: 'var(--sp-3)' }}>Schedule preview</h3>
              <Facts
                rows={[
                  ['Payments', String(preview.count)],
                  ['First due', fmtDate(preview.first_due)],
                  ['First payment', fmtTZS(preview.amount_first)],
                  ['Last payment', fmtTZS(preview.amount_last)],
                  ['Total', fmtTZS(preview.total)],
                ]}
              />
              <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>
                A preview only. The schedule is generated when the contract is activated.
              </p>
            </div>
          ) : null}
        </div>
      </Section>

      <Section title="Decision">
        {pending ? (
          <div style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 640 }}>
            <p style={{ color: 'var(--ink-soft)' }}>
              Approving tells the renter their contract is on the way. The unit stays vacant until the
              contract is signed and activated.
            </p>
            <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
              <button type="button" className="btn btn-primary" onClick={() => setConfirmOpen(true)} disabled={busy}>
                <Icon icon="solar:check-circle-linear" width={20} /> Approve
              </button>
              <button type="button" className="btn btn-danger" onClick={() => setRejectOpen(true)} disabled={busy}>
                <Icon icon="solar:close-circle-linear" width={20} /> Reject
              </button>
            </div>
          </div>
        ) : (
          <p style={{ color: 'var(--ink-soft)' }}>
            This request was already {request.status}
            {request.decided_at ? ` on ${fmtDate(request.decided_at)}` : ''}. Decisions are not undone —
            a renter can send a new request.
          </p>
        )}
      </Section>

      <Sheet open={confirmOpen} title="Approve this request?" onClose={() => setConfirmOpen(false)} width={480}>
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          <p>
            {request.renter?.full_name} will be linked to {request.unit?.name} for {request.term_days} days
            from {fmtDate(request.start_date)}, paying {request.payment_period?.label?.toLowerCase()}.
          </p>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void decide(() => linkRequestsApi.approve(request.id), 'Approved. The renter has been notified.')}
            >
              {busy ? 'Approving…' : 'Yes, approve'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setConfirmOpen(false)} disabled={busy}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>

      <Sheet open={rejectOpen} title="Reject this request" onClose={() => setRejectOpen(false)} width={480}>
        <RejectForm
          busy={busy}
          error={actionError}
          onCancel={() => setRejectOpen(false)}
          onSubmit={(reason) =>
            void decide(() => linkRequestsApi.reject(request.id, reason), 'Rejected. The renter has been notified.')
          }
        />
      </Sheet>
    </>
  );
}

export default function LinkRequestPage() {
  const params = useParams<{ id: string }>();
  const id = String(params?.id ?? '');
  return (
    <Shell>
      <RequestBody id={id} />
    </Shell>
  );
}
