'use client';

/**
 * "Edit name" (Phase 19.3) — one sheet, wherever a typo has to be fixed.
 *
 * The rules are the server's: 2…80 characters after trimming; for a renter who
 * has signed with this org a **reason** is required (400 on `reason`), and one
 * who has signed with another org is refused (409 `renter_signed_elsewhere`,
 * Phase 21). This component does not pre-judge either; it sends the name and
 * prints what came back. A refusal is remembered for the rest of the visit, so
 * the button stops offering something the server has already said no to.
 */

import { Icon } from '@iconify/react';
import { useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import { type ApiError } from '../lib/api';

export const NAME_MIN = 2;
export const NAME_MAX = 80;

/** True when the name is inside the bounds the backend enforces. */
export function nameLooksValid(value: string): boolean {
  const trimmed = value.trim();
  return trimmed.length >= NAME_MIN && trimmed.length <= NAME_MAX;
}

export function EditNameSheet({
  open,
  title,
  currentName,
  busy,
  error,
  hint,
  withReason = false,
  onClose,
  onSubmit,
}: {
  open: boolean;
  title: string;
  currentName: string;
  busy: boolean;
  error: ApiError | null;
  /** One line above the box — who gets told, typically. */
  hint?: string;
  /** Show the optional "why" box (renter renames, Phase 21). */
  withReason?: boolean;
  onClose: () => void;
  onSubmit: (fullName: string, reason: string) => void;
}) {
  const t = useT();
  const [value, setValue] = useState(currentName);
  const [reason, setReason] = useState('');

  useEffect(() => {
    if (open) {
      setValue(currentName);
      setReason('');
    }
  }, [open, currentName]);

  return (
    <Sheet open={open} title={title} onClose={onClose} width={480}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit(value.trim(), reason.trim());
        }}
        style={{ display: 'grid', gap: 'var(--sp-4)' }}
        noValidate
      >
        <ProblemNote error={error} />
        {hint ? <p style={{ color: 'var(--ink-soft)' }}>{hint}</p> : null}
        <Field
          id="name_edit"
          label={t('names.field.full_name')}
          hint={t('names.field.full_name.hint')}
          error={error?.errors.full_name}
        >
          <input
            id="name_edit"
            className="input"
            maxLength={NAME_MAX}
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
        </Field>
        {withReason ? (
          <Field
            id="name_edit_reason"
            label={t('names.field.reason')}
            hint={t('names.field.reason.hint')}
            error={error?.errors.reason}
          >
            <input
              id="name_edit_reason"
              className="input"
              maxLength={200}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </Field>
        ) : null}
        <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)' }}>
          <button type="submit" className="btn btn-primary" disabled={busy || !nameLooksValid(value)}>
            {busy ? t('common.saving') : t('names.save')}
          </button>
          <button type="button" className="btn btn-quiet" onClick={onClose} disabled={busy}>
            {t('common.cancel')}
          </button>
        </div>
      </form>
    </Sheet>
  );
}

/** The trigger, with the refused state written on it. */
export function EditNameButton({
  onClick,
  blockedReason,
}: {
  onClick: () => void;
  blockedReason?: string | null;
}) {
  const t = useT();
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
      <button
        type="button"
        className="btn btn-quiet"
        style={{ minHeight: 32 }}
        disabled={Boolean(blockedReason)}
        title={blockedReason ?? undefined}
        onClick={onClick}
      >
        <Icon icon="solar:pen-linear" width={18} /> {t('names.edit')}
      </button>
      {blockedReason ? (
        <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)', maxWidth: 320 }}>
          {blockedReason}
        </span>
      ) : null}
    </span>
  );
}
