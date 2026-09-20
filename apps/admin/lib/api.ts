import { API_ORIGIN } from './basePath';
/**
 * Thin fetch wrapper for the Go REST API — platform admin audience.
 *
 * Same rules the other two apps follow:
 *  - Base is `/api/v1` on the SAME ORIGIN, written as an absolute path so the
 *    Next.js `basePath` ('/admin') is NOT prepended. `basePath` only rewrites
 *    <Link>/router URLs, never `fetch()`.
 *  - `credentials: 'include'` on every call — the session is an httpOnly
 *    cookie (`tms_a` for platform admins). No tokens in JS.
 *  - Errors are RFC-7807 `application/problem+json`, thrown as ApiError.
 *  - No business logic here; the backend is the only source of truth.
 */

// Same origin by default (the dev proxy routes /api). NEXT_PUBLIC_API_URL
// (e.g. https://api.tms.kuzo.co.tz) makes every call cross-origin; the API
// then needs this app's origin in CORS_ALLOWED_ORIGINS and its cookies
// SameSite=None, and `credentials: 'include'` below carries them.
export const API_BASE = `${API_ORIGIN}/api/v1`;

/** Every call from this app carries the admin audience. */
export const AUDIENCE = 'admin';

export interface Problem {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  errors?: Record<string, string>;
}

export class ApiError extends Error {
  readonly status: number;
  readonly title: string;
  readonly detail: string;
  readonly errors: Record<string, string>;
  readonly body: Record<string, unknown>;

  constructor(status: number, problem: Problem) {
    const detail = problem.detail || problem.title || `Request failed (${status})`;
    super(detail);
    this.name = 'ApiError';
    this.status = status;
    this.title = problem.title || 'Error';
    this.detail = detail;
    this.errors = problem.errors || {};
    this.body = problem as Record<string, unknown>;
  }

  /** True when there is no valid admin session. */
  get isUnauthorized() {
    return this.status === 401;
  }

  /** Error codes ride in the problem `type` (DECISIONS.md). */
  get code(): string {
    const t = this.body.type;
    return typeof t === 'string' && t !== 'about:blank' ? t : '';
  }

  /**
   * The backend lane ships the Phase 7 admin endpoints in parallel; until they
   * land the router answers 404 for the whole path. Screens use this to say
   * "not deployed yet" instead of "not found".
   */
  get isMissingEndpoint(): boolean {
    return this.status === 404 && /no route matches/i.test(this.detail);
  }
}

/** Network failure (backend down) surfaces as status 0. */
function offlineError(cause: unknown): ApiError {
  return new ApiError(0, {
    title: 'Cannot reach the server',
    detail:
      cause instanceof Error && cause.message
        ? `Cannot reach the server. ${cause.message}`
        : 'Cannot reach the server. Check your connection and try again.',
  });
}

export interface RequestOptions {
  body?: unknown;
  /** Appended as a query string; null/undefined/'' entries are dropped. */
  query?: Record<string, string | number | boolean | null | undefined>;
  signal?: AbortSignal;
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = `${API_BASE}${path}`;
  if (!query) return url;
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === null || v === undefined || v === '') continue;
    qs.set(k, String(v));
  }
  const s = qs.toString();
  return s ? `${url}?${s}` : url;
}

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  let res: Response;
  try {
    res = await fetch(buildUrl(path, opts.query), {
      method,
      credentials: 'include',
      headers:
        opts.body === undefined
          ? { Accept: 'application/json' }
          : { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
    });
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause;
    throw offlineError(cause);
  }

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  let parsed: unknown = undefined;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = undefined;
    }
  }

  if (!res.ok) {
    const problem: Problem =
      parsed && typeof parsed === 'object'
        ? (parsed as Problem)
        : { title: res.statusText, detail: text.slice(0, 300) || res.statusText };
    throw new ApiError(res.status, { ...problem, status: res.status });
  }

  return parsed as T;
}

export const api = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>('POST', path, { ...opts, body }),
  patch: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>('PATCH', path, { ...opts, body }),
  put: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>('PUT', path, { ...opts, body }),
  del: <T>(path: string, opts?: RequestOptions) => request<T>('DELETE', path, opts),
};

