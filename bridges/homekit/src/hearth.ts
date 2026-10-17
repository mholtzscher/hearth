import { readFileSync } from 'node:fs';
import { Ajv2020 } from 'ajv/dist/2020.js';
import { BoundaryError, canonicalId, object } from './boundary.js';
import type { Config } from './config.js';
import { diagnostic } from './logging.js';

export type EntityDescriptor =
  | { id: string; deviceId: string; kind: 'power' }
  | { id: string; deviceId: string; kind: 'brightness'; maximum: number; commandStep: number };

export type EntityRead = {
  descriptor: EntityDescriptor; enabled: boolean;
  availability: 'unknown' | 'available' | 'unavailable';
  state: null | { observationId: string; value: boolean | number };
};

export type SatisfiedCommand = { commandId: string; observationId: string; value: boolean | number };
export type ReadPriority = 'foreground' | 'background';

export class HearthError extends BoundaryError {
  constructor(code: string, readonly status?: number, readonly descriptor?: EntityDescriptor) { super(code); }
}

const ajv = new Ajv2020({ strict: true, allErrors: false });
function schema(kind: 'power' | 'brightness', file: string) {
  const input: unknown = JSON.parse(readFileSync(new URL(`./schemas/${kind}/${file}.schema.json`, import.meta.url), 'utf8'));
  return ajv.compile(object(input));
}
const schemas = {
  power: { support: schema('power', 'support'), state: schema('power', 'state') },
  brightness: { support: schema('brightness', 'support'), state: schema('brightness', 'state') },
};

function readValue(value: unknown, descriptor: EntityDescriptor): boolean | number {
  if (!schemas[descriptor.kind].state(value)) throw new HearthError('invalid_state');
  if (descriptor.kind === 'power' && typeof value === 'boolean') return value;
  if (descriptor.kind === 'brightness' && typeof value === 'number' && value <= descriptor.maximum) return value;
  throw new HearthError('invalid_state');
}

/** API response boundary; additive fields are allowed, every consumed field is validated. */
function decodeEntity(input: unknown, entityId: string): EntityRead {
  const data = object(input);
  const id = canonicalId(data.id, 'ent');
  const deviceId = canonicalId(data.device_id, 'dev');
  if (id !== entityId) throw new HearthError('entity_id_mismatch');
  let descriptor: EntityDescriptor;
  if (data.type === 'hearth.power/v1') {
    if (!schemas.power.support(data.support)) throw new HearthError('invalid_support');
    descriptor = { id, deviceId, kind: 'power' };
  } else if (data.type === 'hearth.brightness/v1') {
    if (!schemas.brightness.support(data.support)) throw new HearthError('invalid_support');
    const support = object(data.support);
    const maximum = object(support.state).maximum;
    const commandStep = object(object(support.operations).set).step;
    // Narrow the schema-proven fields without unchecked assertions.
    if (typeof maximum !== 'number' || typeof commandStep !== 'number') throw new HearthError('invalid_support');
    descriptor = { id, deviceId, kind: 'brightness', maximum, commandStep };
  } else throw new HearthError('unsupported_entity_type');
  try {
    const availability = object(data.availability).status;
    if (availability !== 'unknown' && availability !== 'available' && availability !== 'unavailable') throw new HearthError('invalid_availability');
    if (typeof data.enabled !== 'boolean') throw new HearthError('invalid_enablement');
    const state = data.state === null ? null : object(data.state);
    return {
      descriptor, enabled: data.enabled, availability,
      state: state === null ? null : { observationId: canonicalId(state.observation_id, 'obs'), value: readValue(state.value, descriptor) },
    };
  } catch (error) {
    // A schema-validated descriptor is independent of malformed dynamic State.
    // Retain that fact so shrinking support cannot leave a sibling control usable.
    throw new HearthError(error instanceof HearthError ? error.code : 'invalid_entity_response', undefined, descriptor);
  }
}

/** One bounded request per invocation. POST is never retried, even after an ambiguous failure. */
export class HearthClient {
  private requestFailed = false;
  private backgroundReads = 0;
  private readonly backgroundQueue = new Set<() => void>();
  constructor(private readonly coreURL: string) {}

  private acquireBackground(signal: AbortSignal): Promise<() => void> {
    signal.throwIfAborted();
    const release = () => {
      this.backgroundReads--;
      this.backgroundQueue.values().next().value?.();
    };
    if (this.backgroundReads < 8) { this.backgroundReads++; return Promise.resolve(release); }
    return new Promise((resolve, reject) => {
      const abort = () => { this.backgroundQueue.delete(enter); reject(new HearthError('core_timeout', 504)); };
      const enter = () => {
        this.backgroundQueue.delete(enter); signal.removeEventListener('abort', abort);
        if (signal.aborted) { reject(new HearthError('core_timeout', 504)); return; }
        this.backgroundReads++; resolve(release);
      };
      this.backgroundQueue.add(enter);
      signal.addEventListener('abort', abort, { once: true });
    });
  }

