'use client';

/**
 * Phase 22.2 — tenancy rules. A template sets them; each contract copies them
 * when it is written, so a signed tenancy keeps the rules it was signed
 * under. The backend validates and acts on them; these pieces only edit the
 * template's copy and read a contract's back in plain words.
 */

import type { Translator } from '@tms/ui';
import { useT } from '@tms/ui';
import { Field } from './FormBits';
import { Facts } from './ContractBits';
import type { ContractPolicy, DepositMode, EarlyExitPrepaid, MoveOutProration } from '../lib/api';
import { fmtTZS } from '../lib/format';

const PRORATIONS: MoveOutProration[] = ['full_month', 'pro_rata'];
const PREPAID: EarlyExitPrepaid[] = ['refund', 'forfeit', 'landlord_decides'];
const DEPOSITS: DepositMode[] = ['none', 'fixed', 'months'];

/**
 * The form's copy of a policy. Numbers stay strings while being typed so a
 * field can be cleared; `toPolicy` turns them back for the API.
 */
export interface PolicyDraft {
  move_out_proration: MoveOutProration;
  early_exit_prepaid: EarlyExitPrepaid;
  deposit_mode: DepositMode;
  deposit_amount: string;
  deposit_months: string;
  deductions_may_exceed_deposit: boolean;
  tenant_notice_days: string;
  eviction_notice_days: string;
}

/** What "This agreement sets tenancy rules" starts from. */
export const DEFAULT_POLICY_DRAFT: PolicyDraft = {
  move_out_proration: 'full_month',
  early_exit_prepaid: 'landlord_decides',
  deposit_mode: 'none',
  deposit_amount: '',
  deposit_months: '1',
  deductions_may_exceed_deposit: false,
  tenant_notice_days: '30',
  eviction_notice_days: '30',
};

export function toDraft(p: ContractPolicy): PolicyDraft {
  return {
    move_out_proration: p.move_out_proration,
    early_exit_prepaid: p.early_exit_prepaid,
    deposit_mode: p.deposit_mode,
    deposit_amount: p.deposit_amount ? String(p.deposit_amount) : '',
    deposit_months: p.deposit_months ? String(p.deposit_months) : '1',
    deductions_may_exceed_deposit: p.deductions_may_exceed_deposit,
    tenant_notice_days: String(p.tenant_notice_days),
    eviction_notice_days: String(p.eviction_notice_days),
  };
}

/** Blank numbers go as 0 — the backend says which ones that breaks. */
export function toPolicy(d: PolicyDraft): ContractPolicy {
  const n = (v: string) => (v.trim() === '' ? 0 : Math.round(Number(v)));
  return {
    move_out_proration: d.move_out_proration,
    early_exit_prepaid: d.early_exit_prepaid,
    deposit_mode: d.deposit_mode,
    deposit_amount: d.deposit_mode === 'fixed' ? n(d.deposit_amount) : 0,
    deposit_months: d.deposit_mode === 'months' ? n(d.deposit_months) : 0,
    deductions_may_exceed_deposit: d.deposit_mode !== 'none' && d.deductions_may_exceed_deposit,
    tenant_notice_days: n(d.tenant_notice_days),
    eviction_notice_days: n(d.eviction_notice_days),
  };
}

/* -------------------------------- editor --------------------------------- */

/**
 * The "Tenancy rules" block under a template's body. `value` null = the
 * template sets no rules. `errors` is the save's field map; the backend keys
 * these `policy.<field>`.
 */
