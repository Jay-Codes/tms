'use client';

/**
 * Record or edit an expense (FLOWS flow 12 step 2).
 *
 * One sheet for both jobs and for every place it opens from — the ledger, a
 * property page, the detail screen — so a landlord learns one form. When it is
 * opened from a property the property is prefilled and locked: the reader is
 * already inside that property's books and moving the money to another one by
 * accident is the mistake worth designing out.
 *
 * The receipt is deliberately a second step. The expense is written first and
 * is safe; only then is the file presigned, PUT at MinIO and completed. If the
 * upload fails the money is still recorded and the sheet says so, rather than
 * throwing away a form the landlord has already filled in.
 */

import { Icon } from '@iconify/react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  expenseCategoriesApi,
  expensesApi,
  propertiesApi,
  toApiError,
  unitsApi,
  unwrapExpense,
  uploadReceipt,
  RECEIPT_ACCEPT,
  RECEIPT_MAX_BYTES,
  RECEIPT_TYPES,
  type Expense,
  type ExpenseCategory,
  type ExpenseInput,
  type Property,
  type Unit,
} from '../lib/api';
import { fmtAmount, isoPlusDays, todayISO } from '../lib/format';
import { useT } from '@tms/ui';

export interface ExpenseSheetProps {
  open: boolean;
  /** Present = edit that expense; absent = record a new one. */
  expense?: Expense | null;
  /** Prefill and lock the property (opened from a property page). */
  lockedPropertyId?: string;
  /** Printed at the head of the sheet when the property is locked. */
  lockedPropertyName?: string;
  onClose: () => void;
  onSaved: (expense: Expense) => void;
}

/** Digits only — the box holds an integer number of shillings, nothing else. */
function digitsOf(value: string): string {
  return value.replace(/\D+/g, '').replace(/^0+(?=\d)/, '');
}

type ReceiptPlan = 'keep' | 'replace' | 'remove';

/**
 * The same rule `receiptFileProblem()` applies, but answered with a key so the
 * refusal can be read in the landlord's own language.
 */
function receiptProblemKey(file: File): string | null {
  if (!(RECEIPT_TYPES as readonly string[]).includes(file.type)) return 'expenses.receipt.bad_type';
  if (file.size > RECEIPT_MAX_BYTES) return 'expenses.receipt.too_big';
  return null;
}

