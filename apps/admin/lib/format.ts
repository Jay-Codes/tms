/** Money, count and date formatting. Display only — the backend owns numbers. */

/** `250000` → `TZS 250,000`. Integer TZS, no decimals (SPEC §5 money rule). */
export function fmtTZS(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '—';
  return `TZS ${Math.round(Number(n)).toLocaleString('en-US')}`;
}

/** Thousands-separated integer; em dash when the payload left it out. */
export function fmtNum(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '—';
  return Math.round(Number(n)).toLocaleString('en-US');
}

/** RFC3339 or `YYYY-MM-DD` → `5 Sep 2026`. */
export function fmtDate(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value.length === 10 ? `${value}T00:00:00Z` : value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

/** RFC3339 → `5 Sep 2026, 14:30`. */
export function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return `${d.toLocaleDateString('en-GB', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  })}, ${d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}`;
}

/** Pretty-print a JSON blob for the audit row's before/after panel. */
export function fmtJson(value: unknown): string {
  if (value === null || value === undefined) return '—';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}
