'use client';

/**
 * Shared notification pieces: the delivery stamp, the log table (used on
 * /notifications and on a renter's record) and the custom bulk-SMS composer.
 *
 * Everything shown is the backend's own `notification_log` row — this file
 * never decides whether a message was sent, only how the row reads.
 *
 * Phase 13: a bulk send carries one body per language. Every renter has a
 * `locale`, and the backend picks the body that matches it; when only one body
 * is written that body goes to everyone. The composer therefore has to show
 * two things a single-language composer did not: how many recipients read each
 * language, and what each of the two bodies will actually look like.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { LOCALE_LABELS, TableScroll, useT, type Locale, type Translator } from '@tms/ui';
import { Note, ProblemNote } from './FormBits';
import { Sheet } from './Sheet';
import {
  ApiError,
  CUSTOM_SMS_VARIABLES,
  SMS_MAX_CHARS,
  creditsAreLow,
  notificationsApi,
  orgApi,
  rentersApi,
  resolveVariables,
  toApiError,
  type ByLanguage,
  type CustomSendResult,
  type NotificationLogEntry,
  type RecipientsPreview,
  type RenterSummary,
  type SmsCredits,
} from '../lib/api';
import { fmtDateTime } from '../lib/format';
import { useMe } from '../lib/auth';

/** The kinds the log can show, as dictionary keys (the API sends the id). */
const KIND_KEYS: Record<string, string> = {
  reminder_7d: 'msg.kind.reminder_7d',
  reminder_due: 'msg.kind.reminder_due',
  overdue_daily: 'msg.kind.overdue_daily',
  thank_you: 'msg.kind.thank_you',
  unsigned_reminder: 'msg.kind.unsigned_reminder',
  otp: 'msg.kind.otp',
  custom: 'msg.kind.custom',
  link_approved: 'msg.kind.link_approved',
  link_rejected: 'msg.kind.link_rejected',
};

/** Every kind the filter offers, in the order the log tab lists them. */
export const KIND_FILTER_IDS = Object.keys(KIND_KEYS);

export function kindLabelFor(t: Translator, kind: string | null | undefined): string {
  if (!kind) return '—';
  const key = KIND_KEYS[kind];
  return key ? t(key) : String(kind).replace(/_/g, ' ');
}

/** Sent is stamped, queued is pencilled, failed is stamped red (SPEC §2.0). */
export function DeliveryStamp({ status }: { status: string }) {
  const t = useT();
  if (status === 'sent') return <span className="stamp stamp-paid">{t('msg.status.sent')}</span>;
  if (status === 'failed') return <span className="stamp stamp-overdue">{t('msg.status.failed')}</span>;
  if (status === 'sending') return <span className="pencil">{t('msg.status.sending')}</span>;
  // Held is not failed: the message is intact and leaves on the next top-up,
  // so it is pencilled like a queued row rather than stamped like a loss.
  if (status === 'held_no_credit') return <span className="pencil">{t('msg.status.held')}</span>;
  return <span className="pencil">{t('msg.status.queued')}</span>;
}

/* ------------------------------ SMS credits ------------------------------ */

/**
 * The org's prepaid SMS balance (API.md Phase 14, `GET /org/sms-credits`).
 *
 * Read-only everywhere in this app: only the platform admin tops an org up, so
 * nothing here offers a purchase. A 404 (or any read failure) leaves `credits`
 * null and every credits element simply does not render — the screens below
 * are the Phase 13 screens until the endpoint answers.
 */
export function useSmsCredits(): { credits: SmsCredits | null; reload: () => void } {
  const [credits, setCredits] = useState<SmsCredits | null>(null);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    const ac = new AbortController();
    orgApi
      .smsCredits(ac.signal)
      .then(setCredits)
      .catch(() => setCredits(null));
    return () => ac.abort();
  }, [nonce]);

  return { credits, reload: useCallback(() => setNonce((n) => n + 1), []) };
}

/** The band shown when the balance is at or under the platform's watermark. */
function LowCreditWarning({ balance }: { balance: number }) {
  const t = useT();
  return (
    <p
      role="status"
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 'var(--sp-2)',
        margin: 0,
        color: 'var(--stamp-overdue)',
        fontSize: 'var(--text-sm)',
        border: '1px solid var(--stamp-overdue)',
        borderRadius: 'var(--radius-sm)',
        padding: 'var(--sp-2) var(--sp-3)',
      }}
    >
      <Icon icon="solar:danger-triangle-linear" width={18} style={{ flexShrink: 0, marginTop: 2 }} />
      <span>
        <strong style={{ fontWeight: 600 }}>{t('credits.low.title', { balance })}</strong>{' '}
        {t('credits.low.body')}
      </span>
    </p>
  );
}

