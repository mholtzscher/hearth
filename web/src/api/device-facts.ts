import { AckPolicy, DeliverPolicy, ReplayPolicy, nanos } from "nats.ws";
import type { ConsumerMessages, JsMsg, NatsConnection } from "nats.ws";

/**
 * JetStream transport for the Device Facts debug view. This module owns every
 * broker interaction; the React page owns only rendering and filtering.
 *
 * Stream HEARTH_DEVICE_FACTS_V1 is Core-provisioned and browser readers never
 * create, update, or purge it. Each read path creates its own ephemeral
 * consumer, which self-deletes when it stops making progress (`inactive_threshold`
 * is orphan safety for a tab that never runs cleanup) and is also deleted
 * explicitly as soon as the bounded read finishes or live is switched off.
 * There is no durable recovery here: a snapshot is a bounded retained read and
 * live is an explicit ephemeral DeliverNew tail.
 */

export const DEVICE_FACT_STREAM = "HEARTH_DEVICE_FACTS_V1";
export const DEVICE_FACT_SUBJECT_FILTER = "hearth.v1.core.fact.>";
/** Newest retained facts a snapshot read asks for. */
export const DEVICE_FACT_SNAPSHOT_LIMIT = 200;

const OBSERVATION_FACT_SCHEMA = "urn:hearth:schema:observation-fact:v1";
const ENTITY_EVENT_FACT_SCHEMA = "urn:hearth:schema:entity-event-fact:v1";

/**
 * Ephemeral consumers self-delete after this much inactivity. Live is normally
 * deleted on disable/unmount; this bounds the damage when a tab dies mid-read.
 */
const CONSUMER_INACTIVE_MS = 5 * 60_000;
/**
 * A bounded snapshot read returns short of DEVICE_FACT_SNAPSHOT_LIMIT when the
 * retained stream holds fewer facts, and the pull request then must wait out its
 * expiry before the read completes. Retention gaps are normal, so the count in
 * range cannot be predicted from sequence numbers; keep the wait small.
 */
const SNAPSHOT_FETCH_EXPIRES_MS = 1500;
/** Live pull refill size: small batches keep the tail responsive. */
const LIVE_PULL_BATCH = 100;
const LIVE_PULL_EXPIRES_MS = 30_000;

export type DeviceFactFamily = "observation" | "entity-event";
export type DeviceFactDelivery = "snapshot" | "live";

export interface ObservationFactData {
  observation_id: string;
  entity_id: string;
  disposition: "applied" | "unchanged";
  value: unknown;
  adapter_received_at: string;
  source_updated_at?: string;
  observed_at: string;
}

export interface EntityEventFactData {
  event_id: string;
  entity_id: string;
  name: string;
  reported_at: string;
  received_at: string;
  recorded_at: string;
}

export type DeviceFactData = ObservationFactData | EntityEventFactData;

export interface DeviceFactEnvelope {
  id: string;
  schema: string;
  emitted_at: string;
  correlation_id: string;
  causation_id: string;
  data: DeviceFactData;
}

/** One delivered fact plus broker- and page-side metadata kept out of the payload. */
export interface DeviceFact {
  /** Envelope identity, also the published Nats-Msg-Id and the dedupe key. */
  id: string;
  family: DeviceFactFamily;
  /** Subject variant: the Observation disposition or the Entity Event name. */
  variant: string;
  subject: string;
  entityId: string;
  envelope: DeviceFactEnvelope;
  data: DeviceFactData;
  /**
   * The payload exactly as decoded from the wire: the parsed JSON object with
   * every published field, including any the typed envelope above does not
   * model. Rendering this is honest; rebuilding the payload from the envelope
   * would drop fields and reorder keys.
   */
  rawPayload: Record<string, unknown>;
  streamSequence: number;
  /** Broker storage time from the delivery reply, not a payload field. */
  brokerStoredAt: string;
  consumer: string;
  deliveryCount: number;
  deliveredBy: DeviceFactDelivery;
  /** Page receive time; not broker metadata. */
  deliveredAt: string;
  headers: Record<string, string[]>;
  /** Published Nats-Msg-Id, if the publisher set one. */
  msgIdHeader: string | null;
  /** Contract mismatches worth showing instead of assuming away. */
  warnings: string[];
}

