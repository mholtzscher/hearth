import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import type { SetStateAction } from "react";

import { useBaseUrlVersion } from "./client.ts";

interface ApiState<T> {
  request: string;
  data: T | null;
  error: Error | null;
  loading: boolean;
}

const isStateUpdater = <T>(
  action: SetStateAction<T>
): action is (previous: T) => T => typeof action === "function";

/** Minimal GET hook with manual refresh. POST/PATCH use apiFetch directly. */
export const useApi = <T>(key: string, fetcher: () => Promise<T>) => {
  // The hearthd base URL is part of the request identity: changing servers
  // clears stale data and refetches, like navigating to a new resource.
  const baseVersion = useBaseUrlVersion();
  const request = `${key}:${baseVersion}`;
  const [state, setState] = useState<ApiState<T>>({
    request,
    data: null,
    error: null,
    loading: true,
  });
  const generation = useRef(0);
  const requestRef = useRef(request);
  const fetcherRef = useRef(fetcher);
  // Keep the fetcher that belongs to the current request identity: refreshes
  // reuse it until the key or base URL changes, exactly like the request scope.
  // A layout effect commits the mirror before passive work (refresh, timers) runs.
  useLayoutEffect(() => {
    if (requestRef.current === request) {
      return;
    }
    requestRef.current = request;
    fetcherRef.current = fetcher;
  }, [fetcher, request]);

  const load = useCallback(async () => {
    const current = generation.current + 1;
    generation.current = current;
    try {
      const value = await fetcherRef.current();
      if (generation.current !== current) {
        return;
      }
      setState({ request, data: value, error: null, loading: false });
    } catch (error_) {
      if (generation.current !== current) {
        return;
      }
      setState((previous) => ({
        request,
        data: previous.request === request ? previous.data : null,
        error: error_ instanceof Error ? error_ : new Error(String(error_)),
        loading: false,
      }));
    }
  }, [request]);

  const refresh = useCallback(() => {
    setState((previous) => {
      if (
        previous.request === request &&
        previous.loading &&
        previous.error === null
      ) {
        return previous;
      }
      return {
        request,
        data: previous.request === request ? previous.data : null,
        error: null,
        loading: true,
      };
    });
    return load();
  }, [load, request]);

  useEffect(() => {
    // A new request identity clears stale data on navigation or server switch,
    // but not on manual or polling refreshes.
    void load();
  }, [load]);

  const setData = useCallback((action: SetStateAction<T | null>) => {
    setState((previous) => ({
      ...previous,
      data: isStateUpdater(action) ? action(previous.data) : action,
    }));
  }, []);

  // Until a response for the current request identity arrives, the scope shows
  // its initial loading state rather than the previous scope's data.
  const active = state.request === request;
  return {
    data: active ? state.data : null,
    error: active ? state.error : null,
    loading: active ? state.loading : true,
    refresh,
    setData,
  };
};

/** Polling variant for health indicators. Skips a tick while a request is still
    in flight instead of piling up overlapping fetches. */
export const usePolling = <T>(
  key: string,
  fetcher: () => Promise<T>,
  intervalMs: number
) => {
  const result = useApi<T>(key, fetcher);
  const resultRef = useRef(result);
  // The latest committed result must be visible to the interval after commit and
  // before passive work, not only after the passive flush.
  useLayoutEffect(() => {
    resultRef.current = result;
  }, [result]);
  const polling = useRef<{
    key: string;
    intervalMs: number;
    timer?: ReturnType<typeof setInterval>;
  }>({ key, intervalMs });
  // A key or interval change restarts the interval; other renders leave it be.
  useEffect(() => {
    const previous = polling.current;
    if (
      previous.key === key &&
      previous.intervalMs === intervalMs &&
      previous.timer !== undefined
    ) {
      return;
    }
    previous.key = key;
    previous.intervalMs = intervalMs;
    if (previous.timer !== undefined) {
      clearInterval(previous.timer);
    }
    previous.timer = setInterval(() => {
      const latest = resultRef.current;
      if (!latest.loading) {
        void latest.refresh();
      }
    }, intervalMs);
  });
  useEffect(
    () => () => {
      const { timer } = polling.current;
      if (timer !== undefined) {
        clearInterval(timer);
      }
      // An owned timer must be forgotten on cleanup so a replayed mount setup
      // (StrictMode) creates a fresh interval instead of trusting a dead handle.
      polling.current.timer = undefined;
    },
    []
  );
  return result;
};
