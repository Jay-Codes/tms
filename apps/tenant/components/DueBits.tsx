"use client";

/**
 * Phase 16 §16.3 — the small marks that put "when is the next rent due" on
 * screens that used to make a landlord open a contract to find out.
 *
 * Every figure printed here comes from the backend (`next_due_date`,
 * `next_due_amount`, `overdue_amount` on `/renters`, `next_due_date` on
 * `/units`). The only thing decided in the browser is whether a date has
 * already passed, which is a matter of drawing it in red — not of money.
 */

import { useT } from "@tms/ui";
import { fmtDate, fmtTZS, todayISO } from "../lib/format";

/** `YYYY-MM-DD` of the value, whatever spelling the API used for it. */
function dayOf(value: string | null | undefined): string | null {
  if (!value) return null;
  const d = value.slice(0, 10);
  return /^\d{4}-\d{2}-\d{2}$/.test(d) ? d : null;
}

/** Today in Dar es Salaam — the calendar every due date is written on. */
function todayEAT(): string {
  try {
    return new Intl.DateTimeFormat("en-CA", {
      timeZone: "Africa/Dar_es_Salaam",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).format(new Date());
  } catch {
    return todayISO();
  }
}

/** True once the due date is behind us. Dates sort as strings, so no parsing. */
export function isPastDue(value: string | null | undefined): boolean {
  const d = dayOf(value);
  return d !== null && d < todayEAT();
}

/**
 * The renter directory's "Next due" cell, and the same line on the renter's
 * own record. An overdue balance wins the space: what the landlord needs to
 * see first is the arrears figure, with the next instalment under it.
 */
export function NextDueCell({
  date,
  amount,
  overdue,
}: {
  date: string | null | undefined;
  amount: number | null | undefined;
  overdue: number | null | undefined;
}) {
  const t = useT();
  const arrears = Number(overdue ?? 0);
  const late = arrears > 0;

  if (!date && !late)
    return <span style={{ color: "var(--ink-faint)" }}>—</span>;

  return (
    <span style={{ display: "grid", gap: 2 }}>
      {late ? (
        <span style={{ color: "var(--stamp-overdue)", fontWeight: 600 }}>
          {t("due.overdue_by", { amount: fmtTZS(arrears) })}
        </span>
      ) : null}
      {date ? (
        <span
          style={{
            color: late
              ? "var(--ink-soft)"
              : isPastDue(date)
                ? "var(--stamp-overdue)"
                : "var(--ink)",
            fontSize: late ? "var(--text-sm)" : undefined,
          }}
        >
          {fmtDate(date)}
          {amount === null || amount === undefined ? null : (
            <span style={{ color: "var(--ink-soft)" }}>
              {" "}
              · {fmtTZS(amount)}
            </span>
          )}
        </span>
      ) : null}
    </span>
  );
}

/** The vacancy board's chip: "Due 3 Oct 2026", stamped red once it is late. */
export function DueChip({ date }: { date: string | null | undefined }) {
  const t = useT();
  if (!date) return null;
  const late = isPastDue(date);
  return (
    <span
      className={late ? "stamp stamp-overdue" : "stamp"}
      style={
        late
          ? undefined
          : { color: "var(--ink-soft)", borderColor: "var(--ink-soft)" }
      }
      title={t(late ? "due.chip.late_title" : "due.chip.title")}
    >
      {t("due.chip", { date: fmtDate(date) })}
    </span>
  );
}
