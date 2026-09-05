'use client';

/**
 * The collection account (FLOWS flow 7, renter step 2).
 *
 * This is the only screen in the landlord app whose output is read by someone
 * else: whatever is typed here is printed on the renter's payment screen, so
 * the copy leans on that fact rather than hiding it. Nothing here is a secret —
 * it is a "pay to" notice, not a credential — but a typo in the account number
 * is a renter's money going somewhere else, which is why the form previews
 * exactly what they will see.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { Field, Note, ProblemNote } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import {
  ApiError,
  bankAccountApi,
  toApiError,
  unwrapBankAccount,
  type BankAccount,
} from '../../../../lib/api';

const EMPTY: BankAccount = { bank_name: '', account_name: '', account_number: '', instructions: '' };

function BankAccountBody() {
  const t = useT();
  const [form, setForm] = useState<BankAccount>(EMPTY);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    bankAccountApi
      .get(ac.signal)
      .then((res) => setForm({ ...EMPTY, ...(unwrapBankAccount(res) ?? {}) }))
      .catch((e) => {
        // No account set yet reads as a 404 on some backends; an empty form is
        // the right answer to that, not an error.
        if (e instanceof DOMException) return;
        const err = toApiError(e);
        if (err.status !== 404) setError(err);
      })
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, []);

  const set = <K extends keyof BankAccount>(k: K, v: BankAccount[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const res = await bankAccountApi.save({
        bank_name: form.bank_name.trim(),
        account_name: form.account_name.trim(),
        account_number: form.account_number.trim(),
        instructions: form.instructions.trim(),
      });
      const next = unwrapBankAccount(res);
      if (next) setForm({ ...EMPTY, ...next });
      setSaved(true);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  const filled = form.bank_name || form.account_name || form.account_number;

  return (
    <>
      <PageHead
        title={t('bank.title')}
        lead={t('bank.lead')}
        actions={
          <Link href="/settings" className="btn btn-quiet">
            <Icon icon="solar:arrow-left-linear" width={20} /> {t('nav.settings')}
          </Link>
        }
      />

      <hr className="rule rule-strong" />

      {loading ? (
        <p style={{ marginTop: 'var(--sp-5)', color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'minmax(320px, 520px) minmax(260px, 1fr)',
            gap: 'var(--sp-7)',
            alignItems: 'start',
            marginTop: 'var(--sp-5)',
          }}
        >
          <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
            <ProblemNote error={error} />
            {saved ? <Note>{t('bank.saved')}</Note> : null}

            <Field id="bank_name" label={t('bank.field.bank')} error={error?.errors.bank_name}>
              <input
                id="bank_name"
                className="input"
                value={form.bank_name}
                onChange={(e) => set('bank_name', e.target.value)}
                placeholder={t('bank.field.bank.placeholder')}
              />
            </Field>

            <Field
              id="account_name"
              label={t('bank.field.account_name')}
              hint={t('bank.field.account_name.hint')}
              error={error?.errors.account_name}
            >
              <input
                id="account_name"
                className="input"
                value={form.account_name}
                onChange={(e) => set('account_name', e.target.value)}
                placeholder={t('bank.field.account_name.placeholder')}
              />
            </Field>

            <Field id="account_number" label={t('bank.field.account_number')} error={error?.errors.account_number}>
              <input
                id="account_number"
                className="input num"
                value={form.account_number}
                onChange={(e) => set('account_number', e.target.value)}
                placeholder={t('bank.field.account_number.placeholder')}
              />
            </Field>

            <Field
              id="instructions"
              label={t('bank.field.instructions')}
              hint={t('bank.field.instructions.hint', { n: form.instructions.length })}
              error={error?.errors.instructions}
            >
              <textarea
                id="instructions"
                className="input"
                rows={4}
                maxLength={300}
                value={form.instructions}
                onChange={(e) => set('instructions', e.target.value)}
                placeholder={t('bank.field.instructions.placeholder')}
              />
            </Field>

            <div>
              <button type="submit" className="btn btn-primary" disabled={busy}>
                {busy ? t('common.saving') : t('bank.save')}
              </button>
            </div>
          </form>

          {/* What the renter reads. Same words, their screen. */}
          <aside className="sheet" style={{ padding: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-3)' }}>
            <h2 style={{ fontSize: 'var(--text-md)' }}>{t('bank.preview.heading')}</h2>
            <hr className="rule" />
            {filled ? (
              <>
                <div>
                  <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('bank.field.bank')}</div>
                  <div style={{ fontWeight: 600 }}>{form.bank_name || '—'}</div>
                </div>
                <div>
                  <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('bank.field.account_name')}</div>
                  <div style={{ fontWeight: 600 }}>{form.account_name || '—'}</div>
                </div>
                <div>
                  <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('bank.field.account_number')}</div>
                  <div className="num" style={{ fontWeight: 600, letterSpacing: '0.06em' }}>
                    {form.account_number || '—'}
                  </div>
                </div>
                {form.instructions ? (
                  <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{form.instructions}</p>
                ) : null}
              </>
            ) : (
              <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {t('bank.preview.empty')}
              </p>
            )}
          </aside>
        </div>
      )}
    </>
  );
}

export default function BankAccountPage() {
  return (
    <>
      <BankAccountBody />
    </>
  );
}
