/** Date in the viewer's calendar, not UTC. */
export const localDate = (now: number): string => {
  const date = new Date(now);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
};
export const upcomingHours = <T extends { starts_at: string }>(
  periods: T[],
  now: number,
  count: number
): T[] =>
  periods
    .filter((period) => Date.parse(period.starts_at) + 3_600_000 > now)
    .slice(0, count);
export const upcomingDays = <T extends { date: string }>(
  periods: T[],
  now: number,
  count: number
): T[] =>
  periods.filter((period) => period.date >= localDate(now)).slice(0, count);
export const chartLabelStep = (count: number): number => {
  if (count <= 6) {
    return 1;
  }
  if (count <= 12) {
    return 2;
  }
  if (count <= 24) {
    return 3;
  }
  return 6;
};
