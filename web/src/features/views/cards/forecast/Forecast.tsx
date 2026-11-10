import type { z } from "zod";
import type { Quantity } from "../../bindings/quantity";
import { useForecast, type DailyPeriod, type HourlyPeriod } from "../../bindings/forecast";
import { CardFrame } from "../CardFrame";
import { useMinuteClock } from "../observation";
import { cardStatus } from "../status";
import { forecastSchema, forecastTypes } from "./schema";
import { conditionWords, ForecastIcon } from "./icons";
import { chartLabelStep, localDate, upcomingDays, upcomingHours } from "./upcoming";

type Props = z.infer<typeof forecastSchema>;
// Periods start on the hour in UTC; half-hour and quarter-hour zones keep their minutes.
const hour = (date: Date) => new Intl.DateTimeFormat("en-US", date.getMinutes() ? { hour: "numeric", minute: "2-digit" } : { hour: "numeric" }).format(date);
const weekday = (date: Date) => new Intl.DateTimeFormat("en-US", { weekday: "short" }).format(date);
const dateValue = (date: string) => new Date(`${date}T12:00:00`);
function temperature(value: number | undefined, quantity: Quantity | undefined): string {
  return value === undefined || !quantity ? "—" : `${value.toFixed(quantity.fraction_digits)}${quantity.label}`;
}
function chance(value: number | undefined, quantity: Quantity | undefined): string | undefined {
  return value !== undefined && value >= 10 && quantity ? `${value.toFixed(quantity.fraction_digits)}${quantity.label}` : undefined;
}
function dailyLabel(p: DailyPeriod, now: number): string {
  const day = p.date === localDate(now) ? "Today" : weekday(dateValue(p.date));
  const details = [p.condition && conditionWords[p.condition], `low ${temperature(p.temperature_min, p.quantities.temperature_min)}`, `high ${temperature(p.temperature_max, p.quantities.temperature_max)}`, chance(p.precipitation_probability, p.quantities.precipitation_probability) && `${chance(p.precipitation_probability, p.quantities.precipitation_probability)} chance of precipitation`].filter(Boolean);
  return `${day}: ${details.join(", ")}`;
}
function Daily({ periods, now }: { periods: DailyPeriod[]; now: number }) {
  const values = periods.flatMap(p => [p.temperature_min, p.temperature_max].filter((v): v is number => v !== undefined));
  const min = Math.min(...values), max = Math.max(...values), range = max - min || 1;
  return <div className="forecast-daily-wrap"><ul className="forecast-daily">{periods.map(p => {
    const probability = chance(p.precipitation_probability, p.quantities.precipitation_probability);
    const bar = p.temperature_min !== undefined && p.temperature_max !== undefined;
    return <li key={p.date} aria-label={dailyLabel(p, now)}>
      <span className="forecast-day" aria-hidden="true">{p.date === localDate(now) ? "Today" : weekday(dateValue(p.date))}</span>
      <ForecastIcon condition={p.condition} />
      <span className="forecast-chance" aria-hidden="true">{probability}</span>
      <span className="forecast-temp forecast-low" aria-hidden="true">{temperature(p.temperature_min, p.quantities.temperature_min)}</span>
      <span className="forecast-range" aria-hidden="true">{bar && <span style={{ left: `${(p.temperature_min! - min) / range * 100}%`, right: `${100 - (p.temperature_max! - min) / range * 100}%` }} />}</span>
      <span className="forecast-temp" aria-hidden="true">{temperature(p.temperature_max, p.quantities.temperature_max)}</span>
    </li>;
  })}</ul></div>;
}
function hourlyLabel(p: HourlyPeriod, index: number): string {
  return [index === 0 ? "Now" : hour(new Date(p.starts_at)), p.condition && conditionWords[p.condition], temperature(p.temperature, p.quantities.temperature), chance(p.precipitation_probability, p.quantities.precipitation_probability) && `${chance(p.precipitation_probability, p.quantities.precipitation_probability)} chance of precipitation`].filter(Boolean).join(", ");
}
function Hourly({ periods, count }: { periods: HourlyPeriod[]; count: number }) {
  const values = periods.map(p => p.temperature);
  const known = values.filter((v): v is number => v !== undefined);
  const low = Math.min(...known), high = Math.max(...known);
  const peak = periods.reduce<HourlyPeriod | undefined>((best, p) => p.precipitation_probability !== undefined && (best?.precipitation_probability === undefined || p.precipitation_probability > best.precipitation_probability) ? p : best, undefined);
  const lowest = periods.find(p => p.temperature === low);
  const highest = periods.find(p => p.temperature === high);
  const unit = periods.find(p => p.temperature !== undefined)?.quantities.temperature.label;
  // The summary follows the drawn bars: chance below 10% is not shown, but it is not unavailable either.
  const precipitation = !peak ? "precipitation chance unavailable"
    : chance(peak.precipitation_probability, peak.quantities.precipitation_probability) ? `precipitation chance peaks at ${chance(peak.precipitation_probability, peak.quantities.precipitation_probability)} at ${hour(new Date(peak.starts_at))}`
    : "precipitation chance below 10%";
  const summary = `Next ${periods.length} hours: ${known.length ? `low ${temperature(low, lowest?.quantities.temperature)}, high ${temperature(high, highest?.quantities.temperature)}` : "temperature unavailable"}; ${precipitation}.`;
  const width = Math.max(264, periods.length * 22), step = chartLabelStep(count);
  const x = (i: number) => 14 + i * ((width - 28) / Math.max(1, periods.length - 1));
  // Icons occupy y 8–32; the line starts at 58 so a label 8px above the highest point clears them.
  const y = (v: number) => 58 + (1 - (v - low) / (high - low || 1)) * 70;
  let drawing = false;
  const path = values.map((v, i) => {
    if (v === undefined) { drawing = false; return ""; }
    const command = drawing ? "L" : "M"; drawing = true;
    return `${command}${x(i).toFixed(1)},${y(v).toFixed(1)}`;
  }).join("");
  return <><div className="forecast-legend" aria-hidden="true"><span>Temperature{unit && ` ${unit}`}</span><span>Chance of precipitation</span></div>
    <div className="view-chart-scroll" role="img" tabIndex={0} aria-label={summary}>
      <svg className="forecast-chart" style={{ minWidth: width }} viewBox={`0 0 ${width} 216`} aria-hidden="true">
        <line className="forecast-grid" x1="0" x2={width} y1="176" y2="176" />
        {periods.map((p, i) => {
          const date = new Date(p.starts_at), probability = p.precipitation_probability;
          // A new local day starts between this period and the previous one; place the rule at
          // local midnight by time, which in half-hour zones falls between two hourly points.
          const previous = i > 0 ? Date.parse(periods[i - 1].starts_at) : undefined;
          const midnight = previous !== undefined && localDate(date.getTime()) !== localDate(previous)
            ? x(i - 1) + (new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime() - previous) / (date.getTime() - previous) * (x(i) - x(i - 1))
            : undefined;
          return <g key={p.starts_at}>
            {midnight !== undefined && <><line className="forecast-midnight" x1={midnight} x2={midnight} y1="10" y2="176" /><text x={midnight} y="212" textAnchor="middle">{weekday(date)}</text></>}
            {probability !== undefined && probability >= 10 && <rect className="forecast-bar" x={x(i) - 7} y={176 - 26 * probability / 100} width="14" height={26 * probability / 100} rx="2" />}
            {i % step === 0 && <><ForecastIcon condition={p.condition} x={x(i) - 12} y={8} />{values[i] !== undefined && <text className="forecast-temp-label" x={x(i)} y={y(values[i]!) - 8} textAnchor="middle">{temperature(p.temperature, p.quantities.temperature)}</text>}<text x={x(i)} y="192" textAnchor="middle">{i === 0 ? "Now" : hour(date)}</text></>}
          </g>;
        })}
        <path className="forecast-line" d={path} />
      </svg>
    </div></>;
}

