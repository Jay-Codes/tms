/**
 * Thin fetch wrapper around the Go REST API (see API.md).
 *
 * - Base is `/api/v1` on the same origin, resolved from the origin root and
 *   NOT from the app's `basePath` (`/enduser`) — the proxy routes `/api/*`
 *   straight to the Go service.
 * - Cookies are the auth mechanism (`tms_r` for renters), so every request
 *   sends `credentials: 'include'`.
 * - Errors arrive as RFC-7807 `application/problem+json` and are re-thrown as
 *   `ApiError`.
 *
 * No business logic lives here — this is transport only.
 */

export const API_BASE = '/api/v1';

export interface ProblemDetail {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  errors?: Record<string, string>;
  /** Present on some 429s; seconds until the caller may retry. */
  retry_after_seconds?: number;
}

/** Typed error thrown for any non-2xx response (and for network failures). */
export class ApiError extends Error {
  readonly status: number;
  readonly title: string;
  readonly detail: string;
  readonly errors: Record<string, string>;
  readonly retryAfterSeconds?: number;
  /** RFC-7807 `type`, verbatim. */
  readonly type: string;

  constructor(status: number, problem: ProblemDetail = {}) {
    const title = problem.title || defaultTitle(status);
    super(problem.detail || title);
    this.name = 'ApiError';
    this.status = status;
    this.title = title;
    this.detail = problem.detail || '';
    this.errors = problem.errors || {};
    this.type = problem.type || '';
    this.retryAfterSeconds =
      typeof problem.retry_after_seconds === 'number' ? problem.retry_after_seconds : undefined;
  }

  /**
   * Machine-readable problem code — the last segment of `type`
   * (`.../unit_occupied` → `unit_occupied`). Screens branch on this rather
   * than on prose, since only the status + code are contract.
   */
  get code(): string {
    if (!this.type) return '';
    const trimmed = this.type.replace(/\/+$/, '');
    return trimmed.slice(trimmed.lastIndexOf('/') + 1);
  }

  /** True when the backend named this exact problem code. */
  is(code: string): boolean {
    return this.code === code;
  }

  /** Message worth showing a renter: prefer `detail`, fall back to `title`. */
  get userMessage(): string {
    return this.detail || this.title;
  }

  /** First field-level message, if the backend sent one. */
  fieldError(field: string): string | undefined {
    return this.errors[field];
  }
}

function defaultTitle(status: number): string {
  if (status === 0) return 'Cannot reach the server. Check your connection.';
  if (status === 401) return 'Not signed in';
  if (status === 403) return 'Not allowed';
  if (status === 404) return 'Not found';
  if (status === 409) return 'Already exists';
  if (status === 429) return 'Too many attempts';
  if (status >= 500) return 'Something went wrong on our side';
  return 'Request failed';
}

export type Query = Record<string, string | number | boolean | undefined>;

interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Query;
  signal?: AbortSignal;
}

function withQuery(path: string, query?: Query): string {
  if (!query) return API_BASE + path;
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined) qs.set(k, String(v));
  }
  const s = qs.toString();
  return API_BASE + path + (s ? `?${s}` : '');
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, query, signal } = opts;

  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let res: Response;
  try {
    res = await fetch(withQuery(path, query), {
      method,
      headers,
      credentials: 'include',
      signal,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err;
    throw new ApiError(0);
  }

  if (res.status === 204 || res.status === 205) return undefined as T;

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
    const problem: ProblemDetail =
      parsed && typeof parsed === 'object' ? (parsed as ProblemDetail) : {};
    throw new ApiError(res.status, problem);
  }

  return parsed as T;
}