export class DeviceFactParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DeviceFactParseError";
  }
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function requireString(source: Record<string, unknown>, key: string, context: string): string {
  const value = source[key];
  if (typeof value !== "string" || value.length === 0) {
    throw new DeviceFactParseError(`fact ${context} is missing string field "${key}"`);
  }
  return value;
}

function optionalString(source: Record<string, unknown>, key: string): string | undefined {
  const value = source[key];
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

interface DeviceFactRoute {
  entityId: string;
  family: DeviceFactFamily;
  variant: string;
}

/** Split `hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>` strictly. */
export function parseDeviceFactSubject(subject: string): DeviceFactRoute {
  const tokens = subject.split(".");
  if (
    tokens.length !== 8 ||
    tokens[0] !== "hearth" ||
    tokens[1] !== "v1" ||
    tokens[2] !== "core" ||
    tokens[3] !== "fact" ||
    tokens[4] !== "entity"
  ) {
    throw new DeviceFactParseError(
      `fact subject is not hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>: ${subject}`,
    );
  }
  const entityId = tokens[5];
  const family = tokens[6];
  const variant = tokens[7];
  if (family !== "observation" && family !== "entity-event") {
    throw new DeviceFactParseError(`fact subject has unknown family "${family}": ${subject}`);
  }
  if (!entityId || !variant) {
    throw new DeviceFactParseError(`fact subject is missing an entity id or variant: ${subject}`);
  }
  return { entityId, family, variant };
}

function parseObservationData(data: Record<string, unknown>, context: string): ObservationFactData {
  const disposition = requireString(data, "disposition", context);
  if (disposition !== "applied" && disposition !== "unchanged") {
    throw new DeviceFactParseError(`fact ${context} has unknown disposition "${disposition}"`);
  }
  if (!("value" in data)) {
    throw new DeviceFactParseError(`fact ${context} is missing "value"`);
  }
  return {
    observation_id: requireString(data, "observation_id", context),
    entity_id: requireString(data, "entity_id", context),
    disposition,
    value: data.value,
    adapter_received_at: requireString(data, "adapter_received_at", context),
    source_updated_at: optionalString(data, "source_updated_at"),
    observed_at: requireString(data, "observed_at", context),
  };
}

function parseEntityEventData(
  data: Record<string, unknown>,
  context: string,
): EntityEventFactData {
  return {
    event_id: requireString(data, "event_id", context),
    entity_id: requireString(data, "entity_id", context),
    name: requireString(data, "name", context),
    reported_at: requireString(data, "reported_at", context),
    received_at: requireString(data, "received_at", context),
    recorded_at: requireString(data, "recorded_at", context),
  };
}

/** The delivery reply carries nanoseconds since the epoch as a JS number. */
function brokerStoredAtFromNanos(nanosSinceEpoch: number): string {
  const millis = nanosSinceEpoch / 1e6;
  return Number.isFinite(millis) ? new Date(millis).toISOString() : "";
}

function readHeaders(msg: JsMsg): Record<string, string[]> {
  const headers: Record<string, string[]> = {};
  if (!msg.headers) return headers;
  for (const [key, values] of msg.headers) headers[key] = values;
  return headers;
}

/**
 * Parse one delivered message into the strict v1 envelope. Throws
 * DeviceFactParseError for anything that is not a well-formed Observation or
 * Entity Event fact; callers count those instead of crashing the reader.
 * Envelope/subject/header disagreements become visible warnings, never silent
 * assumptions.
 */
export function parseDeviceFact(msg: JsMsg, deliveredBy: DeviceFactDelivery): DeviceFact {
  const route = parseDeviceFactSubject(msg.subject);
  const context = msg.subject;

  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(msg.data)) as unknown;
  } catch {
    throw new DeviceFactParseError(`fact payload is not JSON: ${context}`);
  }
  if (!isRecord(parsed)) {
    throw new DeviceFactParseError(`fact payload is not a JSON object: ${context}`);
  }
  const dataRaw = parsed.data;
  if (!isRecord(dataRaw)) {
    throw new DeviceFactParseError(`fact payload has no "data" object: ${context}`);
  }

  const data: DeviceFactData =
    route.family === "observation"
      ? parseObservationData(dataRaw, context)
      : parseEntityEventData(dataRaw, context);

  const envelope: DeviceFactEnvelope = {
    id: requireString(parsed, "id", context),
    schema: requireString(parsed, "schema", context),
    emitted_at: requireString(parsed, "emitted_at", context),
    correlation_id: requireString(parsed, "correlation_id", context),
    causation_id: requireString(parsed, "causation_id", context),
    data,
  };

  const headers = readHeaders(msg);
  const msgIdHeader = msg.headers?.get("Nats-Msg-Id") ?? null;

  const warnings: string[] = [];
  if (!msgIdHeader) {
    warnings.push("published without a Nats-Msg-Id header");
  } else if (msgIdHeader !== envelope.id) {
    warnings.push(`Nats-Msg-Id ${msgIdHeader} does not match envelope id ${envelope.id}`);
  }
  if (data.entity_id !== route.entityId) {
    warnings.push(
      `subject entity ${route.entityId} does not match payload entity_id ${data.entity_id}`,
    );
  }
  const expectedSchema =
    route.family === "observation" ? OBSERVATION_FACT_SCHEMA : ENTITY_EVENT_FACT_SCHEMA;
  if (envelope.schema !== expectedSchema) {
    warnings.push(`schema ${envelope.schema} is not the v1 ${route.family} fact schema`);
  }
  const expectedVariant =
    route.family === "observation"
      ? (data as ObservationFactData).disposition
      : (data as EntityEventFactData).name;
  if (route.variant !== expectedVariant) {
    warnings.push(`subject variant ${route.variant} does not match payload ${expectedVariant}`);
  }

  return {
    id: envelope.id,
    family: route.family,
    variant: route.variant,
    subject: msg.subject,
    entityId: data.entity_id,
    envelope,
    data,
    rawPayload: parsed,
    streamSequence: msg.info.streamSequence,
    brokerStoredAt: brokerStoredAtFromNanos(msg.info.timestampNanos),
    consumer: msg.info.consumer,
    deliveryCount: msg.info.deliveryCount,
    deliveredBy,
    deliveredAt: new Date().toISOString(),
    headers,
    msgIdHeader,
    warnings,
  };
}

