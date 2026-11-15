import type { Resolved } from "../bindings/types";

export type StatusTone = "error" | "warn" | "neutral";
export type StatusKey =
  | "loading"
  | "missing"
  | "device-missing"
  | "error"
  | "unavailable"
  | "disabled"
  | "refresh-failed"
  | "availability-unknown"
  | "unobserved"
  | "absent";
export interface StatusEntry {
  key: StatusKey;
  tone: StatusTone;
  text: string;
  detail?: string;
}

/** How a card's value renders under its current status. */
export type ValueTreatment = "live" | "dimmed" | "empty";
export interface CardStatus {
  /** Ordered by precedence; empty when the card is ready and its Entity is available. */
  entries: StatusEntry[];
  value: ValueTreatment;
  /** Only ready data from an available, enabled Entity glows when a new observation arrives. */
  glows: boolean;
}

const entries: Record<StatusKey, StatusEntry> = {
  loading: { key: "loading", tone: "neutral", text: "Loading…" },
  missing: {
    key: "missing",
    tone: "error",
    text: "Entity not found",
    detail: "It may have been removed.",
  },
  "device-missing": {
    key: "device-missing",
    tone: "error",
    text: "Device not found",
    detail: "It may have been removed.",
  },
  error: { key: "error", tone: "error", text: "Couldn't load this reading" },
  unavailable: { key: "unavailable", tone: "warn", text: "Unavailable" },
  disabled: { key: "disabled", tone: "neutral", text: "Disabled" },
  "refresh-failed": {
    key: "refresh-failed",
    tone: "warn",
    text: "Couldn't refresh",
    detail: "Showing the last reading.",
  },
  "availability-unknown": {
    key: "availability-unknown",
    tone: "neutral",
    text: "Availability unknown",
  },
  unobserved: { key: "unobserved", tone: "neutral", text: "Never observed" },
  absent: {
    key: "absent",
    tone: "neutral",
    text: "No value",
    detail: "The field isn't in the current State.",
  },
};

/** One status entry, with a request error's message as its detail. */
export const statusEntry = (key: StatusKey, error?: string): StatusEntry =>
  key === "error" && error ? { ...entries.error, detail: error } : entries[key];

const statusKeys = (resource: Resolved<unknown>): StatusKey[] => {
  const found: StatusKey[] = [];
  const { status, metadata } = resource;
  if (status === "loading" || status === "missing" || status === "error") {
    found.push(status);
  }
  if (metadata?.availability === "unavailable") {
    found.push("unavailable");
  }
  if (metadata && !metadata.enabled) {
    found.push("disabled");
  }
  if (resource.refreshFailed && status !== "error" && status !== "missing") {
    found.push("refresh-failed");
  }
  if (
    metadata &&
    metadata.availability !== "available" &&
    metadata.availability !== "unavailable"
  ) {
    found.push("availability-unknown");
  }
  if (
    status === "unobserved" ||
    (resource.unobserved && status !== "loading")
  ) {
    found.push("unobserved");
  }
  if (status === "absent") {
    found.push("absent");
  }
  return found;
};

/**
 * Derives the View cards state table in DESIGN.md for one resource. Status,
 * State, and Entity availability stay separate facts; a card shows every entry
 * that applies, in precedence order.
 */
export const cardStatus = (resource: Resolved<unknown>): CardStatus => {
  const found = statusKeys(resource);
  const result = found.map((key) =>
    key === "error" && resource.error
      ? { ...entries.error, detail: resource.error }
      : entries[key]
  );
  const hasData = resource.data !== undefined && resource.status === "ready";
  let value: ValueTreatment = "empty";
  if (hasData) {
    value =
      found.includes("unavailable") || found.includes("disabled")
        ? "dimmed"
        : "live";
  }
  return {
    entries: result,
    value,
    glows:
      hasData &&
      resource.metadata?.availability === "available" &&
      resource.metadata.enabled,
  };
};
