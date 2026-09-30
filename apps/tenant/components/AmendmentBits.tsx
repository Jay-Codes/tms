'use client';

/**
 * Phase 31 — the review of a contract change (maker-checker). A manager or
 * owner drafts; an owner approves, returns with a reason, or rejects; only
 * then does the renter see it, with the note in their language, and sign or
 * decline. This panel sits on the draft's own page: what changed against the
 * running contract (fields and a paragraph-level wording diff), both notes,
 * who drafted it, and the actions the viewer's role allows.
 */

import { Icon } from '@iconify/react';
import { useEffect, useState } from 'react';
import { useT, type Translator } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  contractsApi,
  toApiError,
  unwrapContract,
  type AmendmentStage,
  type Contract,
} from '../lib/api';
import { fmtDate, fmtTZS } from '../lib/format';

const STAGE_KEYS: Record<AmendmentStage, string> = {
  draft: 'amendment.stage.draft',
  submitted: 'amendment.stage.submitted',
  approved: 'amendment.stage.approved',
  rejected: 'amendment.stage.rejected',
  declined: 'amendment.stage.declined',
  withdrawn: 'amendment.stage.withdrawn',
};

export function AmendmentStageChip({ contract }: { contract: Contract }) {
  const t = useT();
  const a = contract.amendment;
  if (!a) return null;
  // A draft the owner sent back reads "Returned", not "Draft".
  const key = a.stage === 'draft' && a.review_note ? 'amendment.stage.returned' : STAGE_KEYS[a.stage];
  return <span className="stamp">{t(key)}</span>;
}

/** Block-level text of a rendered document, one entry per paragraph. */
function paragraphs(html: string): string[] {
  if (typeof window === 'undefined' || !html) return [];
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const blocks = Array.from(doc.body.querySelectorAll('p, li, h1, h2, h3, blockquote, td'));
  const out = (blocks.length ? blocks : [doc.body])
    .map((el) => (el.textContent ?? '').replace(/\s+/g, ' ').trim())
    .filter(Boolean);
  return out;
}

type DiffLine = { kind: 'same' | 'add' | 'del'; text: string };

/** Longest-common-subsequence diff over paragraphs. */
function diffParagraphs(a: string[], b: string[]): DiffLine[] {
  const n = a.length;
  const m = b.length;
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ kind: 'same', text: a[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: 'del', text: a[i++] });
    } else {
      out.push({ kind: 'add', text: b[j++] });
    }
  }
  while (i < n) out.push({ kind: 'del', text: a[i++] });
  while (j < m) out.push({ kind: 'add', text: b[j++] });
  return out;
}

function fieldRows(t: Translator, before: Contract, after: Contract): [string, string, string][] {
  const period = (c: Contract) => c.payment_period?.label ?? '—';
  const rows: [string, string, string][] = [
    [t('contracts.col.rent'), `${fmtTZS(before.rent_amount)} / ${before.rent_period_days}`, `${fmtTZS(after.rent_amount)} / ${after.rent_period_days}`],
    [t('contracts.new.period'), period(before), period(after)],
    [t('contracts.new.due_day'), before.due_day ? String(before.due_day) : '—', after.due_day ? String(after.due_day) : '—'],
    [t('amendment.field.from'), fmtDate(before.start_date), fmtDate(after.start_date)],
    [t('amendment.field.to'), fmtDate(before.end_date), fmtDate(after.end_date)],
    [t('contracts.new.term'), String(before.term_days), String(after.term_days)],
    [t('amendment.field.language'), (before.language ?? '—').toUpperCase(), (after.language ?? '—').toUpperCase()],
  ];
  return rows;
}