/**
 * Newest stream sequence first, collapsing repeated fact ids to the delivery
 * with the highest stream sequence, whatever order the input is in. A retry
 * stored again past the stream's duplicate window arrives as a later message,
 * so last-write-wins would keep the older delivery's retry metadata.
 */
export function dedupeDeviceFacts(facts: DeviceFact[]): DeviceFact[] {
  const byId = new Map<string, DeviceFact>();
  for (const fact of facts) {
    const existing = byId.get(fact.id);
    if (!existing || fact.streamSequence > existing.streamSequence) byId.set(fact.id, fact);
  }
  return [...byId.values()].sort((a, b) => b.streamSequence - a.streamSequence);
}

export interface DeviceFactSnapshot {
  facts: DeviceFact[];
  /** Ephemeral consumer name, or null when the stream held nothing to read. */
  consumer: string | null;
  streamMessages: number;
  streamFirstSequence: number;
  streamLastSequence: number;
  readAt: string;
  /** Messages that were not well-formed v1 facts. */
  malformed: number;
}

/**
 * Bounded retained read of the newest facts. Creates one ephemeral consumer
 * clamped to start at `max(first_seq, last_seq - LIMIT + 1)`, drains at most
 * DEVICE_FACT_SNAPSHOT_LIMIT messages, then deletes the consumer. The captured
 * last sequence is a watermark: facts published after it are drained but never
 * reported, so a gap-refilled batch cannot leak newer facts into the snapshot.
 * An empty stream creates no consumer at all.
 */