/**
 * The Messages header card: what is left, what is held, and — when the balance
 * is low — who to ask. There is no self-serve top-up, by design.
 */
export function SmsCreditsCard({ credits }: { credits: SmsCredits | null }) {
  const t = useT();
  if (!credits) return null;
  const low = creditsAreLow(credits);
  const held = credits.held_count ?? 0;
  return (
    <section
      aria-label={t('credits.title')}
      className="sheet"
      style={{
        padding: 'var(--sp-4) var(--sp-5)',
        marginBottom: 'var(--sp-5)',
        display: 'grid',
        gap: 'var(--sp-3)',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          justifyContent: 'space-between',
          gap: 'var(--sp-4)',
          flexWrap: 'wrap',
        }}
      >
        <span
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--sp-2)',
            color: 'var(--ink-soft)',
            fontSize: 'var(--text-sm)',
          }}
        >
          <Icon icon="solar:wallet-money-linear" width={18} /> {t('credits.title')}
        </span>
        <span
          className="amount"
          style={{ fontSize: 'var(--text-2xl)', color: low ? 'var(--stamp-overdue)' : 'var(--ink)' }}
        >
          {credits.balance}
        </span>
      </div>

      <p style={{ margin: 0, color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
        {t.n('credits.balance_sub', credits.balance)}
      </p>

      {held > 0 ? (
        <p style={{ margin: 0, fontSize: 'var(--text-sm)' }}>
          <span className="pencil">{t.n('credits.held', held)}</span>{' '}
          <span style={{ color: 'var(--ink-soft)' }}>{t('credits.held_note')}</span>
        </p>
      ) : null}

      {low ? <LowCreditWarning balance={credits.balance} /> : null}
    </section>
  );
}

/**
 * The dashboard's one-line version: a persistent strip, not a card, shown only
 * when there is something to act on (low balance or held messages).
 */
export function SmsCreditsBanner() {
  const t = useT();
  const { credits } = useSmsCredits();
  if (!credits) return null;
  const low = creditsAreLow(credits);
  const held = credits.held_count ?? 0;
  if (!low && held === 0) return null;
  return (
    <p
      role="status"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-2)',
        flexWrap: 'wrap',
        margin: 'var(--sp-4) 0 0',
        padding: 'var(--sp-2) var(--sp-3)',
        border: `1px solid ${low ? 'var(--stamp-overdue)' : 'var(--rule-strong)'}`,
        borderRadius: 'var(--radius-sm)',
        fontSize: 'var(--text-sm)',
        color: low ? 'var(--stamp-overdue)' : 'var(--ink)',
      }}
    >
      <Icon icon="solar:wallet-money-linear" width={18} style={{ flexShrink: 0 }} />
      <span>
        {low ? t('credits.banner.low', { balance: credits.balance }) : null}
        {low && held > 0 ? ' ' : null}
        {held > 0 ? t.n('credits.banner.held', held) : null}
      </span>
      <Link href="/notifications" style={{ color: 'inherit' }}>
        {t('credits.banner.link')}
      </Link>
    </p>
  );
}

/** The body, clamped to one line until the reader asks for the rest. */
function Body({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const t = useT();
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
        {open ? t('msg.less') : t('common.more')}
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
  emptyText,
  showRenter = true,
  onRetry,
  retryingId,
}: NotificationLogTableProps) {
  const t = useT();
  // The language column only earns its width once the backend is stamping it,
  // so it appears when at least one loaded row carries one.
  const showLanguage = (items ?? []).some((n) => n.language === 'sw' || n.language === 'en');
  const cols = 5 + (showRenter ? 1 : 0) + (showLanguage ? 1 : 0) + (onRetry ? 1 : 0);
  return (
    <TableScroll label={t('msg.log')}>
    <table className="ledger">
      <thead>
        <tr>
          <th>{t('msg.col.time')}</th>
          {showRenter ? <th>{t('common.renter')}</th> : null}
          <th>{t('msg.col.kind')}</th>
          <th>{t('msg.col.message')}</th>
          {showLanguage ? <th>{t('common.language')}</th> : null}
          <th>{t('common.status')}</th>
          <th className="num">{t('msg.col.attempts')}</th>
          {onRetry ? <th className="num" /> : null}
        </tr>
      </thead>
      <tbody>
        {items === null ? (
          <tr>
            <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
              {t('common.loading')}
            </td>
          </tr>
        ) : items.length === 0 ? (
          <tr>
            <td colSpan={cols} style={{ color: 'var(--ink-soft)' }}>
              {emptyText ?? t('msg.log.empty')}
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
              <td style={{ fontSize: 'var(--text-sm)' }}>{kindLabelFor(t, n.kind)}</td>
              <td style={{ maxWidth: 420 }}>
                <Body text={n.body ?? ''} />
              </td>
              {showLanguage ? (
                <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', whiteSpace: 'nowrap' }}>
                  {n.language === 'sw' || n.language === 'en' ? LOCALE_LABELS[n.language] : '—'}
                </td>
              ) : null}
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
                      {retryingId === n.id ? t('msg.retrying') : t('common.retry')}
                    </button>
                  ) : null}
                </td>
              ) : null}
            </tr>
          ))
        )}
      </tbody>
    </table>
    </TableScroll>
  );
}

