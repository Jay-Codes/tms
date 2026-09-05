'use client';

/**
 * Shared notification pieces: the delivery stamp, the log table (used on
 * /notifications and on a renter's record) and the custom bulk-SMS composer.
 *
 * Everything shown is the backend's own `notification_log` row — this file
 * never decides whether a message was sent, only how the row reads.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Note, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  CUSTOM_SMS_VARIABLES,
  SMS_MAX_CHARS,
  kindLabel,
  notificationsApi,
  rentersApi,
  resolveVariables,
  toApiError,
  type CustomSendResult,
  type NotificationLogEntry,
  type RenterSummary,
} from '../lib/api';
import { fmtDateTime } from '../lib/format';
import { useMe } from '../lib/auth';

/** Sent is stamped, queued is pencilled, failed is stamped red (SPEC §2.0). */
export function DeliveryStamp({ status }: { status: string }) {
  if (status === 'sent') return <span className="stamp stamp-paid">Sent</span>;
  if (status === 'failed') return <span className="stamp stamp-overdue">Failed</span>;
  if (status === 'sending') return <span className="pencil">sending…</span>;
  return <span className="pencil">queued</span>;
}

/** The body, clamped to one line until the reader asks for the rest. */
function Body({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const long = text.length > 90;
  if (!long) return <span>{text}</span>;
  return (
    <span>
      {open ? text : `${text.slice(0, 90)}…`}{' '}
      <button
        type="button"
        className="btn btn-quiet"
        onClick={() => setOpen((o) => !o)}
        style={{ minHeight: 24, padding: '0 var(--sp-2)', fontSize: 'var(--text-xs)' }}
      >
        {open ? 'Less' : 'More'}
      </button>
    </span>
  );
}

export interface NotificationLogTableProps {
  items: NotificationLogEntry[] | null;
  emptyText?: string;
  /** Omitted on a renter's own record — the column would repeat the page title. */
  showRenter?: boolean;
  /** Retry is offered only where the reader can act on it. */
  onRetry?: (entry: NotificationLogEntry) => void;
  retryingId?: string | null;
}

export function NotificationLogTable({
  items,
  emptyText = 'No messages yet.',
  showRenter = true,
  onRetry,
  retryingId,
}: NotificationLogTableProps) {
  const cols = 5 + (showRenter ? 1 : 0) + (onRetry ? 1 : 0);
  return (
    <table className="ledger">
      <thead>
        <tr>
          <th>Time</th>
          {showRenter ? <th>Renter</th> : null}
          <th>Kind</th>
          <th>Message</th>
          <th>Status</th>
          <th className="num">Attempts</th>
          {onRetry ? <th className="num" /> : null}
        </tr>
      </thead>
      <tbody>
        {items === null ? (
          <tr>
            <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
              Loading…
            </td>
          </tr>
        ) : items.length === 0 ? (
          <tr>
            <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
              {emptyText}
            </td>
          </tr>
        ) : (
          items.map((n) => (
            <tr key={n.id}>
              <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', whiteSpace: 'nowrap' }}>
                {fmtDateTime(n.sent_at ?? n.created_at)}
              </td>
              {showRenter ? (
                <td>
                  <span style={{ fontWeight: 500 }}>
                    {n.user_id ? (
                      <Link href={`/renters/${n.user_id}`} style={{ color: 'inherit' }}>
                        {n.renter_name ?? '—'}
                      </Link>
                    ) : (
                      (n.renter_name ?? '—')
                    )}
                  </span>
                  <span
                    className="num"
                    style={{ display: 'block', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}
                  >
                    {n.to_phone ?? '—'}
                  </span>
                </td>
              ) : null}
              <td style={{ fontSize: 'var(--text-sm)' }}>{kindLabel(n.kind)}</td>
              <td style={{ maxWidth: 420 }}>
                <Body text={n.body ?? ''} />
              </td>
              <td>
                <DeliveryStamp status={n.status} />
                {n.error ? (
                  <span
                    style={{
                      display: 'block',
                      fontSize: 'var(--text-xs)',
                      color: 'var(--stamp-overdue)',
                      marginTop: 'var(--sp-2)',
                    }}
                  >
                    {n.error}
                  </span>
                ) : null}
              </td>
              <td className="num">{n.attempts ?? 0}</td>
              {onRetry ? (
                <td className="num">
                  {n.status === 'failed' ? (
                    <button
                      type="button"
                      className="btn btn-quiet"
                      onClick={() => onRetry(n)}
                      disabled={retryingId === n.id}
                      style={{ minHeight: 32 }}
                    >
                      {retryingId === n.id ? 'Retrying…' : 'Retry'}
                    </button>
                  ) : null}
                </td>
              ) : null}
            </tr>
          ))
        )}
      </tbody>
    </table>
  );
}

/* --------------------------- custom bulk SMS ----------------------------- */

/** A renter is "active" for `all_active` when the backend says so; this list
 * is only for picking names, so it shows everyone the org knows. */
function RenterPicker({
  selected,
  onToggle,
}: {
  selected: string[];
  onToggle: (id: string) => void;
}) {
  const [q, setQ] = useState('');
  const [items, setItems] = useState<RenterSummary[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    const t = window.setTimeout(() => {
      rentersApi
        .list({ q: q.trim(), limit: 200 }, ac.signal)
        .then((r) => {
          setItems(r.items ?? []);
          setError(null);
        })
        .catch((e) => {
          if (e instanceof DOMException && e.name === 'AbortError') return;
          setError(toApiError(e));
          setItems([]);
        });
    }, 250);
    return () => {
      window.clearTimeout(t);
      ac.abort();
    };
  }, [q]);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
      <div className="field">
        <label htmlFor="renter_q">Find renters</label>
        <input
          id="renter_q"
          className="input"
          value={q}
          placeholder="Name or phone"
          onChange={(e) => setQ(e.target.value)}
        />
      </div>
      <ProblemNote error={error} />
      <div
        style={{
          maxHeight: 260,
          overflowY: 'auto',
          border: '1px solid var(--rule)',
          borderRadius: 'var(--radius-md)',
        }}
      >
        {items === null ? (
          <p style={{ padding: 'var(--sp-3)', color: 'var(--ink-soft)' }}>Loading…</p>
        ) : items.length === 0 ? (
          <p style={{ padding: 'var(--sp-3)', color: 'var(--ink-soft)' }}>No renters match.</p>
        ) : (
          items.map((r) => (
            <label
              key={r.user_id}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--sp-3)',
                minHeight: 'var(--touch-min)',
                padding: '0 var(--sp-3)',
                borderBottom: '1px solid var(--rule)',
                cursor: 'pointer',
              }}
            >
              <input
                type="checkbox"
                checked={selected.includes(r.user_id)}
                onChange={() => onToggle(r.user_id)}
                style={{ width: 18, height: 18 }}
              />
              <span style={{ flex: 1, minWidth: 0 }}>
                <span style={{ fontWeight: 500 }}>{r.full_name}</span>
                <span className="num" style={{ marginLeft: 'var(--sp-3)', color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
                  {r.phone ?? '—'}
                </span>
              </span>
              <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                {(r.units ?? []).map((u) => u.unit_name).join(', ')}
              </span>
            </label>
          ))
        )}
      </div>
    </div>
  );
}

