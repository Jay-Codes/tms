'use client';

/**
 * The SMS credits panel on an organization's detail page — `GET
 * /admin/orgs/{id}/sms` plus the three levers the platform has over a wallet:
 * top up, adjust, and move the low watermark (API.md Phase 14).
 *
 * Two things this screen must not fudge:
 *  - a *held* message is not a failed one. It is queued, waiting for credit,
 *    and it leaves on the next top-up — the copy says so wherever the count
 *    appears, because "held" reads like "lost" otherwise.
 *  - a negative adjustment is a correction against real money, so it is
 *    confirmed with the resulting balance spelled out before it is sent.
 */

import { useCallback, useEffect, useState } from 'react';
import { TableScroll } from '@tms/ui';
import { Field, Note, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  adminApi,
  toApiError,
  type AdminOrgSmsCredits,
  type AdminSmsLedgerEntry,
} from '../lib/api';
import { fmtDateTime, fmtNum } from '../lib/format';

/** The API returns the newest 100 ledger rows unless it hands back a cursor. */
const LEDGER_CAP = 100;

function Figure({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: string;
  sub?: React.ReactNode;
  tone?: 'paid' | 'overdue';
}) {
  const color =
    tone === 'overdue' ? 'var(--stamp-overdue)' : tone === 'paid' ? 'var(--stamp-paid)' : 'var(--ink)';
  return (
    <div style={{ borderTop: '1px solid var(--rule-strong)', paddingTop: 'var(--sp-3)' }}>
      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</div>
      <div className="amount" style={{ fontSize: 'var(--text-2xl)', color, marginTop: 'var(--sp-1)' }}>
        {value}
      </div>
      {sub ? (
        <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', marginTop: 'var(--sp-1)' }}>
          {sub}
        </div>
      ) : null}
    </div>
  );
}

/** A ledger reason stamped the way a money status is — it already happened. */
function ReasonStamp({ reason }: { reason: string }) {
  const cls =
    reason === 'topup' || reason === 'refund'
      ? 'stamp stamp-paid'
      : reason === 'adjust'
        ? 'stamp'
        : 'stamp stamp-overdue';
  const title =
    reason === 'topup'
      ? 'Credits added by the platform'
      : reason === 'refund'
        ? 'Credits returned after a send that never left'
        : reason === 'adjust'
          ? 'Manual correction by an admin'
          : 'Spent sending a message';
  return (
    <span className={cls} title={title} style={reason === 'adjust' ? { color: 'var(--ink-soft)' } : undefined}>
      {reason === 'topup'
        ? 'Top-up'
        : reason === 'adjust'
          ? 'Adjust'
          : reason === 'refund'
            ? 'Refund'
            : 'Debit'}
    </span>
  );
}

function Delta({ delta }: { delta: number }) {
  const up = delta > 0;
  return (
    <span
      className="num"
      style={{ color: up ? 'var(--stamp-paid)' : 'var(--stamp-overdue)', fontWeight: 600 }}
    >
      {up ? '+' : '−'}
      {fmtNum(Math.abs(delta))}
    </span>
  );
}

function LedgerRow({ e, i }: { e: AdminSmsLedgerEntry; i: number }) {
  return (
    <tr key={e.id ?? `${e.created_at}-${i}`}>
      <td style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(e.created_at)}</td>
      <td>
        <ReasonStamp reason={e.reason} />
      </td>
      <td className="num">
        <Delta delta={e.delta} />
      </td>
      <td className="num">{fmtNum(e.balance_after)}</td>
      <td>{e.note ? e.note : <span className="pencil">no note</span>}</td>
      <td>{e.admin_name ? e.admin_name : <span className="pencil">system</span>}</td>
    </tr>
  );
}

