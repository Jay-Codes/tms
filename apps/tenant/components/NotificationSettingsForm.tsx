'use client';

/**
 * Org notification settings (API.md Phase 6, `GET/PUT /org/notification-settings`).
 *
 * Used twice: full width on /settings/notifications, and `compact` inside the
 * setup wizard's Notifications step. Nothing here decides when a message goes
 * out — the scheduler does. This screen only says which kinds are on, at what
 * hour, in which language, and with what words.
 */

import { Icon } from '@iconify/react';
import { useT } from '@tms/ui';
import { useCallback, useEffect, useState } from 'react';
import { Field, Note, ProblemNote } from './FormBits';
import {
  ApiError,
  SCHEDULED_KINDS,
  SMS_MAX_CHARS,
  SMS_VARIABLES,
  notificationsApi,
  toApiError,
  unwrapNotificationSettings,
  withSettingsDefaults,
  type NotificationKindConfig,
  type NotificationSettings,
  type NotificationTemplate,
  type ScheduledKind,
} from '../lib/api';

/** What each kind does, said the way a landlord would say it. */
const KIND_KEYS: Record<ScheduledKind, string> = {
  reminder_7d: 'notifysettings.kind.reminder_7d',
  reminder_due: 'notifysettings.kind.reminder_due',
  overdue_daily: 'notifysettings.kind.overdue_daily',
  thank_you: 'notifysettings.kind.thank_you',
  unsigned_reminder: 'notifysettings.kind.unsigned_reminder',
};

/** `0` → `00:00 EAT`. The scheduler works in Africa/Dar_es_Salaam (API.md). */
function hourLabel(h: number): string {
  return `${String(h).padStart(2, '0')}:00 EAT`;
}

/** The API returns 400 `errors` keyed by path; try the spellings it might use. */
function fieldError(err: ApiError | null, ...keys: string[]): string | undefined {
  if (!err) return undefined;
  for (const k of keys) if (err.errors[k]) return err.errors[k];
  return undefined;
}

/** Buttons that drop a `{{variable}}` into whichever box has the caret. */
function VariableChips({
  names,
  onInsert,
}: {
  names: readonly string[];
  onInsert: (token: string) => void;
}) {
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
      {names.map((v) => (
        <button
          key={v}
          type="button"
          className="btn btn-quiet"
          onClick={() => onInsert(`{{${v}}}`)}
          style={{ minHeight: 28, padding: '0 var(--sp-3)', fontSize: 'var(--text-xs)' }}
        >
          {`{{${v}}}`}
        </button>
      ))}
    </div>
  );
}

/** Live length against the one-SMS ceiling; over the line it turns red. */
export function CharCount({ value, max = SMS_MAX_CHARS }: { value: string; max?: number }) {
  const over = value.length > max;
  return (
    <span
      style={{
        fontSize: 'var(--text-xs)',
        color: over ? 'var(--stamp-overdue)' : 'var(--ink-faint)',
        fontVariantNumeric: 'tabular-nums',
      }}
    >
      {value.length} / {max}
    </span>
  );
}

/** Insert `token` at the caret of a textarea and hand back the new body. */
function insertAt(el: HTMLTextAreaElement | null, current: string, token: string): string {
  if (!el) return current + token;
  const start = el.selectionStart ?? current.length;
  const end = el.selectionEnd ?? start;
  const next = current.slice(0, start) + token + current.slice(end);
  window.requestAnimationFrame(() => {
    el.focus();
    el.setSelectionRange(start + token.length, start + token.length);
  });
  return next;
}

function TemplateBox({
  kind,
  lang,
  value,
  onChange,
  error,
}: {
  kind: ScheduledKind;
  lang: 'sw' | 'en';
  value: string;
  onChange: (v: string) => void;
  error?: string;
}) {
  const [el, setEl] = useState<HTMLTextAreaElement | null>(null);
  /* The two boxes are labelled with the language they hold — each stays written
     in its own language whatever the portal is set to. */
  const id = `tpl_${kind}_${lang}`;
  return (
    <div className={error ? 'field invalid' : 'field'}>
      <label htmlFor={id}>{lang === 'sw' ? 'Kiswahili' : 'English'}</label>
      <textarea
        id={id}
        ref={setEl}
        className="input"
        rows={3}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{ resize: 'vertical' }}
      />
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--sp-3)',
          flexWrap: 'wrap',
          marginTop: 'var(--sp-2)',
        }}
      >
        <VariableChips names={SMS_VARIABLES} onInsert={(t) => onChange(insertAt(el, value, t))} />
        <CharCount value={value} />
      </div>
      {error ? <span className="error">{error}</span> : null}
    </div>
  );
}

