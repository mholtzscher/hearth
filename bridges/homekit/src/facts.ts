import { connect } from '@nats-io/transport-node';
import { BoundaryError, canonicalId, object } from './boundary.js';
import type { Config } from './config.js';
import { diagnostic } from './logging.js';

/** Facts invalidate HTTP projections; no payload value ever becomes State. */
export async function startFactHints(config: Config, hint: (entityId: string) => void, signal: AbortSignal): Promise<{ close(): Promise<void> }> {
  signal.throwIfAborted();
  const ids = new Set(config.accessories.flatMap(item => item.kind === 'light' && item.brightnessEntity ? [item.powerEntity, item.brightnessEntity] : [item.powerEntity]));
  const connection = await connect({ servers: config.natsURL, timeout: 2000, maxReconnectAttempts: -1, reconnectTimeWait: 250, reconnectJitter: 100 });
  let closing = false;
  const abort = () => { void connection.close(); };
  signal.addEventListener('abort', abort, { once: true });
  if (signal.aborted) abort();
  const subscriptions = [...ids].map(id => connection.subscribe(`hearth.v1.core.fact.entity.${id}.observation.*`, {
    callback: (error, message) => {
      if (error || closing) return;
      try {
        const tokens = message.subject.split('.');
        if (tokens.length !== 8 || tokens.slice(0, 5).join('.') !== 'hearth.v1.core.fact.entity' || tokens[6] !== 'observation') return;
        const entityId = canonicalId(tokens[5], 'ent');
        const variant = tokens[7];
        if (!ids.has(entityId) || (variant !== 'applied' && variant !== 'unchanged') || message.data.byteLength > 1024 * 1024) return;
        const parsed: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(message.data));
        const envelope = object(parsed);
        const data = object(envelope.data);
        if (envelope.schema !== 'urn:hearth:schema:observation-fact:v1' || data.entity_id !== entityId || data.disposition !== variant) return;
        hint(entityId);
      } catch { /* Malformed/unrelated hints have no authority and allocate no work. */ }
    },
  }));
  try {
    await connection.flush();
    signal.throwIfAborted();
  } catch {
    signal.removeEventListener('abort', abort); await connection.close();
    throw new BoundaryError('nats_startup_failed');
  }
  signal.removeEventListener('abort', abort);
  const statusTask = (async () => {
    for await (const status of connection.status()) {
      if (closing) break;
      if (status.type === 'disconnect') diagnostic('facts', 'nats_disconnected', 'dependency');
      if (status.type === 'reconnect') {
        try {
          await connection.flush();
          if (closing) continue;
          diagnostic('facts', 'nats_reconnected', 'dependency');
          for (const id of ids) hint(id);
        } catch { diagnostic('facts', 'nats_readiness_failed', 'dependency'); }
      }
    }
  })().catch(() => { if (!closing) diagnostic('facts', 'nats_status_failed', 'dependency'); });
  return { async close() {
    closing = true;
    for (const subscription of subscriptions) subscription.unsubscribe();
    await connection.close();
    await statusTask;
  } };
}
