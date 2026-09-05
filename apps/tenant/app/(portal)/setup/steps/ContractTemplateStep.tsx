'use client';

/**
 * Wizard step: contract template.
 *
 * Editing happens in the default template — org creation seeds one, so there is
 * almost always something to edit; if there is not, the step sends the landlord
 * to the templates screen rather than inventing one behind their back.
 *
 * The due day and grace days sit here too because they are the two numbers the
 * template's `{{due_day}}` and the schedule generator actually read.
 */

import Link from 'next/link';
import { useT } from '@tms/ui';
import { useEffect, useState } from 'react';
import { Field, Note, ProblemNote } from '../../../../components/FormBits';
import { TemplateEditor } from '../../../../components/TemplateEditor';
import {
  ApiError,
  orgApi,
  templatesApi,
  toApiError,
  unwrapOrg,
  type ContractTemplateSummary,
  type Org,
} from '../../../../lib/api';

function DueDayForm() {
  const t = useT();
  const [org, setOrg] = useState<Org | null>(null);
  const [dueDay, setDueDay] = useState('');
  const [graceDays, setGraceDays] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    orgApi
      .get(ac.signal)
      .then((o) => {
        setOrg(o);
        setDueDay(o.settings?.due_day == null ? '' : String(o.settings.due_day));
        setGraceDays(String(o.settings?.grace_days ?? 0));
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      });
    return () => ac.abort();
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const next = unwrapOrg(
        await orgApi.update({
          settings: {
            due_day: dueDay.trim() === '' ? null : Math.round(Number(dueDay)),
            grace_days: Math.round(Number(graceDays || 0)),
          },
        }),
      );
      setOrg(next);
      setSaved(true);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  if (!org && !error) return <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>;

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }} noValidate>
      <ProblemNote error={error} />
      {saved ? <Note>{t('setup.template.dates_saved')}</Note> : null}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field
          id="w_due_day"
          label={t('settings.field.due_day')}
          hint={t('setup.template.due_day.hint')}
          error={error?.errors['settings.due_day']}
        >
          <input
            id="w_due_day"
            className="input num"
            type="number"
            min={1}
            max={28}
            value={dueDay}
            onChange={(e) => {
              setDueDay(e.target.value);
              setSaved(false);
            }}
          />
        </Field>
        <Field
          id="w_grace_days"
          label={t('settings.field.grace_days')}
          hint={t('settings.field.grace_days.hint')}
          error={error?.errors['settings.grace_days']}
        >
          <input
            id="w_grace_days"
            className="input num"
            type="number"
            min={0}
            value={graceDays}
            onChange={(e) => {
              setGraceDays(e.target.value);
              setSaved(false);
            }}
          />
        </Field>
      </div>
      <div>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('common.saving') : t('setup.template.save_dates')}
        </button>
      </div>
    </form>
  );
}

export function ContractTemplateStep() {
  const t = useT();
  const [templates, setTemplates] = useState<ContractTemplateSummary[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    templatesApi
      .list(ac.signal)
      .then((r) => {
        setTemplates(r.items ?? []);
        setError(null);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setTemplates([]);
      });
    return () => ac.abort();
  }, []);

  const target = templates ? (templates.find((t) => t.is_default) ?? templates[0] ?? null) : null;

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-6)' }}>
      <p style={{ maxWidth: 'var(--measure)' }}>{t('setup.template.lead')}</p>

      <DueDayForm />

      <div>
        <hr className="rule rule-strong" />
        <h3 style={{ fontSize: 'var(--text-lg)', margin: 'var(--sp-4) 0' }}>
          {target ? t('setup.template.default_named', { name: target.name }) : t('setup.template.default')}
        </h3>
        <ProblemNote error={error} />
        {templates === null ? (
          <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
        ) : target ? (
          <TemplateEditor templateId={target.id} />
        ) : error ? null : (
          <p style={{ color: 'var(--ink-soft)' }}>
            {t('setup.template.none')}{' '}
            <Link href="/contracts/templates" className="btn btn-quiet">
              {t('setup.template.create_one')}
            </Link>
          </p>
        )}
      </div>
    </div>
  );
}