/* --------------------------- custom bulk SMS ----------------------------- */

/**
 * How many SMS one body costs. GSM-7 fits 160 characters in a single message
 * and 153 per part once it is split; anything outside that alphabet forces
 * UCS-2, which is 70 / 67. Swahili and English both sit inside GSM-7, so this
 * only bites when a landlord pastes a curly quote or an emoji.
 */
const GSM7 =
  "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?" +
  '¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà';
/** These cost two GSM-7 characters each (escape + character). */
const GSM7_EXTENDED = '^{}\\[~]|€';

export function smsSegments(body: string): { chars: number; segments: number; unicode: boolean } {
  const unicode = [...body].some((c) => !GSM7.includes(c) && !GSM7_EXTENDED.includes(c));
  const chars = unicode
    ? [...body].length
    : [...body].reduce((n, c) => n + (GSM7_EXTENDED.includes(c) ? 2 : 1), 0);
  const single = unicode ? 70 : 160;
  const multi = unicode ? 67 : 153;
  const segments = chars === 0 ? 0 : chars <= single ? 1 : Math.ceil(chars / multi);
  return { chars, segments, unicode };
}

/** A renter is "active" for `all_active` when the backend says so; this list
 * is only for picking names, so it shows everyone the org knows. */
function RenterPicker({
  selected,
  onToggle,
}: {
  selected: string[];
  onToggle: (id: string) => void;
}) {
  const t = useT();
  const [q, setQ] = useState('');
  const [items, setItems] = useState<RenterSummary[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    const timer = window.setTimeout(() => {
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
      window.clearTimeout(timer);
      ac.abort();
    };
  }, [q]);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
      <div className="field">
        <label htmlFor="renter_q">{t('msg.picker.find')}</label>
        <input
          id="renter_q"
          className="input"
          value={q}
          placeholder={t('msg.picker.placeholder')}
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
          <p style={{ padding: 'var(--sp-3)', color: 'var(--ink-soft)' }}>{t('common.loading')}</p>
        ) : items.length === 0 ? (
          <p style={{ padding: 'var(--sp-3)', color: 'var(--ink-soft)' }}>{t('msg.picker.none')}</p>
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
                {r.locale ? LOCALE_LABELS[r.locale] : (r.units ?? []).map((u) => u.unit_name).join(', ')}
              </span>
            </label>
          ))
        )}
      </div>
    </div>
  );
}

const COMPOSE_LANGS: readonly Locale[] = ['sw', 'en'] as const;

