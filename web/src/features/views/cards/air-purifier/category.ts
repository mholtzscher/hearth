export type AirCategory = { words: string; tone: "ok" | "warn" | "error" };

/** EPA 2024 24-hour PM2.5 bands, in µg/m³. */
export function airCategory(pm25: number): AirCategory {
  if (pm25 <= 9) return { words: "Good", tone: "ok" };
  if (pm25 <= 35.4) return { words: "Moderate", tone: "warn" };
  if (pm25 <= 55.4) return { words: "Unhealthy for sensitive groups", tone: "warn" };
  if (pm25 <= 125.4) return { words: "Unhealthy", tone: "error" };
  if (pm25 <= 225.4) return { words: "Very unhealthy", tone: "error" };
  return { words: "Hazardous", tone: "error" };
}
export function choiceWords(choice: string): string { return choice.charAt(0).toUpperCase() + choice.slice(1); }

export function choicesOf(support: unknown): string[] {
  const choices = (support as { state?: { choices?: unknown } } | undefined)?.state?.choices;
  return Array.isArray(choices) ? choices.filter((choice): choice is string => typeof choice === "string") : [];
}
