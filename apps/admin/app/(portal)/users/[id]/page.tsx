'use client';

/**
 * One platform user — `GET /admin/users/{id}` (PLAN2 19.2), plus the four levers
 * an admin has: suspend, activate, fix the name (19.3) and reveal the NIDA
 * number (19.1, same contract as the landlord's reveal).
 *
 * Opening this page is itself audited (`admin.user_view`) because it aggregates
 * cross-org PII. The reveal is a POST whose answer lives in component state for
 * 60 s and is then dropped: it never reaches a URL, localStorage,
 * sessionStorage or the console.
 */

import Link from 'next/link';
import { useParams } from 'next/navigation';
import { Fragment, useCallback, useEffect, useRef, useState } from 'react';
import { Field, Note, ProblemNote, StatusStamp } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import { Sheet } from '../../../../components/Sheet';
import { KindChip, KycStamp } from '../../../../components/UserBits';
import {
  ApiError,
  adminUsers,
  orgOf,
  orgRoleLabel,
  toApiError,
  unitOf,
  unwrapAdminUser,
  unwrapUserDetail,
  userKindLabel,
  type AdminAuditEntry,
  type AdminUserDetail,
} from '../../../../lib/api';
import { fmtDate, fmtDateTime, fmtJson, fmtNum, fmtTZS } from '../../../../lib/format';

/** How long a revealed number stays on screen before it re-masks itself. */
const REVEAL_SECONDS = 60;

type Tab = 'overview' | 'tenancies' | 'payments' | 'activity';

const TABS: { value: Tab; label: string }[] = [
  { value: 'overview', label: 'Overview' },
  { value: 'tenancies', label: 'Tenancies' },
  { value: 'payments', label: 'Payments' },
  { value: 'activity', label: 'Activity' },
];

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        gap: 'var(--sp-4)',
        padding: 'var(--sp-2) 0',
        borderBottom: '1px solid var(--rule)',
      }}
    >
      <div style={{ width: 200, flexShrink: 0, color: 'var(--ink-soft)' }}>{label}</div>
      <div style={{ minWidth: 0 }}>{value}</div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section style={{ marginTop: 'var(--sp-6)' }}>
      <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>{title}</h2>
      {children}
    </section>
  );
}

function OrgLink({ id, name }: { id?: string | null; name?: string | null }) {
  if (!id) return <span className="pencil">{name || '—'}</span>;
  return <Link href={`/orgs/${id}`}>{name || id.slice(0, 8)}</Link>;
}

/* ------------------------------------------------------------------ */
/* NIDA reveal — the audited, time-boxed one                           */
/* ------------------------------------------------------------------ */