export const api = {
  get: <T>(path: string, opts: Omit<RequestOptions, 'method' | 'body'> = {}) =>
    request<T>(path, { ...opts, method: 'GET' }),
  post: <T>(path: string, body?: unknown, opts: Omit<RequestOptions, 'method' | 'body'> = {}) =>
    request<T>(path, { ...opts, method: 'POST', body }),
  put: <T>(path: string, body?: unknown, opts: Omit<RequestOptions, 'method' | 'body'> = {}) =>
    request<T>(path, { ...opts, method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown, opts: Omit<RequestOptions, 'method' | 'body'> = {}) =>
    request<T>(path, { ...opts, method: 'PATCH', body }),
  del: <T>(path: string, opts: Omit<RequestOptions, 'method' | 'body'> = {}) =>
    request<T>(path, { ...opts, method: 'DELETE' }),
};

/* ---------------------------------------------------------------- */
/* Shapes from API.md — Phase 1                                       */
/* ---------------------------------------------------------------- */

export type UserKind = 'renter' | 'org_user' | 'platform_admin';

export interface User {
  id: string;
  kind: UserKind;
  phone: string;
  email: string;
  full_name: string;
  email_verified: boolean;
  status: string;
  created_at: string;
}

export interface Org {
  id: string;
  name: string;
  slug: string;
  role?: string;
}

export interface MeResponse {
  user: User;
  org: Org | null;
}

export type OtpPurpose = 'register' | 'login' | 'sign';

export interface OtpSendResponse {
  phone: string;
  resend_after_seconds: number;
}

export interface OtpVerifyRegisterResponse {
  otp_token: string;
}

export interface SessionResponse {
  user: User;
  org?: Org | null;
}

export const authApi = {
  me: (signal?: AbortSignal) =>
    api.get<MeResponse>('/auth/me', { query: { audience: 'renter' }, signal }),

  otpSend: (phone: string, purpose: OtpPurpose) =>
    api.post<OtpSendResponse>('/auth/otp/send', { phone, purpose }),

  otpVerifyRegister: (phone: string, code: string) =>
    api.post<OtpVerifyRegisterResponse>('/auth/otp/verify', {
      phone,
      code,
      purpose: 'register' as const,
    }),

  otpVerifyLogin: (phone: string, code: string) =>
    api.post<SessionResponse>('/auth/otp/verify', {
      phone,
      code,
      purpose: 'login' as const,
    }),

  registerRenter: (input: {
    phone: string;
    otp_token: string;
    pin: string;
    full_name: string;
  }) => api.post<SessionResponse>('/auth/register/renter', input),

  loginWithPin: (phone: string, pin: string) =>
    api.post<SessionResponse>('/auth/login', { phone, pin }),

  logout: () => api.post<void>('/auth/logout', undefined, { query: { audience: 'renter' } }),
};

/* ---------------------------------------------------------------- */
/* Shapes from API.md — Phase 2 (public) + Phase 3 (renter)           */
/* ---------------------------------------------------------------- */

/** Org branding as served pre-auth; `theme` feeds `applyOrgTheme()`. */
export interface PublicBranding {
  display_name: string;
  logo_url: string | null;
  theme: { primary_color: string; font_id: string };
}

/**
 * A payment period the landlord offers for this unit. `amount` is the
 * server's proration of the unit price over `days`; it is `null` when the
 * unit has no price yet.
 */
export interface OfferedPeriod {
  id: string;
  label: string;
  days: number;
  is_recommended: boolean;
  amount: number | null;
}

export interface UnitPrice {
  amount: number;
  currency: string;
  period_days: number;
}

/** `GET /public/units/{unit_code}` — the QR landing payload. */
export interface PublicUnit {
  org: { id: string; name: string; slug: string };
  branding: PublicBranding;
  property: { name: string; location_text: string | null };
  unit: { id: string; name: string; status: string };
  price: UnitPrice | null;
  periods: OfferedPeriod[];
  occupied: boolean;
}

export type KycStatus = 'none' | 'submitted' | 'verified';

export interface RenterProfile {
  full_name: string;
  /** Server-masked (`••••••••1234`); the raw NIDA never leaves the backend. */
  nida_masked: string | null;
  next_of_kin_name: string;
  next_of_kin_phone: string;
  email: string;
  kyc_status: KycStatus;
  kyc_doc_uploaded: boolean;
  updated_at: string;
}

export interface ProfileResponse {
  user: { id: string; phone: string; full_name: string; email: string };
  profile: RenterProfile;
}

export interface ProfileInput {
  full_name: string;
  /** Omitted or empty keeps the stored number. */
  nida_number?: string;
  next_of_kin_name: string;
  next_of_kin_phone: string;
  email?: string;
}

/** Presigned PUT ticket (KYC photo, drawn signature — same shape everywhere). */
export interface UploadTicket {
  upload_url: string;
  object_key: string;
  headers: Record<string, string>;
}

/** @deprecated name kept for the KYC call sites. */
export type KycUploadTicket = UploadTicket;

export type LinkRequestStatus = 'pending' | 'approved' | 'rejected' | 'cancelled';

/** Server-computed schedule summary — authoritative over any client preview. */
export interface SchedulePreview {
  count: number;
  first_due: string;
  amount_first: number;
  amount_last: number;
  total: number;
}

export interface LinkRequest {
  id: string;
  unit: { id: string; name: string; property_name: string };
  org: { name: string; slug: string };
  status: LinkRequestStatus;
  payment_period: { id: string; label: string; days: number; amount: number };
  term_days: number;
  start_date: string;
  end_date: string;
  schedule_preview: SchedulePreview;
  created_at: string;
  rejection_reason?: string | null;
}

export interface LinkRequestInput {
  payment_period_id: string;
  term_days: number;
  start_date: string;
  accepted_terms: true;
}

export const publicApi = {
  unit: (unitCode: string, signal?: AbortSignal) =>
    api.get<PublicUnit>(`/public/units/${encodeURIComponent(unitCode)}`, { signal }),
};

export const renterApi = {
  profile: (signal?: AbortSignal) => api.get<ProfileResponse>('/me/profile', { signal }),

  saveProfile: (input: ProfileInput) => api.put<{ profile: RenterProfile }>('/me/profile', input),

  kycUploadTicket: (contentType: string, sizeBytes: number) =>
    api.post<KycUploadTicket>('/me/profile/kyc-upload', {
      content_type: contentType,
      size_bytes: sizeBytes,
    }),

  kycUploadComplete: (objectKey: string) =>
    api.post<{ profile: RenterProfile }>('/me/profile/kyc-upload/complete', {
      object_key: objectKey,
    }),

  createLinkRequest: (unitCode: string, input: LinkRequestInput) =>
    api.post<{ request: LinkRequest }>(`/units/${encodeURIComponent(unitCode)}/link`, input),

  linkRequests: (signal?: AbortSignal) =>
    api.get<{ items: LinkRequest[] }>('/me/link-requests', { signal }),

  cancelLinkRequest: (id: string) => api.del<void>(`/me/link-requests/${encodeURIComponent(id)}`),
};

/**
 * Push the chosen ID photo straight at MinIO with the presigned URL the
 * backend just handed out. Not an API call — hence the raw `fetch` and the
 * deliberate absence of `credentials` (a signed URL must stay cookie-free).
 */
export async function uploadToPresignedUrl(
  ticket: UploadTicket,
  body: Blob,
): Promise<void> {
  let res: Response;
  try {
    res = await fetch(ticket.upload_url, {
      method: 'PUT',
      headers: ticket.headers ?? {},
      body,
    });
  } catch {
    throw new ApiError(0);
  }
  if (!res.ok) {
    throw new ApiError(res.status, {
      title: 'Upload failed',
      detail: 'That file could not be uploaded. Please try again.',
    });
  }
}

/* ---------------------------------------------------------------- */
/* Shapes from API.md — Phase 4 (contracts, signing, schedules)       */
/* ---------------------------------------------------------------- */

export type ContractStatus =
  | 'draft'
  | 'pending_signature'
  | 'active'
  | 'expiring'
  | 'ended'
  | 'terminated';

export type SignatureParty = 'renter' | 'landlord';

/** `otp_accept` = tapped Accept & sign; `drawn` = also drew a signature. */
export type SignatureMethod = 'otp_accept' | 'drawn';

export interface ContractSignature {
  party: SignatureParty;
  name: string;
  signed_at: string;
  method: SignatureMethod | string;
  /** Server-masked; the raw number never reaches the document. */
  phone_masked?: string | null;
  has_image?: boolean;
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
  renter: { user_id: string; full_name: string; phone: string };
  template_id?: string | null;
  status: ContractStatus;
  rent_amount: number;
  rent_period_days: number;
  payment_period: { id: string; label: string; days: number };
  term_days: number;
  start_date: string;
  end_date: string;
  due_day?: number | null;
  /** sha256 over the snapshotted terms + commercial facts (SPEC §5.5). */
  snapshot_hash: string;
  signatures: ContractSignature[];
  link_request_id?: string | null;
  created_at: string;
  activated_at?: string | null;
  terminated_at?: string | null;
  termination_reason?: string | null;
  schedules_summary?: SchedulesSummary | null;
}

/** One line of the payment schedule shown inside the document. */
export interface DocumentScheduleRow {
  period_start: string;
  period_end: string;
  due_date: string;
  amount: number;
}

export interface DocumentSignature extends ContractSignature {
  /** Presigned GET for the drawn PNG, when one was uploaded. */
  signature_image_url?: string | null;
}

/**
 * `GET /contracts/{id}/document` — everything needed to paint (and print) the
 * agreement. `terms_html` is the server-sanitized snapshot; it is rendered
 * verbatim, never re-derived here.
 */
export interface ContractDocument {
  contract_id: string;
  status: ContractStatus;
  org: {
    display_name: string;
    logo_url?: string | null;
    letterhead_url?: string | null;
    footer_text?: string | null;
  };
  parties: {
    landlord: { name: string };
    renter: { name: string; phone_masked?: string | null };
  };
  terms_html: string;
  schedule: DocumentScheduleRow[];
  signatures: DocumentSignature[];
  snapshot_hash: string;
  generated_at: string;
}

export interface VerifyResponse {
  valid: boolean;
  computed_hash: string;
  stored_hash: string;
  signatures: ContractSignature[];
}

export type ScheduleStatus = 'pending' | 'paid' | 'partial' | 'overdue' | 'waived';

export interface PaymentSchedule {
  id: string;
  period_start: string;
  period_end: string;
  due_date: string;
  amount: number;
  status: ScheduleStatus;
  paid_amount: number;
}

/** `GET /me/schedules` rows carry the contract they belong to. */
export interface MySchedule extends PaymentSchedule {
  contract: { id: string; unit_name: string };
}

export interface MySchedulesResponse {
  items: MySchedule[];
  next_due: MySchedule | null;
}

export interface SignInput {
  otp_code: string;
  /** Key returned by `/signature-upload`, once the PNG is in the bucket. */
  signature_object_key?: string;
}

export const contractApi = {
  mine: (signal?: AbortSignal) =>
    api.get<{ items: Contract[]; next_cursor?: string | null }>('/me/contracts', { signal }),

  get: (id: string, signal?: AbortSignal) =>
    api.get<{ contract: Contract }>(`/contracts/${encodeURIComponent(id)}`, { signal }),

  document: (id: string, signal?: AbortSignal) =>
    api.get<ContractDocument>(`/contracts/${encodeURIComponent(id)}/document`, { signal }),

  verify: (id: string, signal?: AbortSignal) =>
    api.get<VerifyResponse>(`/contracts/${encodeURIComponent(id)}/verify`, { signal }),

  schedules: (id: string, signal?: AbortSignal) =>
    api.get<{ items: PaymentSchedule[] }>(`/contracts/${encodeURIComponent(id)}/schedules`, {
      signal,
    }),

  sendSignOtp: (id: string) =>
    api.post<{ resend_after_seconds: number }>(`/contracts/${encodeURIComponent(id)}/sign/otp`),

  signatureUploadTicket: (id: string, sizeBytes: number) =>
    api.post<UploadTicket>(`/contracts/${encodeURIComponent(id)}/signature-upload`, {
      content_type: 'image/png',
      size_bytes: sizeBytes,
    }),

  sign: (id: string, input: SignInput) =>
    api.post<{ contract: Contract }>(`/contracts/${encodeURIComponent(id)}/sign`, input),

  mySchedules: (signal?: AbortSignal) =>
    api.get<MySchedulesResponse>('/me/schedules', { signal }),
};

/** True once the renter's own signature row exists on this contract. */
export function hasRenterSignature(c: Pick<Contract, 'signatures'>): boolean {
  return (c.signatures ?? []).some((s) => s.party === 'renter');
}

/** True once the landlord has countersigned (i.e. activated). */
export function hasLandlordSignature(c: Pick<Contract, 'signatures'>): boolean {
  return (c.signatures ?? []).some((s) => s.party === 'landlord');
}

/** Contracts the renter still has to sign — the home screen's nudge. */
export function needsRenterSignature(c: Contract): boolean {
  return c.status === 'pending_signature' && !hasRenterSignature(c);
}