/** Normalise any thrown value into an ApiError so screens can render it. */
export function toApiError(e: unknown): ApiError {
  return e instanceof ApiError ? e : new ApiError(0, { detail: String(e) });
}

function unwrap<T>(res: unknown, key: string): T {
  if (res && typeof res === 'object' && key in (res as Record<string, unknown>)) {
    return (res as Record<string, unknown>)[key] as T;
  }
  return res as T;
}

/* ------------------------------------------------------------------ */
/* Auth — API.md Phase 1 (admin audience)                              */
/* ------------------------------------------------------------------ */

export type UserKind = 'renter' | 'org_user' | 'platform_admin';

export interface User {
  id: string;
  kind: UserKind;
  phone: string | null;
  email: string | null;
  full_name: string;
  email_verified: boolean;
  status: string;
  created_at: string;
}

export interface Session {
  user: User;
  /** Always null for a platform admin — an admin belongs to no org. */
  org: { id: string; name: string; slug: string; role: string } | null;
}

export const authApi = {
  me: (signal?: AbortSignal) =>
    api.get<Session>('/auth/me', { query: { audience: AUDIENCE }, signal }),
  /** `{email,password}` sets `tms_a` when the user is a platform_admin. */
  login: (email: string, password: string) => api.post<Session>('/auth/login', { email, password }),
  logout: () => api.post<void>('/auth/logout', undefined, { query: { audience: AUDIENCE } }),
};

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md "Phase 7 Platform admin" exactly.            */
/* ------------------------------------------------------------------ */

export type OrgStatus = 'active' | 'suspended' | string;

export interface AdminOrgCounts {
  properties: number;
  units: number;
  renters: number;
  active_contracts: number;
}

export interface AdminOrgSms {
  sent_30d: number;
  failed_30d: number;
}

/**
 * Phase 14 — the SMS credit wallet an org spends at send time. `balance` is
 * whole credits (one per 160-char GSM segment, 70 for UCS-2); `low_watermark`
 * is the line under which the landlord sees a low-balance banner.
 */
export interface AdminOrgCredits {
  balance: number;
  low_watermark: number;
}

export interface AdminOrgOwner {
  name: string;
  email: string;
}

export interface AdminOrgSummary {
  id: string;
  name: string;
  slug: string;
  status: OrgStatus;
  owner: AdminOrgOwner | null;
  counts: AdminOrgCounts | null;
  sms: AdminOrgSms | null;
  /**
   * Phase 14. Present only when the list/detail payload carries it — the
   * Credits column on the directory renders itself away when it does not,
   * rather than firing one request per row (see orgs/page.tsx).
   */
  credits?: AdminOrgCredits | null;
  created_at: string;
  /** Set once an org has been suspended; shown in the detail header. */
  suspended_at?: string | null;
  suspended_reason?: string | null;
}

export interface AdminOrgMember {
  id?: string;
  user_id?: string;
  email: string;
  full_name: string;
  role: string;
  status: string;
  created_at?: string;
}

/**
 * `GET /admin/orgs/{id}` — "detail incl. settings summary, members". The
 * settings block is a free-form summary; it is rendered generically so a
 * backend addition shows up without a frontend change.
 */
export interface AdminOrgDetail {
  org: AdminOrgSummary;
  members: AdminOrgMember[];
  settings: Record<string, unknown> | null;
}

export interface AdminMetrics {
  orgs: { total: number; active: number; suspended: number };
  renters: { total: number };
  units: { total: number; occupied: number };
  contracts: { active: number };
  sms: {
    sent_24h: number;
    failed_24h: number;
    queued: number;
    /** Phase 14 — absent until the credits backend lands; the tiles say so. */
    credits_used_today?: number;
    orgs_under_watermark?: number;
    held_total?: number;
  };
  payments: { recorded_30d: number; amount_30d: number };
  db: { ok: boolean };
  redis: { ok: boolean };
  minio: { ok: boolean };
}

