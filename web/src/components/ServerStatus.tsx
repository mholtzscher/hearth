import { useEffect, useState } from "react";
import { api, ApiError, useBaseUrlVersion } from "../api/client";
import { usePolling } from "../api/hooks";

const labels = {
  checking: "Checking…",
  ready: "Ready",
  "not-ready": "Not ready",
  unavailable: "Status unavailable",
  unknown: "Status unknown",
};

export function ServerStatus() {
  const version = useBaseUrlVersion();
  return <ConnectedServerStatus key={version} />;
}

function ConnectedServerStatus() {
  const { data, error, loading } = usePolling("readyz", api.ready, 10_000);
  const [state, setState] = useState<keyof typeof labels>("checking");
  // Retain the last completed check during polling, including a failed check.
  useEffect(() => {
    if (loading) return;
    if (error instanceof ApiError && error.status === 503) setState("not-ready");
    else if (error) setState("unavailable");
    else if (data?.status === "ready") setState("ready");
    else if (data?.status === "not_ready") setState("not-ready");
    else setState("unknown");
  }, [data, error, loading]);

  return <p className="server-status" data-state={state} role="status" aria-busy={state === "checking"}>
    <span className="server-status-dot" aria-hidden="true" />
    <span>Hearth server <strong>{labels[state]}</strong></span>
  </p>;
}
