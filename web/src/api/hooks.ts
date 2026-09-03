import { useCallback, useEffect, useRef, useState } from "react";
import { useBaseUrlVersion } from "./client.ts";

/** Minimal GET hook with manual refresh. POST/PATCH use apiFetch directly. */
export function useApi<T>(key: string, fetcher: () => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const generation = useRef(0);
  // The hearthd base URL is part of the request identity: changing servers
  // clears stale data and refetches, like navigating to a new resource.
  const baseVersion = useBaseUrlVersion();

  const refresh = useCallback(async () => {
    const current = ++generation.current;
    setLoading(true);
    setError(null);
    try {
      const value = await fetcher();
      if (generation.current !== current) return;
      setData(value);
    } catch (e) {
      if (generation.current !== current) return;
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (generation.current === current) setLoading(false);
    }
  }, [key, baseVersion]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    // refresh identity changes only when the key or base URL changes, so this
    // clears stale data on navigation or server switch but not on manual or
    // polling refreshes.
    setData(null);
    void refresh();
  }, [refresh]);

  return { data, error, loading, refresh };
}

/** Polling variant for health indicators. */
export function usePolling<T>(key: string, fetcher: () => Promise<T>, intervalMs: number) {
  const result = useApi<T>(key, fetcher);
  useEffect(() => {
    const timer = setInterval(() => void result.refresh(), intervalMs);
    return () => clearInterval(timer);
  }, [key, intervalMs]); // eslint-disable-line react-hooks/exhaustive-deps
  return result;
}
