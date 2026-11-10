const HOURLY = "hearth.hourlyforecast/v1";
const DAILY = "hearth.dailyforecast/v1";

function firstPeriod(value: unknown): { count: number; first: Record<string, unknown> } | null {
  const periods = (value as { periods?: unknown } | null)?.periods;
  if (!Array.isArray(periods) || periods.length === 0) return null;
  const first = periods[0];
  if (typeof first !== "object" || first === null) return null;
  return { count: periods.length, first: first as Record<string, unknown> };
}

/** A local calendar day from a YYYY-MM-DD string, without a timezone shift. */
function localDay(date: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  return match ? new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3])) : null;
}

/**
 * A one-line description of a forecast snapshot, such as
 * "48 hourly periods from Mon 7 PM" or "8 daily periods from Mon, Nov 9".
 * Null for other types and malformed values, so callers fall back to their
 * ordinary rendering. Forecast State is large; tables show this instead.
 */
export function forecastSummary(type: string | undefined, value: unknown): string | null {
  if (type !== HOURLY && type !== DAILY) return null;
  const parsed = firstPeriod(value);
  if (!parsed) return null;
  const { count, first } = parsed;
  if (type === HOURLY) {
    const start = typeof first.starts_at === "string" ? new Date(first.starts_at) : null;
    if (!start || Number.isNaN(start.getTime())) return null;
    const when = new Intl.DateTimeFormat("en-US", { weekday: "short", hour: "numeric" }).format(start);
    return `${count} hourly ${count === 1 ? "period" : "periods"} from ${when}`;
  }
  const day = typeof first.date === "string" ? localDay(first.date) : null;
  if (!day) return null;
  const when = new Intl.DateTimeFormat("en-US", { weekday: "short", month: "short", day: "numeric" }).format(day);
  return `${count} daily ${count === 1 ? "period" : "periods"} from ${when}`;
}

/** A State value for a compact table cell: a forecast summary, or raw JSON. */
export function stateCellText(type: string | undefined, value: unknown): string {
  return forecastSummary(type, value) ?? JSON.stringify(value) ?? String(value);
}
