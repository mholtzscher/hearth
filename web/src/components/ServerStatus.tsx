import { useEffect, useState } from "react";
import { api, ApiError, useBaseUrlVersion } from "../api/client";
import { usePolling } from "../api/hooks";
import { cn } from "../lib/utils";

export const serverLabels = {
  checking: "Checking…",
  ready: "Ready",
  "not-ready": "Not ready",
  unavailable: "Status unavailable",
  unknown: "Status unknown",
};

export type ServerState = keyof typeof serverLabels;

/** The one readiness poller; onChange lets the phone bar reflect a problem without polling again. */
export function ServerStatus({ onChange, rail = false }: { onChange?: (state: ServerState) => void; rail?: boolean }) {
  const version = useBaseUrlVersion();
  return <ConnectedServerStatus key={version} onChange={onChange} rail={rail} />;
}

function ConnectedServerStatus({ onChange, rail }: { onChange?: (state: ServerState) => void; rail: boolean }) {
  const { data, error, loading } = usePolling("readyz", api.ready, 10_000);
  const [state, setState] = useState<ServerState>("checking");
  // Retain the last completed check during polling, including a failed check.
  useEffect(() => {
    if (loading) return;
    if (error instanceof ApiError && error.status === 503) setState("not-ready");
    else if (error) setState("unavailable");
    else if (data?.status === "ready") setState("ready");
    else if (data?.status === "not_ready") setState("not-ready");
    else setState("unknown");
  }, [data, error, loading]);
  useEffect(() => onChange?.(state), [onChange, state]);

  const tone = state === "ready" ? "text-success" : state === "not-ready" ? "text-warning" : state === "unavailable" ? "text-destructive" : "text-muted-foreground";
  return <p data-slot="server-status" data-hide-on-search className={cn("m-0 flex shrink-0 items-center gap-2 border-t border-border px-3 pt-3 text-label leading-normal text-muted-foreground", rail && "mt-auto flex-col gap-1 px-0 text-center text-caption")} data-state={state} role="status" aria-busy={state === "checking"}>
    <span className={cn("size-1.5 shrink-0 rounded-full bg-current", tone)} aria-hidden="true" />
    <span>Hearth server <strong className={cn("ms-2 font-medium", rail && "ms-0", tone)}>{serverLabels[state]}</strong></span>
  </p>;
}