export function BoundForecast({ label, source, periods }: Props) {
  const resource = useForecast(source);
  const status = cardStatus(resource), now = useMinuteClock();
  const count = periods ?? (source.entity_type === forecastTypes.hourly ? 24 : 8);
  const data = status.value === "empty" ? undefined : resource.data;
  const hours = data?.kind === "hourly" ? upcomingHours(data.periods, now, count) : [];
  const days = data?.kind === "daily" ? upcomingDays(data.periods, now, count) : [];
  // Words merge day and night variants, so the afterglow also compares each drawn condition icon.
  const shown = data?.kind === "hourly" ? hours.map((p, i) => [hourlyLabel(p, i), p.condition]) : days.map(p => [dailyLabel(p, now), p.condition]);
  const title = label ?? `Next ${count} ${source.entity_type === forecastTypes.hourly ? "hours" : "days"}`;
  const metadata = resource.metadata;
  const partial = data?.kind === "daily" && days.length > 0 && data.periods.some(p => p.date < localDate(now)) && metadata?.availability === "unavailable";
  return <CardFrame className="view-forecast" title={title} status={status.entries} observedAt={data?.observed_at}
    arrival={data ? { updatedAt: data.observed_at, shown: JSON.stringify(shown), eligible: status.glows } : undefined}
    sources={metadata ? [{ entityId: metadata.id, label: metadata.label, availability: metadata.availability, enabled: metadata.enabled }] : []}
    sourceExtra={<p>Modeled forecast from the bound Entity's Adapter; periods that have ended are hidden.</p>}>
    <div data-value={status.value} className="forecast-body">
      {!data ? <p className="view-metric-value" data-value="empty" aria-hidden="true">—</p> : data.kind === "hourly" ?
        hours.length ? <Hourly periods={hours} count={count} /> : <p>Every hour in the last forecast has passed.</p> :
        days.length ? <>{partial && <p className="forecast-note">Showing the remaining days of a forecast from {weekday(new Date(data.observed_at))}.</p>}<Daily periods={days} now={now} /></> : <p>Every day in the last forecast has passed.</p>}
    </div>
  </CardFrame>;
}