export function SendMessageForm({ onSent }: { onSent?: () => void }) {
  const { org } = useMe();
  const t = useT();
  const [recipients, setRecipients] = useState<'all_active' | 'selected'>('all_active');
  const [selected, setSelected] = useState<string[]>([]);
  /** One body per language; at least one must be written. */
  const [bodies, setBodies] = useState<Record<Locale, string>>({ sw: '', en: '' });
  const [tab, setTab] = useState<Locale>('sw');
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [result, setResult] = useState<CustomSendResult | null>(null);
  const [audience, setAudience] = useState<RecipientsPreview | null>(null);
  const [samples, setSamples] = useState<Partial<Record<Locale, RenterSummary>>>({});
  const areas = useRef<Partial<Record<Locale, HTMLTextAreaElement | null>>>({});

  const bodySw = bodies.sw.trim();
  const bodyEn = bodies.en.trim();
  const filled = COMPOSE_LANGS.filter((l) => bodies[l].trim().length > 0);
  const onlyOne = filled.length === 1 ? filled[0] : null;

  /* --- who this would reach, split by the language they read --------------- */
  const loadAudience = useCallback(
    (signal?: AbortSignal) => {
      if (recipients === 'selected' && selected.length === 0) {
        setAudience({ count: 0, by_language: { sw: 0, en: 0 } });
        return;
      }
      notificationsApi
        .recipientsPreview(
          { recipients, renter_user_ids: recipients === 'selected' ? selected : undefined },
          signal,
        )
        .then(setAudience)
        // Until the endpoint lands the counts are simply unknown; the composer
        // still works, it just cannot say how the audience splits.
        .catch(() => setAudience(null));
    },
    [recipients, selected],
  );

  useEffect(() => {
    const ac = new AbortController();
    setAudience(null);
    loadAudience(ac.signal);
    return () => ac.abort();
  }, [loadAudience]);

  /* --- one sample renter per language, to substitute the variables against -- */
  useEffect(() => {
    if (recipients === 'selected' && selected.length === 0) {
      setSamples({});
      return;
    }
    const ac = new AbortController();
    rentersApi
      .list({ limit: 200 }, ac.signal)
      .then((r) => {
        const all = r.items ?? [];
        const pool = recipients === 'selected' ? all.filter((x) => selected.includes(x.user_id)) : all;
        const next: Partial<Record<Locale, RenterSummary>> = {};
        for (const l of COMPOSE_LANGS) next[l] = pool.find((x) => x.locale === l) ?? pool[0];
        setSamples(next);
      })
      .catch(() => setSamples({}));
    return () => ac.abort();
  }, [recipients, selected]);

  const previewFor = useCallback(
    (lang: Locale) => {
      const who = samples[lang];
      const unit = who?.units?.[0];
      return resolveVariables(bodies[lang], {
        name: who?.full_name ?? t('msg.sample.name'),
        unit: unit?.unit_name ?? t('msg.sample.unit'),
        property: unit?.property_name ?? t('msg.sample.property'),
        org: org?.name ?? t('msg.sample.org'),
      });
    },
    [bodies, samples, org, t],
  );

  const tooLong = COMPOSE_LANGS.some((l) => bodies[l].length > SMS_MAX_CHARS);
  const canSend =
    filled.length > 0 && !tooLong && (recipients === 'all_active' || selected.length > 0);

  /**
   * What this send would cost, in credits: one credit per SMS segment per
   * recipient (API.md Phase 14). An estimate, and said as one — the backend
   * debits at send time against the bodies it actually renders, and a renter
   * whose number is missing is skipped without spending anything.
   */
  const needed = useMemo(() => {
    if (filled.length === 0) return null;
    const reach = audience?.count ?? (recipients === 'selected' ? selected.length : null);
    if (onlyOne) {
      if (reach === null) return null;
      return smsSegments(bodies[onlyOne].trim()).segments * reach;
    }
    if (!audience) return null;
    return COMPOSE_LANGS.reduce(
      (sum, l) => sum + smsSegments(bodies[l].trim()).segments * (audience.by_language?.[l] ?? 0),
      0,
    );
  }, [audience, bodies, filled.length, onlyOne, recipients, selected.length]);

  const insert = (lang: Locale, token: string) => {
    const el = areas.current[lang];
    const current = bodies[lang];
    const start = el?.selectionStart ?? current.length;
    const end = el?.selectionEnd ?? start;
    const next = current.slice(0, start) + token + current.slice(end);
    setBodies((b) => ({ ...b, [lang]: next }));
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
        // Only the languages that were actually written are sent; the backend
        // falls back to the other one for a renter whose language is missing.
        ...(bodySw ? { body_sw: bodySw } : {}),
        ...(bodyEn ? { body_en: bodyEn } : {}),
      });
      setResult(res);
      setConfirming(false);
      setBodies({ sw: '', en: '' });
      setSelected([]);
      onSent?.();
    } catch (e) {
      setError(toApiError(e));
      setConfirming(false);
    } finally {
      setBusy(false);
    }
  };

  const count = recipients === 'selected' ? selected.length : (audience?.count ?? null);

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 720 }}>
      <ProblemNote error={error} />
      {error?.status === 429 ? (
        <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{t('msg.rate_limited')}</p>
      ) : null}
      {/* 409 insufficient_sms_credits — nothing was queued, so this says the
          shortfall plainly and points at the one party who can fix it. */}
      {error?.code === 'insufficient_sms_credits' ? (
        <p style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>
          {t('credits.insufficient', {
            needed: Number(error.body.needed ?? needed ?? 0),
            balance: Number(error.body.balance ?? 0),
          })}{' '}
          <span style={{ color: 'var(--ink-soft)' }}>{t('credits.low.body')}</span>
        </p>
      ) : null}
      {result ? (
        <Note>
          {t.n('msg.result.queued', result.queued)}
          {result.by_language
            ? ` ${t('msg.result.by_language', {
                sw: result.by_language.sw ?? 0,
                en: result.by_language.en ?? 0,
              })}`
            : ''}
          {result.skipped ? ` ${t('msg.result.skipped', { count: result.skipped })}` : ''}{' '}
          {t('msg.result.tail')}
        </Note>
      ) : null}

      <fieldset style={{ border: 0, padding: 0, margin: 0, display: 'grid', gap: 'var(--sp-2)' }}>
        <legend style={{ fontWeight: 500, marginBottom: 'var(--sp-2)' }}>{t('msg.who.legend')}</legend>
        <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', minHeight: 'var(--touch-min)' }}>
          <input
            type="radio"
            name="recipients"
            checked={recipients === 'all_active'}
            onChange={() => setRecipients('all_active')}
            style={{ width: 18, height: 18 }}
          />
          {t('msg.who.all_active')}
        </label>
        <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', minHeight: 'var(--touch-min)' }}>
          <input
            type="radio"
            name="recipients"
            checked={recipients === 'selected'}
            onChange={() => setRecipients('selected')}
            style={{ width: 18, height: 18 }}
          />
          {t('msg.who.selected')}
          {selected.length ? ` (${selected.length})` : ''}
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

      <AudienceLine audience={audience} />

      {/* ------------------------- the two bodies ------------------------- */}
      <div>
        <div className="wrap-sm" role="tablist" aria-label={t('msg.body.tablist')} style={{ display: 'flex', gap: 'var(--sp-2)' }}>
          {COMPOSE_LANGS.map((l) => {
            const active = tab === l;
            const written = bodies[l].trim().length > 0;
            return (
              <button
                key={l}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => setTab(l)}
                aria-label={`${LOCALE_LABELS[l]} — ${written ? t('msg.body.written') : t('msg.body.empty')}`}
                style={{
                  minHeight: 'var(--touch-min)',
                  padding: '0 var(--sp-4)',
                  border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
                  borderRadius: 'var(--radius-md) var(--radius-md) 0 0',
                  borderBottom: 'none',
                  background: active ? 'var(--primary-soft)' : 'transparent',
                  color: 'var(--ink)',
                  fontWeight: active ? 600 : 400,
                  fontSize: 'var(--text-sm)',
                  cursor: 'pointer',
                }}
              >
                {LOCALE_LABELS[l]}
                {written ? (
                  <span aria-hidden style={{ marginLeft: 'var(--sp-2)', color: 'var(--stamp-paid)' }}>
                    •
                  </span>
                ) : null}
              </button>
            );
          })}
        </div>

        {COMPOSE_LANGS.map((l) =>
          tab === l ? (
            <BodyEditor
              key={l}
              lang={l}
              value={bodies[l]}
              onChange={(v) => setBodies((b) => ({ ...b, [l]: v }))}
              onInsert={(token) => insert(l, token)}
              registerRef={(el) => {
                areas.current[l] = el;
              }}
              preview={previewFor(l)}
              sampleName={samples[l]?.full_name ?? null}
              reach={audience ? audience.by_language[l] : null}
            />
          ) : null,
        )}

        {onlyOne ? (
          <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            <Icon icon="solar:info-circle-linear" width={16} style={{ verticalAlign: '-3px' }} />{' '}
            {t('msg.single_body', { language: LOCALE_LABELS[onlyOne] })}
          </p>
        ) : filled.length === 2 ? (
          <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            <Icon icon="solar:check-circle-linear" width={16} style={{ verticalAlign: '-3px' }} />{' '}
            {t('msg.both_bodies')}
          </p>
        ) : null}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-4)', flexWrap: 'wrap' }}>
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
          <Icon icon="solar:plain-2-linear" width={20} /> {t('msg.send')}
        </button>
        {canSend && needed !== null ? (
          <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
            {t.n('credits.needed', needed)}
          </span>
        ) : null}
      </div>

      <Sheet
        open={confirming}
        title={count === null ? t('msg.confirm.all') : t.n('msg.confirm.count', count)}
        onClose={() => setConfirming(false)}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          {COMPOSE_LANGS.filter((l) => bodies[l].trim()).map((l) => (
            <div key={l}>
              <h3 style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', fontWeight: 500 }}>
                {LOCALE_LABELS[l]}
                {audience ? ` · ${t.n('msg.reach', onlyOne ? audience.count : audience.by_language[l])}` : ''}
              </h3>
              <p style={{ marginTop: 'var(--sp-2)', whiteSpace: 'pre-wrap' }}>{previewFor(l)}</p>
            </div>
          ))}
          <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>
            {t('msg.confirm.warning')}
            {needed !== null ? ` ${t.n('credits.needed', needed)}` : ''}
          </p>
          <div style={{ display: 'flex', gap: 'var(--sp-3)' }}>
            <button type="button" className="btn btn-primary" onClick={() => void send()} disabled={busy}>
              {busy ? t('msg.sending') : t('msg.send_now')}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setConfirming(false)} disabled={busy}>
              {t('common.cancel')}
            </button>
          </div>
        </div>
      </Sheet>
    </div>
  );
}

