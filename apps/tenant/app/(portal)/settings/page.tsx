'use client';

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { TableScroll, useT, type Translator } from '@tms/ui';
import { Field, Note, ProblemNote } from '../../../components/FormBits';
import { PageHead } from '../../../components/PageHead';
import {
  ApiError,
  orgApi,
  unwrapOrg,
  type Member,
  type Org,
  type OrgRole,
  type OrgSettings,
} from '../../../lib/api';
import { useMe } from '../../../lib/auth';

/** Membership status is a small server enum; anything unknown prints as it came. */
const MEMBER_STATUS_KEYS: Record<string, string> = {
  active: 'settings.members.status.active',
  invited: 'settings.members.status.invited',
  disabled: 'settings.members.status.disabled',
  removed: 'settings.members.status.removed',
};

function memberStatus(t: Translator, status: string): string {
  const key = MEMBER_STATUS_KEYS[status];
  return key ? t(key) : status;
}

/* ------------------------------ org profile ------------------------------ */

function OrgProfile({ org, onSaved }: { org: Org; onSaved: (o: Org) => void }) {
  const t = useT();
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
      {saved ? <Note>{t('settings.saved')}</Note> : null}

      <Field id="name" label={t('settings.field.business_name')} error={error?.errors.name}>
        <input id="name" className="input" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>

      <div className="field">
        <label htmlFor="auto_approve">{t('settings.field.link_requests')}</label>
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
          {t('settings.link_requests.auto')}
        </label>
        <span className="hint">{t('settings.link_requests.hint')}</span>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field
          id="due_day"
          label={t('settings.field.due_day')}
          hint={t('settings.field.due_day.hint')}
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
        <Field id="grace_days" label={t('settings.field.grace_days')} hint={t('settings.field.grace_days.hint')}>
          <input
            id="grace_days"
            className="input num"
            type="number"
            min={0}
            value={settings.grace_days ?? 0}
            onChange={(e) => set('grace_days', Number(e.target.value))}
          />
        </Field>
        <Field
          id="sms_language"
          label={t('notifysettings.language.label')}
          hint={t('notifysettings.language.hint')}
        >
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
          label={t('settings.field.unsigned_reminder')}
          hint={t('settings.field.unsigned_reminder.hint')}
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
          {busy ? t('common.saving') : t('settings.save')}
        </button>
      </div>
    </form>
  );
}

/* -------------------------------- members -------------------------------- */

