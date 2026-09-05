'use client';

/**
 * Shared marks for Phase 3 screens (SPEC §2.0): what has happened is stamped,
 * what is still waiting is only pencilled.
 */

import { Icon } from '@iconify/react';
import { useState } from 'react';
import { ApiError, rentersApi, toApiError, type KycStatus, type LinkRequestStatus } from '../lib/api';

export function KycStamp({ status }: { status: KycStatus | null | undefined }) {
  if (status === 'verified') {
    return (
      <span className="stamp stamp-paid" title="Identity verified">
        Verified
      </span>
    );
  }
  if (status === 'submitted') {
    return <span className="pencil">submitted</span>;
  }
  return (
    <span style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)' }} title="No KYC on file">
      no KYC
    </span>
  );
}

export function LinkStatusStamp({ status }: { status: LinkRequestStatus | string | null | undefined }) {
  if (status === 'approved') {
    return <span className="stamp stamp-paid">Approved</span>;
  }
  if (status === 'rejected') {
    return <span className="stamp stamp-overdue">Rejected</span>;
  }
  if (status === 'pending') {
    return <span className="pencil">pending</span>;
  }
  return (
    <span style={{ color: 'var(--ink-faint)', fontSize: 'var(--text-sm)', textTransform: 'capitalize' }}>
      {status ?? '—'}
    </span>
  );
}

/**
 * Fetches a 5-minute presigned URL and opens it in a new tab. The document is
 * never proxied through this app and the link is never stored.
 */
export function ViewIdDocButton({
  userId,
  disabled,
  disabledReason,
}: {
  userId: string;
  disabled?: boolean;
  disabledReason?: string;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const open = async () => {
    setBusy(true);
    setError(null);
    try {
      const { url } = await rentersApi.kycDoc(userId);
      if (url) window.open(url, '_blank', 'noopener,noreferrer');
      else setError(new ApiError(0, { detail: 'The server returned no document link.' }));
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
      <div>
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => void open()}
          disabled={busy || disabled}
          title={disabled ? disabledReason : undefined}
        >
          <Icon icon="solar:document-linear" width={20} /> {busy ? 'Opening…' : 'View ID document'}
        </button>
      </div>
      {disabled && disabledReason ? (
        <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>{disabledReason}</span>
      ) : null}
      {error ? (
        <span role="alert" style={{ fontSize: 'var(--text-sm)', color: 'var(--stamp-overdue)' }}>
          {error.detail}
        </span>
      ) : null}
    </div>
  );
}

/** Two-column definition list used by the KYC and profile panels. */
export function Facts({ rows }: { rows: [string, React.ReactNode][] }) {
  return (
    <dl
      style={{
        display: 'grid',
        gridTemplateColumns: 'max-content 1fr',
        gap: 'var(--sp-2) var(--sp-4)',
        margin: 0,
      }}
    >
      {rows.map(([k, v], i) => (
        <div key={`${k}-${i}`} style={{ display: 'contents' }}>
          <dt style={{ color: 'var(--ink-soft)' }}>{k}</dt>
          <dd style={{ margin: 0 }}>{v ?? '—'}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Status tabs, styled like the vacancy board's (app/units/page.tsx). */
export function FilterTabs<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  label: string;
}) {
  return (
    <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }} role="tablist" aria-label={label}>
      {options.map((t) => {
        const active = value === t.value;
        return (
          <button
            key={t.value || 'all'}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onChange(t.value)}
            style={{
              minHeight: 'var(--touch-min)',
              padding: '0 var(--sp-4)',
              border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
              borderRadius: 'var(--radius-md)',
              background: active ? 'var(--primary-soft)' : 'transparent',
              color: 'var(--ink)',
              fontWeight: active ? 600 : 400,
              fontSize: 'var(--text-sm)',
              cursor: 'pointer',
            }}
          >
            {t.label}
          </button>
        );
      })}
    </div>
  );
}
