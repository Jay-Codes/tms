'use client';

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { Field, Note, ProblemNote } from '../../components/FormBits';
import { PageHead, Shell } from '../../components/Shell';
import {
  ApiError,
  orgApi,
  unwrapOrg,
  type Member,
  type Org,
  type OrgRole,
  type OrgSettings,
} from '../../lib/api';
import { useMe } from '../../lib/auth';

/* ------------------------------ org profile ------------------------------ */

function OrgProfile({ org, onSaved }: { org: Org; onSaved: (o: Org) => void }) {
  const [name, setName] = useState(org.name);
  const [settings, setSettings] = useState<OrgSettings>(org.settings);
  const [error, setError] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof OrgSettings>(k: K, v: OrgSettings[K]) =>
    setSettings((s) => ({ ...s, [k]: v }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const res = await orgApi.update({
        name: name.trim(),
        settings: {
          auto_approve_links: settings.auto_approve_links,
          due_day: settings.due_day,
          grace_days: settings.grace_days,
          sms_language: settings.sms_language,
          unsigned_reminder_days: settings.unsigned_reminder_days,
        },
      });
      const next = unwrapOrg(res);
      onSaved(next);
      setSettings(next.settings);
      setName(next.name);
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, { detail: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const num = (v: string): number | null => (v.trim() === '' ? null : Number(v));

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }} noValidate>
      <ProblemNote error={error} />
      {saved ? <Note>Settings saved.</Note> : null}

      <Field id="name" label="Business name" error={error?.errors.name}>
        <input id="name" className="input" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>

      <div className="field">
        <label htmlFor="auto_approve">Link requests</label>
        <label
          htmlFor="auto_approve"
          style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)', fontWeight: 400 }}
        >
          <input
            id="auto_approve"
            type="checkbox"
            checked={settings.auto_approve_links}
            onChange={(e) => set('auto_approve_links', e.target.checked)}
            style={{ width: 18, height: 18 }}
          />
          Approve renter link requests automatically
        </label>
        <span className="hint">Off means every scan waits for you to approve it.</span>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field
          id="due_day"
          label="Due day of month"
          hint="1–28, or leave blank to use each contract's start date."
          error={error?.errors['settings.due_day']}
        >
          <input
            id="due_day"
            className="input num"
            type="number"
            min={1}
            max={28}
            value={settings.due_day ?? ''}
            onChange={(e) => set('due_day', num(e.target.value))}
          />
        </Field>
        <Field id="grace_days" label="Grace days" hint="Days after the due date before rent counts as overdue.">
          <input
            id="grace_days"
            className="input num"
            type="number"
            min={0}
            value={settings.grace_days ?? 0}
            onChange={(e) => set('grace_days', Number(e.target.value))}
          />
        </Field>
        <Field id="sms_language" label="SMS language">
          <select
            id="sms_language"
            className="input"
            value={settings.sms_language}
            onChange={(e) => set('sms_language', e.target.value as 'sw' | 'en')}
          >
            <option value="sw">Kiswahili</option>
            <option value="en">English</option>
          </select>
        </Field>
        <Field
          id="unsigned_reminder_days"
          label="Unsigned contract reminder"
          hint="Days to wait before nudging a renter who has not signed."
        >
          <input
            id="unsigned_reminder_days"
            className="input num"
            type="number"
            min={0}
            value={settings.unsigned_reminder_days ?? 0}
            onChange={(e) => set('unsigned_reminder_days', Number(e.target.value))}
          />
        </Field>
      </div>

      <div>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Saving…' : 'Save settings'}
        </button>
      </div>
    </form>
  );
}

/* -------------------------------- members -------------------------------- */

