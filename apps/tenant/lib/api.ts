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

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 2 exactly.                             */
/* ------------------------------------------------------------------ */

export type UnitStatus = 'vacant' | 'occupied' | 'unlisted' | 'maintenance';
/** Statuses a landlord may set by hand; `occupied` is derived from contracts. */
export type UnitStatusOverride = Exclude<UnitStatus, 'occupied'>;

export interface PaymentPeriod {
  id: string;
  label: string;
  days: number;
  is_recommended: boolean;
  sort_order: number;
  active: boolean;
  created_at: string;
}

export interface UnitCounts {
  total: number;
  vacant: number;
  occupied: number;
  maintenance: number;
  unlisted: number;
}

export interface Property {
  id: string;
  name: string;
  location_text: string | null;
  lat: number | null;
  lng: number | null;
  notes: string | null;
  unit_counts: UnitCounts;
  created_at: string;
}

export interface Price {
  id: string;
  amount: number;
  currency: string;
  period_days: number;
  effective_from: string;
  created_at?: string;
  created_by_name?: string | null;
}

export interface Unit {
  id: string;
  org_id: string;
  property_id: string;
  property_name: string;
  name: string;
  unit_code: string;
  status: UnitStatus;
  status_override: boolean;
  allowed_period_ids: string[] | null;
  current_price: Price | null;
  scan_url: string;
  vacant_since: string | null;
  created_at: string;
  updated_at: string;
}

export interface QrSheetItem {
  unit_id: string;
  unit_name: string;
  unit_code: string;
  scan_url: string;
  png_url: string;
}

export interface UnitQr {
  unit_code: string;
  scan_url: string;
  png_url: string;
}

export interface PublicBranding {
  org: { id: string; name: string; slug: string };
  display_name: string;
  logo_url: string | null;
  theme: { primary_color: string; font_id: string };
}

export interface PriceInput {
  amount: number;
  period_days: number;
}

/* ------------------------------------------------------------------ */
/* Phase 2 endpoint helpers                                             */
/* ------------------------------------------------------------------ */

export const periodsApi = {
  list: (includeInactive = false, signal?: AbortSignal) =>
    api.get<{ items: PaymentPeriod[] }>('/org/payment-periods', {
      query: includeInactive ? { include_inactive: 'true' } : undefined,
      signal,
    }),
  create: (body: { label: string; days: number }) =>
    api.post<{ period: PaymentPeriod } | PaymentPeriod>('/org/payment-periods', body),
  update: (
    id: string,
    body: { label?: string; days?: number; sort_order?: number; active?: boolean },
  ) => api.patch<{ period: PaymentPeriod } | PaymentPeriod>(`/org/payment-periods/${id}`, body),
  deactivate: (id: string) => api.del<void>(`/org/payment-periods/${id}`),
  restoreRecommended: () =>
    api.post<{ items: PaymentPeriod[] }>('/org/payment-periods/restore-recommended'),
};

export const propertiesApi = {
  list: (query: { cursor?: string; limit?: number } = {}, signal?: AbortSignal) =>
    api.get<{ items: Property[]; next_cursor: string | null }>('/properties', { query, signal }),
  get: (id: string, signal?: AbortSignal) =>
    api.get<{ property: Property } | Property>(`/properties/${id}`, { signal }),
  create: (body: {
    name: string;
    location_text?: string;
    lat?: number;
    lng?: number;
    notes?: string;
  }) => api.post<{ property: Property } | Property>('/properties', body),
  update: (
    id: string,
    body: {
      name?: string;
      location_text?: string;
      lat?: number | null;
      lng?: number | null;
      notes?: string | null;
    },
  ) => api.patch<{ property: Property } | Property>(`/properties/${id}`, body),
  remove: (id: string) => api.del<void>(`/properties/${id}`),
  units: (id: string, signal?: AbortSignal) =>
    api.get<{ items: Unit[] }>(`/properties/${id}/units`, { signal }),
  addUnit: (
    id: string,
    body: { name: string; price?: PriceInput; allowed_period_ids?: string[] | null },
  ) => api.post<{ unit: Unit } | Unit>(`/properties/${id}/units`, body),
  addUnitsBulk: (
    id: string,
    body: { names: string[]; price?: PriceInput; allowed_period_ids?: string[] | null },
  ) =>
    api.post<{ items: Unit[] }>(`/properties/${id}/units/bulk`, body),
  qrSheet: (id: string, signal?: AbortSignal) =>
    api.get<{ items: QrSheetItem[] }>(`/properties/${id}/qr-sheet`, { signal }),
};

