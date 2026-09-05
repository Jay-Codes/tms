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
  body_html: string;
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
  link_request_id?: string;
}

/* --------------------------------- branding -------------------------------- */

export interface OrgBranding {
  display_name: string;
  logo_url: string | null;
  letterhead_url: string | null;
  theme: { primary_color: string; font_id: string };
  dashboard_prefs?: Record<string, unknown>;
  document_footer_text: string | null;
}

export interface BrandingInput {
  display_name?: string;
  theme?: { primary_color: string; font_id: string };
  dashboard_prefs?: Record<string, unknown>;
  document_footer_text?: string | null;
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
  create: (body: { name: string; body_html: string; is_default?: boolean }) =>
    api.post<{ template: ContractTemplate } | ContractTemplate>('/contract-templates', body),
  update: (id: string, body: { name?: string; body_html?: string; is_default?: boolean }) =>
    api.patch<{ template: ContractTemplate } | ContractTemplate>(`/contract-templates/${id}`, body),
  remove: (id: string) => api.del<void>(`/contract-templates/${id}`),
  preview: (id: string, sample = true) =>
    api.post<TemplatePreview>(`/contract-templates/${id}/preview`, { sample }),
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

export interface BankAccount {
  bank_name: string;
  account_name: string;
  account_number: string;
  instructions: string;
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
    api.get<{ bank_account: BankAccount | null } | BankAccount>('/org/bank-account', { signal }),
  save: (body: BankAccount) =>
    api.put<{ bank_account: BankAccount } | BankAccount>('/org/bank-account', body),
};

export const unwrapPayment = (res: { payment: Payment } | Payment) => unwrap<Payment>(res, 'payment');
export const unwrapBankAccount = (res: { bank_account: BankAccount | null } | BankAccount) =>
  unwrap<BankAccount | null>(res, 'bank_account');

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

export type NotificationStatus = 'queued' | 'sending' | 'sent' | 'failed' | string;

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
  created_at: string;
  sent_at: string | null;
}

/** `202 {batch_id, queued, skipped}` from a custom bulk send. */
export interface CustomSendResult {
  batch_id?: string;
  queued: number;
  skipped: number;
}

export interface CustomSendInput {
  recipients: 'all_active' | 'selected';
  renter_user_ids?: string[];
  body: string;
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
  };
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

export type CollectionsGroup = 'day' | 'week' | 'month';

export interface CollectionsBucket {
  start: string;
  expected: number;
  collected: number;
}

export interface CollectionsReport {
  buckets: CollectionsBucket[];
  totals: { expected: number; collected: number };
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
] as const;

export type DashboardCard = (typeof DASHBOARD_CARDS)[number];

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
    cards: cards.length ? cards : [...DASHBOARD_CARDS],
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
  /** `period` is `month` or `YYYY-MM`; omitted means the current month. */
  summary: (period?: string, signal?: AbortSignal) =>
    api.get<ReportSummary>('/reports/summary', { query: { period: period || undefined }, signal }),
  paymentStatus: (
    query: { status?: PaymentStatusValue | ''; property_id?: string } = {},
    signal?: AbortSignal,
  ) => api.get<{ items: PaymentStatusRow[] }>('/reports/payment-status', { query, signal }),
  collections: (
    query: { from: string; to: string; group: CollectionsGroup },
    signal?: AbortSignal,
  ) => api.get<CollectionsReport>('/reports/collections', { query, signal }),
  /**
   * The CSV is a download, not a fetch: the browser opens it same-origin so the
   * httpOnly `tms_o` cookie rides along and the attachment lands in Downloads.
   * Written against API_BASE (absolute path) so the '/tenant' basePath is not
   * prepended — the same rule the fetch wrapper follows.
   */
  paymentStatusCsvUrl: (query: { status?: PaymentStatusValue | ''; property_id?: string } = {}) => {
    const qs = new URLSearchParams({ format: 'csv' });
    if (query.status) qs.set('status', query.status);
    if (query.property_id) qs.set('property_id', query.property_id);
    return `${API_BASE}/reports/payment-status?${qs.toString()}`;
  },
};
