import { connect } from "nats.ws";
import type { NatsConnection, Subscription } from "nats.ws";

export interface NatsMessage {
  at: string;
  subject: string;
  payload: string;
  json: unknown;
}

export type { Subscription };

/** Websocket URL for NATS. Defaults to the loopback listener in configs/nats-server.conf. */
export function getNatsWsUrl(): string {
  return localStorage.getItem("hearth.natsWsUrl") ?? "ws://127.0.0.1:4223";
}

export function setNatsWsUrl(value: string): void {
  localStorage.setItem("hearth.natsWsUrl", value.replace(/\/$/, ""));
}

let connection: NatsConnection | null = null;

export async function natsConnect(): Promise<NatsConnection> {
  if (connection && !connection.isClosed()) return connection;
  connection = await connect({ servers: getNatsWsUrl(), timeout: 5000 });
  return connection;
}

export async function natsDisconnect(): Promise<void> {
  const nc = connection;
  connection = null;
  if (nc && !nc.isClosed()) await nc.close();
}

const decoder = new TextDecoder();

/** Decode a message payload: pretty JSON when possible, raw text otherwise. */
export function decodeNatsMessage(subject: string, data: Uint8Array): NatsMessage {
  const payload = decoder.decode(data);
  let json: unknown = null;
  try {
    json = JSON.parse(payload) as unknown;
  } catch {
    json = null;
  }
  return { at: new Date().toISOString(), subject, payload, json };
}