function NidaBlock({
  userId,
  masked,
  onRevealed,
}: {
  userId: string;
  masked: string | null | undefined;
  onRevealed: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  /** The full number. State only — never written anywhere that outlives the tab. */
  const [full, setFull] = useState<string | null>(null);
  const [left, setLeft] = useState(0);
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);

  const drop = useCallback(() => {
    if (timer.current) clearInterval(timer.current);
    timer.current = null;
    setFull(null);
    setLeft(0);
    setCopied(false);
  }, []);

  /** Re-mask on unmount too, so a route change cannot leave it rendered. */
  useEffect(() => drop, [drop]);

  const reveal = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await adminUsers.revealNida(userId, reason.trim());
      setOpen(false);
      setReason('');
      setFull(res.nida_number);
      setLeft(REVEAL_SECONDS);
      if (timer.current) clearInterval(timer.current);
      timer.current = setInterval(() => {
        setLeft((s) => {
          if (s <= 1) {
            if (timer.current) clearInterval(timer.current);
            timer.current = null;
            setFull(null);
            setCopied(false);
            return 0;
          }
          return s - 1;
        });
      }, 1000);
      onRevealed();
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!full) return;
    try {
      await navigator.clipboard.writeText(full);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  if (!masked && !full) return <span className="pencil">no NIDA on file</span>;

  return (
    <>
      {full ? (
        <div style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'center', flexWrap: 'wrap' }}>
          <span className="num" style={{ fontWeight: 600, letterSpacing: '0.04em' }}>
            {full}
          </span>
          <button
            type="button"
            className="btn btn-quiet"
            style={{ minHeight: 28, padding: '0 var(--sp-2)', fontSize: 'var(--text-sm)' }}
            onClick={() => void copy()}
          >
            {copied ? 'Copied' : 'Copy'}
          </button>
          <span className="pencil" style={{ fontSize: 'var(--text-sm)' }}>
            hides in {left}s
          </span>
          <button
            type="button"
            className="btn btn-quiet"
            style={{ minHeight: 28, padding: '0 var(--sp-2)', fontSize: 'var(--text-sm)' }}
            onClick={drop}
          >
            Hide now
          </button>
        </div>
      ) : (
        <div style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'center', flexWrap: 'wrap' }}>
          <span className="num">{masked}</span>
          <button
            type="button"
            className="btn btn-quiet"
            style={{ minHeight: 28, padding: '0 var(--sp-2)', fontSize: 'var(--text-sm)' }}
            onClick={() => {
              setError(null);
              setOpen(true);
            }}
          >
            Reveal NIDA
          </button>
        </div>
      )}

      <Sheet
        open={open}
        title="Reveal this NIDA number?"
        onClose={() => {
          setOpen(false);
          setError(null);
        }}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={error} />
          <p style={{ color: 'var(--ink-soft)' }}>
            The full number is shown for {REVEAL_SECONDS} seconds and then hidden again. The reveal is
            recorded against your account and the renter sees it in their own profile, so use it for a
            support case only — never to copy numbers in bulk.
          </p>
          <Field
            id="nida-reason"
            label="Why you need it"
            hint="Required. Kept on the audit record and shown to the renter's landlord trail."
            error={error?.errors.reason}
          >
            <textarea
              id="nida-reason"
              className="input"
              rows={3}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              maxLength={200}
              required
            />
          </Field>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy || reason.trim().length === 0}
              onClick={() => void reveal()}
            >
              {busy ? 'Revealing…' : 'Reveal for 60 seconds'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setOpen(false)}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>
    </>
  );
}

/* ------------------------------------------------------------------ */
/* Activity tab — the user's audit trail, paged                         */
/* ------------------------------------------------------------------ */

