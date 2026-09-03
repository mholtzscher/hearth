import { useCallback, useEffect, useState } from "react";

/** Minimal GET hook with manual refresh. POST/PATCH use apiFetch directly. */
export function useApi<T>(key: string, fetcher: () => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setData(await fetcher());
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setLoading(false);
    }
  }, [key]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
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