export async function readDeviceFactSnapshot(nc: NatsConnection): Promise<DeviceFactSnapshot> {
  const js = nc.jetstream();
  const jsm = await nc.jetstreamManager();
  const info = await jsm.streams.info(DEVICE_FACT_STREAM);
  const firstSeq = info.state.first_seq;
  const lastSeq = info.state.last_seq;
  const readAt = new Date().toISOString();

  const empty: DeviceFactSnapshot = {
    facts: [],
    consumer: null,
    streamMessages: info.state.messages,
    streamFirstSequence: firstSeq,
    streamLastSequence: lastSeq,
    readAt,
    malformed: 0,
  };
  // Nothing retained: creating a consumer would add an orphan for no data.
  if (info.state.messages <= 0 || lastSeq < firstSeq) return empty;

  const startSequence = Math.max(firstSeq, lastSeq - DEVICE_FACT_SNAPSHOT_LIMIT + 1);
  // Bound the batch by the sequence span actually in range. When no sequence was
  // evicted or deleted inside that span the batch is satisfied immediately; a
  // retention gap leaves the batch short and the read waits out the expiry, which
  // is expected and keeps the read bounded rather than exact.
  const consumerInfo = await jsm.consumers.add(DEVICE_FACT_STREAM, {
    description: "hearth-device-facts snapshot (bounded retained read)",
    name: undefined,
    durable_name: undefined,
    filter_subject: DEVICE_FACT_SUBJECT_FILTER,
    deliver_policy: DeliverPolicy.StartSequence,
    opt_start_seq: startSequence,
    ack_policy: AckPolicy.None,
    replay_policy: ReplayPolicy.Instant,
    inactive_threshold: nanos(CONSUMER_INACTIVE_MS),
    mem_storage: true,
    num_replicas: 1,
  });
  const consumer = js.consumers.getPullConsumerFor(consumerInfo);

  try {
    const requested = Math.min(
      DEVICE_FACT_SNAPSHOT_LIMIT,
      lastSeq - startSequence + 1,
    );
    const messages = await consumer.fetch({
      max_messages: requested,
      expires: SNAPSHOT_FETCH_EXPIRES_MS,
    });
    const facts: DeviceFact[] = [];
    let malformed = 0;
    for await (const msg of messages) {
      // The pull batch can be refilled by facts published after this read
      // captured its upper bound: a retention gap leaves the batch short, so
      // the server keeps the request open and new facts can satisfy it. Anything
      // above the watermark is newer than this snapshot and is drained but never
      // reported, here or as malformed.
      if (msg.info.streamSequence > lastSeq) continue;
      try {
        facts.push(parseDeviceFact(msg, "snapshot"));
      } catch {
        malformed += 1;
      }
    }
    return {
      facts: dedupeDeviceFacts(facts),
      consumer: consumerInfo.name,
      streamMessages: info.state.messages,
      streamFirstSequence: firstSeq,
      streamLastSequence: lastSeq,
      readAt,
      malformed,
    };
  } finally {
    // The snapshot ephemeral is a bounded read, never reused: delete it even if
    // the drain failed, so a retry starts from a fresh consumer.
    await consumer.delete().catch(() => {});
  }
}

export interface DeviceFactLiveHandlers {
  onFact: (fact: DeviceFact) => void;
  onMalformed?: (error: Error) => void;
  onError?: (error: Error) => void;
}

export interface DeviceFactLive {
  consumer: string;
  close(): Promise<void>;
}

/**
 * Explicit ephemeral DeliverNew tail. It sees nothing already stored and only
 * facts published after it is created; it keeps no position, so switching live
 * off deletes it and re-enabling starts a fresh consumer at the then-current
 * tail. This is not durable recovery. `close()` is idempotent and always
 * settles: it keeps draining the pull iterator so nats.ws can resolve its close
 * sentinel, then deletes the consumer. The drain is what close() waits on, not
 * nats.ws's own ConsumerMessages.close(), which can stay pending forever if the
 * iterator dies abruptly. A consumer deleted out from under the tail aborts the
 * iterator and reports through `onError` rather than showing LIVE forever.
 */