export interface NotificationSettingsFormProps {
  /** Wizard mode: no page heading, tighter spacing, "Save and continue" wording. */
  compact?: boolean;
  /** Called with the saved settings after a successful PUT. */
  onSaved?: (s: NotificationSettings) => void;
  saveLabel?: string;
}

export function NotificationSettingsForm({ compact, onSaved, saveLabel }: NotificationSettingsFormProps) {
  const t = useT();
  const [settings, setSettings] = useState<NotificationSettings | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  const [openTemplate, setOpenTemplate] = useState<ScheduledKind | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await notificationsApi.settings(signal);
      setSettings(withSettingsDefaults(unwrapNotificationSettings(res)));
      setLoadError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setLoadError(toApiError(e));
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const set = <K extends keyof NotificationSettings>(k: K, v: NotificationSettings[K]) =>
    setSettings((s) => (s ? { ...s, [k]: v } : s));

  const setKind = (kind: ScheduledKind, patch: Partial<NotificationKindConfig>) =>
    setSettings((s) =>
      s
        ? {
            ...s,
            kinds: { ...s.kinds, [kind]: { enabled: false, ...(s.kinds[kind] ?? {}), ...patch } },
          }
        : s,
    );

  const setTemplate = (kind: ScheduledKind, patch: Partial<NotificationTemplate> | null) =>
    setSettings((s) => {
      if (!s) return s;
      if (patch === null) return { ...s, templates: { ...s.templates, [kind]: null } };
      const current = s.templates[kind] ?? { sw: '', en: '' };
      return { ...s, templates: { ...s.templates, [kind]: { ...current, ...patch } } };
    });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!settings) return;
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const name = (settings.sender_name ?? '').trim();
      const res = await notificationsApi.saveSettings({
        // Blank means "use the platform sender ID", which the API stores as null.
        sender_name: name === '' ? null : name,
        language: settings.language,
        send_hour_local: settings.send_hour_local,
        kinds: settings.kinds,
        templates: settings.templates,
      });
      const next = withSettingsDefaults(unwrapNotificationSettings(res));
      setSettings(next);
      setSaved(true);
      onSaved?.(next);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  if (loadError && !settings) {
    return (
      <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
        <ProblemNote error={loadError} />
        <div>
          <button type="button" className="btn btn-secondary" onClick={() => void load()}>
            {t('common.retry')}
          </button>
        </div>
      </div>
    );
  }

  if (!settings) return <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>;

  const gap = compact ? 'var(--sp-4)' : 'var(--sp-5)';

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap, maxWidth: 720 }} noValidate>
      <ProblemNote error={error} />
      {saved ? <Note>{t('notifysettings.saved')}</Note> : null}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 'var(--sp-4)' }}>
        <Field
          id="sender_name"
          label={t('notifysettings.sender.label')}
          hint={t('notifysettings.sender.hint')}
          error={fieldError(error, 'sender_name')}
        >
          <input
            id="sender_name"
            className="input"
            maxLength={11}
            value={settings.sender_name ?? ''}
            placeholder={t('notifysettings.sender.placeholder')}
            onChange={(e) => set('sender_name', e.target.value)}
          />
        </Field>

        <Field
          id="language"
          label={t('notifysettings.language.label')}
          hint={t('notifysettings.language.hint')}
          error={fieldError(error, 'language')}
        >
          <select
            id="language"
            className="input"
            value={settings.language}
            onChange={(e) => set('language', e.target.value === 'en' ? 'en' : 'sw')}
          >
            <option value="sw">Kiswahili</option>
            <option value="en">English</option>
          </select>
        </Field>

        <Field
          id="send_hour_local"
          label={t('notifysettings.send_hour.label')}
          hint={t('notifysettings.send_hour.hint')}
          error={fieldError(error, 'send_hour_local')}
        >
          <select
            id="send_hour_local"
            className="input"
            value={settings.send_hour_local}
            onChange={(e) => set('send_hour_local', Number(e.target.value))}
          >
            {Array.from({ length: 24 }, (_, h) => (
              <option key={h} value={h}>
                {hourLabel(h)}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
        <h3 style={{ fontSize: 'var(--text-lg)' }}>{t('notifysettings.kinds.heading')}</h3>
        {SCHEDULED_KINDS.map((kind) => {
          const cfg = settings.kinds[kind] ?? { enabled: false };
          const tpl = settings.templates[kind] ?? null;
          const open = openTemplate === kind;
          return (
            <div
              key={kind}
              style={{
                border: '1px solid var(--rule)',
                borderRadius: 'var(--radius-md)',
                padding: 'var(--sp-3) var(--sp-4)',
                display: 'grid',
                gap: 'var(--sp-3)',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-4)', flexWrap: 'wrap' }}>
                <label
                  htmlFor={`k_${kind}`}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--sp-3)',
                    minHeight: 'var(--touch-min)',
                    flex: 1,
                    minWidth: 220,
                  }}
                >
                  <input
                    id={`k_${kind}`}
                    type="checkbox"
                    checked={Boolean(cfg.enabled)}
                    onChange={(e) => setKind(kind, { enabled: e.target.checked })}
                    style={{ width: 18, height: 18, flexShrink: 0 }}
                  />
                  <span>
                    <span style={{ fontWeight: 500 }}>{t(KIND_KEYS[kind])}</span>
                    <span style={{ display: 'block', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                      {t(`${KIND_KEYS[kind]}.hint`)}
                    </span>
                  </span>
                </label>

                {kind === 'reminder_7d' ? (
                  <div className={fieldError(error, 'kinds.reminder_7d.offset_days') ? 'field invalid' : 'field'} style={{ width: 150 }}>
                    <label htmlFor="offset_days">{t('notifysettings.days_before')}</label>
                    <input
                      id="offset_days"
                      className="input num"
                      type="number"
                      min={0}
                      max={30}
                      value={cfg.offset_days ?? 7}
                      onChange={(e) => setKind(kind, { offset_days: Number(e.target.value) })}
                    />
                    {fieldError(error, 'kinds.reminder_7d.offset_days') ? (
                      <span className="error">{fieldError(error, 'kinds.reminder_7d.offset_days')}</span>
                    ) : null}
                  </div>
                ) : null}

                {kind === 'unsigned_reminder' ? (
                  <div
                    className={fieldError(error, 'kinds.unsigned_reminder.after_days') ? 'field invalid' : 'field'}
                    style={{ width: 150 }}
                  >
                    <label htmlFor="after_days">{t('notifysettings.days_after')}</label>
                    <input
                      id="after_days"
                      className="input num"
                      type="number"
                      min={0}
                      max={30}
                      value={cfg.after_days ?? 7}
                      onChange={(e) => setKind(kind, { after_days: Number(e.target.value) })}
                    />
                    {fieldError(error, 'kinds.unsigned_reminder.after_days') ? (
                      <span className="error">{fieldError(error, 'kinds.unsigned_reminder.after_days')}</span>
                    ) : null}
                  </div>
                ) : null}

                <button
                  type="button"
                  className="btn btn-quiet"
                  onClick={() => setOpenTemplate(open ? null : kind)}
                  style={{ minHeight: 32 }}
                  aria-expanded={open}
                >
                  <Icon icon={open ? 'solar:alt-arrow-up-linear' : 'solar:alt-arrow-down-linear'} width={18} />
                  {tpl ? t('notifysettings.wording.custom') : t('notifysettings.wording.default')}
                </button>
              </div>

              {open ? (
                <div style={{ display: 'grid', gap: 'var(--sp-3)', borderTop: '1px solid var(--rule)', paddingTop: 'var(--sp-3)' }}>
                  {tpl === null ? (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
                      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', margin: 0 }}>
                        {t('notifysettings.wording.platform', {
                          language: settings.language === 'sw' ? 'Kiswahili' : 'English',
                        })}
                      </p>
                      <button
                        type="button"
                        className="btn btn-secondary"
                        onClick={() => setTemplate(kind, { sw: '', en: '' })}
                        style={{ minHeight: 32 }}
                      >
                        {t('notifysettings.wording.write_own')}
                      </button>
                    </div>
                  ) : (
                    <>
                      <TemplateBox
                        kind={kind}
                        lang="sw"
                        value={tpl.sw ?? ''}
                        onChange={(v) => setTemplate(kind, { sw: v })}
                        error={fieldError(error, `templates.${kind}.sw`)}
                      />
                      <TemplateBox
                        kind={kind}
                        lang="en"
                        value={tpl.en ?? ''}
                        onChange={(v) => setTemplate(kind, { en: v })}
                        error={fieldError(error, `templates.${kind}.en`)}
                      />
                      <div>
                        <button
                          type="button"
                          className="btn btn-quiet"
                          onClick={() => setTemplate(kind, null)}
                          style={{ minHeight: 32 }}
                        >
                          <Icon icon="solar:restart-linear" width={18} /> {t('notifysettings.wording.reset')}
                        </button>
                      </div>
                    </>
                  )}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>

      <div>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('common.saving') : (saveLabel ?? t('notifysettings.save'))}
        </button>
      </div>
    </form>
  );
}
