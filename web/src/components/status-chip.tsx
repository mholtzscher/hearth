import { Badge } from "./badge.tsx";

type Tone = "default" | "success" | "warning" | "error" | "info";

export function statusTone(status: string): Tone {
  switch (status) {
    case "healthy":
    case "available":
    case "satisfied":
    case "ready":
    case "ok":
    // Automation enablement and a Run that reached its required outcomes.
    case "enabled":
    case "succeeded":
      return "success";
    // `dispatched` is in-flight-ish: accepted with no observation to verify.
    case "dispatched":
    // A Run still executing, and where its work came from.
    case "running":
    case "run":
    case "manual":
    case "device_fact":
      return "info";
    case "unhealthy":
    case "unavailable":
    case "rejected":
    case "error":
    case "failed":
      return "error";
    case "unknown":
    case "disabled":
    // An unfinished Step or Run: interrupted work is never a success.
    case "interrupted":
    // A Skip is a match that started no Run, and its two reasons.
    case "skip":
    case "automation_busy":
    case "stale_fact":
      return "warning";
    default:
      return "default";
  }
}

/** Healthy states stay quiet; problems carry the color weight. */
const toneClass: Record<Tone, string> = {
  default: "border-border text-muted-foreground",
  success: "border-success/30 bg-success/10 text-success",
  warning: "border-warning/35 bg-warning/10 text-warning",
  error: "border-destructive/40 bg-destructive/15 text-destructive",
  info: "border-info/35 bg-info/10 text-info",
};

/** Status badge with an optional context-specific tone override. */
export function StatusChip({
  label,
  status,
  tone,
}: {
  label?: string;
  status: string;
  tone?: Tone;
}) {
  return (
    <Badge variant="outline" className={toneClass[tone ?? statusTone(status)]}>
      {label ? `${label}: ${status}` : status}
    </Badge>
  );
}