function Members({ canManage }: { canManage: boolean }) {
  const [items, setItems] = useState<Member[] | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [form, setForm] = useState({ email: '', full_name: '', role: 'org_manager' as OrgRole });
  const [formError, setFormError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await orgApi.members();
      setItems(res.items ?? []);
      setLoadError(null);
    } catch (e) {
      setLoadError(e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const invite = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setFormError(null);
    setNote(null);
    try {
      await orgApi.invite({ email: form.email.trim(), full_name: form.full_name.trim(), role: form.role });
      setForm({ email: '', full_name: '', role: 'org_manager' });
      setNote('Invitation sent. They set their password from the emailed link.');
      await load();
    } catch (err) {
      setFormError(err instanceof ApiError ? err : new ApiError(0, { detail: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (m: Member) => {
    if (!window.confirm(`Remove ${m.full_name || m.email} from this business? They lose access immediately.`)) return;
    setFormError(null);
    setNote(null);
    try {
      await orgApi.removeMember(m.id);
      await load();
    } catch (err) {
      setFormError(err instanceof ApiError ? err : new ApiError(0, { detail: String(err) }));
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      <ProblemNote error={loadError} />
      <table className="ledger">
        <thead>
          <tr>
            <th>Name</th>
            <th>Email</th>
            <th>Role</th>
            <th>Status</th>
            <th style={{ textAlign: 'right' }} />
          </tr>
        </thead>
        <tbody>
          {items === null ? (
            <tr>
              <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                Loading…
              </td>
            </tr>
          ) : items.length === 0 ? (
            <tr>
              <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                No staff yet.
              </td>
            </tr>
          ) : (
            items.map((m) => (
              <tr key={m.id}>
                <td style={{ fontWeight: 500 }}>{m.full_name}</td>
                <td>{m.email}</td>
                <td>{m.role === 'org_owner' ? 'Owner' : 'Manager'}</td>
                <td className="pencil">{m.status}</td>
                <td className="num">
                  {canManage ? (
                    <button type="button" className="btn btn-quiet" onClick={() => void remove(m)} style={{ minHeight: 32 }}>
                      Remove
                    </button>
                  ) : null}
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>

      {canManage ? (
        <form onSubmit={invite} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }} noValidate>
          <h3 style={{ fontSize: 'var(--text-lg)' }}>Invite someone</h3>
          <ProblemNote error={formError} />
          {note ? <Note>{note}</Note> : null}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
            <Field id="m_name" label="Full name" error={formError?.errors.full_name}>
              <input
                id="m_name"
                className="input"
                value={form.full_name}
                onChange={(e) => setForm((f) => ({ ...f, full_name: e.target.value }))}
              />
            </Field>
            <Field id="m_email" label="Email" error={formError?.errors.email}>
              <input
                id="m_email"
                className="input"
                type="email"
                value={form.email}
                onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
              />
            </Field>
            <Field id="m_role" label="Role" hint="Managers cannot invite or remove staff.">
              <select
                id="m_role"
                className="input"
                value={form.role}
                onChange={(e) => setForm((f) => ({ ...f, role: e.target.value as OrgRole }))}
              >
                <option value="org_manager">Manager</option>
                <option value="org_owner">Owner</option>
              </select>
            </Field>
          </div>
          <div>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              <Icon icon="solar:user-plus-linear" width={20} /> {busy ? 'Inviting…' : 'Send invitation'}
            </button>
          </div>
        </form>
      ) : (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          Only the business owner can invite or remove staff.
        </p>
      )}
    </div>
  );
}

/* --------------------------------- page ---------------------------------- */

function SettingsBody() {
  const { org: orgRef } = useMe();
  const [org, setOrg] = useState<Org | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    orgApi
      .get(ac.signal)
      .then(setOrg)
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(e instanceof ApiError ? e : new ApiError(0, { detail: String(e) }));
      });
    return () => ac.abort();
  }, []);

  return (
    <>
      <PageHead title="Settings" lead="How your business behaves: approvals, due dates and who has access." />
      <hr className="rule rule-strong" />

      <section style={{ paddingTop: 'var(--sp-5)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-4)' }}>Business profile</h2>
        <ProblemNote error={error} />
        {org ? (
          <OrgProfile org={org} onSaved={setOrg} />
        ) : error ? null : (
          <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>
        )}
      </section>

      <section style={{ paddingTop: 'var(--sp-7)' }}>
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
            <h2 style={{ fontSize: 'var(--text-lg)' }}>Payment periods</h2>
            <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              Monthly, quarterly or any number of days you like. Changes affect future contracts only.
            </p>
          </div>
          <Link href="/settings/periods" className="btn btn-secondary">
            Manage periods
          </Link>
        </div>
      </section>

      <section style={{ paddingTop: 'var(--sp-7)' }}>
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
            <h2 style={{ fontSize: 'var(--text-lg)' }}>Branding</h2>
            <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              Display name, colour, typeface, logo and the letterhead printed on every contract.
            </p>
          </div>
          <Link href="/settings/branding" className="btn btn-secondary">
            Edit branding
          </Link>
        </div>
      </section>

      <section style={{ paddingTop: 'var(--sp-7)' }}>
        <hr className="rule rule-strong" />
        <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>Staff</h2>
        <Members canManage={orgRef?.role === 'org_owner'} />
      </section>
    </>
  );
}

export default function SettingsPage() {
  return (
    <Shell>
      <SettingsBody />
    </Shell>
  );
}
