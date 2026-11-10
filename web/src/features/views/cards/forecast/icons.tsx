import type { ForecastCondition } from "../../bindings/forecast";

export const conditionWords: Record<ForecastCondition, string> = {
  "clear-day": "Clear", "clear-night": "Clear", "partly-cloudy-day": "Partly cloudy", "partly-cloudy-night": "Partly cloudy",
  cloudy: "Cloudy", rain: "Rain", snow: "Snow", sleet: "Sleet", thunderstorm: "Thunderstorm", wind: "Windy", fog: "Fog",
};
const cloudLow = "M7 19h10a4 4 0 0 0 .6-7.95A6 6 0 0 0 6.2 12.1 3.5 3.5 0 0 0 7 19z";
const cloudHigh = "M7 15h10a4 4 0 0 0 .6-7.95A6 6 0 0 0 6.2 8.1 3.5 3.5 0 0 0 7 15z";

export function ForecastIcon({ condition, x, y }: { condition?: ForecastCondition; x?: number; y?: number }) {
  if (!condition) return <svg className="forecast-icon" x={x} y={y} viewBox="0 0 24 24" aria-hidden="true" />;
  const sun = <g className="forecast-sun"><circle cx="12" cy="12" r="4" /><path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.3 5.3l1.6 1.6M17.1 17.1l1.6 1.6M5.3 18.7l1.6-1.6M17.1 6.9l1.6-1.6" /></g>;
  const moon = <path d="M19.5 14.6A7.8 7.8 0 1 1 9.4 4.5a6.2 6.2 0 0 0 10.1 10.1z" />;
  const partial = condition === "partly-cloudy-day" || condition === "partly-cloudy-night";
  return <svg className="forecast-icon" x={x} y={y} width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {condition === "clear-day" && sun}{condition === "clear-night" && moon}
    {partial && <>{condition === "partly-cloudy-day" ? <g className="forecast-sun"><circle cx="8.5" cy="8" r="3" /><path d="M8.5 2.2v1.3M2.7 8h1.3M4.4 3.9l.9.9M12.6 3.9l-.9.9" /></g> : <path d="M11.8 9.3A4.6 4.6 0 1 1 6 3.5a3.7 3.7 0 0 0 5.8 5.8z" />}<path d="M8.5 20h9a3.6 3.6 0 0 0 .5-7.15A5.4 5.4 0 0 0 7.7 13.8 3.1 3.1 0 0 0 8.5 20z" fill="var(--gray-1)" /></>}
    {condition === "cloudy" && <path d={cloudLow} />}
    {(["rain", "snow", "sleet", "thunderstorm"] as string[]).includes(condition) && <path d={cloudHigh} />}
    {condition === "rain" && <path className="forecast-precip" d="M8.5 17.5l-1 3M12.5 17.5l-1 3M16.5 17.5l-1 3" />}
    {condition === "snow" && <g className="forecast-precip"><circle cx="8" cy="19" r=".9" fill="currentColor" /><circle cx="12" cy="20.5" r=".9" fill="currentColor" /><circle cx="16" cy="19" r=".9" fill="currentColor" /></g>}
    {condition === "sleet" && <g className="forecast-precip"><path d="M8.5 17.5l-1 3M15.5 17.5l-1 3" /><circle cx="12" cy="19.5" r=".9" fill="currentColor" /></g>}
    {condition === "thunderstorm" && <path className="forecast-sun" d="M12.5 15.5l-2 3.5h3l-2 3.5" />}
    {condition === "wind" && <path d="M3 9h11a2.5 2.5 0 1 0-2.5-2.5M3 13h15a2.5 2.5 0 1 1-2.5 2.5M3 17h8" />}
    {condition === "fog" && <path d="M4 8h16M3 12h18M5 16h14M7 20h10" />}
  </svg>;
}