export function OrgSms({ orgId, orgName }: { orgId: string; orgName: string }) {
  const [data, setData] = useState<AdminOrgSmsCredits | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);

  const [topupOpen, setTopupOpen] = useState(false);
  const [adjustOpen, setAdjustOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [credits, setCredits] = useState('');
  const [delta, setDelta] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const [editingMark, setEditingMark] = useState(false);
  const [mark, setMark] = useState('');
  const [markBusy, setMarkBusy] = useState(false);
  const [markError, setMarkError] = useState<ApiError | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const res = await adminApi.orgSms(orgId, {}, signal);
        setData(res);
        setCursor(res.next_cursor ?? null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setData(null);
      } finally {
        setLoading(false);
      }
    },
    [orgId],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const loadMore = async () => {
    if (!cursor) return;
    setLoading(true);
    try {
      const res = await adminApi.orgSms(orgId, { cursor });
      setData((d) => (d ? { ...d, ...res, ledger: [...d.ledger, ...(res.ledger ?? [])] } : res));
      setCursor(res.next_cursor ?? null);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setLoading(false);
    }
  };

  const closeSheets = () => {
    setTopupOpen(false);
    setAdjustOpen(false);
    setConfirming(false);
    setActionError(null);
    setCredits('');
    setDelta('');
    setNote('');
  };

  const creditsNum = Number(credits);
  const creditsValid = credits.trim() !== '' && Number.isFinite(creditsNum) && creditsNum > 0;
  const deltaNum = Number(delta);
  const deltaValid = delta.trim() !== '' && Number.isFinite(deltaNum) && deltaNum !== 0;
  const balance = data?.balance ?? 0;
  const afterTopup = balance + (creditsValid ? Math.round(creditsNum) : 0);
  const afterAdjust = balance + (deltaValid ? Math.round(deltaNum) : 0);
  const wouldGoNegative = deltaValid && afterAdjust < 0;

  const topup = async () => {
    setBusy(true);
    setActionError(null);
    try {
      await adminApi.smsTopup(orgId, { credits: Math.round(creditsNum), note: note.trim() });
      closeSheets();
      setDone(
        `Topped up ${fmtNum(Math.round(creditsNum))} credits. Any messages held for this organization are released in queue order.`,
      );
      void load();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const adjust = async () => {
    setBusy(true);
    setActionError(null);
    try {
      await adminApi.smsAdjust(orgId, { delta: Math.round(deltaNum), note: note.trim() });
      closeSheets();
      setDone(`Balance adjusted by ${deltaNum > 0 ? '+' : '−'}${fmtNum(Math.abs(Math.round(deltaNum)))} credits.`);
      void load();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const saveMark = async () => {
    const n = Number(mark);
    if (!Number.isFinite(n) || n < 0) return;
    setMarkBusy(true);
    setMarkError(null);
    try {
      const res = await adminApi.smsWatermark(orgId, Math.round(n));
      setData((d) => (d ? { ...d, low_watermark: res.low_watermark ?? Math.round(n) } : d));
      setEditingMark(false);
      setDone('Low watermark updated.');
    } catch (e) {
      setMarkError(toApiError(e));
    } finally {
      setMarkBusy(false);
    }
  };

  const under = data ? data.balance < data.low_watermark : false;
  const held = data?.held_count ?? 0;
  const atCap = (data?.ledger?.length ?? 0) >= LEDGER_CAP && !cursor;

  return (
    <section style={{ marginTop: 'var(--sp-6)' }} id="sms">
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-end',
          justifyContent: 'space-between',
          gap: 'var(--sp-4)',
          flexWrap: 'wrap',
          marginBottom: 'var(--sp-4)',
        }}
      >
        <div>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>SMS credits</h2>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', marginTop: 'var(--sp-1)' }}>
            One credit per message segment — 160 GSM characters, 70 when the body needs Unicode.
            Verification codes are exempt.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexShrink: 0 }}>
          <button type="button" className="btn btn-primary" onClick={() => setTopupOpen(true)}>
            Top up
          </button>
          <button type="button" className="btn btn-secondary" onClick={() => setAdjustOpen(true)}>
            Adjust
          </button>
        </div>
      </div>

      <ProblemNote error={error} />
      {done ? <Note>{done}</Note> : null}

      {loading && !data ? <p style={{ color: 'var(--ink-soft)' }}>Loading…</p> : null}

      {data ? (
        <>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
              gap: 'var(--sp-5)',
              marginTop: 'var(--sp-4)',
            }}
          >
            <Figure
              label="Balance"
              value={fmtNum(data.balance)}
              tone={under ? 'overdue' : 'paid'}
              sub={under ? 'Under the low watermark' : 'credits'}
            />
            <Figure
              label="Low watermark"
              value={fmtNum(data.low_watermark)}
              sub={
                editingMark ? null : (
                  <button
                    type="button"
                    className="btn btn-quiet"
                    style={{ minHeight: 28, padding: '0 var(--sp-2)', fontSize: 'var(--text-xs)' }}
                    onClick={() => {
                      setMark(String(data.low_watermark));
                      setMarkError(null);
                      setEditingMark(true);
                    }}
                  >
                    Change
                  </button>
                )
              }
            />
            <Figure label="Used in 30 days" value={fmtNum(data.used_30d)} sub="credits spent" />
            <Figure
              label="Messages held"
              value={fmtNum(held)}
              tone={held > 0 ? 'overdue' : undefined}
              sub={
                held > 0
                  ? 'Held until credits are added — not failed. A top-up releases them in queue order.'
                  : 'Nothing waiting on credit.'
              }
            />
          </div>

          {editingMark ? (
            <div
              style={{
                marginTop: 'var(--sp-4)',
                border: '1px solid var(--rule)',
                borderRadius: 'var(--radius-sm)',
                padding: 'var(--sp-4)',
                maxWidth: 460,
                display: 'grid',
                gap: 'var(--sp-3)',
              }}
            >
              <ProblemNote error={markError} />
              <Field
                id="low_watermark"
                label="Low watermark"
                hint="The landlord sees a low-balance banner below this number."
                error={markError?.errors.low_watermark}
              >
                <input
                  id="low_watermark"
                  className="input"
                  type="number"
                  min={0}
                  step={1}
                  inputMode="numeric"
                  value={mark}
                  onChange={(e) => setMark(e.target.value)}
                />
              </Field>
              <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={markBusy || !(Number(mark) >= 0)}
                  onClick={() => void saveMark()}
                >
                  {markBusy ? 'Saving…' : 'Save watermark'}
                </button>
                <button type="button" className="btn btn-quiet" onClick={() => setEditingMark(false)}>
                  Cancel
                </button>
              </div>
            </div>
          ) : null}

          <h3 style={{ fontSize: 'var(--text-base)', marginTop: 'var(--sp-6)', marginBottom: 'var(--sp-3)' }}>
            Credit ledger
          </h3>
          <TableScroll label="SMS credit ledger">
            <table className="ledger">
              <thead>
                <tr>
                  <th>When</th>
                  <th>Reason</th>
                  <th className="num">Change</th>
                  <th className="num">Balance after</th>
                  <th>Note</th>
                  <th>Admin</th>
                </tr>
              </thead>
              <tbody>
                {data.ledger.length === 0 ? (
                  <tr>
                    <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                      No credit movements yet.
                    </td>
                  </tr>
                ) : (
                  data.ledger.map((e, i) => <LedgerRow key={e.id ?? `${e.created_at}-${i}`} e={e} i={i} />)
                )}
              </tbody>
            </table>
          </TableScroll>

          <div style={{ marginTop: 'var(--sp-3)', display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
            {cursor ? (
              <button type="button" className="btn btn-secondary" disabled={loading} onClick={() => void loadMore()}>
                {loading ? 'Loading…' : 'Load more'}
              </button>
            ) : null}
            {atCap ? (
              <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                Showing the most recent {LEDGER_CAP} movements — the API caps the ledger here.
              </span>
            ) : null}
          </div>
        </>
      ) : null}

      {/* ------------------------------- Top up ------------------------------ */}
      <Sheet open={topupOpen} title={`Top up ${orgName}`} onClose={closeSheets}>
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          {confirming ? (
            <>
              <p style={{ color: 'var(--ink-soft)' }}>
                Adding <strong style={{ color: 'var(--ink)' }}>{fmtNum(Math.round(creditsNum))}</strong> credits
                takes the balance from {fmtNum(balance)} to{' '}
                <strong style={{ color: 'var(--ink)' }}>{fmtNum(afterTopup)}</strong>.
                {held > 0 ? ` ${fmtNum(held)} held message${held === 1 ? '' : 's'} will be released in queue order.` : ''}
              </p>
              <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
                <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void topup()}>
                  {busy ? 'Adding…' : `Add ${fmtNum(Math.round(creditsNum))} credits`}
                </button>
                <button type="button" className="btn btn-quiet" onClick={() => setConfirming(false)}>
                  Back
                </button>
              </div>
            </>
          ) : (
            <>
              <Field
                id="topup_credits"
                label="Credits"
                hint="Whole credits. One credit sends one message segment."
                error={actionError?.errors.credits}
              >
                <input
                  id="topup_credits"
                  className="input"
                  type="number"
                  min={1}
                  step={1}
                  inputMode="numeric"
                  value={credits}
                  onChange={(e) => setCredits(e.target.value)}
                />
              </Field>
              <Field
                id="topup_note"
                label="Note"
                hint="Kept on the ledger row and the audit record — say what was paid for."
                error={actionError?.errors.note}
              >
                <input
                  id="topup_note"
                  className="input"
                  value={note}
                  maxLength={200}
                  onChange={(e) => setNote(e.target.value)}
                />
              </Field>
              <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={!creditsValid}
                  onClick={() => setConfirming(true)}
                >
                  Review
                </button>
                <button type="button" className="btn btn-quiet" onClick={closeSheets}>
                  Cancel
                </button>
              </div>
            </>
          )}
        </div>
      </Sheet>

      {/* ------------------------------- Adjust ------------------------------ */}
      <Sheet open={adjustOpen} title={`Adjust ${orgName}'s balance`} onClose={closeSheets}>
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          {confirming ? (
            <>
              <p style={{ color: 'var(--ink-soft)' }}>
                A change of{' '}
                <strong style={{ color: deltaNum < 0 ? 'var(--stamp-overdue)' : 'var(--ink)' }}>
                  {deltaNum > 0 ? '+' : '−'}
                  {fmtNum(Math.abs(Math.round(deltaNum)))}
                </strong>{' '}
                takes the balance from {fmtNum(balance)} to{' '}
                <strong style={{ color: 'var(--ink)' }}>{fmtNum(afterAdjust)}</strong>.
              </p>
              <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
                <button
                  type="button"
                  className={deltaNum < 0 ? 'btn btn-danger' : 'btn btn-primary'}
                  disabled={busy || wouldGoNegative}
                  onClick={() => void adjust()}
                >
                  {busy ? 'Adjusting…' : 'Apply adjustment'}
                </button>
                <button type="button" className="btn btn-quiet" onClick={() => setConfirming(false)}>
                  Back
                </button>
              </div>
            </>
          ) : (
            <>
              <p style={{ color: 'var(--ink-soft)' }}>
                A correction, not a sale. Use a positive number to give credits back and a negative one to
                take them away; the ledger keeps both with your note.
              </p>
              <Field
                id="adjust_delta"
                label="Change"
                hint="Signed whole credits — e.g. −10 to remove ten."
                error={actionError?.errors.delta}
              >
                <input
                  id="adjust_delta"
                  className="input"
                  type="number"
                  step={1}
                  inputMode="numeric"
                  value={delta}
                  onChange={(e) => setDelta(e.target.value)}
                />
              </Field>
              {deltaValid && deltaNum < 0 ? (
                <p
                  role="alert"
                  style={{
                    color: 'var(--stamp-overdue)',
                    fontSize: 'var(--text-sm)',
                    border: '1px solid var(--stamp-overdue)',
                    borderRadius: 'var(--radius-sm)',
                    padding: 'var(--sp-2) var(--sp-3)',
                    maxWidth: 'none',
                  }}
                >
                  {wouldGoNegative
                    ? `This organization only has ${fmtNum(balance)} credits — a balance cannot go below zero, and the API will refuse this.`
                    : `Removing credits leaves ${fmtNum(afterAdjust)}. Messages queue as held once the balance runs out.`}
                </p>
              ) : null}
              <Field
                id="adjust_note"
                label="Note"
                hint="Kept on the ledger row and the audit record — say why."
                error={actionError?.errors.note}
              >
                <input
                  id="adjust_note"
                  className="input"
                  value={note}
                  maxLength={200}
                  onChange={(e) => setNote(e.target.value)}
                />
              </Field>
              <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={!deltaValid || wouldGoNegative}
                  onClick={() => setConfirming(true)}
                >
                  Review
                </button>
                <button type="button" className="btn btn-quiet" onClick={closeSheets}>
                  Cancel
                </button>
              </div>
            </>
          )}
        </div>
      </Sheet>
    </section>
  );
}