function ReasonSheet({
  open,
  title,
  lead,
  confirm,
  danger,
  onClose,
  onSubmit,
}: {
  open: boolean;
  title: string;
  lead: string;
  confirm: string;
  danger?: boolean;
  onClose: () => void;
  onSubmit: (reason: string) => Promise<void>;
}) {
  const t = useT();
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  return (
    <Sheet open={open} title={title} onClose={onClose} width={480}>
      <form
        style={{ display: 'grid', gap: 'var(--sp-4)' }}
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(null);
          try {
            await onSubmit(reason.trim());
            setReason('');
          } catch (err) {
            setError(toApiError(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <ProblemNote error={error} />
        <p style={{ color: 'var(--ink-soft)' }}>{lead}</p>
        <Field id="amr_reason" label={t('amendment.reason')} error={error?.errors.reason}>
          <textarea
            id="amr_reason"
            className="input"
            rows={3}
            maxLength={200}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          <button type="submit" className={danger ? 'btn btn-danger' : 'btn btn-primary'} disabled={busy || !reason.trim()}>
            {busy ? t('common.saving') : confirm}
          </button>
          <button type="button" className="btn btn-quiet" onClick={onClose} disabled={busy}>
            {t('common.cancel')}
          </button>
        </div>
      </form>
    </Sheet>
  );
}

export function AmendmentPanel({
  contract,
  isOwner,
  onEdit,
  onChanged,
}: {
  /** The amendment (draft or otherwise). */
  contract: Contract;
  isOwner: boolean;
  onEdit: () => void;
  onChanged: (c: Contract) => void;
}) {
  const t = useT();
  const a = contract.amendment;
  const [before, setBefore] = useState<Contract | null>(null);
  const [oldTerms, setOldTerms] = useState<string | null>(null);
  const [newTerms, setNewTerms] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [sheet, setSheet] = useState<'return' | 'reject' | null>(null);

  const oldId = contract.supersedes_contract_id;
  useEffect(() => {
    if (!oldId) return;
    const ac = new AbortController();
    contractsApi
      .get(oldId, ac.signal)
      .then((r) => setBefore(unwrapContract(r)))
      .catch(() => setBefore(null));
    contractsApi
      .document(oldId, ac.signal)
      .then((d) => setOldTerms(d.terms_html))
      .catch(() => setOldTerms(null));
    return () => ac.abort();
  }, [oldId]);
  useEffect(() => {
    const ac = new AbortController();
    contractsApi
      .document(contract.id, ac.signal)
      .then((d) => setNewTerms(d.terms_html))
      .catch(() => setNewTerms(null));
    return () => ac.abort();
  }, [contract.id, contract.snapshot_hash]);

  if (!a) return null;

  const run = async (call: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      const res = await call();
      onChanged(unwrapContract(res as { contract: Contract }));
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const open = contract.status === 'draft';
  const diff = oldTerms !== null && newTerms !== null ? diffParagraphs(paragraphs(oldTerms), paragraphs(newTerms)) : null;
  const changed = diff?.some((d) => d.kind !== 'same') ?? false;

  return (
    <section
      style={{
        marginTop: 'var(--sp-5)',
        padding: 'var(--sp-4)',
        border: '1px solid var(--rule)',
        borderRadius: 'var(--radius-md)',
        display: 'grid',
        gap: 'var(--sp-4)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
        <h2 style={{ fontSize: 'var(--text-lg)', margin: 0 }}>{t('amendment.title')}</h2>
        <AmendmentStageChip contract={contract} />
      </div>

      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
        {a.drafted_by_name ? t('amendment.drafted_by', { name: a.drafted_by_name }) : null}
        {a.submitted_at ? ` · ${t('amendment.submitted_on', { date: fmtDate(a.submitted_at) })}` : ''}
        {a.reviewed_by_name && a.reviewed_at
          ? ` · ${t('amendment.reviewed_by', { name: a.reviewed_by_name, date: fmtDate(a.reviewed_at) })}`
          : ''}
      </p>

      {a.review_note ? (
        <p role="status" style={{ padding: 'var(--sp-3)', background: 'var(--sheet-tint)', borderRadius: 'var(--radius-sm)' }}>
          <strong>{a.stage === 'rejected' ? t('amendment.rejected_because') : t('amendment.returned_because')}</strong>{' '}
          {a.review_note}
        </p>
      ) : null}
      {a.stage === 'declined' && a.decline_reason ? (
        <p role="status" style={{ padding: 'var(--sp-3)', background: 'var(--sheet-tint)', borderRadius: 'var(--radius-sm)' }}>
          <strong>{t('amendment.declined_because')}</strong> {a.decline_reason}
        </p>
      ) : null}

      <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
        <h3 style={{ fontSize: 'var(--text-md)', margin: 0 }}>{t('amendment.note_title')}</h3>
        <p lang="sw">
          <span className="pencil">SW</span> {a.note_sw}
        </p>
        <p lang="en">
          <span className="pencil">EN</span> {a.note_en}
        </p>
      </div>

      {before ? (
        <div style={{ display: 'grid', gap: 'var(--sp-2)', overflowX: 'auto' }}>
          <h3 style={{ fontSize: 'var(--text-md)', margin: 0 }}>{t('amendment.changes_title')}</h3>
          <table className="ledger" style={{ minWidth: 420 }}>
            <thead>
              <tr>
                <th />
                <th>{t('amendment.col.now')}</th>
                <th>{t('amendment.col.new')}</th>
              </tr>
            </thead>
            <tbody>
              {fieldRows(t, before, contract).map(([label, was, now]) => (
                <tr key={label} style={was !== now ? { fontWeight: 600 } : undefined}>
                  <td>{label}</td>
                  <td style={{ color: was !== now ? 'var(--ink-soft)' : undefined }}>{was}</td>
                  <td>{now}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {diff ? (
        <details open={changed}>
          <summary style={{ cursor: 'pointer' }}>
            {changed ? t('amendment.wording_changed') : t('amendment.wording_same')}
          </summary>
          <div style={{ display: 'grid', gap: 'var(--sp-2)', marginTop: 'var(--sp-3)' }}>
            {diff
              .filter((d) => d.kind !== 'same')
              .map((d, i) => (
                <p
                  key={i}
                  style={{
                    padding: 'var(--sp-2) var(--sp-3)',
                    borderLeft: `3px solid ${d.kind === 'add' ? 'var(--stamp-paid)' : 'var(--stamp-overdue)'}`,
                    textDecoration: d.kind === 'del' ? 'line-through' : undefined,
                    color: d.kind === 'del' ? 'var(--ink-soft)' : undefined,
                  }}
                >
                  <span className="pencil">{d.kind === 'add' ? t('amendment.added') : t('amendment.removed')}</span>{' '}
                  {d.text}
                </p>
              ))}
          </div>
        </details>
      ) : null}

      <ProblemNote error={error} />

      {open ? (
        <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          {isOwner && (a.stage === 'submitted' || a.stage === 'draft') ? (
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void run(() => contractsApi.approveAmendment(contract.id))}
            >
              <Icon icon="solar:check-circle-linear" width={20} /> {t('amendment.approve')}
            </button>
          ) : null}
          {!isOwner && a.stage === 'draft' ? (
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void run(() => contractsApi.submitAmendment(contract.id))}
            >
              {t('contracts.amend.submit_approval')}
            </button>
          ) : null}
          {a.stage === 'draft' || isOwner ? (
            <button type="button" className="btn btn-secondary" disabled={busy} onClick={onEdit}>
              <Icon icon="solar:pen-linear" width={20} /> {t('amendment.edit')}
            </button>
          ) : null}
          {isOwner && a.stage === 'submitted' ? (
            <>
              <button type="button" className="btn btn-quiet" disabled={busy} onClick={() => setSheet('return')}>
                {t('amendment.return')}
              </button>
              <button type="button" className="btn btn-danger" disabled={busy} onClick={() => setSheet('reject')}>
                {t('amendment.reject')}
              </button>
            </>
          ) : null}
          <button
            type="button"
            className="btn btn-quiet"
            disabled={busy}
            onClick={() => void run(() => contractsApi.withdrawAmendment(contract.id))}
          >
            {t('amendment.withdraw')}
          </button>
        </div>
      ) : null}
      {open && !isOwner && a.stage === 'submitted' ? (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('amendment.waiting_owner')}</p>
      ) : null}
      {open && isOwner && a.stage === 'draft' && !a.review_note ? (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('amendment.owner_hint')}</p>
      ) : null}

      <ReasonSheet
        open={sheet === 'return'}
        title={t('amendment.return')}
        lead={t('amendment.return_lead')}
        confirm={t('amendment.return')}
        onClose={() => setSheet(null)}
        onSubmit={async (reason) => {
          const res = await contractsApi.returnAmendment(contract.id, reason);
          setSheet(null);
          onChanged(unwrapContract(res));
        }}
      />
      <ReasonSheet
        open={sheet === 'reject'}
        title={t('amendment.reject')}
        lead={t('amendment.reject_lead')}
        confirm={t('amendment.reject')}
        danger
        onClose={() => setSheet(null)}
        onSubmit={async (reason) => {
          const res = await contractsApi.rejectAmendment(contract.id, reason);
          setSheet(null);
          onChanged(unwrapContract(res));
        }}
      />
    </section>
  );
}