/** A cross-org audit row: the org-scoped shape plus which org it happened in. */
export interface AdminAuditEntry {
  id: string;
  org_id: string | null;
  org_name?: string | null;
  org_slug?: string | null;
  actor_user_id: string | null;
  actor_name: string | null;
  action: string;
  entity_type: string;
  entity_id: string | null;
  before: unknown;
  after: unknown;
  ip: string | null;
  at: string;
}

/** One row of `GET /admin/jobs` — name plus whatever the last run recorded. */
export interface AdminJob {
  /** Payloads name the job either way; `jobKey()` reads whichever arrived. */
  key?: string;
  name?: string;
  /** The audit action the run writes — how to find it in the audit search. */
  action?: string | null;
  description?: string | null;
  /** False for a job with no manual trigger. */
  runnable?: boolean;
  last_run_at?: string | null;
  last_status?: string | null;
  last_result?: unknown;
}

export function jobKey(job: AdminJob): string {
  return job.key ?? job.name ?? '';
}

/* ------------------------------------------------------------------ */
/* Phase 14 — SMS credits & platform templates (API.md Part 2)          */
/* ------------------------------------------------------------------ */

export type SmsLedgerReason = 'topup' | 'adjust' | 'debit' | 'refund' | string;

/** One append-only row of `sms_credit_ledger`. */
export interface AdminSmsLedgerEntry {
  id?: string;
  delta: number;
  balance_after: number;
  reason: SmsLedgerReason;
  note: string | null;
  admin_name: string | null;
  notification_id?: string | null;
  created_at: string;
}

/** `GET /admin/orgs/{id}/sms`. The ledger is capped at the newest 100 rows. */
export interface AdminOrgSmsCredits {
  balance: number;
  low_watermark: number;
  used_30d: number;
  held_count: number;
  ledger: AdminSmsLedgerEntry[];
  /** Only sent if the backend ever pages the ledger; the UI honours it. */
  next_cursor?: string | null;
}

/** Every notification kind the platform can word. */
export interface AdminTemplate {
  kind: string;
  sw: string;
  en: string;
  variables: string[];
  locked: boolean;
  version: number;
  updated_by: string | null;
  updated_at: string | null;
}

export interface AdminTemplateVersion {
  version: number;
  sw: string;
  en: string;
  admin_name: string | null;
  created_at: string;
}

/** `PUT /admin/templates/{kind}` — the saved row plus its segment cost. */
export interface AdminTemplateSaveResult {
  item?: AdminTemplate;
  template?: AdminTemplate;
  segments?: { sw: number; en: number };
  warnings?: string[];
}

export interface AdminTemplatePreview {
  body: string;
  segments: number;
  encoding?: 'gsm' | 'ucs2' | string;
}

/** Both `{item}` and `{template}` are accepted; the wire has used each. */
export function unwrapTemplate(res: AdminTemplateSaveResult | AdminTemplate): AdminTemplate {
  const o = res as Record<string, unknown>;
  return (o.item ?? o.template ?? res) as AdminTemplate;
}

/* ------------------------------------------------------------------ */
/* Phase 7 endpoint helpers (audience admin)                            */
/* ------------------------------------------------------------------ */

