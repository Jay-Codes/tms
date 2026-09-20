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

import type { Locale, ThemePreset, ThemeTokens } from '@tms/ui';
import { API_ORIGIN } from './basePath';

// Same origin by default (the dev proxy routes /api). NEXT_PUBLIC_API_URL
// (e.g. https://api.tms.kuzo.co.tz) makes every call cross-origin; the API
// then needs this app's origin in CORS_ALLOWED_ORIGINS and its cookies
// SameSite=None, and `credentials: 'include'` below carries them.
export const API_BASE = `${API_ORIGIN}/api/v1`;

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
  /**
   * The whole problem document. Some 409s carry extra fields the UI needs —
   * `overpay_confirm_required` ships `excess` and `next_schedule` so the
   * confirm prompt can name the money and the date (API.md Phase 5).
   */
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

  /** True when the caller has no valid session for this audience. */
  get isUnauthorized() {
    return this.status === 401;
  }

  /** Error codes ride in the problem `type` (DECISIONS.md). */
  get code(): string {
    const t = this.body.type;
    return typeof t === 'string' && t !== 'about:blank' ? t : '';
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
  /** Language this person reads the app — and receives their SMS — in. */
  locale?: Locale;
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
    /** The language the SW/EN toggle on the signup page was left on. */
    locale?: Locale;
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
  /**
   * Phase 14: the org's prepaid SMS balance. Read-only here — only the platform
   * admin can move it, so this screen never offers a purchase.
   */
  smsCredits: (signal?: AbortSignal) => api.get<SmsCredits>('/org/sms-credits', { signal }),
};

/**
 * `GET /org/sms-credits` (API.md Phase 14). `low` is the backend's own verdict
 * on `balance <= low_watermark`; the frontend does not re-decide it, it only
 * falls back to the comparison when an older payload omits the flag.
 */
export interface SmsCredits {
  balance: number;
  low_watermark: number;
  /** Messages parked as `held_no_credit`, waiting for the next top-up. */
  held_count: number;
  low?: boolean;
}

/** True when the balance sits at or under the watermark. */
export function creditsAreLow(c: SmsCredits | null): boolean {
  if (!c) return false;
  return typeof c.low === 'boolean' ? c.low : c.balance <= c.low_watermark;
}

/**
 * The signed-in org user's own record. Phase 13 adds `PATCH /org/members/me`
 * so a landlord's language choice follows the account, not the browser.
 */
export const membersApi = {
  updateMe: (body: { locale?: Locale; full_name?: string }) =>
    api.patch<{ user: User } | User>('/org/members/me', body),
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
  /**
   * Phase 16 §16.3 — the next unsettled due date on the unit's running
   * contract; null for anything vacant, unlisted, in maintenance, or occupied
   * with nothing outstanding.
   */
  next_due_date?: string | null;
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
  /** Phase 12: the full resolved token set, same shape as `/org/branding`. */
  theme: BrandingTheme;
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
  /**
   * Move the "Recommended" badge to this period. Exactly one period per org
   * carries it (PLAN2 #8), so the server clears the others; the response is
   * the changed period, like PATCH.
   */
  recommend: (id: string) =>
    api.post<{ period: PaymentPeriod } | PaymentPeriod>(`/org/payment-periods/${id}/recommend`),
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
  /** The renter's own language — the default for their contract and SMS. */
  locale?: Locale;
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
  /** The renter's own language — the default for their contract and SMS. */
  locale?: Locale;
  units: RenterUnitLink[];
  created_at: string;
  /**
   * Phase 16 §16.3 — aggregated over the renter's running tenancies with this
   * org, from the same three queries as `/reports/payment-status`. Optional so
   * the directory still renders against the Phase 3 shape.
   */
  next_due_date?: string | null;
  next_due_amount?: number | null;
  overdue_amount?: number;
}

