import { useEffect, useState } from "react";

import { api, ApiError, useBaseUrlVersion } from "../../api/client";
import { usePolling } from "../../api/hooks";
import { cn } from "../../lib/utils";

export const serverLabels = {
  checking: "Checking…",
  ready: "Ready",
  "not-ready": "Not ready",
  unavailable: "Status unavailable",
  unknown: "Status unknown",
};

export type ServerState = keyof typeof serverLabels;

type ReadyStatus = Awaited<ReturnType<typeof api.ready>>;

interface SettledCheck {
  data: ReadyStatus | null;
  error: Error | null;
}

const stateForCheck = ({ data, error }: SettledCheck): ServerState => {
  if (error instanceof ApiError && error.status === 503) {
    return "not-ready";
  }
  if (error) {
    return "unavailable";
  }
  if (data?.status === "ready") {
    return "ready";
  }
  if (data?.status === "not_ready") {
    return "not-ready";
  }
  return "unknown";
};

const toneForState = (state: ServerState) => {
  if (state === "ready") {
    return "text-success";
  }
  if (state === "not-ready") {
    return "text-warning";
  }
  if (state === "unavailable") {
    return "text-destructive";
  }
  return "text-muted-foreground";
};

const ConnectedServerStatus = ({
  onChange,
  rail,
  className,
}: {
  onChange?: (state: ServerState) => void;
  rail: boolean;
  className?: string;
}) => {
  const { data, error, loading } = usePolling("readyz", api.ready, 10_000);
  const [settled, setSettled] = useState<SettledCheck | null>(null);
  // Retain the last completed check during polling, including a failed check.
  if (!loading && (settled?.data !== data || settled?.error !== error)) {
    setSettled({ data, error });
  }
  const state = settled ? stateForCheck(settled) : "checking";
  useEffect(() => onChange?.(state), [onChange, state]);

  const tone = toneForState(state);
  return (
    <output
      data-slot="server-status"
      className={cn(
        "border-border text-label text-muted-foreground m-0 flex shrink-0 items-center gap-2 border-t px-3 pt-3 leading-normal",
        rail && "text-caption mt-auto flex-col gap-1 px-0 text-center",
        className
      )}
      data-state={state}
      aria-busy={state === "checking"}
    >
      <span
        className={cn("size-1.5 shrink-0 rounded-full bg-current", tone)}
        aria-hidden="true"
      />
      <span>
        Hearth server{" "}
        <strong className={cn("ms-2 font-medium", rail && "ms-0", tone)}>
          {serverLabels[state]}
        </strong>
      </span>
    </output>
  );
};

/** The one readiness poller; onChange lets the phone bar reflect a problem without polling again. */
export const ServerStatus = ({
  onChange,
  rail = false,
  className,
}: {
  onChange?: (state: ServerState) => void;
  rail?: boolean;
  className?: string;
}) => {
  const version = useBaseUrlVersion();
  return (
    <ConnectedServerStatus
      key={version}
      onChange={onChange}
      rail={rail}
      className={className}
    />
  );
};