export const adminApi = {
  metrics: (signal?: AbortSignal) => api.get<AdminMetrics>('/admin/metrics', { signal }),

  orgs: (
    query: { q?: string; status?: string; cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ) =>
    api.get<{ items: AdminOrgSummary[]; next_cursor?: string | null }>('/admin/orgs', {
      query,
      signal,
    }),

  org: (id: string, signal?: AbortSignal) =>
    api.get<AdminOrgDetail | AdminOrgSummary>(`/admin/orgs/${id}`, { signal }),

  suspend: (id: string, reason: string) =>
    api.post<{ org: AdminOrgSummary } | AdminOrgSummary>(`/admin/orgs/${id}/suspend`, { reason }),

  activate: (id: string) =>
    api.post<{ org: AdminOrgSummary } | AdminOrgSummary>(`/admin/orgs/${id}/activate`),

  auditLog: (
    query: {
      org_id?: string;
      actor?: string;
      entity_type?: string;
      entity_id?: string;
      q?: string;
      from?: string;
      to?: string;
      cursor?: string;
      limit?: number;
    } = {},
    signal?: AbortSignal,
  ) =>
    api.get<{ items: AdminAuditEntry[]; next_cursor?: string | null }>('/admin/audit-log', {
      query,
      signal,
    }),

  jobs: (signal?: AbortSignal) => api.get<{ items: AdminJob[] }>('/admin/jobs', { signal }),

  /** `{expiring, ended, units_freed}` (API.md Phase 4). */
  runContractLifecycle: () => api.post<Record<string, unknown>>('/admin/jobs/contract-lifecycle'),
  /** `{flipped}` (API.md Phase 5). */
  runOverdue: () => api.post<Record<string, unknown>>('/admin/jobs/overdue'),
  /** `{queued:{kind:n}}`; `force_hour` ignores each org's send hour (Phase 6). */
  runNotifications: (body: { date?: string; force_hour?: boolean } = {}) =>
    api.post<Record<string, unknown>>('/admin/jobs/notifications', body),

  /* ---------------- Phase 14 — SMS credits ---------------- */

  orgSms: (id: string, query: { cursor?: string } = {}, signal?: AbortSignal) =>
    api.get<AdminOrgSmsCredits>(`/admin/orgs/${id}/sms`, { query, signal }),

  /** `{credits>0, note}` → `{balance}`; also releases held rows in queue order. */
  smsTopup: (id: string, body: { credits: number; note: string }) =>
    api.post<{ balance: number }>(`/admin/orgs/${id}/sms/topup`, body),

  /** `{delta≠0, note}` → `{balance}`; 400 when it would take the org below 0. */
  smsAdjust: (id: string, body: { delta: number; note: string }) =>
    api.post<{ balance: number }>(`/admin/orgs/${id}/sms/adjust`, body),

  smsWatermark: (id: string, low_watermark: number) =>
    api.patch<{ low_watermark: number }>(`/admin/orgs/${id}/sms`, { low_watermark }),

  /* ---------------- Phase 14 — platform templates ---------------- */

  templates: (signal?: AbortSignal) =>
    api.get<{ items: AdminTemplate[] }>('/admin/templates', { signal }),

  saveTemplate: (kind: string, body: { sw: string; en: string }) =>
    api.put<AdminTemplateSaveResult>(`/admin/templates/${kind}`, body),

  lockTemplate: (kind: string, locked: boolean) =>
    api.patch<AdminTemplateSaveResult>(`/admin/templates/${kind}`, { locked }),

  previewTemplate: (kind: string, body: { language: 'sw' | 'en'; sample?: boolean }) =>
    api.post<AdminTemplatePreview>(`/admin/templates/${kind}/preview`, body),

  templateVersions: (kind: string, signal?: AbortSignal) =>
    api.get<{ items: AdminTemplateVersion[] }>(`/admin/templates/${kind}/versions`, { signal }),

  revertTemplate: (kind: string, version: number) =>
    api.post<AdminTemplateSaveResult>(`/admin/templates/${kind}/revert`, { version }),
};

export const unwrapAdminOrg = (res: { org: AdminOrgSummary } | AdminOrgSummary) =>
  unwrap<AdminOrgSummary>(res, 'org');

/**
 * `GET /admin/orgs/{id}` may answer `{org, members, settings}` or a flat org
 * carrying `members`/`settings`; normalise both into the triple.
 */
export function unwrapOrgDetail(res: AdminOrgDetail | AdminOrgSummary): AdminOrgDetail {
  const o = res as unknown as Record<string, unknown>;
  const org = (o.org ?? res) as AdminOrgSummary;
  const flat = org as unknown as Record<string, unknown>;
  const members = (o.members ?? flat.members ?? []) as AdminOrgMember[];
  const settings = (o.settings ?? flat.settings ?? null) as Record<string, unknown> | null;
  return { org, members: Array.isArray(members) ? members : [], settings };
}
