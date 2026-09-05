/**
 * UX-only helpers: phone shaping and error copy.
 *
 * None of this is business logic — the Go API normalizes and validates
 * everything again. This exists so a renter finds out about a typo before
 * spending an SMS.
 */

import { ApiError } from './api';

/**
 * Normalize a Tanzanian mobile number to E.164 (`+2557XXXXXXXX`).
 * Accepts `07…`, `7…`, `255…`, `+255…`, with spaces or dashes.
 * Returns null when the input can't be read as a TZ mobile number.
 */
export function normalizePhone(input: string): string | null {
  const digits = input.replace(/[^\d+]/g, '');
  let rest: string;

  if (digits.startsWith('+255')) rest = digits.slice(4);
  else if (digits.startsWith('255')) rest = digits.slice(3);
  else if (digits.startsWith('0')) rest = digits.slice(1);
  else if (/^[67]/.test(digits)) rest = digits;
  else return null;

  if (!/^[67]\d{8}$/.test(rest)) return null;
  return `+255${rest}`;
}

export function isValidPhone(input: string): boolean {
  return normalizePhone(input) !== null;
}

/** `+255712345678` → `0712 345 678` for display back to the renter. */
export function displayPhone(e164: string): string {
  const m = /^\+255(\d{3})(\d{3})(\d{3})$/.exec(e164);
  if (!m) return e164;
  return `0${m[1]} ${m[2]} ${m[3]}`;
}

export const PIN_PATTERN = /^\d{4,6}$/;

export function isValidPin(pin: string): boolean {
  return PIN_PATTERN.test(pin);
}

/**
 * One line of copy for any thrown error. 429s get the "try again in N
 * minutes" wording the flows call for; the backend's own `detail` wins
 * whenever it sent one.
 */
export function errorMessage(err: unknown, fallbackRetryMinutes = 10): string {
  if (err instanceof ApiError) {
    if (err.status === 429) {
      if (err.detail) return err.detail;
      const minutes = err.retryAfterSeconds
        ? Math.max(1, Math.ceil(err.retryAfterSeconds / 60))
        : fallbackRetryMinutes;
      return `Too many attempts, try again in ${minutes} minute${minutes === 1 ? '' : 's'}.`;
    }
    return err.userMessage;
  }
  if (err instanceof Error) return err.message;
  return 'Something went wrong. Please try again.';
}

/** Seconds → `1:05`, for the OTP resend cooldown. */
export function countdown(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return m > 0 ? `${m}:${String(s).padStart(2, '0')}` : `${s}s`;
}
