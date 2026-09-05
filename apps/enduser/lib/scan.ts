/**
 * The unit code a renter arrived with.
 *
 * A QR scan lands on `/u/{unit_code}` while the renter is still anonymous.
 * `?next=` carries them back through register/login, but a renter who wanders
 * off that path (taps "Create an account" from a bare login screen, say) would
 * otherwise lose the unit entirely. sessionStorage keeps it for the length of
 * the browser tab — no longer, because a stale code on a shared phone would be
 * worse than none.
 *
 * Storage can throw (private mode, blocked site data), so every access is
 * guarded and simply degrades to "no remembered unit".
 */

const KEY = 'tms.scan.unit_code';

export function rememberScannedUnit(unitCode: string): void {
  try {
    sessionStorage.setItem(KEY, unitCode);
  } catch {
    /* no session storage — `?next=` still covers the normal path */
  }
}

export function readScannedUnit(): string | null {
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function forgetScannedUnit(): void {
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    /* nothing to forget */
  }
}
