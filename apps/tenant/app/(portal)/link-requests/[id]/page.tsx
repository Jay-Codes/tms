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
import { Field, Note, ProblemNote } from '../../../../components/FormBits';
import { Facts, KycStamp, LinkStatusStamp, ViewIdDocButton, localeLabel } from '../../../../components/RenterBits';
import { PageHead } from '../../../../components/PageHead';
import { Sheet } from '../../../../components/Sheet';
import {
  ApiError,
  hasKycDoc,
  linkRequestsApi,
  toApiError,
  unwrapRequest,
  unwrapRequestDetail,
  type Contract,
  type LinkRequest,
  type RenterProfile,
} from '../../../../lib/api';
import { Amount, fmtDate, fmtTZS } from '../../../../lib/format';
import { useT } from '@tms/ui';

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
      <p style={{ color: 'var(--ink-soft)' }}>{t('linkreq.reject.lead')}</p>
      <Field
        id="reject_reason"
        label={t('linkreq.reject.reason')}
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
          placeholder={t('linkreq.reject.placeholder')}
        />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-danger" disabled={busy || reason.trim().length === 0}>
          {busy ? t('linkreq.reject.busy') : t('linkreq.reject.submit')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/* --------------------------------- page ---------------------------------- */

function RequestBody({ id }: { id: string }) {
  const t = useT();
  const [request, setRequest] = useState<LinkRequest | null>(null);
  const [profile, setProfile] = useState<RenterProfile | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [rejectOpen, setRejectOpen] = useState(false);
  // Phase 4: approving writes the contract in the same transaction and hands it
  // back, so the landlord can go straight to it instead of hunting the list.
  const [contract, setContract] = useState<Contract | null>(null);

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
  const decide = async (
    run: () => Promise<{ request: LinkRequest; contract?: Contract } | LinkRequest>,
    done: string,
  ) => {
    setBusy(true);
    setActionError(null);
    try {
      const res = await run();
      setRequest(unwrapRequest(res));
      const created = (res as { contract?: Contract }).contract;
      if (created) setContract(created);
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
        <PageHead title={t('linkreq.title')} />
        <ProblemNote error={error} />
        <p style={{ marginTop: 'var(--sp-4)' }}>
          <Link href="/link-requests" className="btn btn-quiet">
            {t('linkreq.back_to_inbox')}
          </Link>
        </p>
      </>
    );
  }

  if (!request) {
    return (
      <>
        <PageHead title={t('linkreq.title')} />
        <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
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
        title={request.renter?.full_name ?? t('linkreq.title')}
        lead={`${request.unit?.name ?? '—'} · ${request.unit?.property_name ?? ''}`}
        actions={
          <Link href="/link-requests" className="btn btn-quiet">
            <Icon icon="solar:arrow-left-linear" width={20} /> {t('linkreq.inbox')}
          </Link>
        }
      />

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', marginBottom: 'var(--sp-4)' }}>
        <LinkStatusStamp status={request.status} />
        <span style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('linkreq.requested_at', { date: fmtDate(request.created_at) })}
          {request.decided_at ? ` · ${t('linkreq.decided_at', { date: fmtDate(request.decided_at) })}` : ''}
        </span>
      </div>

      {request.rejection_reason ? (
        <p style={{ color: 'var(--ink-soft)' }}>
          {t('linkreq.reason_given', { reason: request.rejection_reason })}
        </p>
      ) : null}

      <div style={{ display: 'grid', gap: 'var(--sp-3)', marginTop: 'var(--sp-4)' }}>
        {note ? <Note>{note}</Note> : null}
        {actionError ? <ProblemNote error={actionError} /> : null}
        {contract ? (
          <p style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
            <span>{t('linkreq.contract_created')}</span>
            <Link href={`/contracts/${contract.id}`} className="btn btn-secondary">
              {t('linkreq.open_contract')}
            </Link>
          </p>
        ) : null}
      </div>

      <Section title={t('common.renter')}>
        <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <Facts
            rows={[
              [t('renters.field.full_name'), profile?.full_name ?? request.renter?.full_name ?? '—'],
              [t('common.phone'), request.renter?.phone ?? '—'],
              [t('common.email'), profile?.email ?? '—'],
              [t('renters.locale'), localeLabel(t, request.renter?.locale)],
              [
                t('renters.field.nida'),
                <span key="nida" className="num" style={{ letterSpacing: '0.08em' }}>
                  {profile?.nida_masked ?? '—'}
                </span>,
              ],
              [t('renters.field.next_of_kin'), profile?.next_of_kin_name ?? '—'],
              [t('renters.field.next_of_kin_phone'), profile?.next_of_kin_phone ?? '—'],
              [t('renters.col.kyc'), <KycStamp key="kyc" status={kycStatus} />],
            ]}
          />
          {renterId ? (
            <ViewIdDocButton
              userId={renterId}
              disabled={!docOnFile}
              disabledReason={docOnFile ? undefined : t('renters.kyc.no_doc')}
            />
          ) : null}
          {renterId ? (
            <div>
              <Link href={`/renters/${renterId}`} className="btn btn-quiet">
                {t('linkreq.open_renter')}
              </Link>
            </div>
          ) : null}
        </div>
      </Section>

      <Section title={t('linkreq.section.terms')}>
        <div style={{ maxWidth: 640 }}>
          <Facts
            rows={[
              [
                t('common.unit'),
                request.unit?.id ? (
                  <Link key="u" href={`/units/${request.unit.id}`} style={{ color: 'inherit' }}>
                    {request.unit.name} · {request.unit.property_name}
                  </Link>
                ) : (
                  (request.unit?.name ?? '—')
                ),
              ],
              [
                t('linkreq.field.period'),
                `${request.payment_period?.label ?? '—'}${
                  request.payment_period?.days ? ` · ${t.n('common.day', request.payment_period.days)}` : ''
                }`,
              ],
              [
                t('linkreq.field.amount'),
                <Amount key="amt" value={request.payment_period?.amount ?? null} />,
              ],
              [t('linkreq.field.term'), request.term_days ? t.n('common.day', request.term_days) : '—'],
              [t('linkreq.field.starts'), fmtDate(request.start_date)],
              [t('linkreq.field.ends'), fmtDate(request.end_date)],
            ]}
          />

          {preview ? (
            <div style={{ marginTop: 'var(--sp-5)' }}>
              <h3 style={{ fontSize: 'var(--text-base)', marginBottom: 'var(--sp-3)' }}>{t('linkreq.preview.title')}</h3>
              <Facts
                rows={[
                  [t('linkreq.preview.count'), String(preview.count)],
                  [t('linkreq.preview.first_due'), fmtDate(preview.first_due)],
                  [t('linkreq.preview.amount_first'), fmtTZS(preview.amount_first)],
                  [t('linkreq.preview.amount_last'), fmtTZS(preview.amount_last)],
                  [t('common.total'), fmtTZS(preview.total)],
                ]}
              />
              <p style={{ marginTop: 'var(--sp-3)', color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }}>
                {t('linkreq.preview.note')}
              </p>
            </div>
          ) : null}
        </div>
      </Section>

      <Section title={t('linkreq.section.decision')}>
        {pending ? (
          <div style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 640 }}>
            <p style={{ color: 'var(--ink-soft)' }}>{t('linkreq.decide.lead')}</p>
            <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
              <button type="button" className="btn btn-primary" onClick={() => setConfirmOpen(true)} disabled={busy}>
                <Icon icon="solar:check-circle-linear" width={20} /> {t('linkreq.approve')}
              </button>
              <button type="button" className="btn btn-danger" onClick={() => setRejectOpen(true)} disabled={busy}>
                <Icon icon="solar:close-circle-linear" width={20} /> {t('linkreq.reject')}
              </button>
            </div>
          </div>
        ) : (
          <p style={{ color: 'var(--ink-soft)' }}>
            {request.decided_at
              ? t('linkreq.already.on', {
                  date: fmtDate(request.decided_at),
                  status: t(`linkreq.status.${request.status}`).toLowerCase(),
                })
              : t('linkreq.already', { status: t(`linkreq.status.${request.status}`).toLowerCase() })}{' '}
            {t('linkreq.no_undo')}
          </p>
        )}
      </Section>

      <Sheet open={confirmOpen} title={t('linkreq.approve.confirm_title')} onClose={() => setConfirmOpen(false)} width={480}>
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          <p>
            {t('linkreq.approve.confirm_body', {
              name: request.renter?.full_name ?? '—',
              unit: request.unit?.name ?? '—',
              days: request.term_days,
              date: fmtDate(request.start_date),
              period: request.payment_period?.label?.toLowerCase() ?? '—',
            })}
          </p>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void decide(() => linkRequestsApi.approve(request.id), t('linkreq.approve.done'))}
            >
              {busy ? t('linkreq.approve.busy') : t('linkreq.approve.yes')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setConfirmOpen(false)} disabled={busy}>
              {t('common.cancel')}
            </button>
          </div>
        </div>
      </Sheet>

      <Sheet open={rejectOpen} title={t('linkreq.reject.title')} onClose={() => setRejectOpen(false)} width={480}>
        <RejectForm
          busy={busy}
          error={actionError}
          onCancel={() => setRejectOpen(false)}
          onSubmit={(reason) =>
            void decide(() => linkRequestsApi.reject(request.id, reason), t('linkreq.reject.done'))
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
    <>
      <RequestBody id={id} />
    </>
  );
}
