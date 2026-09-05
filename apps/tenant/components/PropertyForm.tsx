'use client';

/** Create or edit a property. Validation is server-side; this is UX only. */

import { useState } from 'react';
import { ApiError, propertiesApi, toApiError, unwrapProperty, type Property } from '../lib/api';
import { Field, ProblemNote } from './FormBits';

export function PropertyForm({
  initial,
  submitLabel = 'Add property',
  onSaved,
  onCancel,
}: {
  initial?: Property;
  submitLabel?: string;
  onSaved: (p: Property) => void;
  onCancel?: () => void;
}) {
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
        label="Property name"
        hint='Your own name for it, e.g. "Mbezi Beach Block A".'
        error={error?.errors.name}
      >
        <input
          id="prop_name"
          className="input"
          value={name}
          maxLength={120}
          onChange={(e) => setName(e.target.value)}
          placeholder="Mbezi Beach Block A"
        />
      </Field>
      <Field id="prop_loc" label="Location" hint="Street, ward or landmark." error={error?.errors.location_text}>
        <input
          id="prop_loc"
          className="input"
          value={location}
          onChange={(e) => setLocation(e.target.value)}
          placeholder="Mbezi Beach, Kinondoni"
        />
      </Field>
      <Field id="prop_notes" label="Notes" hint="Optional. Anything you want to remember about this property.">
        <textarea
          id="prop_notes"
          className="input"
          rows={3}
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          style={{ height: 'auto', paddingTop: 'var(--sp-2)', paddingBottom: 'var(--sp-2)' }}
        />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
        <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()}>
          {busy ? 'Saving…' : submitLabel}
        </button>
        {onCancel ? (
          <button type="button" className="btn btn-quiet" onClick={onCancel}>
            Cancel
          </button>
        ) : null}
      </div>
    </form>
  );
}