export function ExpenseSheet({
  open,
  expense,
  lockedPropertyId,
  lockedPropertyName,
  onClose,
  onSaved,
}: ExpenseSheetProps) {
  const t = useT();
  const editing = !!expense;

  const [properties, setProperties] = useState<Property[] | null>(null);
  const [categories, setCategories] = useState<ExpenseCategory[]>([]);
  const [units, setUnits] = useState<Unit[] | null>(null);

  const [propertyId, setPropertyId] = useState('');
  const [unitId, setUnitId] = useState('');
  const [categoryId, setCategoryId] = useState('');
  const [amount, setAmount] = useState('');
  const [incurredOn, setIncurredOn] = useState(todayISO());
  const [vendor, setVendor] = useState('');
  const [reference, setReference] = useState('');
  const [note, setNote] = useState('');

  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const [receiptPlan, setReceiptPlan] = useState<ReceiptPlan>('keep');

  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  /** The expense saved but its receipt did not — say so, do not pretend. */
  const [receiptWarning, setReceiptWarning] = useState<string | null>(null);

  const maxDate = useMemo(() => isoPlusDays(1), []);

  /* ------------------------------ reset on open ----------------------------- */
  useEffect(() => {
    if (!open) return;
    setPropertyId(expense?.property?.id ?? lockedPropertyId ?? '');
    setUnitId(expense?.unit?.id ?? '');
    setCategoryId(expense?.category?.id ?? '');
    setAmount(expense ? String(Math.round(expense.amount ?? 0)) : '');
    setIncurredOn(expense?.incurred_on ?? todayISO());
    setVendor(expense?.vendor ?? '');
    setReference(expense?.reference ?? '');
    setNote(expense?.note ?? '');
    setFile(null);
    setFileError(null);
    setReceiptPlan('keep');
    setError(null);
    setReceiptWarning(null);
    setStep(null);
  }, [open, expense, lockedPropertyId]);

  /* -------------------------------- the lists ------------------------------- */
  useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    propertiesApi
      .list({ limit: 200 }, ac.signal)
      .then((r) => setProperties(r.items ?? []))
      .catch(() => setProperties([]));
    expenseCategoriesApi
      .list(ac.signal)
      .then((r) => setCategories((r.items ?? []).filter((c) => c.active)))
      .catch(() => setCategories([]));
    return () => ac.abort();
  }, [open]);

  // Units follow the property; a unit from the previous property is dropped
  // rather than sent to the server to be refused.
  useEffect(() => {
    if (!open || !propertyId) {
      setUnits(propertyId ? null : []);
      return;
    }
    const ac = new AbortController();
    setUnits(null);
    unitsApi
      .list({ property_id: propertyId, limit: 200 }, ac.signal)
      .then((r) => setUnits(r.items ?? []))
      .catch(() => setUnits([]));
    return () => ac.abort();
  }, [open, propertyId]);

  useEffect(() => {
    if (!units || !unitId) return;
    if (!units.some((u) => u.id === unitId)) setUnitId('');
  }, [units, unitId]);

  const pickFile = (chosen: File | null) => {
    setFileError(null);
    if (!chosen) {
      setFile(null);
      return;
    }
    const problem = receiptProblemKey(chosen);
    if (problem) {
      setFile(null);
      setFileError(t(problem));
      return;
    }
    setFile(chosen);
    setReceiptPlan('replace');
  };

  const amountNum = Number(digitsOf(amount) || '0');
  const valid = propertyId !== '' && amountNum > 0 && /^\d{4}-\d{2}-\d{2}$/.test(incurredOn);

  const submit = useCallback(async () => {
    setBusy(true);
    setError(null);
    setReceiptWarning(null);
    // Local, not state: the sheet decides whether to close before React has
    // re-rendered with the warning it may have just set.
    let warned = false;
    try {
      const body: ExpenseInput = {
        property_id: propertyId,
        unit_id: unitId || null,
        category_id: categoryId || null,
        amount: amountNum,
        incurred_on: incurredOn,
        vendor: vendor.trim() || undefined,
        reference: reference.trim() || undefined,
        note: note.trim() || undefined,
      };

      setStep(editing ? t('expenses.step.saving') : t('expenses.step.writing'));
      const saved = unwrapExpense(
        editing && expense ? await expensesApi.update(expense.id, body) : await expensesApi.create(body),
      );
      let latest = saved;

      if (receiptPlan === 'remove' && expense?.receipt?.present) {
        setStep(t('expenses.step.removing_receipt'));
        try {
          latest = unwrapExpense(await expensesApi.receiptRemove(saved.id));
        } catch (e) {
          warned = true;
          setReceiptWarning(toApiError(e).detail);
        }
      } else if (file) {
        setStep(t('expenses.step.uploading_receipt'));
        try {
          const ticket = await expensesApi.receiptTicket(saved.id, file.type, file.size);
          await uploadReceipt(ticket, file);
          latest = unwrapExpense(await expensesApi.receiptComplete(saved.id, ticket.object_key));
        } catch (e) {
          // The money is recorded either way — that is the whole point of
          // uploading second. Say what happened and keep the sheet open.
          warned = true;
          setReceiptWarning(
            `${toApiError(e).detail} ${t('expenses.receipt.upload_failed')}`,
          );
        }
      }

      onSaved(latest);
      if (!warned) onClose();
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
      setStep(null);
    }
  }, [
    amountNum,
    categoryId,
    editing,
    expense,
    file,
    incurredOn,
    note,
    onClose,
    onSaved,
    propertyId,
    receiptPlan,
    reference,
    t,
    unitId,
    vendor,
  ]);

  const lockedName =
    lockedPropertyName ??
    expense?.property?.name ??
    (properties ?? []).find((p) => p.id === lockedPropertyId)?.name ??
    '';

  return (
    <Sheet
      open={open}
      title={editing ? t('expenses.edit') : t('expenses.record')}
      onClose={onClose}
      width={620}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
        style={{ display: 'grid', gap: 'var(--sp-4)' }}
        noValidate
      >
        <ProblemNote error={error} />

        {receiptWarning ? (
          <p
            role="alert"
            style={{
              display: 'flex',
              gap: 'var(--sp-3)',
              color: 'var(--stamp-overdue)',
              border: '1px solid var(--stamp-overdue)',
              borderRadius: 'var(--radius-sm)',
              padding: 'var(--sp-2) var(--sp-3)',
              fontSize: 'var(--text-sm)',
              maxWidth: 'none',
            }}
          >
            <Icon icon="solar:danger-triangle-linear" width={20} />
            <span>{receiptWarning}</span>
          </p>
        ) : null}

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--sp-4)' }}>
          <Field
            id="ex_property"
            label={t('common.property')}
            hint={lockedPropertyId ? t('expenses.form.property_locked_hint') : undefined}
            error={error?.errors.property_id}
          >
            {lockedPropertyId ? (
              <input id="ex_property" className="input" value={lockedName} readOnly disabled />
            ) : (
              <select
                id="ex_property"
                className="input"
                value={propertyId}
                disabled={properties === null}
                onChange={(e) => setPropertyId(e.target.value)}
              >
                <option value="">
                  {properties === null ? t('common.loading') : t('expenses.form.choose_property')}
                </option>
                {(properties ?? []).map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            )}
          </Field>

          <Field
            id="ex_unit"
            label={t('common.unit')}
            hint={t('expenses.form.unit_hint')}
            error={error?.errors.unit_id}
          >
            <select
              id="ex_unit"
              className="input"
              value={unitId}
              disabled={!propertyId || units === null}
              onChange={(e) => setUnitId(e.target.value)}
            >
              <option value="">
                {!propertyId
                  ? t('expenses.form.choose_property_first')
                  : units === null
                    ? t('common.loading')
                    : t('expenses.whole_property')}
              </option>
              {(units ?? []).map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name}
                </option>
              ))}
            </select>
          </Field>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--sp-4)' }}>
          <Field id="ex_category" label={t('expenses.category')} error={error?.errors.category_id}>
            <select
              id="ex_category"
              className="input"
              value={categoryId}
              onChange={(e) => setCategoryId(e.target.value)}
            >
              <option value="">{t('expenses.uncategorised')}</option>
              {categories.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </Field>

          <Field
            id="ex_amount"
            label={t('expenses.form.amount_label')}
            hint={amountNum > 0 ? `TZS ${fmtAmount(amountNum)}` : t('expenses.form.amount_hint')}
            error={error?.errors.amount}
          >
            <input
              id="ex_amount"
              className="input num"
              inputMode="numeric"
              autoComplete="off"
              value={amount ? fmtAmount(Number(digitsOf(amount) || '0')) : ''}
              onChange={(e) => setAmount(digitsOf(e.target.value))}
              placeholder="0"
            />
          </Field>

          <Field
            id="ex_date"
            label={t('expenses.incurred_on')}
            hint={t('expenses.form.date_hint')}
            error={error?.errors.incurred_on}
          >
            <input
              id="ex_date"
              className="input"
              type="date"
              max={maxDate}
              value={incurredOn}
              onChange={(e) => setIncurredOn(e.target.value)}
            />
          </Field>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--sp-4)' }}>
          <Field
            id="ex_vendor"
            label={t('expenses.vendor')}
            hint={t('expenses.form.vendor_hint')}
            error={error?.errors.vendor}
          >
            <input
              id="ex_vendor"
              className="input"
              maxLength={120}
              value={vendor}
              onChange={(e) => setVendor(e.target.value)}
              placeholder={t('expenses.form.vendor_placeholder')}
            />
          </Field>
          <Field
            id="ex_reference"
            label={t('expenses.reference')}
            hint={t('expenses.form.reference_hint')}
            error={error?.errors.reference}
          >
            <input
              id="ex_reference"
              className="input"
              maxLength={80}
              value={reference}
              onChange={(e) => setReference(e.target.value)}
              placeholder={t('expenses.form.reference_placeholder')}
            />
          </Field>
        </div>

        <Field
          id="ex_note"
          label={t('common.note')}
          hint={t('expenses.form.note_hint', { count: note.length })}
          error={error?.errors.note}
        >
          <textarea
            id="ex_note"
            className="input"
            rows={2}
            maxLength={500}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
        </Field>

        {/* ------------------------------ receipt ------------------------------ */}
        <div className={fileError ? 'field invalid' : 'field'}>
          <label htmlFor="ex_receipt">{t('expenses.receipt')}</label>

          {editing && expense?.receipt?.present && receiptPlan !== 'replace' ? (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--sp-3)',
                flexWrap: 'wrap',
                minHeight: 'var(--touch-min)',
              }}
            >
              <Icon icon="solar:paperclip-linear" width={18} />
              <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                {receiptPlan === 'remove'
                  ? t('expenses.receipt.will_remove')
                  : expense.receipt.content_type === 'application/pdf'
                    ? t('expenses.receipt.has_pdf')
                    : t('expenses.receipt.has_photo')}
              </span>
              {receiptPlan === 'remove' ? (
                <button
                  type="button"
                  className="btn btn-quiet"
                  style={{ minHeight: 36 }}
                  onClick={() => setReceiptPlan('keep')}
                >
                  {t('expenses.receipt.keep')}
                </button>
              ) : (
                <button
                  type="button"
                  className="btn btn-quiet"
                  style={{ minHeight: 36 }}
                  onClick={() => setReceiptPlan('remove')}
                >
                  {t('common.remove')}
                </button>
              )}
            </div>
          ) : null}

          {receiptPlan === 'remove' ? null : (
            <input
              id="ex_receipt"
              className="input"
              type="file"
              accept={RECEIPT_ACCEPT}
              onChange={(e) => pickFile(e.target.files?.[0] ?? null)}
            />
          )}

          {fileError ? (
            <span className="error">{fileError}</span>
          ) : (
            <span className="hint">
              {file
                ? `${file.name} · ${Math.max(1, Math.round(file.size / 1024))} KB`
                : editing && expense?.receipt?.present
                  ? t('expenses.receipt.replace_hint')
                  : t('expenses.receipt.hint')}
            </span>
          )}
        </div>

        <div style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center', flexWrap: 'wrap' }}>
          <button type="submit" className="btn btn-primary" disabled={busy || !valid}>
            {busy ? (step ?? t('common.saving')) : editing ? t('expenses.save_changes') : t('expenses.record')}
          </button>
          <button type="button" className="btn btn-quiet" onClick={onClose} disabled={busy}>
            {receiptWarning ? t('common.done') : t('common.cancel')}
          </button>
          {busy && step ? (
            <span aria-live="polite" style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
              {step}
            </span>
          ) : null}
        </div>
      </form>
    </Sheet>
  );
}
