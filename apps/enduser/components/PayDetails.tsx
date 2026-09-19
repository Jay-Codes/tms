'use client';

/**
 * Where the money goes — Phase 16 §16.4.
 *
 * The same details are needed in three places (the pinned card on the
 * Payments tab, the proof sheet, and the sheet's success state), so they live
 * here once: bank block, mobile-money block, the landlord's own instructions
 * verbatim, and the reference hint. Nothing is computed — every line is the
 * org's `bank_account` / `mobile_money` as `GET /me/schedules` served it.
 */

import { useState } from 'react';
import { Icon } from '@iconify/react';
import { useT } from '@tms/ui';
import type { BankAccount, MobileMoney } from '../lib/api';

export function CopyButton({ value, label }: { value: string; label: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard is blocked (insecure origin, older browser). The number is
      // on screen in full, so this is a convenience, never the only route.
      setCopied(false);
    }
  }

  return (
    <button
      type="button"
      className="btn btn-quiet"
      onClick={() => void copy()}
      aria-label={copied ? t('common.copiedValue', { label }) : t('common.copyValue', { label })}
      style={{ width: 'auto', paddingInline: 'var(--sp-2)', gap: 'var(--sp-2)' }}
    >
      <Icon icon={copied ? 'solar:check-read-linear' : 'solar:copy-linear'} width={18} aria-hidden />
      {copied ? t('common.copied') : t('common.copy')}
    </button>
  );
}

function DetailRow({
  label,
  value,
  copy,
  copyLabel,
}: {
  label: string;
  value: string;
  copy?: boolean;
  copyLabel?: string;
}) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--sp-3)',
        minHeight: 'var(--touch-min)',
        borderBottom: '1px solid var(--rule)',
      }}
    >
      <span
        style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', flex: '0 0 auto' }}
      >
        {label}
      </span>
      <span
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'flex-end',
          gap: 'var(--sp-2)',
          minWidth: 0,
          textAlign: 'right',
          overflowWrap: 'anywhere',
        }}
      >
        <span style={copy ? { fontWeight: 600, letterSpacing: '0.03em' } : undefined}>
          {value}
        </span>
        {copy && <CopyButton value={value} label={copyLabel ?? label} />}
      </span>
    </div>
  );
}

export interface PayDetailsProps {
  account: BankAccount | null;
  /** The top-level wallet; an org may take mobile money and no bank at all. */
  mobileMoney?: MobileMoney | null;
  /** What to tell the landlord the payment is for — the renter's unit name. */
  reference: string;
  /** Inside the proof sheet the block is a reminder, not the main event. */
  compact?: boolean;
}

export function PayDetails({ account, mobileMoney, reference, compact = false }: PayDetailsProps) {
  const t = useT();
  const wallet = account?.mobile_money ?? mobileMoney ?? null;
  const hasBank = Boolean(account?.account_number || account?.bank_name);

  if (!hasBank && !wallet) {
    return <p className="pencil">{t('payments.noAccount')}</p>;
  }

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-1)' }}>
      {hasBank && account && (
        <>
          <DetailRow label={t('payments.bank')} value={account.bank_name} />
          <DetailRow label={t('payments.accountName')} value={account.account_name} />
          <DetailRow
            label={t('payments.accountNumber')}
            value={account.account_number}
            copy
            copyLabel={t('payments.accountNumberLabel')}
          />
        </>
      )}

      {wallet && (
        <>
          <p
            style={{
              margin: 0,
              paddingTop: hasBank ? 'var(--sp-3)' : 0,
              fontSize: 'var(--text-sm)',
              fontWeight: 600,
            }}
          >
            {t('payments.mobileMoney')}
          </p>
          <DetailRow label={t('payments.mmProvider')} value={wallet.provider} />
          <DetailRow
            label={t('payments.mmNumber')}
            value={wallet.number}
            copy
            copyLabel={t('payments.mmNumberLabel')}
          />
          <DetailRow label={t('payments.mmName')} value={wallet.name} />
        </>
      )}

      {account?.instructions && (
        <p
          style={{
            margin: 0,
            paddingTop: 'var(--sp-3)',
            color: 'var(--ink-soft)',
            fontSize: 'var(--text-sm)',
            whiteSpace: 'pre-wrap',
          }}
        >
          {account.instructions}
        </p>
      )}

      <p
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--sp-2)',
          margin: 0,
          paddingTop: 'var(--sp-3)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:info-circle-linear" width={18} aria-hidden />
        <span>
          {t('payments.reference')}
          {reference ? ' — ' : ''}
          {reference && <strong>{reference}</strong>}.
        </span>
      </p>

      {!compact && (
        <p
          style={{
            margin: 0,
            paddingTop: 'var(--sp-3)',
            color: 'var(--ink-soft)',
            fontSize: 'var(--text-sm)',
          }}
        >
          {t('payments.howMoney')}
        </p>
      )}
    </div>
  );
}
