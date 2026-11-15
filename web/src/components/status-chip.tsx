import { Badge } from "./badge.tsx";

type Tone = "default" | "success" | "warning" | "error" | "info";

const statusTones = new Map<string, Tone>([
  ["healthy", "success"],
  ["available", "success"],
  ["satisfied", "success"],
  ["ready", "success"],
  ["ok", "success"],
  // Automation enablement and a Run that reached its required outcomes.
  ["enabled", "success"],
  ["succeeded", "success"],
  // `dispatched` is in-flight-ish: accepted with no observation to verify.
  ["dispatched", "info"],
  // A Run still executing, and where its work came from.
  ["running", "info"],
  ["run", "info"],
  ["manual", "info"],
  ["device_fact", "info"],
  ["unhealthy", "error"],
  ["unavailable", "error"],
  ["rejected", "error"],
  ["error", "error"],
  ["failed", "error"],
  ["unknown", "warning"],
  ["disabled", "warning"],
  // An unfinished Step or Run: interrupted work is never a success.
  ["interrupted", "warning"],
  // A Skip is a match that started no Run, and its two reasons.
  ["skip", "warning"],
  ["automation_busy", "warning"],
  ["stale_fact", "warning"],
]);

export const statusTone = (status: string): Tone =>
  statusTones.get(status) ?? "default";

/** Healthy states stay quiet; problems carry the color weight. */
const toneClass: Record<Tone, string> = {
  default: "border-border text-muted-foreground",
  success: "border-success/30 bg-success/10 text-success",
  warning: "border-warning/35 bg-warning/10 text-warning",
  error: "border-destructive/40 bg-destructive/15 text-destructive",
  info: "border-info/35 bg-info/10 text-info",
};

/** Status badge with an optional context-specific tone override. */
export const StatusChip = ({
  label,
  status,
  tone,
}: {
  label?: string;
  status: string;
  tone?: Tone;
}) => (
  <Badge variant="outline" className={toneClass[tone ?? statusTone(status)]}>
    {label ? `${label}: ${status}` : status}
  </Badge>
);
