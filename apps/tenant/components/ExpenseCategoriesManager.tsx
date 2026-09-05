'use client';

/**
 * Expense categories (FLOWS flow 12 step 5), modelled on the payment-periods
 * manager: rename in place, reorder with arrows rather than drag so it works
 * with a thumb, deactivate rather than delete once a category has been used.
 *
 * All state lives on the server — every edit is a request to
 * `/org/expense-categories` and the list is re-read afterwards.
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useState } from 'react';
import { Field, Note, ProblemNote } from './FormBits';
import {
  ApiError,
  expenseCategoriesApi,
  toApiError,
  type ExpenseCategory,
} from '../lib/api';
import { TableScroll } from '@tms/ui';

/** The 409 the delete button must translate rather than shout. */
const IN_USE = 'Category is in use; deactivate it instead.';

function categoryError(e: unknown): ApiError {
  const err = toApiError(e);
  if (err.status === 409 && err.code === 'category_in_use') {
    return new ApiError(409, { type: err.code, title: 'Category is in use', detail: IN_USE });
  }
  return err;
}

function Row({
  category,
  first,
  last,
  onChanged,
  onError,
}: {
  category: ExpenseCategory;
  first: boolean;
  last: boolean;
  onChanged: () => Promise<void>;
  onError: (e: ApiError | null) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(category.name);
  const [busy, setBusy] = useState(false);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    onError(null);
    try {
      await fn();
      await onChanged();
      setEditing(false);
    } catch (e) {
      onError(categoryError(e));
    } finally {
      setBusy(false);
    }
  };

  if (editing) {
    return (
      <tr>
        <td colSpan={3}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void run(() => expenseCategoriesApi.update(category.id, { name: name.trim() }));
            }}
            style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'flex-end', flexWrap: 'wrap' }}
          >
            <Field id={`cat-${category.id}`} label="Name">
              <input
                id={`cat-${category.id}`}
                className="input"
                value={name}
                maxLength={60}
                onChange={(e) => setName(e.target.value)}
                style={{ width: 260 }}
              />
            </Field>
            <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()} style={{ minHeight: 40 }}>
              {busy ? 'Saving…' : 'Save'}
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => {
                setEditing(false);
                setName(category.name);
              }}
              style={{ minHeight: 40 }}
            >
              Cancel
            </button>
          </form>
        </td>
      </tr>
    );
  }

  return (
    <tr style={category.active ? undefined : { opacity: 0.55 }}>
      <td style={{ fontWeight: 500 }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          {category.name}
          {category.is_default ? (
            <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-faint)' }}>seeded</span>
          ) : null}
          {category.active ? null : <span className="pencil">inactive</span>}
        </span>
      </td>
      <td className="num" style={{ whiteSpace: 'nowrap' }}>
        {category.active ? (
          <>
            <button
              type="button"
              className="btn btn-quiet"
              aria-label={`Move ${category.name} up`}
              disabled={first || busy}
              onClick={() =>
                void run(() => expenseCategoriesApi.update(category.id, { sort_order: category.sort_order - 1 }))
              }
              style={{ minHeight: 32, padding: '0 var(--sp-2)' }}
            >
              <Icon icon="solar:alt-arrow-up-linear" width={18} />
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              aria-label={`Move ${category.name} down`}
              disabled={last || busy}
              onClick={() =>
                void run(() => expenseCategoriesApi.update(category.id, { sort_order: category.sort_order + 1 }))
              }
              style={{ minHeight: 32, padding: '0 var(--sp-2)' }}
            >
              <Icon icon="solar:alt-arrow-down-linear" width={18} />
            </button>
          </>
        ) : null}
      </td>
      <td className="num" style={{ whiteSpace: 'nowrap' }}>
        {category.active ? (
          <>
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => setEditing(true)}
              disabled={busy}
              style={{ minHeight: 32 }}
            >
              Rename
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => void run(() => expenseCategoriesApi.update(category.id, { active: false }))}
              disabled={busy}
              style={{ minHeight: 32 }}
            >
              Deactivate
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => {
                if (!window.confirm(`Delete "${category.name}"? Only a category never used can be deleted.`)) return;
                void run(() => expenseCategoriesApi.remove(category.id));
              }}
              disabled={busy}
              style={{ minHeight: 32, color: 'var(--stamp-overdue)' }}
            >
              Delete
            </button>
          </>
        ) : (
          <button
            type="button"
            className="btn btn-quiet"
            onClick={() => void run(() => expenseCategoriesApi.update(category.id, { active: true }))}
            disabled={busy}
            style={{ minHeight: 32 }}
          >
            Restore
          </button>
        )}
      </td>
    </tr>
  );
}

export function ExpenseCategoriesManager({ heading }: { heading?: string }) {
  const [items, setItems] = useState<ExpenseCategory[] | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await expenseCategoriesApi.list(signal);
      setItems(res.items ?? []);
      setLoadError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setLoadError(toApiError(e));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const reload = useCallback(async () => {
    await load();
  }, [load]);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await expenseCategoriesApi.create({ name: name.trim() });
      setName('');
      setNote('Category added.');
      await reload();
    } catch (err) {
      const asError = toApiError(err);
      setActionError(
        asError.status === 409 && asError.code === 'category_exists'
          ? new ApiError(409, { type: asError.code, detail: 'You already have a category with that name.' })
          : categoryError(err),
      );
    } finally {
      setBusy(false);
    }
  };

  const active = (items ?? []).filter((c) => c.active);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
      {heading ? <h3 style={{ fontSize: 'var(--text-lg)' }}>{heading}</h3> : null}
      <ProblemNote error={loadError} />
      <ProblemNote error={actionError} />
      {note ? <Note>{note}</Note> : null}

      <TableScroll label="Expense categories">
        <table className="ledger">
          <thead>
            <tr>
              <th>Category</th>
              <th className="num">Order</th>
              <th className="num" />
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={3} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={3} style={{ color: 'var(--ink-soft)' }}>
                  No categories yet. Add the first one below.
                </td>
              </tr>
            ) : (
              items.map((c) => (
                <Row
                  key={c.id}
                  category={c}
                  first={active.length > 0 && active[0].id === c.id}
                  last={active.length > 0 && active[active.length - 1].id === c.id}
                  onChanged={reload}
                  onError={setActionError}
                />
              ))
            )}
          </tbody>
        </table>
      </TableScroll>

      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        A category that has already been used cannot be deleted — deactivate it instead, and the
        expenses filed under it keep their history.
      </p>

      <form onSubmit={add} style={{ display: 'flex', gap: 'var(--sp-3)', alignItems: 'flex-end', flexWrap: 'wrap' }} noValidate>
        <Field
          id="cat_name"
          label="New category"
          hint='What the money went on, e.g. "Generator fuel".'
          error={actionError?.errors.name}
        >
          <input
            id="cat_name"
            className="input"
            value={name}
            maxLength={60}
            onChange={(e) => setName(e.target.value)}
            placeholder="Generator fuel"
            style={{ width: 260 }}
          />
        </Field>
        <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()} style={{ minHeight: 40 }}>
          <Icon icon="solar:add-circle-linear" width={20} /> Add category
        </button>
      </form>
    </div>
  );
}