export interface RenterDetail {
  renter: RenterSummary;
  profile: RenterProfile | null;
  link_requests: LinkRequest[];
  /** Phase 4 fills this in; Phase 3 always answered an empty list. */
  contracts: Contract[];
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
  /** Phase 4: approval also creates the contract, returned alongside the request. */
  approve: (id: string) =>
    api.post<{ request: LinkRequest; contract?: Contract } | LinkRequest>(
      `/link-requests/${id}/approve`,
    ),
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

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 4 + Branding exactly.                  */
/* ------------------------------------------------------------------ */

/** The variables a template body may carry; resolved server-side at creation. */
export const TEMPLATE_VARIABLES = [
  'renter_name',
  'unit',
  'property',
  'rent',
  'start_date',
  'end_date',
  'payment_period',
  'org_name',
  'term_days',
  'due_day',
] as const;

export type TemplateVariable = (typeof TEMPLATE_VARIABLES)[number];

export interface ContractTemplateSummary {
  id: string;
  name: string;
  is_default: boolean;
  updated_at: string;
  created_at: string;
}

export interface ContractTemplate extends ContractTemplateSummary {
  /** English body. Phase 13 kept the original field name for compatibility. */
  body_html: string;
  /** Swahili body; absent on templates written before Phase 13. */
  body_html_sw?: string | null;
  variables?: string[];
}

/**
 * `POST /contract-templates/{id}/preview`. The letterhead fields may arrive
 * flat or nested under `org`; `unwrapTemplatePreview` normalises both.
 */
export interface TemplatePreview {
  html: string;
  letterhead_url?: string | null;
  logo_url?: string | null;
  display_name?: string | null;
  footer_text?: string | null;
}

export type ContractStatus =
  | 'draft'
  | 'pending_signature'
  | 'active'
  | 'expiring'
  | 'ended'
  | 'terminated';

export type SignatureParty = 'renter' | 'landlord';

export interface ContractSignature {
  party: SignatureParty;
  name: string;
  signed_at: string;
  method: string;
  phone_masked?: string | null;
  has_image?: boolean;
  signature_image_url?: string | null;
}

export interface SchedulesSummary {
  count: number;
  total: number;
  next_due_date: string | null;
  next_due_amount: number | null;
  paid_count: number;
  overdue_count: number;
}

export interface Contract {
  id: string;
  unit: { id: string; name: string; property_name: string };
  renter: { user_id: string; full_name: string; phone: string | null };
  template_id: string | null;
  status: ContractStatus;
  rent_amount: number;
  rent_period_days: number;
  payment_period: { id: string; label: string; days: number } | null;
  term_days: number;
  start_date: string;
  end_date: string;
  due_day: number | null;
  snapshot_hash: string;
  /** Language the document was rendered in; absent on pre-Phase-13 contracts. */
  language?: Locale;
  signatures: ContractSignature[];
  link_request_id: string | null;
  created_at: string;
  activated_at: string | null;
  terminated_at: string | null;
  termination_reason: string | null;
  schedules_summary: SchedulesSummary | null;
}

export interface ContractDocument {
  contract_id: string;
  status: ContractStatus;
  org: {
    display_name: string;
    logo_url: string | null;
    letterhead_url: string | null;
    footer_text: string | null;
  };
  parties: {
    landlord: { name: string };
    renter: { name: string; phone_masked: string | null };
  };
  terms_html: string;
  schedule: { period_start: string; period_end: string; due_date: string; amount: number }[];
  signatures: ContractSignature[];
  snapshot_hash: string;
  generated_at: string;
}

export type ScheduleStatus = 'pending' | 'paid' | 'partial' | 'overdue' | 'waived';

export interface ScheduleRow {
  id: string;
  period_start: string;
  period_end: string;
  due_date: string;
  amount: number;
  status: ScheduleStatus | string;
  paid_amount: number;
}

export interface ContractVerification {
  valid: boolean;
  computed_hash: string;
  stored_hash: string;
  signatures: ContractSignature[];
}

export interface ContractInput {
  unit_id: string;
  renter_user_id: string;
  template_id?: string;
  payment_period_id: string;
  term_days: number;
  start_date: string;
  due_day?: number | null;
  /** Which body of the template to render. Defaults to the renter's locale. */
  language?: Locale;
  link_request_id?: string;
}

/* --------------------------------- branding -------------------------------- */

/**
 * The resolved theme (Phase 12). `source` says where it came from: a preset, a
 * custom token set, the v1 single-colour record (`legacy`) or nothing at all.
 * `primary_color` is kept so v1 callers keep working.
 */
export interface BrandingTheme {
  preset_id: string | null;
  tokens: ThemeTokens;
  font_id: string;
  dark: boolean;
  source?: 'preset' | 'custom' | 'legacy' | 'default';
  primary_color?: string;
}

export interface OrgBranding {
  display_name: string;
  logo_url: string | null;
  letterhead_url: string | null;
  theme: BrandingTheme;
  dashboard_prefs?: Record<string, unknown>;
  document_footer_text: string | null;
}

/** `PUT /org/branding` — send `tokens` only when the landlord customised them. */
export interface BrandingThemeInput {
  preset_id?: string | null;
  tokens?: ThemeTokens;
  font_id?: string;
}

export interface BrandingInput {
  display_name?: string;
  theme?: BrandingThemeInput;
  dashboard_prefs?: Record<string, unknown>;
  document_footer_text?: string | null;
}

/** `400` from `PUT /org/branding` when a pair falls under its minimum. */
export interface ThemeContrastFailure {
  pair: string;
  ratio: number;
  minimum: number;
}

/** Read the failing pairs out of an RFC-7807 body, if it carries any. */
export function themeFailures(err: unknown): ThemeContrastFailure[] {
  if (!(err instanceof ApiError)) return [];
  const raw = err.body.failures;
  if (!Array.isArray(raw)) return [];
  return raw.filter(
    (f): f is ThemeContrastFailure =>
      !!f && typeof (f as ThemeContrastFailure).pair === 'string',
  );
}

/** Presigned PUT ticket (logo, letterhead) — the same shape KYC uses. */
export interface UploadTicket {
  upload_url: string;
  object_key: string;
  headers: Record<string, string>;
}

export type BrandingAsset = 'logo' | 'letterhead';

/* ------------------------------------------------------------------ */
/* Phase 4 endpoint helpers (audience org)                              */
/* ------------------------------------------------------------------ */

export const templatesApi = {
  list: (signal?: AbortSignal) =>
    api.get<{ items: ContractTemplateSummary[] }>('/contract-templates', { signal }),
  get: (id: string, signal?: AbortSignal) =>
    api.get<{ template: ContractTemplate } | ContractTemplate>(`/contract-templates/${id}`, {
      signal,
    }),
  create: (body: {
    name: string;
    body_html: string;
    body_html_sw?: string | null;
    is_default?: boolean;
  }) => api.post<{ template: ContractTemplate } | ContractTemplate>('/contract-templates', body),
  update: (
    id: string,
    body: {
      name?: string;
      body_html?: string;
      body_html_sw?: string | null;
      is_default?: boolean;
    },
  ) => api.patch<{ template: ContractTemplate } | ContractTemplate>(`/contract-templates/${id}`, body),
  remove: (id: string) => api.del<void>(`/contract-templates/${id}`),
  /** `language` picks which body is rendered; the backend defaults to English. */
  preview: (id: string, sample = true, language?: Locale) =>
    api.post<TemplatePreview>(`/contract-templates/${id}/preview`, { sample, language }),
};

export const contractsApi = {
  list: (
    query: {
      status?: ContractStatus | '';
      unit_id?: string;
      renter_user_id?: string;
      cursor?: string;
      limit?: number;
    } = {},
    signal?: AbortSignal,
  ) =>
    api.get<{ items: Contract[]; next_cursor?: string | null }>('/contracts', { query, signal }),
  get: (id: string, signal?: AbortSignal) =>
    api.get<{ contract: Contract } | Contract>(`/contracts/${id}`, { signal }),
  create: (body: ContractInput) => api.post<{ contract: Contract } | Contract>('/contracts', body),
  document: (id: string, signal?: AbortSignal) =>
    api.get<ContractDocument>(`/contracts/${id}/document`, { signal }),
  schedules: (id: string, signal?: AbortSignal) =>
    api.get<{ items: ScheduleRow[] }>(`/contracts/${id}/schedules`, { signal }),
  verify: (id: string) => api.get<ContractVerification>(`/contracts/${id}/verify`),
  /** No body = normal countersign; `landlord_recorded` is the FLOWS 3.6 escape hatch. */
  activate: (id: string, body?: { landlord_recorded: true; reason: string }) =>
    api.post<{ contract: Contract } | Contract>(`/contracts/${id}/activate`, body),
  terminate: (id: string, body: { reason: string; effective_date?: string }) =>
    api.post<{ contract: Contract } | Contract>(`/contracts/${id}/terminate`, body),
};

export const brandingApi = {
  /** Public: the eight platform presets. No session needed. */
  presets: (signal?: AbortSignal) =>
    api.get<{ presets: ThemePreset[] }>('/themes/presets', { signal }),
  get: (signal?: AbortSignal) =>
    api.get<{ branding: OrgBranding } | OrgBranding>('/org/branding', { signal }),
  save: (body: BrandingInput) =>
    api.put<{ branding: OrgBranding } | OrgBranding>('/org/branding', body),
  uploadTicket: (asset: BrandingAsset, contentType: string, sizeBytes: number) =>
    api.post<UploadTicket>(`/org/branding/${asset}`, {
      content_type: contentType,
      size_bytes: sizeBytes,
    }),
  uploadComplete: (asset: BrandingAsset, objectKey: string) =>
    api.post<{ branding: OrgBranding } | OrgBranding>(`/org/branding/${asset}/complete`, {
      object_key: objectKey,
    }),
  remove: (asset: BrandingAsset) =>
    api.del<{ branding: OrgBranding } | OrgBranding>(`/org/branding/${asset}`),
};

export const unwrapTemplate = (res: { template: ContractTemplate } | ContractTemplate) =>
  unwrap<ContractTemplate>(res, 'template');
export const unwrapContract = (res: { contract: Contract } | Contract) =>
  unwrap<Contract>(res, 'contract');
export const unwrapBranding = (res: { branding: OrgBranding } | OrgBranding) =>
  unwrap<OrgBranding>(res, 'branding');

/** Preview letterhead fields may be flat or nested under `org`; take either. */
export function unwrapTemplatePreview(res: TemplatePreview): TemplatePreview {
  const o = res as unknown as Record<string, unknown>;
  const org = (o.org as Record<string, unknown> | undefined) ?? {};
  const pick = (k: string) => (o[k] ?? org[k] ?? null) as string | null;
  return {
    html: String(o.html ?? ''),
    letterhead_url: pick('letterhead_url'),
    logo_url: pick('logo_url'),
    display_name: pick('display_name'),
    footer_text: pick('footer_text') ?? pick('document_footer_text'),
  };
}

/**
 * Push a file straight at MinIO with the presigned URL the backend issued.
 * Not an API call — hence the raw fetch and the deliberate absence of
 * `credentials` (a signed URL must stay cookie-free).
 */
export async function uploadToPresignedUrl(ticket: UploadTicket, file: File): Promise<void> {
  let res: Response;
  try {
    res = await fetch(ticket.upload_url, {
      method: 'PUT',
      headers: ticket.headers ?? {},
      body: file,
    });
  } catch (cause) {
    throw offlineError(cause);
  }
  if (!res.ok) {
    throw new ApiError(res.status, {
      title: 'Upload failed',
      detail: 'The file could not be uploaded. Please try again.',
    });
  }
}

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 5 exactly.                             */
/* ------------------------------------------------------------------ */

export type PaymentMethod = 'cash' | 'bank_transfer' | 'mobile_money_manual';
export type PaymentStatus = 'recorded' | 'reversed';

/** Every method the MVP records. Gateway entry is post-MVP (SPEC §5.7). */
export const PAYMENT_METHODS: { value: PaymentMethod; label: string }[] = [
  { value: 'cash', label: 'Cash' },
  { value: 'bank_transfer', label: 'Bank transfer' },
  { value: 'mobile_money_manual', label: 'Mobile money' },
];

export function methodLabel(m: string | null | undefined): string {
  return PAYMENT_METHODS.find((x) => x.value === m)?.label ?? (m ? String(m).replace(/_/g, ' ') : '—');
}

/** The contract a schedule row belongs to, as `GET /schedules` denormalises it. */
export interface ScheduleContractRef {
  id: string;
  unit_name: string;
  property_name: string;
  renter_name: string;
  renter_user_id: string;
}

/**
 * A schedule row. `GET /contracts/{id}/schedules` omits the denormalised
 * `contract` block (the page already knows the contract) — hence optional.
 */
export interface Schedule extends ScheduleRow {
  contract_id?: string;
  days_overdue?: number | null;
  contract?: ScheduleContractRef | null;
}

/** One slice of a payment as the backend allocated it across schedules. */
export interface PaymentAllocation {
  schedule_id: string;
  amount: number;
}

export interface Payment {
  id: string;
  contract_id: string;
  schedule_id: string | null;
  amount: number;
  method: PaymentMethod | string;
  reference: string | null;
  paid_at: string;
  note: string | null;
  status: PaymentStatus | string;
  recorded_by: { user_id: string; name: string } | null;
  reversed_at: string | null;
  reversal_reason: string | null;
  applied: PaymentAllocation[] | null;
  created_at: string;
  /**
   * `GET /payments` denormalises the contract onto the payment itself rather
   * than nesting it the way `GET /schedules` does. Both are tolerated —
   * `paymentWho()` reads whichever arrived.
   */
  unit_name?: string | null;
  property_name?: string | null;
  renter_name?: string | null;
  renter_user_id?: string | null;
  contract?: ScheduleContractRef | null;
}

/** Who and what a payment was against, from either payload shape. */
export function paymentWho(p: Payment): {
  renterName: string;
  renterUserId: string | null;
  unitName: string;
  propertyName: string;
} {
  return {
    renterName: p.renter_name ?? p.contract?.renter_name ?? '—',
    renterUserId: p.renter_user_id ?? p.contract?.renter_user_id ?? null,
    unitName: p.unit_name ?? p.contract?.unit_name ?? 'Contract',
    propertyName: p.property_name ?? p.contract?.property_name ?? '',
  };
}

export interface PaymentInput {
  contract_id: string;
  schedule_id?: string;
  amount: number;
  method: PaymentMethod;
  reference?: string;
  paid_at?: string;
  note?: string;
  allow_overpay_rollover?: boolean;
}

/** `201 {payment, schedules}` — the rows the allocation moved. */
export interface PaymentResult {
  payment: Payment;
  schedules: Schedule[];
}

/**
 * Phase 16 §16.4 — the wallet an org may take rent into. It lives beside
 * `bank_account` in `orgs.settings`, so an org can take mobile money and no
 * bank transfer at all.
 */
export interface MobileMoney {
  provider: string;
  number: string;
  name: string;
}

export interface BankAccount {
  bank_name: string;
  account_name: string;
  account_number: string;
  instructions: string;
  /** Repeated inside the account block: it is what the renter's card renders. */
  mobile_money?: MobileMoney | null;
}

/**
 * `GET /org/bank-account` in full. The wallet appears twice on purpose (API.md
 * §16.4); `payment_instructions_set` is true when *either* block is set, and is
 * the flag behind the landlord's dashboard nudge.
 */
export interface BankAccountView {
  bank_account: BankAccount | null;
  mobile_money: MobileMoney | null;
  payment_instructions_set: boolean;
}

/**
 * `PUT /org/bank-account`: the Phase 5 flat body plus an optional wallet. An
 * object replaces it, an explicit `null` clears it, and **omitting the key
 * keeps what is stored** — which is why `mobile_money` is `?:` and not just
 * nullable here.
 */
export interface BankAccountInput {
  bank_name: string;
  account_name: string;
  account_number: string;
  instructions: string;
  mobile_money?: MobileMoney | null;
}

/**
 * The 409 the record form must handle by asking, not by failing: the money is
 * more than the target schedule needs and the landlord has not yet said the
 * excess may roll forward.
 */
export interface OverpayPrompt {
  excess: number;
  next_schedule: Schedule | null;
  detail: string;
}

/** Read the overpay 409's extra fields; null when this is any other error. */
export function overpayPrompt(e: ApiError): OverpayPrompt | null {
  if (e.status !== 409 || e.code !== 'overpay_confirm_required') return null;
  const excess = Number(e.body.excess ?? 0);
  return {
    excess: Number.isFinite(excess) ? excess : 0,
    next_schedule: (e.body.next_schedule as Schedule | undefined) ?? null,
    detail: e.detail,
  };
}

/* ------------------------------------------------------------------ */
/* Phase 5 endpoint helpers (audience org)                              */
/* ------------------------------------------------------------------ */

export const schedulesApi = {
  list: (
    query: {
      status?: ScheduleStatus | '';
      contract_id?: string;
      renter_user_id?: string;
      due_from?: string;
      due_to?: string;
      cursor?: string;
      limit?: number;
    } = {},
    signal?: AbortSignal,
  ) =>
    api.get<{ items: Schedule[]; next_cursor?: string | null }>('/schedules', { query, signal }),
};

export const paymentsApi = {
  list: (
    query: {
      contract_id?: string;
      renter_user_id?: string;
      method?: PaymentMethod | '';
      from?: string;
      to?: string;
      cursor?: string;
      limit?: number;
    } = {},
    signal?: AbortSignal,
  ) => api.get<{ items: Payment[]; next_cursor?: string | null }>('/payments', { query, signal }),
  get: (id: string, signal?: AbortSignal) =>
    api.get<{ payment: Payment } | Payment>(`/payments/${id}`, { signal }),
  record: (body: PaymentInput) => api.post<PaymentResult>('/payments', body),
  reverse: (id: string, reason: string) =>
    api.post<PaymentResult>(`/payments/${id}/reverse`, { reason }),
};

export const bankAccountApi = {
  get: (signal?: AbortSignal) =>
    api.get<BankAccountView | BankAccount>('/org/bank-account', { signal }),
  save: (body: BankAccountInput) =>
    api.put<BankAccountView | BankAccount>('/org/bank-account', body),
};

export const unwrapPayment = (res: { payment: Payment } | Payment) => unwrap<Payment>(res, 'payment');
export const unwrapBankAccount = (res: BankAccountView | BankAccount) =>
  unwrap<BankAccount | null>(res, 'bank_account');

/**
 * Normalise either spelling of `GET`/`PUT /org/bank-account` into the Phase 16
 * view: the flat Phase 5 body, the `{bank_account}` wrapper, and the full
 * `{bank_account, mobile_money, payment_instructions_set}` all read the same.
 * The wallet is taken from wherever it was sent, and the flag is derived when
 * the backend did not send one.
 */
export function readBankAccountView(res: BankAccountView | BankAccount): BankAccountView {
  const o = (res ?? {}) as Record<string, unknown>;
  const wrapped = 'bank_account' in o;
  const account = (wrapped ? (o.bank_account as BankAccount | null) : (res as BankAccount)) ?? null;
  const wallet =
    (o.mobile_money as MobileMoney | null | undefined) ?? account?.mobile_money ?? null;
  const set =
    typeof o.payment_instructions_set === 'boolean'
      ? o.payment_instructions_set
      : Boolean(
          wallet ||
            (account && (account.bank_name || account.account_name || account.account_number)),
        );
  return { bank_account: account, mobile_money: wallet, payment_instructions_set: set };
}

/** A schedule still owes money — the ones "Record payment" may target. */
export function isUnsettled(s: Pick<Schedule, 'status'>): boolean {
  return s.status === 'pending' || s.status === 'partial' || s.status === 'overdue';
}

/** What is still outstanding on a row; the record form prefills with it. */
export function remainingOn(s: Pick<Schedule, 'amount' | 'paid_amount'>): number {
  return Math.max(0, (s.amount ?? 0) - (s.paid_amount ?? 0));
}

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 6 exactly.                             */
/* ------------------------------------------------------------------ */

/** Kinds the org settings screen can switch on and template. */
export const SCHEDULED_KINDS = [
  'reminder_7d',
  'reminder_due',
  'overdue_daily',
  'thank_you',
  'unsigned_reminder',
] as const;

export type ScheduledKind = (typeof SCHEDULED_KINDS)[number];

/** Every kind that can appear in the log — the scheduled five plus the rest. */
export type NotificationKind = ScheduledKind | 'otp' | 'custom' | 'link_approved' | 'link_rejected' | string;

/**
 * Phase 14 adds `held_no_credit`: the org ran out of SMS credit, so the row is
 * parked rather than failed. It is not retryable — the platform's next top-up
 * releases it in queue order.
 */
export type NotificationStatus =
  | 'queued'
  | 'sending'
  | 'sent'
  | 'failed'
  | 'held_no_credit'
  | string;

/** What each kind is for, and the SMS language, in the landlord's words. */
export const KIND_LABELS: Record<string, string> = {
  reminder_7d: 'Reminder before due',
  reminder_due: 'Reminder on the due date',
  overdue_daily: 'Daily overdue reminder',
  thank_you: 'Thank you for payment',
  unsigned_reminder: 'Unsigned contract reminder',
  otp: 'One-time code',
  custom: 'Custom message',
  link_approved: 'Link approved',
  link_rejected: 'Link rejected',
};

export function kindLabel(kind: string | null | undefined): string {
  if (!kind) return '—';
  return KIND_LABELS[kind] ?? String(kind).replace(/_/g, ' ');
}

/** Variables a template body may carry (API.md Phase 6). */
export const SMS_VARIABLES = [
  'name',
  'amount',
  'due_date',
  'property',
  'unit',
  'org',
  'next_due_date',
  'link',
] as const;

/** The subset a custom bulk message can resolve — there is no schedule behind it. */
export const CUSTOM_SMS_VARIABLES = ['name', 'unit', 'property', 'org'] as const;

/** One SMS is 320 characters at most; the backend rejects longer (API.md). */
export const SMS_MAX_CHARS = 320;

export interface NotificationKindConfig {
  enabled: boolean;
  /** `reminder_7d` only: how many days before the due date to send. */
  offset_days?: number;
  /** `unsigned_reminder` only: days to wait after the contract was created. */
  after_days?: number;
}

export interface NotificationTemplate {
  sw: string;
  en: string;
}

export interface NotificationSettings {
  /** ≤11 chars; null means the platform sender ID is used. */
  sender_name: string | null;
  language: 'sw' | 'en';
  send_hour_local: number;
  kinds: Partial<Record<ScheduledKind, NotificationKindConfig>>;
  /** null for a kind = platform default copy. */
  templates: Partial<Record<ScheduledKind, NotificationTemplate | null>>;
  /**
   * Phase 14. Kinds the platform has locked: the landlord reads the wording but
   * cannot replace it, and a PUT carrying an override answers 409
   * `template_locked`. `otp` ships locked.
   */
  locked_kinds?: string[];
  /**
   * Phase 14. The platform's own wording per kind, in both languages — shown so
   * a landlord can see what an override would be replacing. Keyed by kind, and
   * carries kinds outside the schedulable five (`otp`).
   */
  platform_templates?: Record<string, NotificationTemplate>;
}

export interface NotificationLogEntry {
  id: string;
  kind: NotificationKind;
  to_phone: string | null;
  renter_name: string | null;
  /** Some payloads name the renter id; the renter page filters by it. */
  user_id?: string | null;
  body: string;
  status: NotificationStatus;
  provider_msg_id: string | null;
  error: string | null;
  attempts: number;
  /** Set on rows from a custom broadcast; groups one batch together. */
  batch_id?: string | null;
  /** Phase 13: the language the body was sent in. */
  language?: Locale | null;
  created_at: string;
  sent_at: string | null;
}

/** How a bulk send (or its preview) splits across the two languages. */
export interface ByLanguage {
  sw: number;
  en: number;
}

/** `202 {batch_id, queued, skipped, by_language}` from a custom bulk send. */
export interface CustomSendResult {
  batch_id?: string;
  queued: number;
  skipped: number;
  /** Phase 13: how many messages went out in each language. */
  by_language?: ByLanguage;
}

/**
 * A bulk send carries one body per language; at least one is required. When
 * only one is filled every recipient gets it, whatever their own language.
 */
export interface CustomSendInput {
  recipients: 'all_active' | 'selected';
  renter_user_ids?: string[];
  body_sw?: string;
  body_en?: string;
}

/** `GET /notifications/custom/recipients-preview` — who a filter would reach. */
export interface RecipientsPreview {
  count: number;
  by_language: ByLanguage;
}

/* ------------------------------------------------------------------ */
/* Phase 6 endpoint helpers (audience org)                              */
/* ------------------------------------------------------------------ */

export const notificationsApi = {
  settings: (signal?: AbortSignal) =>
    api.get<{ settings: NotificationSettings } | NotificationSettings>('/org/notification-settings', {
      signal,
    }),
  /** Partial merge — send only what the form changed (API.md). */
  saveSettings: (body: Partial<NotificationSettings>) =>
    api.put<{ settings: NotificationSettings } | NotificationSettings>(
      '/org/notification-settings',
      body,
    ),
  log: (
    query: {
      kind?: string;
      status?: string;
      user_id?: string;
      from?: string;
      to?: string;
      cursor?: string;
      limit?: number;
    } = {},
    signal?: AbortSignal,
  ) =>
    api.get<{ items: NotificationLogEntry[]; next_cursor?: string | null }>('/notifications/log', {
      query,
      signal,
    }),
  retry: (id: string) => api.post<{ entry?: NotificationLogEntry } | void>(`/notifications/log/${id}/retry`),
  sendCustom: (body: CustomSendInput) => api.post<CustomSendResult>('/notifications/custom', body),
  /**
   * Recipient counts for the compose screen, split by the language each renter
   * reads. Same filters as `sendCustom`; refreshed whenever they change.
   */
  recipientsPreview: (
    query: { recipients: 'all_active' | 'selected'; renter_user_ids?: string[] },
    signal?: AbortSignal,
  ) =>
    api.get<RecipientsPreview>('/notifications/custom/recipients-preview', {
      query: {
        recipients: query.recipients,
        renter_user_ids: query.renter_user_ids?.length
          ? query.renter_user_ids.join(',')
          : undefined,
      },
      signal,
    }),
};

/** `GET/PUT /org/notification-settings` may answer `{settings}` or a bare object. */
export function unwrapNotificationSettings(
  res: { settings: NotificationSettings } | NotificationSettings,
): NotificationSettings {
  return 'settings' in res && res.settings ? res.settings : (res as NotificationSettings);
}

/**
 * Fill in what a partial payload left out so the form always has something to
 * bind to. Display only — every value shown is still the backend's.
 */
export function withSettingsDefaults(s: Partial<NotificationSettings> | null): NotificationSettings {
  const kinds = (s?.kinds ?? {}) as NotificationSettings['kinds'];
  return {
    sender_name: s?.sender_name ?? null,
    language: s?.language === 'en' ? 'en' : 'sw',
    send_hour_local: typeof s?.send_hour_local === 'number' ? s.send_hour_local : 9,
    kinds: {
      reminder_7d: { enabled: false, offset_days: 7, ...(kinds.reminder_7d ?? {}) },
      reminder_due: { enabled: false, ...(kinds.reminder_due ?? {}) },
      overdue_daily: { enabled: false, ...(kinds.overdue_daily ?? {}) },
      thank_you: { enabled: false, ...(kinds.thank_you ?? {}) },
      unsigned_reminder: { enabled: false, after_days: 7, ...(kinds.unsigned_reminder ?? {}) },
    },
    templates: s?.templates ?? {},
    // Phase 14 — absent until the backend lands, which reads as "nothing locked
    // and no platform wording to show", i.e. exactly the Phase 13 screen.
    locked_kinds: Array.isArray(s?.locked_kinds) ? s.locked_kinds : [],
    platform_templates: s?.platform_templates ?? {},
  };
}

/** Whether the platform has locked this kind's wording (Phase 14). */
export function isKindLocked(s: NotificationSettings | null, kind: string): boolean {
  return Boolean(s?.locked_kinds?.includes(kind));
}

/** The platform's own wording for a kind, when the backend sends it. */
export function platformTemplateFor(
  s: NotificationSettings | null,
  kind: string,
): NotificationTemplate | null {
  return s?.platform_templates?.[kind] ?? null;
}

/**
 * Resolve `{{var}}` placeholders for the send-message preview. Display only —
 * the backend renders what is actually sent, from its own row.
 */
export function resolveVariables(body: string, values: Record<string, string>): string {
  return body.replace(/\{\{\s*([a-z_]+)\s*\}\}/g, (whole, name: string) =>
    name in values ? values[name] : whole,
  );
}

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 7 exactly (reports + dashboard prefs). */
/* ------------------------------------------------------------------ */

export interface ReportAssets {
  properties: number;
  units: number;
  occupied: number;
  vacant: number;
  maintenance: number;
  unlisted: number;
  /** 0–1; the UI prints it as a percentage. */
  occupancy_rate: number;
}

export interface ReportPeriod {
  from: string;
  to: string;
  expected: number;
  collected: number;
  outstanding: number;
  overdue_count: number;
  overdue_amount: number;
}

export interface VacantUnitRow {
  unit_id: string;
  name: string;
  property_name: string;
  days_vacant: number;
}

export interface ReportSummary {
  assets: ReportAssets;
  renters: { active: number };
  contracts: { active: number; expiring: number; pending_signature: number };
  period: ReportPeriod;
  vacant_units: VacantUnitRow[];
  /**
   * Phase 16 §16.3 — always a 7-day window whatever the summary's period, so
   * the dashboard's `upcoming` card costs no second call for its figure.
   * Optional so the screen still renders against the Phase 7 shape.
   */
  upcoming_7d?: { count: number; total: number };
  /* Phase 11 — the resolved window and the one before it, for comparison.
     Optional so the screen still renders against the Phase 7 shape. */
  window?: ReportWindow;
  previous?: ReportWindow;
  previous_totals?: Partial<ReportPeriod>;
  change_pct?: ChangePct;
}

/** Worst status across a renter's unsettled schedules (API.md). */
export type PaymentStatusValue = 'paid' | 'pending' | 'overdue' | 'partial';

export interface PaymentStatusRow {
  renter_user_id: string;
  renter_name: string;
  phone: string | null;
  unit_name: string;
  property_name: string;
  contract_id: string;
  status: PaymentStatusValue;
  next_due_date: string | null;
  next_due_amount: number | null;
  outstanding: number;
  overdue_amount: number;
  last_payment_at: string | null;
}

/**
 * Phase 16 §16.3 — one row of `GET /reports/upcoming`: the Phase 5 schedule
 * shape *flattened*, with the identity fields beside it rather than nested.
 * `days_until_due` is counted on the Dar es Salaam wall clock by the backend
 * and goes negative once the date has passed; nothing here recomputes it.
 */
export interface UpcomingItem {
  id: string;
  contract_id: string;
  period_start: string;
  period_end: string;
  due_date: string;
  amount: number;
  paid_amount: number;
  status: ScheduleStatus | string;
  days_overdue: number;
  renter_name: string;
  renter_user_id: string;
  phone: string | null;
  unit_name: string;
  property_name: string;
  days_until_due: number;
}

/** The three windows the backend accepts; anything else is a 400 on `days`. */
export const UPCOMING_WINDOWS = [7, 14, 30] as const;
export type UpcomingWindow = (typeof UPCOMING_WINDOWS)[number];

export interface UpcomingReport {
  items: UpcomingItem[];
  /** Sum of `amount − paid_amount` across the window — the backend sums it. */
  total_due: number;
  count: number;
  days: number;
  window: { from: string; to: string };
}

export type CollectionsGroup = 'day' | 'week' | 'month';

export interface CollectionsBucket {
  start: string;
  expected: number;
  collected: number;
}

export interface CollectionsReport {
  buckets: CollectionsBucket[];
  totals: { expected: number; collected: number };
  /* Phase 11 — present once the cadence-aware backend is in. */
  window?: ReportWindow;
  previous?: ReportWindow;
  previous_totals?: { expected: number; collected: number };
  change_pct?: ChangePct;
}

/* ------------------------------------------------------------------ */
/* Shapes — API.md Phase 11 (cadence, revenue series, occupancy).      */
/* ------------------------------------------------------------------ */

/**
 * The window every Phase 11 report echoes back: half-open `[from, to)`, `to`
 * exclusive — the same contract `@tms/ui`'s PeriodPicker emits, so a window
 * never has to be converted on the way in or out.
 */
export interface ReportWindow {
  from: string;
  to: string;
  cadence: string;
}

/** `{collected: 12.4, expenses: null}` — null means the previous window was empty. */
export type ChangePct = Record<string, number | null>;

/** Bucket size for a series. The backend picks one from the window unless told. */
export type ReportBucket = 'day' | 'week' | 'month';

/**
 * What every cadence-aware report takes. Send `cadence` + `anchor` for a named
 * window, or `from` + `to` for a custom one; the PeriodPicker's value maps
 * straight onto the second form.
 */
export interface PeriodQuery {
  /* Indexed so a query can be spread with the extra keys a report takes
     (`bucket`, `property_id`, `group_by`) and still satisfy the fetch
     wrapper's query type. */
  [key: string]: string | undefined;
  cadence?: string;
  anchor?: string;
  from?: string;
  to?: string;
}

export interface RevenueTotals {
  expected: number;
  collected: number;
  expenses: number;
  net: number;
}

export interface RevenueBucket extends RevenueTotals {
  start: string;
}

export interface RevenueReport {
  window: ReportWindow;
  previous: ReportWindow;
  bucket: ReportBucket;
  buckets: RevenueBucket[];
  totals: RevenueTotals;
  previous_totals: RevenueTotals;
  change_pct: ChangePct;
  trend: { slope_collected_per_bucket: number };
  /** collected ÷ expected, 0–1; null when nothing was expected. */
  collection_rate: number | null;
}

export interface RevenuePropertyGroup extends RevenueTotals {
  id: string;
  name: string;
  collection_rate: number | null;
}

export interface RevenueByPropertyReport {
  window: ReportWindow;
  previous: ReportWindow;
  groups: RevenuePropertyGroup[];
  totals: RevenueTotals;
  previous_totals: RevenueTotals;
  change_pct: ChangePct;
}

export interface OccupancyBucket {
  start: string;
  units_total: number;
  units_occupied: number;
  /** 0–1, like `assets.occupancy_rate` on the summary. */
  occupancy_pct: number;
}

export interface OccupancyReport {
  window: ReportWindow;
  bucket: ReportBucket;
  buckets: OccupancyBucket[];
  current: { units_total: number; units_occupied: number; occupancy_pct: number };
}

/**
 * The dashboard card ids the backend validates `dashboard_prefs.cards` against
 * (API.md Phase 7). The order of this array is the default layout for an org
 * that has never opened the customise sheet.
 */
export const DASHBOARD_CARDS = [
  'assets',
  'renters',
  'payment_status',
  'collections',
  'link_requests',
  'overdue',
  // Phase 16 §16.3/§16.1 — `upcoming` sits directly after `overdue` in the
  // backend allowlist order, `proofs` directly after it.
  'upcoming',
  'proofs',
  'expenses',
  'revenue',
  'net_income',
] as const;

export type DashboardCard = (typeof DASHBOARD_CARDS)[number];

/**
 * What an org that has never opened the customise sheet sees. It is a subset of
 * `DASHBOARD_CARDS` on purpose: a card shipped after an org started using the
 * product (Phase 10's "Expenses this month") is offered in the sheet but is not
 * pushed onto anyone's dashboard uninvited.
 */
export const DEFAULT_DASHBOARD_CARDS: readonly DashboardCard[] = [
  'assets',
  'renters',
  'revenue',
  'net_income',
  'payment_status',
  'collections',
  'link_requests',
  'overdue',
  'upcoming',
  'proofs',
];

export interface DashboardPrefs {
  cards: DashboardCard[];
  layout: 'grid' | 'list';
}

const isCard = (v: unknown): v is DashboardCard =>
  typeof v === 'string' && (DASHBOARD_CARDS as readonly string[]).includes(v);

/**
 * Read `dashboard_prefs` off the branding record into something the dashboard
 * can render without further guarding: known ids only, no duplicates, and any
 * card the org has never seen (a new one shipped since they last saved) is
 * appended in default order rather than silently dropped.
 */
export function readDashboardPrefs(prefs: Record<string, unknown> | undefined | null): DashboardPrefs {
  const raw = (prefs ?? {}) as { cards?: unknown; layout?: unknown };
  const listed = Array.isArray(raw.cards) ? raw.cards.filter(isCard) : [];
  const cards = [...new Set(listed)];
  return {
    cards: cards.length ? cards : [...DEFAULT_DASHBOARD_CARDS],
    layout: raw.layout === 'list' ? 'list' : 'grid',
  };
}

/** Cards the org has switched off — kept so the sheet can offer them back. */
export function hiddenDashboardCards(prefs: DashboardPrefs): DashboardCard[] {
  return DASHBOARD_CARDS.filter((c) => !prefs.cards.includes(c));
}

/* ------------------------------------------------------------------ */
/* Phase 7 endpoint helpers (audience org)                              */
/* ------------------------------------------------------------------ */

export const reportsApi = {
  /**
   * The period is given the Phase 11 way — `cadence` + `anchor`, or the
   * PeriodPicker's `from`/`to`. `period` (`month` or `YYYY-MM`) is the Phase 7
   * spelling and still works.
   */
  summary: (query: PeriodQuery & { period?: string } = {}, signal?: AbortSignal) =>
    api.get<ReportSummary>('/reports/summary', { query, signal }),
  paymentStatus: (
    query: PeriodQuery & { status?: PaymentStatusValue | ''; property_id?: string } = {},
    signal?: AbortSignal,
  ) => api.get<{ items: PaymentStatusRow[] }>('/reports/payment-status', { query, signal }),
  /**
   * Phase 16 §16.3 — what falls due inside the next `days` (7, 14 or 30;
   * default 14), sorted by due date. Unsettled rows on running contracts only.
   */
  upcoming: (
    query: { days?: UpcomingWindow | number; property_id?: string } = {},
    signal?: AbortSignal,
  ) => api.get<UpcomingReport>('/reports/upcoming', { query, signal }),
  collections: (
    query: PeriodQuery & { group?: CollectionsGroup; property_id?: string },
    signal?: AbortSignal,
  ) => api.get<CollectionsReport>('/reports/collections', { query, signal }),

  /* ------------------------------ phase 11 ------------------------------ */

  /** Expected / collected / expenses / net per bucket across the window. */
  revenue: (
    query: PeriodQuery & { bucket?: ReportBucket; property_id?: string } = {},
    signal?: AbortSignal,
  ) => api.get<RevenueReport>('/reports/revenue', { query, signal }),
  /** The same window, cut by property instead of by time. */
  revenueByProperty: (query: PeriodQuery = {}, signal?: AbortSignal) =>
    api.get<RevenueByPropertyReport>('/reports/revenue', {
      query: { ...query, group_by: 'property' },
      signal,
    }),
  occupancy: (
    query: PeriodQuery & { bucket?: ReportBucket; property_id?: string } = {},
    signal?: AbortSignal,
  ) => api.get<OccupancyReport>('/reports/occupancy', { query, signal }),
  /**
   * The CSV is a download, not a fetch: the browser opens it same-origin so the
   * httpOnly `tms_o` cookie rides along and the attachment lands in Downloads.
   * Written against API_BASE (absolute path) so the '/tenant' basePath is not
   * prepended — the same rule the fetch wrapper follows.
   */
  paymentStatusCsvUrl: (
    query: PeriodQuery & { status?: PaymentStatusValue | ''; property_id?: string } = {},
  ) => {
    const qs = new URLSearchParams({ format: 'csv' });
    for (const k of ['status', 'property_id', 'cadence', 'anchor', 'from', 'to'] as const) {
      const v = query[k];
      if (v) qs.set(k, String(v));
    }
    return `${API_BASE}/reports/payment-status?${qs.toString()}`;
  },
};

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Phase 10 exactly (expenses + categories).    */
/* ------------------------------------------------------------------ */

/**
 * A spending category. Seeded per org (Repairs & maintenance, Utilities,
 * Security, Cleaning, Taxes & levies, Insurance, Management fees, Other) —
 * `is_default` marks those, which is why the manager offers "deactivate"
 * rather than "delete" for a category that has already been used.
 */
export interface ExpenseCategory {
  id: string;
  name: string;
  is_default: boolean;
  sort_order: number;
  active: boolean;
  created_at: string;
}

export type ExpenseStatus = 'recorded' | 'voided';

/** What the ledger knows about a receipt without fetching it. */
export interface ExpenseReceipt {
  present: boolean;
  content_type: string | null;
  size: number | null;
}

export interface ExpenseRef {
  id: string;
  name: string;
}

export interface Expense {
  id: string;
  property: ExpenseRef;
  unit: ExpenseRef | null;
  category: ExpenseRef | null;
  amount: number;
  /** Calendar date the money was spent, `YYYY-MM-DD`. */
  incurred_on: string;
  vendor: string | null;
  reference: string | null;
  note: string | null;
  receipt: ExpenseReceipt;
  recorded_by: { id: string; name: string } | null;
  status: ExpenseStatus | string;
  voided_at: string | null;
  void_reason: string | null;
  created_at: string;
  updated_at: string;
}

export interface ExpenseInput {
  property_id: string;
  unit_id?: string | null;
  category_id?: string | null;
  amount: number;
  incurred_on: string;
  vendor?: string;
  reference?: string;
  note?: string;
}

/** `GET /expenses` — a page of the ledger plus the totals for the whole filter. */
export interface ExpenseListResult {
  items: Expense[];
  next_cursor: string | null;
  totals: { count: number; amount: number };
}

export interface ExpenseListQuery {
  /** Passed straight to the query-string builder, which drops empty entries. */
  [key: string]: string | number | null | undefined;
  property_id?: string;
  unit_id?: string;
  category_id?: string;
  /** `all` includes voided rows; the default the backend applies is `recorded`. */
  status?: ExpenseStatus | 'all' | '';
  from?: string;
  to?: string;
  cadence?: string;
  anchor?: string;
  q?: string;
  cursor?: string;
  limit?: number;
}

export type ExpenseGroupBy = 'property' | 'category';

export interface ExpenseSummaryGroup {
  id: string;
  name: string;
  amount: number;
  count: number;
}

/**
 * `GET /expenses/summary`. `change_pct` is null when the previous window held
 * nothing — a percentage against zero is not a fact, so the UI prints "—".
 */
export interface ExpenseSummary {
  window: { from: string; to: string; cadence: string };
  previous: { from: string; to: string };
  groups: ExpenseSummaryGroup[];
  total: { amount: number; count: number };
  previous_total: { amount: number; count: number };
  change_pct: number | null;
}

/** `POST /expenses/{id}/receipt` — a presigned PUT for the receipt file. */
export interface ReceiptTicket {
  upload_url: string;
  object_key: string;
  expires_in: number;
  /** Optional; when absent the caller sets `Content-Type` itself. */
  headers?: Record<string, string>;
}

/** `GET /expenses/{id}/receipt` — a short-lived read URL. */
export interface ReceiptView {
  url: string;
  expires_in: number;
}

/** What a receipt may be (SPEC §7, bucket `receipts`). */
export const RECEIPT_TYPES = ['image/jpeg', 'image/png', 'application/pdf'] as const;
export const RECEIPT_MAX_BYTES = 5 * 1024 * 1024;
export const RECEIPT_ACCEPT = 'image/jpeg,image/png,application/pdf';

/** null when the file is acceptable; otherwise the sentence to show. */
export function receiptFileProblem(file: File): string | null {
  if (!(RECEIPT_TYPES as readonly string[]).includes(file.type)) {
    return 'A receipt must be a JPEG, PNG or PDF.';
  }
  if (file.size > RECEIPT_MAX_BYTES) return 'That file is larger than 5 MB. Choose a smaller one.';
  return null;
}

/**
 * PUT the receipt straight at MinIO. Deliberately cookie-free (a signed URL
 * must stay so) and the Content-Type must match the one the ticket was signed
 * for, or the object store rejects the signature.
 */
export async function uploadReceipt(ticket: ReceiptTicket, file: File): Promise<void> {
  let res: Response;
  try {
    res = await fetch(ticket.upload_url, {
      method: 'PUT',
      headers: ticket.headers ?? { 'Content-Type': file.type },
      body: file,
    });
  } catch (cause) {
    throw offlineError(cause);
  }
  if (!res.ok) {
    throw new ApiError(res.status, {
      title: 'Upload failed',
      detail: 'The receipt could not be uploaded. The expense was saved — try the receipt again.',
    });
  }
}

/* ------------------------------------------------------------------ */
/* Phase 10 endpoint helpers (audience org)                             */
/* ------------------------------------------------------------------ */

export const expenseCategoriesApi = {
  list: (signal?: AbortSignal) =>
    api.get<{ items: ExpenseCategory[] }>('/org/expense-categories', { signal }),
  create: (body: { name: string; sort_order?: number }) =>
    api.post<{ category: ExpenseCategory } | ExpenseCategory>('/org/expense-categories', body),
  update: (id: string, body: { name?: string; sort_order?: number; active?: boolean }) =>
    api.patch<{ category: ExpenseCategory } | ExpenseCategory>(
      `/org/expense-categories/${id}`,
      body,
    ),
  remove: (id: string) => api.del<void>(`/org/expense-categories/${id}`),
};

export const expensesApi = {
  list: (query: ExpenseListQuery = {}, signal?: AbortSignal) =>
    api.get<ExpenseListResult>('/expenses', { query, signal }),
  get: (id: string, signal?: AbortSignal) =>
    api.get<{ expense: Expense } | Expense>(`/expenses/${id}`, { signal }),
  create: (body: ExpenseInput) => api.post<{ expense: Expense } | Expense>('/expenses', body),
  update: (id: string, body: Partial<ExpenseInput>) =>
    api.patch<{ expense: Expense } | Expense>(`/expenses/${id}`, body),
  /** Append-style correction: the row stays and is stamped, like reversing a payment. */
  void: (id: string, reason: string) =>
    api.post<{ expense: Expense } | Expense>(`/expenses/${id}/void`, { reason }),
  summary: (
    query: {
      cadence?: string;
      anchor?: string;
      from?: string;
      to?: string;
      group_by?: ExpenseGroupBy;
      property_id?: string;
    } = {},
    signal?: AbortSignal,
  ) => api.get<ExpenseSummary>('/expenses/summary', { query, signal }),

  /* ------------------------------- receipts ------------------------------ */
  receiptTicket: (id: string, contentType: string, size: number) =>
    api.post<ReceiptTicket>(`/expenses/${id}/receipt`, { content_type: contentType, size }),
  /**
   * Confirm the upload landed. The backend wants the key it just signed back
   * (the same shape KYC's complete step uses), so the ticket's `object_key` is
   * echoed rather than the endpoint being called bare.
   */
  receiptComplete: (id: string, objectKey: string) =>
    api.post<{ expense: Expense } | Expense>(`/expenses/${id}/receipt/complete`, {
      object_key: objectKey,
    }),
  receiptUrl: (id: string, signal?: AbortSignal) =>
    api.get<ReceiptView>(`/expenses/${id}/receipt`, { signal }),
  receiptRemove: (id: string) => api.del<{ expense: Expense } | Expense>(`/expenses/${id}/receipt`),

  /**
   * The CSV export. Fetched rather than linked because the filename comes back
   * in `Content-Disposition` and the blob has to be handed to the browser
   * ourselves; `credentials: 'include'` carries the httpOnly `tms_o` cookie.
   */
  csv: async (query: ExpenseListQuery = {}, signal?: AbortSignal): Promise<{ blob: Blob; filename: string }> => {
    const url = buildUrl('/expenses', { ...query, cursor: undefined, limit: undefined, format: 'csv' });
    let res: Response;
    try {
      res = await fetch(url, { credentials: 'include', headers: { Accept: 'text/csv' }, signal });
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') throw cause;
      throw offlineError(cause);
    }
    if (!res.ok) {
      const text = await res.text();
      let problem: Problem = { title: res.statusText, detail: text.slice(0, 300) || res.statusText };
      try {
        problem = JSON.parse(text) as Problem;
      } catch {
        /* not problem+json — keep the text */
      }
      throw new ApiError(res.status, { ...problem, status: res.status });
    }
    return {
      blob: await res.blob(),
      filename: filenameFromDisposition(res.headers.get('Content-Disposition')),
    };
  },
};

/** `attachment; filename="expenses-2026-09-01.csv"` → the filename, or ''. */
function filenameFromDisposition(header: string | null): string {
  if (!header) return '';
  const star = /filename\*=(?:UTF-8'')?([^;]+)/i.exec(header);
  if (star?.[1]) {
    try {
      return decodeURIComponent(star[1].trim().replace(/^"|"$/g, ''));
    } catch {
      /* fall through to the plain form */
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(header);
  return plain?.[1]?.trim() ?? '';
}

/** Save a fetched blob to disk under `filename`. */
export function downloadBlob(blob: Blob, filename: string): void {
  const href = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = href;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // Revoked on the next tick so the click has already been handled.
  setTimeout(() => URL.revokeObjectURL(href), 0);
}

export const unwrapExpense = (res: { expense: Expense } | Expense) => unwrap<Expense>(res, 'expense');
export const unwrapCategory = (res: { category: ExpenseCategory } | ExpenseCategory) =>
  unwrap<ExpenseCategory>(res, 'category');

/** A voided expense is read-only — no edit, no second void (FLOWS flow 12). */
export function isVoided(e: Pick<Expense, 'status'>): boolean {
  return e.status === 'voided';
}

/* ------------------------------------------------------------------ */
/* Phase 16 §16.1 — proof of payment (audience org)                     */
/* ------------------------------------------------------------------ */

/**
 * A proof is a *claim*, not money: the renter says they paid and attaches the
 * evidence. Only an accepted proof runs the allocator, and what the allocator
 * writes is the payment (SPEC §6). So nothing here computes a balance — the
 * landlord's screen puts the claim beside the schedule and the backend decides.
 */
export type ProofStatus = 'submitted' | 'accepted' | 'rejected';

/** The three statuses in the order the review queue offers them. */
export const PROOF_STATUSES: ProofStatus[] = ['submitted', 'accepted', 'rejected'];

export interface Proof {
  id: string;
  contract: ScheduleContractRef;
  /** Null when the renter did not say which instalment the money is for. */
  schedule_id: string | null;
  amount: number;
  paid_at: string;
  /** `bank_transfer` or `mobile_money_manual` — a proof is never cash. */
  method: PaymentMethod | string;
  reference: string | null;
  note: string | null;
  content_type: string;
  size_bytes: number;
  status: ProofStatus | string;
  payment_id: string | null;
  reviewed_at: string | null;
  reviewed_by_name: string | null;
  rejection_reason: string | null;
  created_at: string;
  /** Presigned read, 300 s. Only `GET /proofs/{id}` issues one. */
  view_url?: string | null;
}

/**
 * The landlord's corrections on the way through. Every field is optional: what
 * is left out keeps the renter's own claim. The body is otherwise the
 * `POST /payments` body, which is why the confirm sheet is reused unchanged.
 */
export interface ProofAcceptInput {
  amount?: number;
  schedule_id?: string;
  paid_at?: string;
  allow_overpay_rollover?: boolean;
}

/** `200 {proof, payment, schedules}` — a `PaymentResult` with the proof beside it. */
export interface ProofAcceptResult extends PaymentResult {
  proof: Proof;
}

/** How long a `view_url` lives (API.md §16.1). Refreshed a little before. */
export const PROOF_VIEW_TTL_MS = 300_000;

export const proofsApi = {
  list: (
    query: { status?: ProofStatus | ''; cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ) =>
    api.get<{ items: Proof[]; next_cursor?: string | null; status?: string }>('/proofs', {
      query,
      signal,
    }),
  summary: (signal?: AbortSignal) =>
    api.get<{ submitted_count: number }>('/proofs/summary', { signal }),
  get: (id: string, signal?: AbortSignal) =>
    api.get<{ proof: Proof }>(`/proofs/${id}`, { signal }),
  accept: (id: string, body: ProofAcceptInput) =>
    api.post<ProofAcceptResult>(`/proofs/${id}/accept`, body),
  reject: (id: string, reason: string) =>
    api.post<{ proof: Proof }>(`/proofs/${id}/reject`, { reason }),
};

/** A proof is still waiting for an answer — the only state with actions on it. */
export function isPendingProof(p: Pick<Proof, 'status'>): boolean {
  return p.status === 'submitted';
}

/* ------------------------------------------------------------------ */
/* Shapes — mirror API.md Part 2 §16.2 (CSV import) exactly.           */
/* ------------------------------------------------------------------ */

export type ImportKind = 'units' | 'renters' | 'payments';
export const IMPORT_KINDS: readonly ImportKind[] = ['units', 'renters', 'payments'] as const;

/** The server refuses anything larger — the page says so before the upload. */
export const IMPORT_MAX_BYTES = 2 * 1024 * 1024;

export type ImportBatchStatus = 'previewed' | 'committed' | 'undone';

export interface ImportBatch {
  id: string;
  kind: ImportKind;
  filename: string;
  row_count: number;
  ok_count: number;
  error_count: number;
  status: ImportBatchStatus;
  created_by: { user_id?: string; name: string } | null;
  committed_at: string | null;
  undone_at: string | null;
  /** Committed and still inside the 24 h window — the server decides, not us. */
  can_undo: boolean;
  created_at: string;
}

export interface ImportRow {
  line: number;
  raw: Record<string, string>;
  /** Keyed by column name; the key `_row` is a problem with the whole line. */
  errors: Record<string, string> | null;
  /** What the row will hit or create — names before the commit, ids after. */
  resolved: Record<string, unknown> | null;
  entity_type: string | null;
  entity_id: string | null;
}

export interface ImportTemplateColumn {
  name: string;
  required: boolean;
  example: string;
  help: string;
}

export interface ImportTemplate {
  kind: ImportKind;
  columns: ImportTemplateColumn[];
}

export interface ImportPreview {
  batch: ImportBatch;
  rows: ImportRow[];
}

export interface ImportCreatedCounts {
  units: number;
  properties: number;
  renters: number;
  contracts: number;
  payments: number;
}

export interface ImportCommitResult {
  batch: ImportBatch;
  created: ImportCreatedCounts;
}

export interface ImportUndoResult {
  batch: ImportBatch;
  undone: ImportCreatedCounts;
}

/**
 * The preview upload. `request()` cannot carry it: a FormData body must go up
 * without a `Content-Type` header of our own, so the browser can write the
 * multipart boundary. Everything else — the cookie, the problem+json parsing —
 * follows the same rules as the rest of this module.
 */
async function postMultipart<T>(path: string, form: FormData, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(buildUrl(path), {
      method: 'POST',
      credentials: 'include',
      headers: { Accept: 'application/json' },
      body: form,
      signal,
    });
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause;
    throw offlineError(cause);
  }
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

export const importsApi = {
  /** The column reference the screen prints (the `.csv`-less form of the same path). */
  template: (kind: ImportKind, signal?: AbortSignal) =>
    api.get<ImportTemplate>(`/imports/templates/${kind}`, { signal }),

  /** The blank sheet. Fetched, not linked, so the attachment name is ours to use. */
  templateCsv: async (kind: ImportKind): Promise<{ blob: Blob; filename: string }> => {
    let res: Response;
    try {
      res = await fetch(buildUrl(`/imports/templates/${kind}.csv`), {
        credentials: 'include',
        headers: { Accept: 'text/csv' },
      });
    } catch (cause) {
      throw offlineError(cause);
    }
    if (!res.ok) {
      const text = await res.text();
      let problem: Problem = { title: res.statusText, detail: text.slice(0, 300) || res.statusText };
      try {
        problem = JSON.parse(text) as Problem;
      } catch {
        /* not problem+json — keep the text */
      }
      throw new ApiError(res.status, { ...problem, status: res.status });
    }
    return {
      blob: await res.blob(),
      filename: filenameFromDisposition(res.headers.get('Content-Disposition')) || `tms-import-${kind}.csv`,
    };
  },

  preview: (kind: ImportKind, file: File, signal?: AbortSignal) => {
    const form = new FormData();
    form.set('kind', kind);
    form.set('file', file, file.name);
    return postMultipart<ImportPreview>('/imports/preview', form, signal);
  },

  commit: (id: string, skipErrors = false) =>
    api.post<ImportCommitResult>(`/imports/${id}/commit`, { skip_errors: skipErrors }),
  undo: (id: string) => api.post<ImportUndoResult>(`/imports/${id}/undo`),
  list: (query: { cursor?: string; limit?: number } = {}, signal?: AbortSignal) =>
    api.get<{ items: ImportBatch[]; next_cursor?: string | null }>('/imports', { query, signal }),
  get: (id: string, signal?: AbortSignal) => api.get<ImportPreview>(`/imports/${id}`, { signal }),
};

/**
 * A header row that does not match the template comes back as a 400 carrying
 * the two lists the screen needs. Anything else answers null.
 */
export function importHeaderMismatch(e: ApiError): { missing: string[]; unknown: string[] } | null {
  if (e.status !== 400 || e.code !== 'csv_header_mismatch') return null;
  const list = (v: unknown): string[] =>
    Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
  return { missing: list(e.body.missing), unknown: list(e.body.unknown) };
}

/** The `row_failed` 409 names the line that stopped the whole transaction. */
export function importRowFailure(e: ApiError): { line: number; column: string; reason: string } | null {
  if (e.status !== 409 || e.code !== 'row_failed') return null;
  const line = typeof e.body.line === 'number' ? e.body.line : 0;
  return {
    line,
    column: typeof e.body.column === 'string' ? e.body.column : '',
    reason: typeof e.body.reason === 'string' ? e.body.reason : e.detail,
  };
}