export async function startDeviceFactLive(
  nc: NatsConnection,
  handlers: DeviceFactLiveHandlers,
): Promise<DeviceFactLive> {
  const js = nc.jetstream();
  const jsm = await nc.jetstreamManager();
  const consumerInfo = await jsm.consumers.add(DEVICE_FACT_STREAM, {
    description: "hearth-device-facts live tail (ephemeral DeliverNew)",
    name: undefined,
    durable_name: undefined,
    filter_subject: DEVICE_FACT_SUBJECT_FILTER,
    deliver_policy: DeliverPolicy.New,
    ack_policy: AckPolicy.None,
    replay_policy: ReplayPolicy.Instant,
    inactive_threshold: nanos(CONSUMER_INACTIVE_MS),
    mem_storage: true,
    num_replicas: 1,
  });
  const consumer = js.consumers.getPullConsumerFor(consumerInfo);

  let closed = false;
  let messages: ConsumerMessages | null = null;
  let drain: Promise<void> | null = null;
  let closePromise: Promise<void> | null = null;

  /**
   * Drain one pull iterator to completion, starting at most once. nats.ws
   * settles ConsumerMessages.close() only after the iterator consumes the close
   * sentinel it queues, so a drain that stops before that leaves close() pending
   * forever and blocks consumer deletion and lease release behind it. Once
   * `closed` is set the loop therefore never breaks: it keeps draining and
   * suppresses delivery callbacks instead.
   */
  const startDrain = (source: ConsumerMessages): Promise<void> => {
    if (drain) return drain;
    const draining = (async () => {
      for await (const msg of source) {
        if (closed) continue;
        try {
          handlers.onFact(parseDeviceFact(msg, "live"));
        } catch (err) {
          handlers.onMalformed?.(asError(err));
        }
      }
      // The iterator ends cleanly only once something stopped it: our own
      // close() sets `closed` first. Anything else is a dead tail, and showing
      // LIVE forever is worse than reporting it.
      if (!closed) throw new Error("the live consumer iterator ended without an error");
    })().catch(async (err: unknown) => {
      // A stop initiated by our own close() is not a delivery failure.
      if (closed) return;
      // The iterator has already terminated (that is why this drained), so close
      // without waiting on the drain this handler belongs to.
      await close(false);
      handlers.onError?.(asError(err));
    });
    // Bookkeeping only: nothing awaits this promise, so a throwing handler must
    // not surface as an unhandled rejection.
    drain = draining.catch(() => {});
    return drain;
  };

  /**
   * Stop one pull iterator. The drain must already be running before nats.ws's
   * close() is called, because that close() only resolves once the drain
   * consumes the close sentinel it queues. Its promise is then deliberately not
   * awaited: an iterator that dies abruptly first (the connection dropping
   * mid-batch rejects the iterator's next()) queues a second sentinel that
   * nothing will ever consume, so ConsumerMessages.close() stays pending forever.
   * Its synchronous part already unsubscribes and clears timers; the drain is
   * the promise that always settles.
   */
  const stopIterator = (source: ConsumerMessages): Promise<void> => {
    const drained = startDrain(source);
    void source.close().catch(() => {});
    return drained;
  };

  /**
   * Suppress callbacks, stop the pull iterator, delete the consumer.
   * `waitForDrain` must be false when called from the drain's own failure path,
   * because that path *is* the drain and awaiting it would deadlock.
   */
  const teardown = async (waitForDrain: boolean): Promise<void> => {
    closed = true;
    const current = messages;
    messages = null;
    if (current) {
      const drained = stopIterator(current);
      if (waitForDrain) await drained;
    }
    await consumer.delete().catch(() => {});
  };

  const close = (waitForDrain = true): Promise<void> => {
    if (!closePromise) closePromise = teardown(waitForDrain);
    return closePromise;
  };

  // A socket that drops out from under the tail is a live error, not a silent
  // freeze. close() flips `closed` first, so our own teardown is ignored here.
  void nc.closed().then((err) => {
    if (closed) return;
    void close().finally(() => handlers.onError?.(err ?? new Error("NATS connection closed")));
  });

  let liveMessages: ConsumerMessages;
  try {
    liveMessages = await consumer.consume({
      max_messages: LIVE_PULL_BATCH,
      expires: LIVE_PULL_EXPIRES_MS,
      // A deleted ephemeral must surface as an error the page can re-enable
      // from, not be retried forever while the page still shows LIVE.
      abort_on_missing_resource: true,
    });
    messages = liveMessages;
  } catch (err) {
    await close();
    throw asError(err);
  }

  // Start the drain before anything awaits close(): it is the only thing that
  // consumes nats.ws's queued close sentinel. This also covers close() having
  // run while consume() was still awaiting (the connection closed mid-setup),
  // when no iterator existed yet for close() to stop.
  startDrain(liveMessages);

  if (closed) {
    await stopIterator(liveMessages);
    await close();
  }

  return { consumer: consumerInfo.name, close };
}
