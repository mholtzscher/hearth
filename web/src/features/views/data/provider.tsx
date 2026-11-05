import { createContext, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";
import { getBaseUrl, useBaseUrlVersion } from "../../../api/client";
import { emptySnapshot, ReadCache, type ReadRequest } from "./cache";

const CacheContext = createContext<ReadCache | null>(null);
export function ViewDataProvider({ children }: { children: ReactNode }) {
  const version = useBaseUrlVersion();
  const cache = useMemo(() => new ReadCache(), [version]);
  useEffect(() => () => cache.dispose(), [cache]);
  return <CacheContext.Provider value={cache}>{children}</CacheContext.Provider>;
}
export function useRead(request: ReadRequest | null) {
  return useReads(request ? [request] : [])[0] ?? emptySnapshot;
}
export function useReads(requests: ReadRequest[]) {
  const cache = useContext(CacheContext);
  if (!cache) throw new Error("ViewDataProvider is required");
  const identity = JSON.stringify(requests.map(request => `${getBaseUrl()}|${request.key}`));
  const binding = useMemo(() => {
    const reads = requests.map(request => ({ ...request, key: `${getBaseUrl()}|${request.key}` }));
    let previous = reads.map(read => cache.snapshot(read.key));
    return {
      subscribe: (listener: () => void) => {
        const stops = reads.map(read => cache.subscribe(read, listener));
        return () => stops.forEach(stop => stop());
      },
      snapshot: () => {
        const next = reads.map(read => cache.snapshot(read.key));
        if (next.some((value, index) => value !== previous[index])) previous = next;
        return previous;
      },
    };
  }, [cache, identity]);
  return useSyncExternalStore(binding.subscribe, binding.snapshot);
}
