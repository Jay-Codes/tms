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

import type { Translator } from '@tms/ui';

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
  /**
   * The `title` the backend actually sent, empty when it sent none. Screens
   * show server prose verbatim (it is already in the reader's language) and
   * translate their own fallback for everything else — see `errorMessage()`.
   */
  readonly serverTitle: string;

  constructor(status: number, problem: ProblemDetail = {}) {
    const title = problem.title || defaultTitle(status);
    super(problem.detail || title);
    this.name = 'ApiError';
    this.status = status;
    this.title = title;
    this.detail = problem.detail || '';
    this.serverTitle = problem.title || '';
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

export type UserLocale = 'sw' | 'en';

export interface User {
  id: string;
  kind: UserKind;
  phone: string;
  email: string;
  full_name: string;
  /** Phase 13: the renter's chosen UI/SMS language. Absent on older APIs. */
  locale?: UserLocale;
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
    /** Phase 13 — the language the renter picked before registering. */
    locale?: UserLocale;
  }) => api.post<SessionResponse>('/auth/register/renter', input),

  loginWithPin: (phone: string, pin: string) =>
    api.post<SessionResponse>('/auth/login', { phone, pin }),

  logout: () => api.post<void>('/auth/logout', undefined, { query: { audience: 'renter' } }),
};

/* ---------------------------------------------------------------- */
/* Shapes from API.md — Phase 2 (public) + Phase 3 (renter)           */
/* ---------------------------------------------------------------- */

/**
 * Org branding as served pre-auth; `theme` feeds `toResolvedTheme()`
 * (lib/theme.ts). Phase 12 adds the resolved token set — `preset_id`,
 * `tokens` and `dark` — alongside the v1 `primary_color`, which older
 * clients still read. Every field is optional here because the shape is
 * whatever the deployed backend serves; the adapter fills the gaps.
 */
export interface PublicBrandingTheme {
  preset_id?: string | null;
  tokens?: {
    paper?: string;
    surface?: string;
    ink?: string;
    ink_muted?: string;
    rule?: string;
    primary?: string;
    accent?: string;
  } | null;
  font_id?: string | null;
  dark?: boolean | null;
  /** v1 field, kept by the API for compatibility. */
  primary_color?: string | null;
}