export function SendMessageForm({ onSent }: { onSent?: () => void }) {
  const { org } = useMe();
  const [recipients, setRecipients] = useState<'all_active' | 'selected'>('all_active');
  const [selected, setSelected] = useState<string[]>([]);
  const [body, setBody] = useState('');
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [result, setResult] = useState<CustomSendResult | null>(null);
  const [first, setFirst] = useState<RenterSummary | null>(null);
  const area = useRef<HTMLTextAreaElement | null>(null);

  // The preview needs one renter to substitute against. For a selection it is
  // the first one picked; for "all active" it is the first the directory lists.
  useEffect(() => {
    if (recipients === 'selected' && selected.length === 0) {
      setFirst(null);
      return;
    }
    const ac = new AbortController();
    rentersApi
      .list({ limit: 200 }, ac.signal)
      .then((r) => {
        const items = r.items ?? [];
        setFirst(
          recipients === 'selected'
            ? (items.find((x) => x.user_id === selected[0]) ?? null)
            : (items[0] ?? null),
        );
      })
      .catch(() => setFirst(null));
    return () => ac.abort();
  }, [recipients, selected]);

  const preview = useMemo(() => {
    const unit = first?.units?.[0];
    return resolveVariables(body, {
      name: first?.full_name ?? 'Renter',
      unit: unit?.unit_name ?? 'Unit',
      property: unit?.property_name ?? 'Property',
      org: org?.name ?? 'Your business',
    });
  }, [body, first, org]);

  const count = recipients === 'selected' ? selected.length : null;
  const tooLong = body.length > SMS_MAX_CHARS;
  const canSend =
    body.trim().length > 0 && !tooLong && (recipients === 'all_active' || selected.length > 0);

  const insert = (token: string) => {
    const el = area.current;
    const start = el?.selectionStart ?? body.length;
    const end = el?.selectionEnd ?? start;
    const next = body.slice(0, start) + token + body.slice(end);
    setBody(next);
    window.requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(start + token.length, start + token.length);
    });
  };

  const send = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await notificationsApi.sendCustom({
        recipients,
        renter_user_ids: recipients === 'selected' ? selected : undefined,
        body: body.trim(),
      });
      setResult(res);
      setConfirming(false);
      setBody('');
      setSelected([]);
      onSent?.();
    } catch (e) {
      const err = toApiError(e);
      setError(err);
      setConfirming(false);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
      <ProblemNote error={error} />
      {error?.status === 429 ? (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
          Ten batches an hour is the limit. Wait a while, then send the rest.
        </p>
      ) : null}
      {result ? (
        <Note>
          Queued {result.queued} message{result.queued === 1 ? '' : 's'}
          {result.skipped ? `, skipped ${result.skipped} (no phone number on file or no active contract)` : ''}. They
          appear in the log as they are delivered.
        </Note>
      ) : null}

      <fieldset style={{ border: 0, padding: 0, margin: 0, display: 'grid', gap: 'var(--sp-2)' }}>
        <legend style={{ fontWeight: 500, marginBottom: 'var(--sp-2)' }}>Who gets this</legend>
        <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', minHeight: 'var(--touch-min)' }}>
          <input
            type="radio"
            name="recipients"
            checked={recipients === 'all_active'}
            onChange={() => setRecipients('all_active')}
            style={{ width: 18, height: 18 }}
          />
          Every renter with an active contract
        </label>
        <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', minHeight: 'var(--touch-min)' }}>
          <input
            type="radio"
            name="recipients"
            checked={recipients === 'selected'}
            onChange={() => setRecipients('selected')}
            style={{ width: 18, height: 18 }}
          />
          Only the renters I pick{selected.length ? ` (${selected.length})` : ''}
        </label>
      </fieldset>

      {recipients === 'selected' ? (
        <RenterPicker
          selected={selected}
          onToggle={(id) =>
            setSelected((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s, id]))
          }
        />
      ) : null}

      <div className={tooLong ? 'field invalid' : 'field'}>
        <label htmlFor="custom_body">Message</label>
        <textarea
          id="custom_body"
          ref={area}
          className="input"
          rows={4}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="Habari {{name}}, …"
          style={{ resize: 'vertical' }}
        />
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 'var(--sp-3)',
            flexWrap: 'wrap',
            marginTop: 'var(--sp-2)',
          }}
        >
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
            {CUSTOM_SMS_VARIABLES.map((v) => (
              <button
                key={v}
                type="button"
                className="btn btn-quiet"
                onClick={() => insert(`{{${v}}}`)}
                style={{ minHeight: 28, padding: '0 var(--sp-3)', fontSize: 'var(--text-xs)' }}
              >
                {`{{${v}}}`}
              </button>
            ))}
          </div>
          <span
            style={{
              fontSize: 'var(--text-xs)',
              color: tooLong ? 'var(--stamp-overdue)' : 'var(--ink-faint)',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {body.length} / {SMS_MAX_CHARS}
          </span>
        </div>
        {tooLong ? <span className="error">One message is {SMS_MAX_CHARS} characters at most.</span> : null}
      </div>

      {body.trim() ? (
        <div
          style={{
            border: '1px solid var(--rule)',
            borderRadius: 'var(--radius-md)',
            padding: 'var(--sp-3) var(--sp-4)',
            background: 'var(--paper)',
          }}
        >
          <h3 style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', fontWeight: 500 }}>
            How it reads for {first?.full_name ?? 'the first recipient'}
          </h3>
          <p style={{ marginTop: 'var(--sp-2)', whiteSpace: 'pre-wrap' }}>{preview}</p>
          <p style={{ marginTop: 'var(--sp-2)', fontSize: 'var(--text-xs)', color: 'var(--ink-faint)' }}>
            Preview only — each renter&rsquo;s own name and unit are filled in when the message is sent.
          </p>
        </div>
      ) : null}

      <div>
        <button
          type="button"
          className="btn btn-primary"
          disabled={!canSend}
          onClick={() => {
            setResult(null);
            setError(null);
            setConfirming(true);
          }}
        >
          <Icon icon="solar:plain-2-linear" width={20} /> Send message
        </button>
      </div>

      <Sheet
        open={confirming}
        title={count === null ? 'Send to every active renter?' : `Send to ${count} renter${count === 1 ? '' : 's'}?`}
        onClose={() => setConfirming(false)}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <p style={{ whiteSpace: 'pre-wrap' }}>{preview}</p>
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            SMS costs money and cannot be recalled once queued.
          </p>
          <div style={{ display: 'flex', gap: 'var(--sp-3)' }}>
            <button type="button" className="btn btn-primary" onClick={() => void send()} disabled={busy}>
              {busy ? 'Sending…' : 'Send now'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setConfirming(false)} disabled={busy}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>
    </div>
  );
}
