import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";
import type { ReactNode } from "react";

import { getBaseUrl, useBaseUrlVersion } from "../../../api/client";
import { executeCommand } from "../../../api/commands";
import { CommandQueue } from "../controls/queue";
import type {
  ControlState,
  ControlTarget,
  ControlValue,
  RequestPolicy,
} from "../controls/queue";
import { emptySnapshot, ReadCache } from "./cache";
import type { ReadRequest } from "./cache";

const CacheContext = createContext<ReadCache | null>(null);
const QueueContext = createContext<CommandQueue | null>(null);
const createScope = (version: number) => {
  const cache = new ReadCache();
  return {
    version,
    cache,
    queue: new CommandQueue({
      execute: executeCommand,
      refreshState: () => cache.refreshState(),
    }),
  };
};

/** One read cache and one Command queue per Core connection. */
export const ViewDataProvider = ({ children }: { children: ReactNode }) => {
  const version = useBaseUrlVersion();
  const [scope, setScope] = useState(() => createScope(version));
  if (scope.version !== version) {
    setScope(createScope(version));
  }
  const { cache, queue } = scope;
  useEffect(
    () => () => {
      queue.dispose();
      cache.dispose();
    },
    [cache, queue]
  );
  return (
    <CacheContext.Provider value={cache}>
      <QueueContext.Provider value={queue}>{children}</QueueContext.Provider>
    </CacheContext.Provider>
  );
};

/** The shared control state for one Entity Operation and a function that requests a value. */
export const useControl = (
  target: ControlTarget,
  policy: RequestPolicy = "queue"
): [ControlState, (value: ControlValue) => void] => {
  const queue = useContext(QueueContext);
  if (!queue) {
    throw new Error("ViewDataProvider is required");
  }
  const { entityId, operation } = target;
  const subscribe = useCallback(
    (listener: () => void) =>
      queue.subscribe({ entityId, operation }, listener),
    [queue, entityId, operation]
  );
  const state = useSyncExternalStore(subscribe, () =>
    queue.snapshot({ entityId, operation })
  );
  const request = useCallback(
    (value: ControlValue) =>
      queue.request({ entityId, operation }, value, policy),
    [queue, entityId, operation, policy]
  );
  return [state, request];
};

/** Stops showing an Operation's Command without affecting the Command itself. */
export const useForgetControl = (): ((target: ControlTarget) => void) => {
  const queue = useContext(QueueContext);
  if (!queue) {
    throw new Error("ViewDataProvider is required");
  }
  return useCallback((target: ControlTarget) => queue.forget(target), [queue]);
};

/**
 * Re-reads one State request every second while active and the page is
 * visible, for a reading that is changing quickly, such as a moving cover.
 */
export const useFastRefresh = (
  request: ReadRequest | null,
  active: boolean
): void => {
  const cache = useContext(CacheContext);
  if (!cache) {
    throw new Error("ViewDataProvider is required");
  }
  const key = request ? `${getBaseUrl()}|${request.key}` : undefined;
  useEffect(
    () => (active && key ? cache.boost(key) : undefined),
    [cache, key, active]
  );
};

/** Observe a list's per-Entity queue entries without introducing a group Command lifecycle. */
export const useControls = (
  targets: ControlTarget[]
): [ControlState[], (target: ControlTarget, value: ControlValue) => void] => {
  const queue = useContext(QueueContext);
  if (!queue) {
    throw new Error("ViewDataProvider is required");
  }
  const identity = JSON.stringify(targets);
  const [keyed, setKeyed] = useState({ queue, identity, targets });
  if (keyed.queue !== queue || keyed.identity !== identity) {
    setKeyed({ queue, identity, targets });
  }
  const binding = useMemo(() => {
    const { queue: currentQueue, targets: currentTargets } = keyed;
    const previous = {
      value: currentTargets.map((target) => currentQueue.snapshot(target)),
    };
    return {
      subscribe: (listener: () => void) => {
        const stops = currentTargets.map((target) =>
          currentQueue.subscribe(target, listener)
        );
        return () => {
          for (const stop of stops) {
            stop();
          }
        };
      },
      snapshot: () => {
        const next = currentTargets.map((target) =>
          currentQueue.snapshot(target)
        );
        if (next.some((state, index) => state !== previous.value[index])) {
          previous.value = next;
        }
        return previous.value;
      },
    };
  }, [keyed]);
  const states = useSyncExternalStore(binding.subscribe, binding.snapshot);
  const request = useCallback(
    (target: ControlTarget, value: ControlValue) =>
      queue.request(target, value),
    [queue]
  );
  return [states, request];
};

export const useReads = (requests: ReadRequest[]) => {
  const cache = useContext(CacheContext);
  if (!cache) {
    throw new Error("ViewDataProvider is required");
  }
  const identity = JSON.stringify(
    requests.map((request) => `${getBaseUrl()}|${request.key}`)
  );
  const [keyed, setKeyed] = useState({ cache, identity, requests });
  if (keyed.cache !== cache || keyed.identity !== identity) {
    setKeyed({ cache, identity, requests });
  }
  const binding = useMemo(() => {
    const { cache: currentCache, requests: currentRequests } = keyed;
    const reads = currentRequests.map((request) => ({
      ...request,
      key: `${getBaseUrl()}|${request.key}`,
    }));
    const previous = {
      value: reads.map((read) => currentCache.snapshot(read.key)),
    };
    return {
      subscribe: (listener: () => void) => {
        const stops = reads.map((read) =>
          currentCache.subscribe(read, listener)
        );
        return () => {
          for (const stop of stops) {
            stop();
          }
        };
      },
      snapshot: () => {
        const next = reads.map((read) => currentCache.snapshot(read.key));
        if (next.some((value, index) => value !== previous.value[index])) {
          previous.value = next;
        }
        return previous.value;
      },
    };
  }, [keyed]);
  return useSyncExternalStore(binding.subscribe, binding.snapshot);
};

export const useRead = (request: ReadRequest | null) =>
  useReads(request ? [request] : [])[0] ?? emptySnapshot;