export interface PublicBranding {
  display_name: string;
  logo_url: string | null;
  theme: PublicBrandingTheme;
  /**
   * Phase 13: the org's default language for renters who have not chosen one
   * (`sms_language` is the older name for the same setting). Optional — an
   * API without it simply leaves the app on its own default.
   */
  language?: string | null;
  sms_language?: string | null;
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

/** `GET /public/orgs/{slug}/branding` — the same branding, without a unit. */
export interface OrgBranding extends PublicBranding {
  org: { id: string; name: string; slug: string };
}

export const publicApi = {
  unit: (unitCode: string, signal?: AbortSignal) =>
    api.get<PublicUnit>(`/public/units/${encodeURIComponent(unitCode)}`, { signal }),

  branding: (slug: string, signal?: AbortSignal) =>
    api.get<OrgBranding>(`/public/orgs/${encodeURIComponent(slug)}/branding`, { signal }),
};

export const renterApi = {
  profile: (signal?: AbortSignal) => api.get<ProfileResponse>('/me/profile', { signal }),

  /** `PATCH /me` — Phase 13; the only field the renter app sends is `locale`. */
  updateLocale: (locale: UserLocale) => api.patch<{ user?: User }>('/me', { locale }),

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

  /* Phase 5 — payments (shapes declared further down; types hoist). */

  /** `GET /me/schedules` — ledger, next due, overdue total, bank account. */
  schedules: (signal?: AbortSignal) => api.get<MySchedulesResponse>('/me/schedules', { signal }),

  /** `GET /me/payments` — the renter's own receipts, newest first. */
  payments: (signal?: AbortSignal) => api.get<MyPaymentsResponse>('/me/payments', { signal }),

  /* Phase 16 — proof of payment (API.md Part 2 §16.1). */

  /** `POST /me/proofs/upload` — presigned PUT ticket for the receipt file. */
  proofUploadTicket: (contractId: string, contentType: string, sizeBytes: number) =>
    api.post<ProofUploadTicket>('/me/proofs/upload', {
      contract_id: contractId,
      content_type: contentType,
      size_bytes: sizeBytes,
    }),

  /** `POST /me/proofs` — the claim itself, once the object is in the bucket. */
  createProof: (input: ProofInput) => api.post<{ proof: Proof }>('/me/proofs', input),

  /** `GET /me/proofs` — the renter's own claims, newest first. */
  proofs: (signal?: AbortSignal) => api.get<MyProofsResponse>('/me/proofs', { signal }),

  /** `DELETE /me/proofs/{id}` — withdraw, only while `submitted`. */
  withdrawProof: (id: string) => api.del<void>(`/me/proofs/${encodeURIComponent(id)}`),
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
    // No prose: the code is what the screen translates (lib/format).
    throw new ApiError(res.status, { type: 'client/upload_failed' });
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
  /** The unit price basis: `rent_amount` per `rent_period_days`. */
  rent_amount: number;
  rent_period_days: number;
  /**
   * The same rent scaled to one payment period — what the renter actually
   * hands over each time (SPEC §6, "rent per payment period"). Optional:
   * an older API omits it, and then the unit price is all we can show.
   */
  rent_per_period?: number;
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
  contract: {
    id: string;
    unit_name: string;
    property_name?: string | null;
    org_name?: string | null;
    status?: ContractStatus | string;
  };
  /** Days past `due_date`; only meaningful while `status === 'overdue'`. */
  days_overdue?: number;
  /**
   * Phase 16: days left until `due_date` on the Dar es Salaam wall clock —
   * `0` today, negative once past. Absent on an older API, where the screens
   * fall back to counting from `due_date` in the reader's own timezone.
   */
  days_until_due?: number;
  /** Phase 16: the caller's newest still-`submitted` proof for this row. */
  proof?: ScheduleProof | null;
}

export interface MySchedulesResponse {
  items: MySchedule[];
  next_due: MySchedule | null;
  /** Phase 5: sum of what is owed on overdue rows. Absent before Phase 5. */
  overdue_total?: number;
  /** Phase 5: where to send the money (API.md `GET /org/bank-account`). */
  bank_account?: BankAccount | null;
  /**
   * Phase 16: the same wallet as `bank_account.mobile_money`, repeated at the
   * top level because an org may take mobile money and no bank transfer.
   */
  mobile_money?: MobileMoney | null;
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

/* ---------------------------------------------------------------- */
/* Shapes from API.md — Phase 5 (offline payments & statuses)         */
/* ---------------------------------------------------------------- */

/**
 * The org's collection account, as shown to a renter on the payment screen
 * (FLOWS 7 step 2). Read-only here: only the landlord can edit it.
 */
export interface BankAccount {
  bank_name: string;
  account_name: string;
  account_number: string;
  instructions: string;
  /** Phase 16 (§16.4): the org's mobile-money wallet, when it has one. */
  mobile_money?: MobileMoney | null;
}

/** A mobile-money wallet as the landlord published it (API.md §16.4). */
export interface MobileMoney {
  provider: string;
  number: string;
  name: string;
}

/** How the money actually moved. The renter never picks this — the landlord does. */
export type PaymentMethod = 'cash' | 'bank_transfer' | 'mobile_money_manual';

export type PaymentStatus = 'recorded' | 'reversed';

/** One schedule row a payment was applied to (API.md `applied[]`). */
export interface PaymentApplication {
  schedule_id: string;
  amount: number;
}

/**
 * `GET /me/payments` — a receipt in the renter's own rent book. Landlords
 * record these; the renter only ever reads them, reversals included, because
 * a reversed row that vanished would be a worse surprise than a stamped one.
 */
export interface MyPayment {
  id: string;
  contract_id: string;
  schedule_id?: string | null;
  amount: number;
  method: PaymentMethod | string;
  reference?: string | null;
  paid_at: string;
  note?: string | null;
  status: PaymentStatus;
  recorded_by?: { name: string } | null;
  reversed_at?: string | null;
  reversal_reason?: string | null;
  applied?: PaymentApplication[];
  created_at?: string;
  /** Denormalized on the wire so a receipt names its unit without a join. */
  unit_name?: string | null;
  property_name?: string | null;
}

export interface MyPaymentsResponse {
  items: MyPayment[];
  next_cursor?: string | null;
}

/** Human wording for `payment.method`, in the reader's language. */
export function paymentMethodLabel(t: Translator, method: string): string {
  if (method === 'cash' || method === 'bank_transfer' || method === 'mobile_money_manual') {
    return t(`payments.method.${method}`);
  }
  return method.replace(/_/g, ' ');
}

/** What is still owed on a schedule row. Never negative. */
export function scheduleOutstanding(s: Pick<PaymentSchedule, 'amount' | 'paid_amount'>): number {
  return Math.max(0, s.amount - (s.paid_amount ?? 0));
}

/* ---------------------------------------------------------------- */
/* Shapes from API.md — Phase 16 (proof of payment)                   */
/* ---------------------------------------------------------------- */

/**
 * A proof is a *claim*, never a fact: the renter says they paid and attaches
 * the receipt, the landlord accepts or rejects it, and only an accepted proof
 * ever turns into a payment on the ledger (SPEC §6).
 */
export type ProofStatus = 'submitted' | 'accepted' | 'rejected';

/** The two ways money can have moved when a renter sends proof of it. */
export type ProofMethod = 'bank_transfer' | 'mobile_money_manual';

/** The proof stub `GET /me/schedules` hangs off `next_due`. */
export interface ScheduleProof {
  id: string;
  status: ProofStatus;
}

export interface Proof {
  id: string;
  contract: {
    id: string;
    unit_name: string;
    property_name?: string | null;
    renter_name?: string | null;
    renter_user_id?: string | null;
  };
  schedule_id?: string | null;
  amount: number;
  paid_at: string;
  method: ProofMethod | string;
  reference?: string | null;
  note?: string | null;
  content_type: string;
  size_bytes: number;
  status: ProofStatus;
  payment_id?: string | null;
  reviewed_at?: string | null;
  reviewed_by_name?: string | null;
  rejection_reason?: string | null;
  created_at: string;
}

export interface MyProofsResponse {
  items: Proof[];
  next_cursor?: string | null;
}

/** `POST /me/proofs/upload` — the KYC ticket plus the id the row will carry. */
export interface ProofUploadTicket extends UploadTicket {
  proof_id: string;
  expires_in?: number;
}

export interface ProofInput {
  contract_id: string;
  schedule_id?: string;
  amount: number;
  /** RFC3339; the sheet sends the chosen day at local midnight. */
  paid_at: string;
  method: ProofMethod;
  reference?: string;
  note?: string;
  object_key: string;
}

/** Uploads a renter may send as proof, and the ceiling the API enforces. */
export const PROOF_TYPES = ['image/jpeg', 'image/png', 'application/pdf'] as const;
export const PROOF_MAX_BYTES = 5 * 1024 * 1024;
