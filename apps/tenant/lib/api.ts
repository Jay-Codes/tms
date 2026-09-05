/**
 * Thin fetch wrapper for the Go REST API.
 *
 * Rules that other frontend lanes should copy verbatim:
 *  - Base is `/api/v1` on the SAME ORIGIN, written as an absolute path so the
 *    Next.js `basePath` ('/tenant') is NOT prepended. `basePath` only rewrites
 *    <Link>/router URLs, never `fetch()`, so this stays correct.
 *  - `credentials: 'include'` on every call — sessions are httpOnly cookies
 *    (`tms_o` for org users). No tokens in JS, no Authorization header.
 *  - Errors are RFC-7807 `application/problem+json` and are thrown as ApiError.
 *  - No business logic lives here; the backend is the only source of truth.
 */

export const API_BASE = '/api/v1';

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

  constructor(status: number, problem: Problem) {
    const detail = problem.detail || problem.title || `Request failed (${status})`;
    super(detail);
    this.name = 'ApiError';
    this.status = status;
    this.title = problem.title || 'Error';
    this.detail = detail;
    this.errors = problem.errors || {};
  }

  /** True when the caller has no valid session for this audience. */
  get isUnauthorized() {
    return this.status === 401;
  }
}

/** Network/CORS failure (backend down) surfaces as status 0. */
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
  /** Parsed and sent as JSON. */
  body?: unknown;
  /** Appended as a query string; null/undefined/'' entries are dropped. */
  query?: Record<string, string | number | null | undefined>;
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
      headers: opts.body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' },
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

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 1 exactly.                             */
/* ------------------------------------------------------------------ */

export type UserKind = 'renter' | 'org_user' | 'platform_admin';
export type OrgRole = 'org_owner' | 'org_manager';

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

export interface OrgRef {
  id: string;
  name: string;
  slug: string;
  role: OrgRole;
}

export interface OrgSettings {
  auto_approve_links: boolean;
  due_day: number | null;
  grace_days: number;
  reminder_offsets_days: number[];
  unsigned_reminder_days: number;
  sms_language: 'sw' | 'en';
}

export interface Org {
  id: string;
  name: string;
  slug: string;
  status: string;
  settings: OrgSettings;
  created_at: string;
}

export interface Member {
  id: string;
  user_id: string;
  email: string;
  full_name: string;
  role: OrgRole;
  status: string;
  created_at: string;
}

export interface AuditEntry {
  id: string;
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

export interface Session {
  user: User;
  org: OrgRef | null;
}

/* ------------------------------------------------------------------ */
/* Endpoint helpers                                                     */
/* ------------------------------------------------------------------ */

export const authApi = {
  me: (signal?: AbortSignal) =>
    api.get<Session>('/auth/me', { query: { audience: 'org' }, signal }),
  login: (email: string, password: string) =>
    api.post<Session>('/auth/login', { email, password }),
  logout: () => api.post<void>('/auth/logout', undefined, { query: { audience: 'org' } }),
  signupOrg: (body: {
    org_name: string;
    owner_name: string;
    email: string;
    phone: string;
    password: string;
  }) => api.post<{ org: Org; user: User }>('/orgs', body),
  verifyEmail: (token: string) => api.post<{ verified: boolean }>('/auth/verify-email', { token }),
  resendVerification: () => api.post<void>('/auth/verify-email/resend'),
  acceptInvite: (token: string, password: string) =>
    api.post<Session>('/auth/invite/accept', { token, password }),
};

export const orgApi = {
  get: (signal?: AbortSignal) => api.get<Org>('/org', { signal }),
  update: (body: { name?: string; settings?: Partial<OrgSettings> }) =>
    api.patch<{ org: Org } | Org>('/org', body),
  members: (signal?: AbortSignal) => api.get<{ items: Member[] }>('/org/members', { signal }),
  invite: (body: { email: string; full_name: string; role: OrgRole }) =>
    api.post<{ member: Member; invite?: unknown }>('/org/members', body),
  removeMember: (id: string) => api.del<void>(`/org/members/${id}`),
  auditLog: (
    query: {
      entity_type?: string;
      actor?: string;
      from?: string;
      to?: string;
      cursor?: string;
      limit?: number;
    },
    signal?: AbortSignal,
  ) => api.get<{ items: AuditEntry[]; next_cursor: string | null }>('/audit-log', { query, signal }),
};

/** `PATCH /org` may answer `{org}` or a bare org; normalise. */
export function unwrapOrg(res: { org: Org } | Org): Org {
  return 'org' in res && res.org ? res.org : (res as Org);
}