function Members({ canManage }: { canManage: boolean }) {
  const t = useT();
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
      setNote(t('settings.members.invite_sent'));
      await load();
    } catch (err) {
      setFormError(err instanceof ApiError ? err : new ApiError(0, { detail: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (m: Member) => {
    if (!window.confirm(t('settings.members.remove_confirm', { name: m.full_name || m.email }))) return;
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
      <TableScroll label={t('settings.members.table_label')}>
<table className="ledger">
        <thead>
          <tr>
            <th>{t('common.name')}</th>
            <th>{t('common.email')}</th>
            <th>{t('settings.members.col.role')}</th>
            <th>{t('common.status')}</th>
            <th style={{ textAlign: 'right' }} />
          </tr>
        </thead>
        <tbody>
          {items === null ? (
            <tr>
              <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                {t('common.loading')}
              </td>
            </tr>
          ) : items.length === 0 ? (
            <tr>
              <td colSpan={5} style={{ color: 'var(--ink-soft)' }}>
                {t('settings.members.empty')}
              </td>
            </tr>
          ) : (
            items.map((m) => (
              <tr key={m.id}>
                <td style={{ fontWeight: 500 }}>{m.full_name}</td>
                <td>{m.email}</td>
                <td>{m.role === 'org_owner' ? t('settings.members.role.owner') : t('settings.members.role.manager')}</td>
                <td className="pencil">{memberStatus(t, m.status)}</td>
                <td className="num">
                  {canManage ? (
                    <button type="button" className="btn btn-quiet" onClick={() => void remove(m)} style={{ minHeight: 32 }}>
                      {t('common.remove')}
                    </button>
                  ) : null}
                </td>
              </tr>
            ))
          )}
        </tbody>
      </table>
</TableScroll>

      {canManage ? (
        <form onSubmit={invite} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }} noValidate>
          <h3 style={{ fontSize: 'var(--text-lg)' }}>{t('settings.members.invite_heading')}</h3>
          <ProblemNote error={formError} />
          {note ? <Note>{note}</Note> : null}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
            <Field id="m_name" label={t('settings.members.full_name')} error={formError?.errors.full_name}>
              <input
                id="m_name"
                className="input"
                value={form.full_name}
                onChange={(e) => setForm((f) => ({ ...f, full_name: e.target.value }))}
              />
            </Field>
            <Field id="m_email" label={t('common.email')} error={formError?.errors.email}>
              <input
                id="m_email"
                className="input"
                type="email"
                value={form.email}
                onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
              />
            </Field>
            <Field id="m_role" label={t('settings.members.col.role')} hint={t('settings.members.role_hint')}>
              <select
                id="m_role"
                className="input"
                value={form.role}
                onChange={(e) => setForm((f) => ({ ...f, role: e.target.value as OrgRole }))}
              >
                <option value="org_manager">{t('settings.members.role.manager')}</option>
                <option value="org_owner">{t('settings.members.role.owner')}</option>
              </select>
            </Field>
          </div>
          <div>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              <Icon icon="solar:user-plus-linear" width={20} />{' '}
              {busy ? t('settings.members.inviting') : t('settings.members.send_invite')}
            </button>
          </div>
        </form>
      ) : (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          {t('settings.members.owner_only')}
        </p>
      )}
    </div>
  );
}

/* --------------------------------- page ---------------------------------- */

/** One row of the settings index: a heading, a line of copy and a way in. */
function SettingsCard({
  title,
  lead,
  href,
  action,
}: {
  title: string;
  lead: string;
  href: string;
  action: string;
}) {
  return (
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
          <h2 style={{ fontSize: 'var(--text-lg)' }}>{title}</h2>
          <p style={{ marginTop: 'var(--sp-2)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {lead}
          </p>
        </div>
        <Link href={href} className="btn btn-secondary">
          {action}
        </Link>
      </div>
    </section>
  );
}

function SettingsBody() {
  const t = useT();
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
      <PageHead title={t('settings.title')} lead={t('settings.lead')} />
      <hr className="rule rule-strong" />

      <section style={{ paddingTop: 'var(--sp-5)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-4)' }}>{t('settings.profile.heading')}</h2>
        <ProblemNote error={error} />
        {org ? (
          <OrgProfile org={org} onSaved={setOrg} />
        ) : error ? null : (
          <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
        )}
      </section>

      <SettingsCard
        title={t('settings.card.periods.title')}
        lead={t('settings.card.periods.lead')}
        href="/settings/periods"
        action={t('settings.card.periods.action')}
      />

      <SettingsCard
        title={t('settings.card.branding.title')}
        lead={t('settings.card.branding.lead')}
        href="/settings/branding"
        action={t('settings.card.branding.action')}
      />

      <SettingsCard
        title={t('settings.card.bank.title')}
        lead={t('settings.card.bank.lead')}
        href="/settings/bank-account"
        action={t('settings.card.bank.action')}
      />

      <SettingsCard
        title={t('settings.card.notifications.title')}
        lead={t('settings.card.notifications.lead')}
        href="/settings/notifications"
        action={t('settings.card.notifications.action')}
      />

      <SettingsCard
        title={t('settings.card.expcat.title')}
        lead={t('settings.card.expcat.lead')}
        href="/settings/expense-categories"
        action={t('settings.card.expcat.action')}
      />

      <SettingsCard
        title={t('settings.card.preferences.title')}
        lead={t('settings.card.preferences.lead')}
        href="/settings/preferences"
        action={t('settings.card.preferences.action')}
      />

      <section style={{ paddingTop: 'var(--sp-7)' }}>
        <hr className="rule rule-strong" />
        <h2 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>{t('settings.members.heading')}</h2>
        <Members canManage={orgRef?.role === 'org_owner'} />
      </section>
    </>
  );
}

export default function SettingsPage() {
  return (
    <>
      <SettingsBody />
    </>
  );
}
