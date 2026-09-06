'use client';

/** Create or edit a property. Validation is server-side; this is UX only. */

import { useState } from 'react';
import { useT } from '@tms/ui';
import { ApiError, propertiesApi, toApiError, unwrapProperty, type Property } from '../lib/api';
import { Field, ProblemNote } from './FormBits';

export function PropertyForm({
  initial,
  submitLabelKey = 'properties.add',
  onSaved,
  onCancel,
}: {
  initial?: Property;
  submitLabelKey?: string;
  onSaved: (p: Property) => void;
  onCancel?: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(initial?.name ?? '');
  const [location, setLocation] = useState(initial?.location_text ?? '');
  const [notes, setNotes] = useState(initial?.notes ?? '');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const body = {
        name: name.trim(),
        location_text: location.trim(),
        notes: notes.trim(),
      };
      const res = initial
        ? await propertiesApi.update(initial.id, body)
        : await propertiesApi.create(body);
      onSaved(unwrapProperty(res));
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <Field
        id="prop_name"
        label={t('properties.form.name')}
        hint={t('properties.form.name_hint')}
        error={error?.errors.name}
      >
        <input
          id="prop_name"
          className="input"
          value={name}
          maxLength={120}
          onChange={(e) => setName(e.target.value)}
          placeholder={t('properties.form.name_ph')}
        />
      </Field>
      <Field
        id="prop_loc"
        label={t('properties.form.location')}
        hint={t('properties.form.location_hint')}
        error={error?.errors.location_text}
      >
        <input
          id="prop_loc"
          className="input"
          value={location}
          onChange={(e) => setLocation(e.target.value)}
          placeholder={t('properties.form.location_ph')}
        />
      </Field>
      <Field id="prop_notes" label={t('common.notes')} hint={t('properties.form.notes_hint')}>
        <textarea
          id="prop_notes"
          className="input"
          rows={3}
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          style={{ height: 'auto', paddingTop: 'var(--sp-2)', paddingBottom: 'var(--sp-2)' }}
        />
      </Field>
      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()}>
          {busy ? t('common.saving') : t(submitLabelKey)}
        </button>
        {onCancel ? (
          <button type="button" className="btn btn-quiet" onClick={onCancel}>
            {t('common.cancel')}
          </button>
        ) : null}
      </div>
    </form>
  );
}
