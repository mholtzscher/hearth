/** Date in the viewer's calendar, not UTC. */
export function localDate(now: number): string {
  const date = new Date(now);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}
export function upcomingHours<T extends { starts_at: string }>(periods: T[], now: number, count: number): T[] {
  return periods.filter(period => Date.parse(period.starts_at) + 3_600_000 > now).slice(0, count);
}
export function upcomingDays<T extends { date: string }>(periods: T[], now: number, count: number): T[] {
  return periods.filter(period => period.date >= localDate(now)).slice(0, count);
}
export function chartLabelStep(count: number): number {
  return count <= 6 ? 1 : count <= 12 ? 2 : count <= 24 ? 3 : 6;
}
