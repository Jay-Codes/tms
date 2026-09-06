/**
 * SMS segment arithmetic — the same rule the tenant composer uses
 * (apps/tenant/components/NotificationBits.tsx) and the same rule the worker
 * bills against: one credit per segment, GSM-7 fitting 160 characters in a
 * single message and 153 per part once split, UCS-2 70 / 67.
 *
 * Kept as its own module (rather than imported across apps) because the two
 * frontends never import from each other; if a third copy appears it belongs
 * in @tms/ui.
 */

const GSM7 =
  "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?" +
  '¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà';

/** These cost two GSM-7 characters each (escape + character). */
const GSM7_EXTENDED = '^{}\\[~]|€';

export interface SegmentCount {
  /** Billed characters — an extended GSM character counts twice. */
  chars: number;
  segments: number;
  /** True when the body left GSM-7 and every character now costs a UCS-2 slot. */
  unicode: boolean;
  /** Characters still free in the current segment. */
  remaining: number;
}

export function smsSegments(body: string): SegmentCount {
  const unicode = [...body].some((c) => !GSM7.includes(c) && !GSM7_EXTENDED.includes(c));
  const chars = unicode
    ? [...body].length
    : [...body].reduce((n, c) => n + (GSM7_EXTENDED.includes(c) ? 2 : 1), 0);
  const single = unicode ? 70 : 160;
  const multi = unicode ? 67 : 153;
  const segments = chars === 0 ? 0 : chars <= single ? 1 : Math.ceil(chars / multi);
  const capacity = segments <= 1 ? single : segments * multi;
  return { chars, segments, unicode, remaining: Math.max(0, capacity - chars) };
}

/** The placeholders a body actually uses, in order of first appearance. */
export function usedVariables(body: string): string[] {
  const out: string[] = [];
  for (const m of body.matchAll(/\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}/g)) {
    const name = m[1];
    if (!out.includes(name)) out.push(name);
  }
  return out;
}

/** Placeholders the kind does not allow — the same check the API runs (400). */
export function unknownVariables(body: string, allowed: string[]): string[] {
  return usedVariables(body).filter((v) => !allowed.includes(v));
}

/** Prose label for a kind id: `reminder_due` → `Reminder due`. */
export function kindLabel(kind: string): string {
  const s = kind.replace(/_/g, ' ');
  return s.charAt(0).toUpperCase() + s.slice(1);
}