export const unitsApi = {
  list: (
    query: { status?: string; property_id?: string; q?: string; cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ) => api.get<{ items: Unit[]; next_cursor: string | null }>('/units', { query, signal }),
  get: (id: string, signal?: AbortSignal) => api.get<{ unit: Unit } | Unit>(`/units/${id}`, { signal }),
  update: (
    id: string,
    body: { name?: string; status?: UnitStatusOverride; allowed_period_ids?: string[] | null },
  ) => api.patch<{ unit: Unit } | Unit>(`/units/${id}`, body),
  remove: (id: string) => api.del<void>(`/units/${id}`),
  qr: (id: string) => api.post<UnitQr>(`/units/${id}/qr`),
  prices: (id: string, signal?: AbortSignal) =>
    api.get<{ items: Price[] }>(`/units/${id}/prices`, { signal }),
  addPrice: (id: string, body: { amount: number; period_days: number; effective_from?: string }) =>
    api.post<{ price: Price } | Price>(`/units/${id}/prices`, body),
  bulkPrice: (body: {
    unit_ids: string[];
    mode: 'percent' | 'set';
    value: number;
    period_days?: number;
    effective_from?: string;
  }) => api.post<{ items: Price[] }>('/units/bulk-price', body),
};

export const publicApi = {
  // slug and unit_code are typed by a human (or read off a sticker), so they
  // are escaped: an unencoded '/' or '?' would rewrite the request path.
  branding: (slug: string, signal?: AbortSignal) =>
    api.get<PublicBranding>(`/public/orgs/${encodeURIComponent(slug)}/branding`, { signal }),
  unit: (unitCode: string, signal?: AbortSignal) =>
    api.get<unknown>(`/public/units/${encodeURIComponent(unitCode)}`, { signal }),
};

/* ------------------------------------------------------------------ */
/* Envelope helpers — the API answers `{thing}`; tolerate a bare body.  */
/* ------------------------------------------------------------------ */

function unwrap<T>(res: unknown, key: string): T {
  if (res && typeof res === 'object' && key in (res as Record<string, unknown>)) {
    return (res as Record<string, unknown>)[key] as T;
  }
  return res as T;
}

export const unwrapProperty = (res: { property: Property } | Property) =>
  unwrap<Property>(res, 'property');
export const unwrapUnit = (res: { unit: Unit } | Unit) => unwrap<Unit>(res, 'unit');
export const unwrapPrice = (res: { price: Price } | Price) => unwrap<Price>(res, 'price');
export const unwrapPeriod = (res: { period: PaymentPeriod } | PaymentPeriod) =>
  unwrap<PaymentPeriod>(res, 'period');

/** Normalise any thrown value into an ApiError so screens can render it. */
export function toApiError(e: unknown): ApiError {
  return e instanceof ApiError ? e : new ApiError(0, { detail: String(e) });
}

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 3 (landlord side) exactly.             */
/* ------------------------------------------------------------------ */

export type KycStatus = 'none' | 'submitted' | 'verified';
export type LinkRequestStatus = 'pending' | 'approved' | 'rejected' | 'cancelled';

export interface LinkRequestUnit {
  id: string;
  name: string;
  property_name: string;
  /** Present on some payloads; used only to deep-link the unit screen. */
  property_id?: string | null;
}

export interface LinkRequestRenter {
  user_id: string;
  full_name: string;
  phone: string | null;
  kyc_status: KycStatus;
}

export interface LinkRequestPeriod {
  id?: string;
  label: string;
  days: number;
  /** Prorated amount for this period, integer TZS. */
  amount: number | null;
}

export interface SchedulePreview {
  count: number;
  first_due: string;
  amount_first: number;
  amount_last: number;
  total: number;
}

export interface LinkRequest {
  id: string;
  unit: LinkRequestUnit;
  renter: LinkRequestRenter;
  payment_period: LinkRequestPeriod;
  term_days: number;
  start_date: string;
  end_date: string;
  status: LinkRequestStatus;
  created_at: string;
  decided_at: string | null;
  rejection_reason: string | null;
  schedule_preview?: SchedulePreview | null;
}

/** Renter KYC as the landlord may see it — NIDA is masked server-side. */
export interface RenterProfile {
  full_name: string;
  nida_masked: string | null;
  next_of_kin_name: string | null;
  next_of_kin_phone: string | null;
  email: string | null;
  kyc_status: KycStatus;
  kyc_doc_uploaded?: boolean;
  /** Some payloads name the flag differently; both are tolerated in the UI. */
  kyc_doc_available?: boolean;
  updated_at?: string | null;
}

export interface LinkRequestDetail {
  request: LinkRequest;
  renter_profile: RenterProfile | null;
}

export interface RenterUnitLink {
  unit_id: string;
  unit_name: string;
  property_name: string;
  link_status: string;
}

export interface RenterSummary {
  user_id: string;
  full_name: string;
  phone: string | null;
  email: string | null;
  kyc_status: KycStatus;
  units: RenterUnitLink[];
  created_at: string;
}

export interface RenterDetail {
  renter: RenterSummary;
  profile: RenterProfile | null;
  link_requests: LinkRequest[];
  /** Phase 4 fills this in; Phase 3 always answers an empty list. */
  contracts: unknown[];
}

/* ------------------------------------------------------------------ */
/* Phase 3 endpoint helpers (landlord; audience org)                    */
/* ------------------------------------------------------------------ */

export const linkRequestsApi = {
  list: (
    query: { status?: LinkRequestStatus | ''; cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ) =>
    api.get<{ items: LinkRequest[]; next_cursor?: string | null; total?: number }>(
      '/link-requests',
      { query, signal },
    ),
  get: (id: string, signal?: AbortSignal) =>
    api.get<LinkRequestDetail | LinkRequest>(`/link-requests/${id}`, { signal }),
  approve: (id: string) => api.post<{ request: LinkRequest } | LinkRequest>(`/link-requests/${id}/approve`),
  reject: (id: string, reason: string) =>
    api.post<{ request: LinkRequest } | LinkRequest>(`/link-requests/${id}/reject`, { reason }),
};

export const rentersApi = {
  list: (
    query: { q?: string; kyc_status?: KycStatus | ''; cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ) => api.get<{ items: RenterSummary[]; next_cursor?: string | null }>('/renters', { query, signal }),
  get: (userId: string, signal?: AbortSignal) =>
    api.get<RenterDetail>(`/renters/${userId}`, { signal }),
  kycDoc: (userId: string) => api.get<{ url: string }>(`/renters/${userId}/kyc-doc`),
};

export const unwrapRequest = (res: { request: LinkRequest } | LinkRequest) =>
  unwrap<LinkRequest>(res, 'request');

/**
 * `GET /link-requests/{id}` may answer `{request, renter_profile}` or a flat
 * request carrying `renter_profile`; normalise both into the pair.
 */
export function unwrapRequestDetail(res: LinkRequestDetail | LinkRequest): LinkRequestDetail {
  const o = res as unknown as Record<string, unknown>;
  const request = (o.request ?? res) as LinkRequest;
  const profile =
    (o.renter_profile as RenterProfile | undefined) ??
    ((request as unknown as Record<string, unknown>)?.renter_profile as RenterProfile | undefined) ??
    null;
  return { request, renter_profile: profile };
}

/** True when the renter uploaded an ID document (payload names it either way). */
export function hasKycDoc(profile: RenterProfile | null | undefined): boolean {
  if (!profile) return false;
  return Boolean(profile.kyc_doc_uploaded ?? profile.kyc_doc_available);
}
