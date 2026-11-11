import { useEffect, useState } from "react";
import { api, ApiError, useBaseUrlVersion } from "../api/client";
import { usePolling } from "../api/hooks";

export const serverLabels = {
  checking: "Checking…",
  ready: "Ready",
  "not-ready": "Not ready",
  unavailable: "Status unavailable",
  unknown: "Status unknown",
};

export type ServerState = keyof typeof serverLabels;

/** The one readiness poller; onChange lets the phone bar reflect a problem without polling again. */
export function ServerStatus({ onChange }: { onChange?: (state: ServerState) => void }) {
  const version = useBaseUrlVersion();
  return <ConnectedServerStatus key={version} onChange={onChange} />;
}

function ConnectedServerStatus({ onChange }: { onChange?: (state: ServerState) => void }) {
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

  return <p className="server-status" data-state={state} role="status" aria-busy={state === "checking"}>
    <span className="server-status-dot" aria-hidden="true" />
    <span>Hearth server <strong>{serverLabels[state]}</strong></span>
  </p>;
}