  private dependency(failed: boolean): void {
    if (failed === this.requestFailed) return;
    this.requestFailed = failed;
    diagnostic('hearth', failed ? 'core_request_failed' : 'core_request_recovered', 'dependency');
  }

  async readEntity(entityId: string, signal: AbortSignal, priority: ReadPriority = 'foreground'): Promise<EntityRead> {
    canonicalId(entityId, 'ent');
    const bounded = AbortSignal.any([signal, AbortSignal.timeout(2000)]);
    const release = priority === 'background' ? await this.acquireBackground(bounded) : undefined;
    try {
      const data = await this.request(`/v1/entities/${entityId}`, { method: 'GET' }, bounded, 2000);
      try { return decodeEntity(data, entityId); }
      catch (error) {
        if (error instanceof HearthError) throw error;
        throw new HearthError('invalid_entity_response');
      }
    } finally { release?.(); }
  }

  async setEntity(entityId: string, value: boolean | number, signal: AbortSignal): Promise<SatisfiedCommand> {
    canonicalId(entityId, 'ent');
    if (typeof value !== 'boolean' && (typeof value !== 'number' || !Number.isInteger(value) || value < 0 || value > 100)) throw new HearthError('invalid_command_value');
    const input = await this.request(`/v1/entities/${entityId}/commands`, {
      method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ operation: 'set', parameters: { value } }),
    }, signal, 8000);
    try {
      const data = object(input);
      if (data.status !== 'satisfied' || typeof data.value !== typeof value) throw new HearthError('invalid_command_response');
      const outcome = data.value;
      if (typeof outcome !== 'boolean' && (typeof outcome !== 'number' || !Number.isInteger(outcome) || outcome < 0 || outcome > 100)) throw new HearthError('invalid_command_response');
      return { commandId: canonicalId(data.command_id, 'cmd'), observationId: canonicalId(data.observation_id, 'obs'), value: outcome };
    } catch { throw new HearthError('invalid_command_response'); }
  }

  private async request(path: string, init: RequestInit, signal: AbortSignal, timeout: number): Promise<unknown> {
    const bounded = AbortSignal.any([signal, AbortSignal.timeout(timeout)]);
    const deadline = performance.now() + timeout;
    const check = () => {
      bounded.throwIfAborted();
      if (performance.now() >= deadline) throw new HearthError('core_timeout', 504);
    };
    try {
      check();
      const response = await fetch(`${this.coreURL}${path}`, { ...init, signal: bounded, redirect: 'manual' });
      if (!response.ok) {
        await response.body?.cancel();
        throw new HearthError('core_http_error', response.status);
      }
      if (!response.body) throw new HearthError('invalid_json_response');
      const reader = response.body.getReader();
      const chunks: Uint8Array[] = [];
      let length = 0;
      try {
        while (true) {
          const { done, value } = await reader.read();
          check();
          if (done) break;
          length += value.byteLength;
          if (length > 1024 * 1024) throw new HearthError('response_too_large');
          chunks.push(value);
        }
      } finally {
        await reader.cancel().catch(() => undefined);
        reader.releaseLock();
      }
      check();
      const parsed: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)));
      check();
      this.dependency(false);
      return parsed;
    } catch (error) {
      if (bounded.aborted || !(error instanceof HearthError) || (error.status !== undefined && error.status >= 500)) this.dependency(true);
      if (bounded.aborted) throw new HearthError('core_timeout', 504);
      if (error instanceof HearthError) throw error;
      throw new HearthError('core_response_failed');
    }
  }
}

/** Startup eligibility is descriptor-based, not current availability or Device kind. */
export function validateMappings(config: Config, reads: ReadonlyMap<string, EntityRead>): void {
  for (const accessory of config.accessories) {
    const power = reads.get(accessory.powerEntity)?.descriptor;
    if (!power || power.kind !== 'power') throw new HearthError('incompatible_mapping');
    if (accessory.kind === 'light' && accessory.brightnessEntity !== undefined) {
      const brightness = reads.get(accessory.brightnessEntity)?.descriptor;
      if (!brightness || brightness.kind !== 'brightness' || brightness.deviceId !== power.deviceId) throw new HearthError('incompatible_mapping');
    }
  }
}
