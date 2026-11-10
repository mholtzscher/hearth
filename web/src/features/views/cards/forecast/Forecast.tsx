import type { z } from "zod";
import { useUnitSystem } from "../../../units/household";
import { present, type UnitSystem } from "../../../units/units";
import { useForecast, type DailyPeriod, type HourlyPeriod } from "../../bindings/forecast";
import { CardFrame } from "../CardFrame";
import { useMinuteClock } from "../observation";
import { cardStatus } from "../status";
import { forecastSchema, forecastTypes } from "./schema";
import { conditionWords, ForecastIcon } from "./icons";
import { chartLabelStep, localDate, upcomingDays, upcomingHours } from "./upcoming";

type Props = z.infer<typeof forecastSchema>;
const hour = (date: Date) => new Intl.DateTimeFormat("en-US", { hour: "numeric" }).format(date);
const weekday = (date: Date) => new Intl.DateTimeFormat("en-US", { weekday: "short" }).format(date);
const dateValue = (date: string) => new Date(`${date}T12:00:00`);
function temperature(value: number | undefined, system: UnitSystem, full = false): string {
  if (value === undefined) return "—";
  const shown = present(value, "Cel", "number", system);
  return `${Math.round(shown.value)}${full ? shown.label : "°"}`;
}
function chance(value: number | undefined): string | undefined {
  return value !== undefined && value >= 10 ? `${Math.round(value)}%` : undefined;
}
function dailyLabel(p: DailyPeriod, now: number, system: UnitSystem): string {
  const day = p.date === localDate(now) ? "Today" : weekday(dateValue(p.date));
  const details = [p.condition && conditionWords[p.condition], `low ${temperature(p.temperature_min_c, system, true)}`, `high ${temperature(p.temperature_max_c, system, true)}`, chance(p.precipitation_probability_percent) && `${chance(p.precipitation_probability_percent)} chance of precipitation`].filter(Boolean);
  return `${day}: ${details.join(", ")}`;
}
function Daily({ periods, now, system }: { periods: DailyPeriod[]; now: number; system: UnitSystem }) {
  const lows = periods.flatMap(p => p.temperature_min_c === undefined ? [] : [p.temperature_min_c]);
  const highs = periods.flatMap(p => p.temperature_max_c === undefined ? [] : [p.temperature_max_c]);
  const min = Math.min(...lows), max = Math.max(...highs), range = max - min || 1;
  return <div className="forecast-daily-wrap"><ul className="forecast-daily">{periods.map(p => {
    const probability = chance(p.precipitation_probability_percent);
    const bar = p.temperature_min_c !== undefined && p.temperature_max_c !== undefined;
    return <li key={p.date} aria-label={dailyLabel(p, now, system)}>
      <span className="forecast-day" aria-hidden="true">{p.date === localDate(now) ? "Today" : weekday(dateValue(p.date))}</span>
      <ForecastIcon condition={p.condition} />
      <span className="forecast-chance" aria-hidden="true">{probability}</span>
      <span className="forecast-temp forecast-low" aria-hidden="true">{temperature(p.temperature_min_c, system)}</span>
      <span className="forecast-range" aria-hidden="true">{bar && <span style={{ left: `${(p.temperature_min_c! - min) / range * 100}%`, right: `${100 - (p.temperature_max_c! - min) / range * 100}%` }} />}</span>
      <span className="forecast-temp" aria-hidden="true">{temperature(p.temperature_max_c, system)}</span>
    </li>;
  })}</ul></div>;
}
function hourlyLabel(p: HourlyPeriod, index: number, system: UnitSystem): string {
  return [index === 0 ? "Now" : hour(new Date(p.starts_at)), p.condition && conditionWords[p.condition], temperature(p.temperature_c, system, true), chance(p.precipitation_probability_percent) && `${chance(p.precipitation_probability_percent)} chance of precipitation`].filter(Boolean).join(", ");
}
function Hourly({ periods, count, system }: { periods: HourlyPeriod[]; count: number; system: UnitSystem }) {
  const values = periods.map(p => p.temperature_c === undefined ? undefined : present(p.temperature_c, "Cel", "number", system).value);
  const known = values.filter((v): v is number => v !== undefined);
  const low = Math.min(...known), high = Math.max(...known);
  const peak = periods.reduce<HourlyPeriod | undefined>((best, p) => p.precipitation_probability_percent !== undefined && (best?.precipitation_probability_percent === undefined || p.precipitation_probability_percent > best.precipitation_probability_percent) ? p : best, undefined);
  const unit = present(0, "Cel", "number", system).label;
  const summary = `Next ${periods.length} hours: ${known.length ? `low ${Math.round(low)}${unit}, high ${Math.round(high)}${unit}` : "temperature unavailable"}; ${peak ? `precipitation chance peaks at ${Math.round(peak.precipitation_probability_percent!)}% at ${hour(new Date(peak.starts_at))}` : "precipitation chance unavailable"}.`;
  const width = Math.max(264, periods.length * 22), step = chartLabelStep(count);
  const x = (i: number) => 14 + i * ((width - 28) / Math.max(1, periods.length - 1));
  const y = (v: number) => 46 + (1 - (v - low) / (high - low || 1)) * 78;
  let drawing = false;
  const path = values.map((v, i) => {
    if (v === undefined) { drawing = false; return ""; }
    const command = drawing ? "L" : "M"; drawing = true;
    return `${command}${x(i).toFixed(1)},${y(v).toFixed(1)}`;
  }).join("");
  return <><div className="forecast-legend" aria-hidden="true"><span>Temperature {unit}</span><span>Chance of precipitation</span></div>
    <div className="view-chart-scroll" role="img" tabIndex={0} aria-label={summary}>
      <svg className="forecast-chart" style={{ minWidth: width }} viewBox={`0 0 ${width} 216`} aria-hidden="true">
        <line className="forecast-grid" x1="0" x2={width} y1="176" y2="176" />
        {periods.map((p, i) => {
          const date = new Date(p.starts_at), probability = p.precipitation_probability_percent;
          const midnight = date.getHours() === 0;
          return <g key={p.starts_at}>
            {midnight && <><line className="forecast-midnight" x1={x(i)} x2={x(i)} y1="10" y2="176" /><text x={x(i)} y="212" textAnchor="middle">{weekday(date)}</text></>}
            {probability !== undefined && probability >= 10 && <rect className="forecast-bar" x={x(i) - 7} y={176 - 26 * probability / 100} width="14" height={26 * probability / 100} rx="2" />}
            {i % step === 0 && <><ForecastIcon condition={p.condition} x={x(i) - 12} y={8} />{values[i] !== undefined && <text className="forecast-temp-label" x={x(i)} y={y(values[i]!) - 8} textAnchor="middle">{temperature(p.temperature_c, system)}</text>}<text x={x(i)} y="192" textAnchor="middle">{i === 0 ? "Now" : hour(date)}</text></>}
          </g>;
        })}
        <path className="forecast-line" d={path} />
      </svg>
    </div></>;
}