export function PolicyEditor({
  value,
  onChange,
  errors,
}: {
  value: PolicyDraft | null;
  onChange: (d: PolicyDraft | null) => void;
  errors: Record<string, string>;
}) {
  const t = useT();
  const err = (f: string) => errors[`policy.${f}`];
  const set = <K extends keyof PolicyDraft>(k: K, v: PolicyDraft[K]) => value && onChange({ ...value, [k]: v });

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
      <h3 style={{ fontSize: 'var(--text-md)' }}>{t('policy.title')}</h3>
      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
        {t('policy.hint', { example: '{{deposit}}' })}
      </p>
      <label
        htmlFor="pol_on"
        style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)' }}
      >
        <input
          id="pol_on"
          type="checkbox"
          checked={value !== null}
          onChange={(e) => onChange(e.target.checked ? { ...DEFAULT_POLICY_DRAFT } : null)}
          style={{ width: 18, height: 18 }}
        />
        {t('policy.toggle')}
      </label>

      {value ? (
        <>
          <Field id="pol_proration" label={t('policy.proration')} error={err('move_out_proration')}>
            <select
              id="pol_proration"
              className="input"
              value={value.move_out_proration}
              onChange={(e) => set('move_out_proration', e.target.value as MoveOutProration)}
            >
              {PRORATIONS.map((v) => (
                <option key={v} value={v}>
                  {t(`policy.proration.${v}`)}
                </option>
              ))}
            </select>
          </Field>

          <Field id="pol_prepaid" label={t('policy.prepaid')} error={err('early_exit_prepaid')}>
            <select
              id="pol_prepaid"
              className="input"
              value={value.early_exit_prepaid}
              onChange={(e) => set('early_exit_prepaid', e.target.value as EarlyExitPrepaid)}
            >
              {PREPAID.map((v) => (
                <option key={v} value={v}>
                  {t(`policy.prepaid.${v}`)}
                </option>
              ))}
            </select>
          </Field>

          <Field id="pol_deposit" label={t('policy.deposit')} error={err('deposit_mode')}>
            <select
              id="pol_deposit"
              className="input"
              value={value.deposit_mode}
              onChange={(e) => set('deposit_mode', e.target.value as DepositMode)}
            >
              {DEPOSITS.map((v) => (
                <option key={v} value={v}>
                  {t(`policy.deposit.${v}`)}
                </option>
              ))}
            </select>
          </Field>

          {value.deposit_mode === 'fixed' ? (
            <Field
              id="pol_deposit_amount"
              label={t('policy.deposit_amount')}
              hint={value.deposit_amount ? fmtTZS(Number(value.deposit_amount)) : undefined}
              error={err('deposit_amount')}
            >
              <input
                id="pol_deposit_amount"
                className="input num"
                type="number"
                min={1}
                step={1}
                inputMode="numeric"
                value={value.deposit_amount}
                onChange={(e) => set('deposit_amount', e.target.value)}
              />
            </Field>
          ) : null}

          {value.deposit_mode === 'months' ? (
            <Field
              id="pol_deposit_months"
              label={t('policy.deposit_months')}
              hint={t('policy.deposit_months_hint')}
              error={err('deposit_months')}
            >
              <input
                id="pol_deposit_months"
                className="input num"
                type="number"
                min={1}
                max={24}
                step={1}
                value={value.deposit_months}
                onChange={(e) => set('deposit_months', e.target.value)}
              />
            </Field>
          ) : null}

          {value.deposit_mode !== 'none' ? (
            <label
              htmlFor="pol_exceed"
              style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)' }}
            >
              <input
                id="pol_exceed"
                type="checkbox"
                checked={value.deductions_may_exceed_deposit}
                onChange={(e) => set('deductions_may_exceed_deposit', e.target.checked)}
                style={{ width: 18, height: 18 }}
              />
              {t('policy.exceed')}
            </label>
          ) : null}

          <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
            <Field id="pol_tenant_notice" label={t('policy.tenant_notice')} error={err('tenant_notice_days')}>
              <input
                id="pol_tenant_notice"
                className="input num"
                type="number"
                min={0}
                max={365}
                value={value.tenant_notice_days}
                onChange={(e) => set('tenant_notice_days', e.target.value)}
              />
            </Field>
            <Field id="pol_eviction_notice" label={t('policy.eviction_notice')} error={err('eviction_notice_days')}>
              <input
                id="pol_eviction_notice"
                className="input num"
                type="number"
                min={0}
                max={365}
                value={value.eviction_notice_days}
                onChange={(e) => set('eviction_notice_days', e.target.value)}
              />
            </Field>
          </div>
        </>
      ) : null}
    </div>
  );
}

/* --------------------------------- facts --------------------------------- */

/** A contract's deposit is always an amount by now, whatever the mode was. */
function depositText(t: Translator, p: ContractPolicy): string {
  if (p.deposit_mode === 'none') return t('policy.deposit.none');
  const amount = fmtTZS(p.deposit_amount);
  return p.deposit_mode === 'months'
    ? t('policy.fact.deposit_months', { amount, months: p.deposit_months })
    : amount;
}

/** The five rules a contract was written under, in plain words. */
export function PolicyFacts({ policy }: { policy: ContractPolicy }) {
  const t = useT();
  return (
    <Facts
      rows={[
        [t('policy.proration'), t(`policy.proration.${policy.move_out_proration}`)],
        [t('policy.prepaid'), t(`policy.prepaid.${policy.early_exit_prepaid}`)],
        [
          t('policy.deposit'),
          policy.deposit_mode === 'none'
            ? depositText(t, policy)
            : `${depositText(t, policy)} · ${t(
                policy.deductions_may_exceed_deposit ? 'policy.fact.exceed_yes' : 'policy.fact.exceed_no',
              )}`,
        ],
        [t('policy.tenant_notice'), t.n('common.day', policy.tenant_notice_days)],
        [t('policy.eviction_notice'), t.n('common.day', policy.eviction_notice_days)],
      ]}
    />
  );
}
