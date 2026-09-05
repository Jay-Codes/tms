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

  constructor(status: number, problem: ProblemDetail = {}) {
    const title = problem.title || defaultTitle(status);
    super(problem.detail || title);
    this.name = 'ApiError';
    this.status = status;
    this.title = title;
    this.detail = problem.detail || '';
    this.errors = problem.errors || {};
    this.retryAfterSeconds =
      typeof problem.retry_after_seconds === 'number' ? problem.retry_after_seconds : undefined;
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
