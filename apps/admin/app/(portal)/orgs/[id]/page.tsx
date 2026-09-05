'use client';

/**
 * One organization — `GET /admin/orgs/{id}`, plus the two levers an admin has:
 * suspend (with a reason, which the audit row keeps) and activate.
 *
 * Suspension is not a soft toggle: the org's users get 403 `org_suspended` on
 * every route and public unit pages 404, so both actions ask first.
 */

import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { Field, Note, ProblemNote, StatusStamp } from '../../../components/FormBits';
import { Sheet } from '../../../components/Sheet';
import { PageHead, Shell } from '../../../components/Shell';
import {
  ApiError,
  adminApi,
  toApiError,
  unwrapAdminOrg,
  unwrapOrgDetail,
  type AdminOrgDetail,
} from '../../../lib/api';
import { fmtDate, fmtDateTime, fmtNum } from '../../../lib/format';

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 'var(--sp-4)', padding: 'var(--sp-2) 0', borderBottom: '1px solid var(--rule)' }}>
      <div style={{ width: 200, flexShrink: 0, color: 'var(--ink-soft)' }}>{label}</div>
      <div style={{ minWidth: 0 }}>{value}</div>
    </div>
  );
}

/** A settings value as the payload sent it — scalars plainly, blobs as JSON. */
function SettingValue({ value }: { value: unknown }) {
  if (value === null || value === undefined) return <span className="pencil">not set</span>;
  if (typeof value === 'boolean') return <>{value ? 'Yes' : 'No'}</>;
  if (typeof value === 'number' || typeof value === 'string') return <>{String(value)}</>;
  return (
    <pre
      style={{
        margin: 0,
        fontSize: 'var(--text-sm)',
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
        color: 'var(--ink-soft)',
      }}
    >
      {JSON.stringify(value)}
    </pre>
  );
}

