import { connect } from "nats.ws";
import type { NatsConnection, Subscription } from "nats.ws";

export interface NatsMessage {
  at: string;
  subject: string;
  payload: string;
  json: unknown;
}

export type { Subscription };

/** NATS WebSocket URL: a saved browser override wins over the Vite stack default. */
export const getNatsWsUrl = (): string =>
  localStorage.getItem("hearth.natsWsUrl") ??
  import.meta.env.VITE_NATS_WS_URL ??
  "ws://127.0.0.1:4223";

export const setNatsWsUrl = (value: string): void => {
  localStorage.setItem("hearth.natsWsUrl", value.replace(/\/$/u, ""));
};

/**
 * A refcounted lease on one shared websocket connection, keyed by URL.
 *
 * Each acquire must be paired with exactly one release. The connection closes
 * when the last lease for its URL is released, so two owners (the NATS page and
 * the Device Facts page) never close a connection the other is still reading.
 * A page that abandons a lease leaks the socket until the tab closes, so hold a
 * lease only for as long as the reader needs it.
 */
export interface NatsConnectionLease {
  readonly connection: NatsConnection;
  release: () => Promise<void>;
}

interface PooledConnection {
  url: string;
  connection: NatsConnection | null;
  connecting: Promise<NatsConnection> | null;
  leases: number;
}

const pool = new Map<string, PooledConnection>();

const makeLease = (target: PooledConnection): NatsConnectionLease => {
  let released = false;
  return {
    get connection(): NatsConnection {
      if (!target.connection) {
        throw new Error("NATS connection lease was already released");
      }
      return target.connection;
    },
    async release(): Promise<void> {
      if (released) {
        return;
      }
      released = true;
      target.leases -= 1;
      if (target.leases > 0) {
        return;
      }
      if (pool.get(target.url) === target) {
        pool.delete(target.url);
      }
      const nc = target.connection;
      target.connection = null;
      if (nc && !nc.isClosed()) {
        await nc.close();
      }
    },
  };
};

/**
 * Acquire a shared connection to the configured websocket URL. Concurrent
 * acquirers share one in-flight connect. Safe under React StrictMode
 * double-mounts: a released-but-in-flight attempt is retried rather than
 * handed back as a lease on a closed socket.
 */
export const acquireNatsConnection = async (): Promise<NatsConnectionLease> => {
  const url = getNatsWsUrl();
  let entry = pool.get(url);
  // Reuse the entry only while its socket is live; a closed socket is gone.
  if (entry && entry.connection?.isClosed()) {
    pool.delete(url);
    entry = undefined;
  }
  if (!entry) {
    const created: PooledConnection = {
      url,
      connection: null,
      connecting: null,
      leases: 0,
    };
    pool.set(url, created);
    const connectEntry = async (): Promise<NatsConnection> => {
      try {
        const nc = await connect({ servers: url, timeout: 5000 });
        created.connection = nc;
        created.connecting = null;
        return nc;
      } catch (error) {
        created.connecting = null;
        if (created.leases === 0 && pool.get(url) === created) {
          pool.delete(url);
        }
        throw error;
      }
    };
    created.connecting = connectEntry();
    entry = created;
  }

  if (entry.connection) {
    entry.leases += 1;
    return makeLease(entry);
  }
  if (!entry.connecting) {
    // Defensive: an entry with neither a socket nor an in-flight connect can
    // never yield a lease. Drop it and start over.
    if (pool.get(url) === entry) {
      pool.delete(url);
    }
    return acquireNatsConnection();
  }
  await entry.connecting;
  // Another owner may have closed and dropped the entry while this connect
  // resolved; retry and acquire a fresh connection instead of a dead lease.
  if (entry.connection && pool.get(url) === entry) {
    entry.leases += 1;
    return makeLease(entry);
  }
  return acquireNatsConnection();
};

const decoder = new TextDecoder();

/** Decode a message payload: pretty JSON when possible, raw text otherwise. */
export const decodeNatsMessage = (
  subject: string,
  data: Uint8Array
): NatsMessage => {
  const payload = decoder.decode(data);
  let json: unknown = null;
  try {
    json = JSON.parse(payload) as unknown;
  } catch {
    json = null;
  }
  return { at: new Date().toISOString(), subject, payload, json };
};