/** "42 renters · 30 Kiswahili · 12 English", refreshed as the filters change. */
function AudienceLine({ audience }: { audience: RecipientsPreview | null }) {
  const t = useT();
  if (!audience) {
    return (
      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>{t('msg.audience.unknown')}</p>
    );
  }
  const by: ByLanguage = audience.by_language ?? { sw: 0, en: 0 };
  return (
    <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
      <strong style={{ fontWeight: 600 }}>{t.n('msg.audience.count', audience.count)}</strong>
      {' · '}
      {t('msg.audience.split', { sw: by.sw ?? 0, en: by.en ?? 0 })}
    </p>
  );
}

function BodyEditor({
  lang,
  value,
  onChange,
  onInsert,
  registerRef,
  preview,
  sampleName,
  reach,
}: {
  lang: Locale;
  value: string;
  onChange: (v: string) => void;
  onInsert: (token: string) => void;
  registerRef: (el: HTMLTextAreaElement | null) => void;
  preview: string;
  sampleName: string | null;
  reach: number | null;
}) {
  const t = useT();
  const tooLong = value.length > SMS_MAX_CHARS;
  const { chars, segments, unicode } = smsSegments(value);

  return (
    <div
      role="tabpanel"
      style={{
        border: '1px solid var(--rule)',
        borderRadius: '0 var(--radius-md) var(--radius-md) var(--radius-md)',
        padding: 'var(--sp-4)',
      }}
    >
      <div className={tooLong ? 'field invalid' : 'field'} style={{ margin: 0 }}>
        <label htmlFor={`custom_body_${lang}`}>
          {t('msg.body.label', { language: LOCALE_LABELS[lang] })}
          {reach !== null ? (
            <span style={{ fontWeight: 400, color: 'var(--ink-soft)' }}> · {t.n('msg.reach', reach)}</span>
          ) : null}
        </label>
        <textarea
          id={`custom_body_${lang}`}
          ref={registerRef}
          className="input"
          rows={4}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={t(lang === 'sw' ? 'msg.body.placeholder_sw' : 'msg.body.placeholder_en')}
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
                onClick={() => onInsert(`{{${v}}}`)}
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
            {value.length} / {SMS_MAX_CHARS} · {t.n('msg.segments', segments)}
            {unicode ? ` · ${t('msg.unicode')}` : ''}
            {chars !== value.length ? ` · ${t('msg.billed_chars', { count: chars })}` : ''}
          </span>
        </div>
        {tooLong ? <span className="error">{t('msg.too_long', { max: SMS_MAX_CHARS })}</span> : null}
      </div>

      {value.trim() ? (
        <div
          style={{
            marginTop: 'var(--sp-4)',
            borderTop: '1px solid var(--rule)',
            paddingTop: 'var(--sp-3)',
          }}
        >
          <h3 style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', fontWeight: 500 }}>
            {t('msg.preview.heading', { name: sampleName ?? t('msg.preview.first_recipient') })}
          </h3>
          <p style={{ marginTop: 'var(--sp-2)', whiteSpace: 'pre-wrap' }}>{preview}</p>
          <p style={{ marginTop: 'var(--sp-2)', fontSize: 'var(--text-xs)', color: 'var(--ink-faint)' }}>
            {t('msg.preview.note')}
          </p>
        </div>
      ) : null}
    </div>
  );
}
