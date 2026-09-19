'use client';

/**
 * The collection account (FLOWS flow 7, renter step 2; Phase 16 §16.4).
 *
 * This is the only screen in the landlord app whose output is read by someone
 * else: whatever is typed here is printed on the renter's payment screen, so
 * the copy leans on that fact rather than hiding it. Nothing here is a secret —
 * it is a "pay to" notice, not a credential — but a typo in the account number
 * is a renter's money going somewhere else, which is why the form previews
 * exactly what they will see.
 *
 * Phase 16 adds the mobile-money wallet beside the bank account. The two are
 * independent: an org may take one, the other, or both, and the renter's card
 * prints whichever is set. Clearing the wallet is its own action, because
 * `PUT /org/bank-account` keeps a wallet whose key is omitted — saving bank
 * details must never silently drop it.
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
  readBankAccountView,
  toApiError,
  type BankAccount,
  type BankAccountInput,
  type MobileMoney,
} from '../../../../lib/api';

const EMPTY: BankAccount = { bank_name: '', account_name: '', account_number: '', instructions: '' };
const EMPTY_WALLET: MobileMoney = { provider: '', number: '', name: '' };

/**
 * The wallet is sent only when the landlord typed something into it. A blank
 * block is not "clear it" — clearing is the button, which sends an explicit
 * `null`; a blank block simply means "leave whatever is stored alone".
 */
function walletTouched(w: MobileMoney): boolean {
  return Boolean(w.provider.trim() || w.number.trim() || w.name.trim());
}

/** The 400 names the wallet's fields nested; accept either spelling. */
function walletError(error: ApiError | null, field: keyof MobileMoney): string | undefined {
  if (!error) return undefined;
  return error.errors[`mobile_money.${field}`] ?? error.errors[`mobile_money_${field}`];
}

function BankAccountBody() {
  const t = useT();
  const [form, setForm] = useState<BankAccount>(EMPTY);
  const [wallet, setWallet] = useState<MobileMoney>(EMPTY_WALLET);
  /** True when the org has a wallet stored — what the Clear button acts on. */
  const [walletStored, setWalletStored] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const apply = (res: Parameters<typeof readBankAccountView>[0]) => {
    const view = readBankAccountView(res);
    setForm({ ...EMPTY, ...(view.bank_account ?? {}) });
    setWallet({ ...EMPTY_WALLET, ...(view.mobile_money ?? {}) });
    setWalletStored(Boolean(view.mobile_money));
  };

  useEffect(() => {
    const ac = new AbortController();
    bankAccountApi
      .get(ac.signal)
      .then(apply)
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
  const setWalletField = (k: keyof MobileMoney, v: string) =>
    setWallet((w) => ({ ...w, [k]: v }));

  const body = (mobile: BankAccountInput['mobile_money']): BankAccountInput => ({
    bank_name: form.bank_name.trim(),
    account_name: form.account_name.trim(),
    account_number: form.account_number.trim(),
    instructions: form.instructions.trim(),
    ...(mobile === undefined ? {} : { mobile_money: mobile }),
  });

  const send = async (mobile: BankAccountInput['mobile_money']) => {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      apply(await bankAccountApi.save(body(mobile)));
      setSaved(true);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    // Typed in → send it; left blank → omit the key and keep what is stored.
    await send(
      walletTouched(wallet)
        ? {
            provider: wallet.provider.trim(),
            number: wallet.number.trim(),
            name: wallet.name.trim(),
          }
        : undefined,
    );
  };

  const clearWallet = async () => {
    setWallet(EMPTY_WALLET);
    await send(null);
  };

  const bankFilled = Boolean(form.bank_name || form.account_name || form.account_number);
  const walletFilled = walletTouched(wallet);

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
            // Form and "what the renter sees" sit side by side only where both
            // fit; a phone and a portrait tablet get one column.
            gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))',
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

            {/* Phase 16 §16.4 — the wallet. Its own ruled block, because it is
                a second way to be paid, not a detail of the bank account. */}
            <fieldset
              style={{
                border: 0,
                padding: 0,
                margin: 0,
                borderTop: '1px solid var(--rule-strong)',
                paddingTop: 'var(--sp-4)',
                display: 'grid',
                gap: 'var(--sp-4)',
              }}
            >
              <legend style={{ padding: 0, fontSize: 'var(--text-md)', fontWeight: 600 }}>
                {t('bank.mm.heading')}
              </legend>
              <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                {t('bank.mm.lead')}
              </p>

              <Field
                id="mm_provider"
                label={t('bank.mm.provider')}
                error={walletError(error, 'provider')}
              >
                <input
                  id="mm_provider"
                  className="input"
                  maxLength={40}
                  value={wallet.provider}
                  onChange={(e) => setWalletField('provider', e.target.value)}
                  placeholder={t('bank.mm.provider.placeholder')}
                />
              </Field>

              <Field
                id="mm_number"
                label={t('bank.mm.number')}
                error={walletError(error, 'number')}
              >
                <input
                  id="mm_number"
                  className="input num"
                  maxLength={20}
                  inputMode="tel"
                  value={wallet.number}
                  onChange={(e) => setWalletField('number', e.target.value)}
                  placeholder={t('bank.mm.number.placeholder')}
                />
              </Field>

              <Field
                id="mm_name"
                label={t('bank.mm.name')}
                hint={t('bank.mm.name.hint')}
                error={walletError(error, 'name')}
              >
                <input
                  id="mm_name"
                  className="input"
                  maxLength={120}
                  value={wallet.name}
                  onChange={(e) => setWalletField('name', e.target.value)}
                  placeholder={t('bank.mm.name.placeholder')}
                />
              </Field>

              {walletStored || walletFilled ? (
                <div>
                  <button
                    type="button"
                    className="btn btn-quiet"
                    disabled={busy}
                    onClick={() => void clearWallet()}
                  >
                    {t('bank.mm.clear')}
                  </button>
                </div>
              ) : null}
            </fieldset>

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

          {/* What the renter reads — the "How to pay" card of their Payments
              tab, in the same order and with the same reference hint. */}
          <aside className="sheet" style={{ padding: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-3)' }}>
            <h2 style={{ fontSize: 'var(--text-md)' }}>{t('bank.preview.heading')}</h2>
            <hr className="rule" />
            {bankFilled || walletFilled ? (
              <>
                {bankFilled ? (
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
                  </>
                ) : null}

                {walletFilled ? (
                  <>
                    {bankFilled ? <hr className="rule" /> : null}
                    <div>
                      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('bank.mm.heading')}</div>
                      <div style={{ fontWeight: 600 }}>{wallet.provider || '—'}</div>
                    </div>
                    <div>
                      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('bank.mm.number')}</div>
                      <div className="num" style={{ fontWeight: 600, letterSpacing: '0.06em' }}>
                        {wallet.number || '—'}
                      </div>
                    </div>
                    <div>
                      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('bank.mm.name')}</div>
                      <div style={{ fontWeight: 600 }}>{wallet.name || '—'}</div>
                    </div>
                  </>
                ) : null}

                {form.instructions ? (
                  <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{form.instructions}</p>
                ) : null}

                {/* The renter's card always ends with this line, whether or
                    not the landlord wrote instructions of their own. */}
                <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)', margin: 0 }}>
                  {t('bank.preview.reference_hint')}
                </p>
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