function ActivityTab({ userId, first }: { userId: string; first: { items: AdminAuditEntry[]; next_cursor?: string | null } }) {
  const [items, setItems] = useState<AdminAuditEntry[]>(first.items ?? []);
  const [cursor, setCursor] = useState<string | null>(first.next_cursor ?? null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  useEffect(() => {
    setItems(first.items ?? []);
    setCursor(first.next_cursor ?? null);
  }, [first]);

  const more = async () => {
    if (!cursor) return;
    setLoading(true);
    setError(null);
    try {
      const res = await adminUsers.auditPage(userId, cursor);
      setItems((prev) => [...prev, ...(res.items ?? [])]);
      setCursor(res.next_cursor ?? null);
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Section title="Activity">
      <p style={{ color: 'var(--ink-soft)', marginBottom: 'var(--sp-3)' }}>
        Every recorded action where this user was the actor or the subject, newest first.
      </p>
      <ProblemNote error={error} />
      <table className="ledger" style={{ marginTop: 'var(--sp-3)' }}>
        <thead>
          <tr>
            <th>When</th>
            <th>Organization</th>
            <th>Who</th>
            <th>Action</th>
            <th>Entity</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {items.length === 0 && !loading ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                Nothing recorded for this user yet.
              </td>
            </tr>
          ) : (
            items.map((row) => (
              <Fragment key={row.id}>
                <tr>
                  <td style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(row.at)}</td>
                  <td>{row.org_name || (row.org_id ? row.org_id.slice(0, 8) : 'Platform')}</td>
                  <td style={{ fontWeight: 500 }}>{row.actor_name || 'System'}</td>
                  <td>{row.action}</td>
                  <td style={{ color: 'var(--ink-soft)' }}>
                    {row.entity_type}
                    {row.entity_id ? ` · ${row.entity_id.slice(0, 8)}` : ''}
                  </td>
                  <td>
                    <button
                      type="button"
                      className="btn btn-quiet"
                      style={{ minHeight: 32 }}
                      aria-expanded={open === row.id}
                      onClick={() => setOpen((cur) => (cur === row.id ? null : row.id))}
                    >
                      {open === row.id ? 'Hide' : 'Details'}
                    </button>
                  </td>
                </tr>
                {open === row.id ? (
                  <tr>
                    <td colSpan={6}>
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
                        <div>
                          <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>Before</div>
                          <pre
                            style={{
                              margin: 0,
                              padding: 'var(--sp-3)',
                              background: 'var(--sheet-tint)',
                              borderRadius: 'var(--radius-sm)',
                              fontSize: 'var(--text-sm)',
                              whiteSpace: 'pre-wrap',
                              wordBreak: 'break-word',
                              maxHeight: 320,
                              overflow: 'auto',
                            }}
                          >
                            {fmtJson(row.before)}
                          </pre>
                        </div>
                        <div>
                          <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>After</div>
                          <pre
                            style={{
                              margin: 0,
                              padding: 'var(--sp-3)',
                              background: 'var(--sheet-tint)',
                              borderRadius: 'var(--radius-sm)',
                              fontSize: 'var(--text-sm)',
                              whiteSpace: 'pre-wrap',
                              wordBreak: 'break-word',
                              maxHeight: 320,
                              overflow: 'auto',
                            }}
                          >
                            {fmtJson(row.after)}
                          </pre>
                        </div>
                      </div>
                    </td>
                  </tr>
                ) : null}
              </Fragment>
            ))
          )}
          {loading ? (
            <tr>
              <td colSpan={6} style={{ color: 'var(--ink-soft)' }}>
                Loading…
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
      {cursor ? (
        <div style={{ marginTop: 'var(--sp-4)' }}>
          <button type="button" className="btn btn-secondary" disabled={loading} onClick={() => void more()}>
            {loading ? 'Loading…' : 'Show older'}
          </button>
        </div>
      ) : null}
    </Section>
  );
}

/* ------------------------------------------------------------------ */
/* The page                                                            */
/* ------------------------------------------------------------------ */

function UserDetailBody({ id }: { id: string }) {
  const [detail, setDetail] = useState<AdminUserDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);
  const [tab, setTab] = useState<Tab>('overview');

  const [suspendOpen, setSuspendOpen] = useState(false);
  const [activateOpen, setActivateOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        setDetail(unwrapUserDetail(await adminUsers.get(id, signal)));
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setDetail(null);
      } finally {
        setLoading(false);
      }
    },
    [id],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const user = detail?.user ?? null;
  const suspended = user?.status === 'suspended';

  const act = async (
    run: () => Promise<{ user: NonNullable<typeof user> } | NonNullable<typeof user>>,
    message: string,
    close: () => void,
  ) => {
    setBusy(true);
    setActionError(null);
    try {
      const updated = unwrapAdminUser(await run());
      setDetail((d) => (d ? { ...d, user: { ...d.user, ...updated } } : d));
      close();
      setReason('');
      setDone(message);
      void load();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const renters = user?.kind === 'renter';
  const linkRequests = detail?.link_requests ?? [];
  const contracts = detail?.contracts ?? [];
  const memberships = detail?.memberships ?? [];
  const payments = detail?.payments ?? null;

  return (
    <>
      <p style={{ marginBottom: 'var(--sp-3)', fontSize: 'var(--text-sm)' }}>
        <Link href="/users" style={{ color: 'var(--ink-soft)' }}>
          ← All users
        </Link>
      </p>

      <PageHead
        title={user?.full_name || (loading ? 'Loading…' : 'User')}
        lead={user ? `${userKindLabel(user.kind)} · ${user.phone || user.email || user.id}` : undefined}
        actions={
          user ? (
            <>
              <button type="button" className="btn btn-quiet" onClick={() => {
                setName(user.full_name ?? '');
                setReason('');
                setActionError(null);
                setRenameOpen(true);
              }}>
                Edit name
              </button>
              {suspended ? (
                <button type="button" className="btn btn-primary" onClick={() => {
                  setReason('');
                  setActionError(null);
                  setActivateOpen(true);
                }}>
                  Activate
                </button>
              ) : (
                <button type="button" className="btn btn-danger" onClick={() => {
                  setReason('');
                  setActionError(null);
                  setSuspendOpen(true);
                }}>
                  Suspend
                </button>
              )}
            </>
          ) : undefined
        }
      />

      <ProblemNote error={error} />
      {done ? <Note>{done}</Note> : null}

      {user ? (
        <div
          style={{
            display: 'flex',
            gap: 'var(--sp-3)',
            alignItems: 'center',
            flexWrap: 'wrap',
            marginTop: 'var(--sp-3)',
          }}
        >
          <KindChip kind={user.kind} />
          <StatusStamp status={user.status} />
          {user.suspended_reason ? (
            <span className="pencil" style={{ fontSize: 'var(--text-sm)' }}>
              {user.suspended_reason}
            </span>
          ) : null}
        </div>
      ) : null}

      {user ? (
        <div
          className="tabs"
          role="tablist"
          aria-label="User sections"
          style={{ gridAutoFlow: 'column', justifyContent: 'start', marginTop: 'var(--sp-4)' }}
        >
          {TABS.map((t) => (
            <button
              key={t.value}
              type="button"
              role="tab"
              className="tab"
              aria-selected={tab === t.value}
              aria-current={tab === t.value ? 'page' : undefined}
              onClick={() => setTab(t.value)}
            >
              {t.label}
            </button>
          ))}
        </div>
      ) : null}

      {user && tab === 'overview' ? (
        <>
          <section style={{ marginTop: 'var(--sp-5)', maxWidth: 760 }}>
            <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>Identity</h2>
            <Row label="Name" value={user.full_name || <span className="pencil">no name</span>} />
            <Row label="Kind" value={userKindLabel(user.kind)} />
            <Row label="Phone" value={user.phone || <span className="pencil">—</span>} />
            <Row label="E-mail" value={user.email || <span className="pencil">—</span>} />
            <Row label="Status" value={<StatusStamp status={user.status} />} />
            {user.suspended_at ? <Row label="Suspended at" value={fmtDateTime(user.suspended_at)} /> : null}
            <Row label="Created" value={fmtDate(user.created_at)} />
            {user.last_seen_at ? <Row label="Last seen" value={fmtDateTime(user.last_seen_at)} /> : null}
            {renters ? <Row label="KYC" value={<KycStamp status={user.kyc_status} />} /> : null}
            {renters ? (
              <Row
                label="NIDA"
                value={
                  <NidaBlock
                    userId={user.id}
                    masked={user.nida_masked}
                    onRevealed={() => setDone('NIDA revealed. The reveal is on the audit trail and the renter can see it.')}
                  />
                }
              />
            ) : null}
            <Row
              label="User id"
              value={<span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{user.id}</span>}
            />
          </section>

          <Section title={renters ? 'Organizations' : 'Memberships'}>
            {memberships.length === 0 && (user.orgs ?? []).length === 0 ? (
              <p style={{ color: 'var(--ink-soft)' }}>
                {user.kind === 'platform_admin'
                  ? 'A platform admin belongs to no organization.'
                  : 'No organization on record.'}
              </p>
            ) : (
              <table className="ledger">
                <thead>
                  <tr>
                    <th>Organization</th>
                    <th>{renters ? 'Relationship' : 'Role'}</th>
                    <th>Status</th>
                    <th>Since</th>
                  </tr>
                </thead>
                <tbody>
                  {(memberships.length > 0
                    ? memberships.map((m) => ({
                        id: m.org_id,
                        name: m.org_name,
                        detail: orgRoleLabel(m.role),
                        status: m.status ?? m.org_status ?? '',
                        since: m.created_at ?? null,
                      }))
                    : (user.orgs ?? []).map((o) => ({
                        id: o.id,
                        name: o.name,
                        detail: o.role ? orgRoleLabel(o.role) : (o.relationship ?? ''),
                        status: '',
                        since: null,
                      }))
                  ).map((r, i) => (
                    <tr key={`${r.id}-${i}`}>
                      <td style={{ fontWeight: 500 }}>
                        <OrgLink id={r.id} name={r.name} />
                      </td>
                      <td>{r.detail || <span className="pencil">—</span>}</td>
                      <td>{r.status || <span className="pencil">—</span>}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>{r.since ? fmtDate(r.since) : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Section>
        </>
      ) : null}

      {user && tab === 'tenancies' ? (
        <>
          <Section title="Contracts">
            {contracts.length === 0 ? (
              <p style={{ color: 'var(--ink-soft)' }}>No contract on record for this user.</p>
            ) : (
              <table className="ledger">
                <thead>
                  <tr>
                    <th>Organization</th>
                    <th>Unit</th>
                    <th>Status</th>
                    <th>Start</th>
                    <th>End</th>
                    <th className="num">Rent</th>
                  </tr>
                </thead>
                <tbody>
                  {contracts.map((c) => (
                    <tr key={c.id}>
                      <td>
                        <OrgLink id={orgOf(c).id} name={orgOf(c).name} />
                      </td>
                      <td>{unitOf(c)}</td>
                      <td>{c.status}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>{fmtDate(c.start_date)}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>{fmtDate(c.end_date)}</td>
                      <td className="num">{c.rent_amount == null ? '—' : fmtTZS(c.rent_amount)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Section>

          <Section title="Link requests">
            {linkRequests.length === 0 ? (
              <p style={{ color: 'var(--ink-soft)' }}>No request to join a unit.</p>
            ) : (
              <table className="ledger">
                <thead>
                  <tr>
                    <th>Organization</th>
                    <th>Unit</th>
                    <th>Status</th>
                    <th>Wanted start</th>
                    <th>Asked</th>
                  </tr>
                </thead>
                <tbody>
                  {linkRequests.map((r) => (
                    <tr key={r.id}>
                      <td>
                        <OrgLink id={orgOf(r).id} name={orgOf(r).name} />
                      </td>
                      <td>{unitOf(r)}</td>
                      <td>{r.status}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>{fmtDate(r.start_date)}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>{fmtDate(r.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Section>
        </>
      ) : null}

      {user && tab === 'payments' ? (
        <Section title="Payments">
          {!payments || (payments.count ?? 0) === 0 ? (
            <p style={{ color: 'var(--ink-soft)' }}>No payment recorded for this user.</p>
          ) : (
            <div style={{ maxWidth: 560 }}>
              <Row label="Payments recorded" value={fmtNum(payments.count)} />
              <Row label="Total" value={<span className="num">{fmtTZS(payments.total)}</span>} />
              <Row label="Last paid" value={fmtDate(payments.last_paid_at)} />
            </div>
          )}
          <p style={{ marginTop: 'var(--sp-4)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            The rent book itself belongs to the organization — open the org to see the schedules and
            receipts behind these figures.
          </p>
          {contracts.some((c) => orgOf(c).id) ? (
            <ul style={{ marginTop: 'var(--sp-3)', display: 'grid', gap: 'var(--sp-2)', listStyle: 'none', padding: 0 }}>
              {Array.from(new Map(contracts.filter((c) => orgOf(c).id).map((c) => [orgOf(c).id, c])).values()).map((c) => (
                <li key={orgOf(c).id}>
                  <OrgLink id={orgOf(c).id} name={orgOf(c).name} />
                </li>
              ))}
            </ul>
          ) : null}
        </Section>
      ) : null}

      {user && tab === 'activity' ? (
        <ActivityTab userId={user.id} first={detail?.audit ?? { items: [] }} />
      ) : null}

      {/* ---------------- Edit name (PATCH /admin/users/{id}) ---------------- */}
      <Sheet open={renameOpen} title="Fix this name" onClose={() => setRenameOpen(false)}>
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          <p style={{ color: 'var(--ink-soft)' }}>
            Signed contracts keep the name they were signed with — the document and its verification
            hash never change. Only live screens follow the account. The user is notified.
          </p>
          <Field id="full_name" label="Full name" hint="2–80 characters." error={actionError?.errors.full_name}>
            <input
              id="full_name"
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              required
            />
          </Field>
          <Field id="rename_reason" label="Reason" hint="Required. Kept on the audit record." error={actionError?.errors.reason}>
            <textarea
              id="rename_reason"
              className="input"
              rows={3}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              maxLength={200}
              required
            />
          </Field>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy || name.trim().length < 2 || reason.trim().length === 0}
              onClick={() =>
                void act(
                  () => adminUsers.rename(id, name.trim(), reason.trim()),
                  'Name updated. The user has been notified.',
                  () => setRenameOpen(false),
                )
              }
            >
              {busy ? 'Saving…' : 'Save name'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setRenameOpen(false)}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>

      {/* ---------------- Suspend / activate ---------------- */}
      <Sheet
        open={suspendOpen}
        title={`Suspend ${user?.full_name || 'this user'}?`}
        onClose={() => setSuspendOpen(false)}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          <p style={{ color: 'var(--ink-soft)' }}>
            Their sessions are revoked immediately and they cannot sign in again until the account is
            activated. Contracts, payments and schedules are untouched.
          </p>
          <Field id="suspend_reason" label="Reason" hint="Kept on the audit record." error={actionError?.errors.reason}>
            <textarea
              id="suspend_reason"
              className="input"
              rows={3}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              maxLength={200}
              required
            />
          </Field>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn btn-danger"
              disabled={busy || reason.trim().length === 0}
              onClick={() =>
                void act(
                  () => adminUsers.suspend(id, reason.trim()),
                  'User suspended. Their sessions have been revoked.',
                  () => setSuspendOpen(false),
                )
              }
            >
              {busy ? 'Suspending…' : 'Suspend user'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setSuspendOpen(false)}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>

      <Sheet
        open={activateOpen}
        title={`Activate ${user?.full_name || 'this user'}?`}
        onClose={() => setActivateOpen(false)}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          <p style={{ color: 'var(--ink-soft)' }}>They can sign in again immediately.</p>
          <Field id="activate_reason" label="Reason" hint="Kept on the audit record." error={actionError?.errors.reason}>
            <textarea
              id="activate_reason"
              className="input"
              rows={3}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              maxLength={200}
              required
            />
          </Field>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy || reason.trim().length === 0}
              onClick={() =>
                void act(
                  () => adminUsers.activate(id, reason.trim()),
                  'User activated. They can sign in again.',
                  () => setActivateOpen(false),
                )
              }
            >
              {busy ? 'Activating…' : 'Activate user'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setActivateOpen(false)}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>
    </>
  );
}

export default function UserDetailPage() {
  const params = useParams<{ id: string }>();
  const id = String(params?.id ?? '');
  return (
    <>
      <UserDetailBody id={id} />
    </>
  );
}