export function BoundForecast({ label, source, periods }: Props) {
  const selectedSystem = useUnitSystem(), system = selectedSystem ?? "metric";
  const forecast = useForecast(source), resource = selectedSystem === undefined ? { status: "loading" as const } : forecast;
  const status = cardStatus(resource), now = useMinuteClock();
  const count = periods ?? (source.entity_type === forecastTypes.hourly ? 24 : 8);
  const data = status.value === "empty" ? undefined : resource.data;
  const hours = data?.kind === "hourly" ? upcomingHours(data.periods, now, count) : [];
  const days = data?.kind === "daily" ? upcomingDays(data.periods, now, count) : [];
  const shown = data?.kind === "hourly" ? hours.map((p, i) => hourlyLabel(p, i, system)) : days.map(p => dailyLabel(p, now, system));
  const title = label ?? `Next ${count} ${source.entity_type === forecastTypes.hourly ? "hours" : "days"}`;
  const metadata = resource.metadata;
  const partial = data?.kind === "daily" && days.length > 0 && data.periods.some(p => p.date < localDate(now)) && metadata?.availability === "unavailable";
  return <CardFrame className="view-forecast" title={title} status={status.entries} observedAt={data?.observed_at}
    arrival={data ? { updatedAt: data.observed_at, shown: JSON.stringify(shown), eligible: status.glows } : undefined}
    sources={metadata ? [{ entityId: metadata.id, label: metadata.label, availability: metadata.availability, enabled: metadata.enabled }] : []}
    sourceExtra={<p>Modeled forecast from the bound Entity's Adapter; periods that have ended are hidden.</p>}>
    <div data-value={status.value} className="forecast-body">
      {!data ? <p className="view-metric-value" data-value="empty" aria-hidden="true">—</p> : data.kind === "hourly" ?
        hours.length ? <Hourly periods={hours} count={count} system={system} /> : <p>Every hour in the last forecast has passed.</p> :
        days.length ? <>{partial && <p className="forecast-note">Showing the remaining days of a forecast from {weekday(new Date(data.observed_at))}.</p>}<Daily periods={days} now={now} system={system} /></> : <p>Every day in the last forecast has passed.</p>}
    </div>
  </CardFrame>;
}
