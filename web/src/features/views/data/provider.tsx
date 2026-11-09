import { createContext, useCallback, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";
import { getBaseUrl, useBaseUrlVersion } from "../../../api/client";
import { executeCommand } from "../../../api/commands";
import { CommandQueue, type ControlState, type ControlTarget } from "../controls/queue";
import { emptySnapshot, ReadCache, type ReadRequest } from "./cache";

const CacheContext = createContext<ReadCache | null>(null);
const QueueContext = createContext<CommandQueue | null>(null);
/** One read cache and one Command queue per Core connection. */
export function ViewDataProvider({ children }: { children: ReactNode }) {
  const version = useBaseUrlVersion();
  const cache = useMemo(() => new ReadCache(), [version]);
  const queue = useMemo(() => new CommandQueue({ execute: executeCommand, refreshState: () => cache.refreshState() }), [cache]);
  useEffect(() => () => { queue.dispose(); cache.dispose(); }, [cache, queue]);
  return <CacheContext.Provider value={cache}><QueueContext.Provider value={queue}>{children}</QueueContext.Provider></CacheContext.Provider>;
}

/** The shared control state for one Entity Operation and a function that requests a value. */
export function useControl(target: ControlTarget): [ControlState, (value: unknown) => void] {
  const queue = useContext(QueueContext);
  if (!queue) throw new Error("ViewDataProvider is required");
  const { entityId, operation } = target;
  const subscribe = useCallback((listener: () => void) => queue.subscribe({ entityId, operation }, listener), [queue, entityId, operation]);
  const state = useSyncExternalStore(subscribe, () => queue.snapshot({ entityId, operation }));
  const request = useCallback((value: unknown) => queue.request({ entityId, operation }, value), [queue, entityId, operation]);
  return [state, request];
}

/** Observe a list's per-Entity queue entries without introducing a group Command lifecycle. */
export function useControls(targets: ControlTarget[]): [ControlState[], (target: ControlTarget, value: unknown) => void] {
  const queue = useContext(QueueContext);
  if (!queue) throw new Error("ViewDataProvider is required");
  const identity = JSON.stringify(targets);
  const binding = useMemo(() => {
    let previous = targets.map(target => queue.snapshot(target));
    return {
      subscribe: (listener: () => void) => {
        const stops = targets.map(target => queue.subscribe(target, listener));
        return () => stops.forEach(stop => stop());
      },
      snapshot: () => {
        const next = targets.map(target => queue.snapshot(target));
        if (next.some((state, index) => state !== previous[index])) previous = next;
        return previous;
      },
    };
  }, [queue, identity]);
  const states = useSyncExternalStore(binding.subscribe, binding.snapshot);
  const request = useCallback((target: ControlTarget, value: unknown) => queue.request(target, value), [queue]);
  return [states, request];
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