function humanKey(key: string): string {
  const s = key.replace(/_/g, ' ');
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function OrgDetailBody({ id }: { id: string }) {
  const [detail, setDetail] = useState<AdminOrgDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const [suspendOpen, setSuspendOpen] = useState(false);
  const [activateOpen, setActivateOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        setDetail(unwrapOrgDetail(await adminApi.org(id, signal)));
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

  const org = detail?.org ?? null;
  const suspended = org?.status === 'suspended';

  const suspend = async () => {
    setBusy(true);
    setActionError(null);
    try {
      const updated = unwrapAdminOrg(await adminApi.suspend(id, reason.trim()));
      setDetail((d) => (d ? { ...d, org: { ...d.org, ...updated } } : d));
      setSuspendOpen(false);
      setReason('');
      setDone('Organization suspended. Its users are locked out until it is activated again.');
      void load();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const activate = async () => {
    setBusy(true);
    setActionError(null);
    try {
      const updated = unwrapAdminOrg(await adminApi.activate(id));
      setDetail((d) => (d ? { ...d, org: { ...d.org, ...updated } } : d));
      setActivateOpen(false);
      setDone('Organization activated. Its users can sign in again.');
      void load();
    } catch (e) {
      setActionError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const settingsEntries = Object.entries(detail?.settings ?? {});

  return (
    <>
      <p style={{ marginBottom: 'var(--sp-3)', fontSize: 'var(--text-sm)' }}>
        <Link href="/orgs" style={{ color: 'var(--ink-soft)' }}>
          ← All organizations
        </Link>
      </p>

      <PageHead
        title={org?.name ?? (loading ? 'Loading…' : 'Organization')}
        lead={org ? org.slug : undefined}
        actions={
          org ? (
            suspended ? (
              <button type="button" className="btn btn-primary" onClick={() => setActivateOpen(true)}>
                Activate
              </button>
            ) : (
              <button type="button" className="btn btn-danger" onClick={() => setSuspendOpen(true)}>
                Suspend
              </button>
            )
          ) : undefined
        }
      />

      <ProblemNote error={error} />
      {done ? <Note>{done}</Note> : null}

      {org ? (
        <>
          <section style={{ marginTop: 'var(--sp-5)', maxWidth: 720 }}>
            <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>Organization</h2>
            <Row label="Status" value={<StatusStamp status={org.status} />} />
            {org.suspended_reason ? <Row label="Suspension reason" value={org.suspended_reason} /> : null}
            {org.suspended_at ? <Row label="Suspended at" value={fmtDateTime(org.suspended_at)} /> : null}
            <Row
              label="Owner"
              value={
                org.owner ? (
                  `${org.owner.name} · ${org.owner.email}`
                ) : (
                  <span className="pencil">no owner</span>
                )
              }
            />
            <Row label="Created" value={fmtDate(org.created_at)} />
            <Row label="Properties" value={fmtNum(org.counts?.properties)} />
            <Row label="Units" value={fmtNum(org.counts?.units)} />
            <Row label="Renters" value={fmtNum(org.counts?.renters)} />
            <Row label="Active contracts" value={fmtNum(org.counts?.active_contracts)} />
            <Row
              label="SMS (30 days)"
              value={`${fmtNum(org.sms?.sent_30d)} sent · ${fmtNum(org.sms?.failed_30d)} failed`}
            />
          </section>

          <section style={{ marginTop: 'var(--sp-6)' }}>
            <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>Members</h2>
            <table className="ledger">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Email</th>
                  <th>Role</th>
                  <th>Status</th>
                  <th>Added</th>
                </tr>
              </thead>
              <tbody>
                {detail && detail.members.length === 0 ? (
                  <tr>
                    <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                      No members.
                    </td>
                  </tr>
                ) : (
                  detail?.members.map((m, i) => (
                    <tr key={m.user_id ?? m.id ?? `${m.email}-${i}`}>
                      <td style={{ fontWeight: 500 }}>{m.full_name}</td>
                      <td>{m.email}</td>
                      <td>{m.role === 'org_owner' ? 'Owner' : m.role === 'org_manager' ? 'Manager' : m.role}</td>
                      <td>{m.status}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>{fmtDate(m.created_at)}</td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </section>

          <section style={{ marginTop: 'var(--sp-6)', maxWidth: 720 }}>
            <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>Settings summary</h2>
            {settingsEntries.length === 0 ? (
              <p style={{ color: 'var(--ink-soft)' }}>No settings returned for this organization.</p>
            ) : (
              settingsEntries.map(([k, v]) => (
                <Row key={k} label={humanKey(k)} value={<SettingValue value={v} />} />
              ))
            )}
          </section>

          <p style={{ marginTop: 'var(--sp-6)', fontSize: 'var(--text-sm)' }}>
            <Link href={`/audit?org_id=${org.id}`}>View this organization&apos;s audit trail →</Link>
          </p>
        </>
      ) : null}

      <Sheet
        open={suspendOpen}
        title={`Suspend ${org?.name ?? 'organization'}?`}
        onClose={() => {
          setSuspendOpen(false);
          setActionError(null);
        }}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          <p style={{ color: 'var(--ink-soft)' }}>
            Its owner and staff will be refused on every screen, its public unit pages stop
            resolving, and the notification scheduler skips it. Renters keep their own accounts.
          </p>
          <Field id="reason" label="Reason" hint="Kept on the audit record." error={actionError?.errors.reason}>
            <textarea
              id="reason"
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
              onClick={() => void suspend()}
            >
              {busy ? 'Suspending…' : 'Suspend organization'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setSuspendOpen(false)}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>

      <Sheet
        open={activateOpen}
        title={`Activate ${org?.name ?? 'organization'}?`}
        onClose={() => {
          setActivateOpen(false);
          setActionError(null);
        }}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={actionError} />
          <p style={{ color: 'var(--ink-soft)' }}>
            Its users regain access immediately and scheduled messages resume.
          </p>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void activate()}>
              {busy ? 'Activating…' : 'Activate organization'}
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

export default function OrgDetailPage() {
  const params = useParams<{ id: string }>();
  const id = String(params?.id ?? '');
  return (
    <Shell>
      <OrgDetailBody id={id} />
    </Shell>
  );
}
