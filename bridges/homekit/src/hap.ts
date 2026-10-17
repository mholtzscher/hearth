import { request } from 'node:http';
import { Accessory, Bridge, Categories, Characteristic, HAPStatus, HapStatusError, MDNSAdvertiser, Perms, Service } from '@homebridge/hap-nodejs';
import type { CharacteristicGetCallback, CharacteristicSetCallback } from '@homebridge/hap-nodejs';
import { BoundaryError, object } from './boundary.js';
import type { AccessoryConfig, Config } from './config.js';
import { diagnostic } from './logging.js';
import { HearthClient, HearthError } from './hearth.js';
import type { BridgeManifest } from './identity.js';
import { accessoryUUID, bridgeUUID } from './identity.js';
import type { EntityProjection } from './projection.js';

export type Operation = { deadline: number; signal: AbortSignal; state: 'pending' | 'completed' };
type Admitted = { operation: Operation; done: Promise<void>; fail(error: Error): void };
type Slot = { projection: EntityProjection; characteristic: Characteristic };

function communication(): HapStatusError { return new HapStatusError(HAPStatus.SERVICE_COMMUNICATION_FAILURE); }
function mapError(error: unknown): HapStatusError {
  if (error instanceof HapStatusError) return error;
  if (error instanceof HearthError && error.status === 400) return new HapStatusError(HAPStatus.INVALID_VALUE_IN_REQUEST);
  if (error instanceof HearthError && error.status === 504) return new HapStatusError(HAPStatus.OPERATION_TIMED_OUT);
  return communication();
}

/** Public external listener observation: recognizable HAP, bounded bytes AND elapsed time. */
export function probeHAPListener(port: number, signal: AbortSignal): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const timeout = AbortSignal.any([signal, AbortSignal.timeout(1000)]);
    const req = request({ hostname: '127.0.0.1', port, path: '/accessories', method: 'GET', signal: timeout }, response => {
      let size = 0;
      const chunks: Buffer[] = [];
      response.on('data', (chunk: Buffer) => {
        size += chunk.length;
        if (size > 4096) { req.destroy(); reject(new BoundaryError('hap_probe_failed')); }
        else chunks.push(chunk);
      });
      response.on('error', () => reject(new BoundaryError('hap_probe_failed')));
      response.on('end', () => {
        try {
          timeout.throwIfAborted();
          const value: unknown = JSON.parse(Buffer.concat(chunks).toString('utf8'));
          if (size > 4096 || response.statusCode !== 470 || object(value).status !== -70401) throw new Error();
          resolve();
        } catch { reject(new BoundaryError('hap_probe_failed')); }
      });
    });
    req.on('error', () => reject(new BoundaryError('hap_probe_failed')));
    req.end();
  });
}

/** Owns topology, public handlers, operation completion, and explicit notifications. */
export class HomeKitBridge {
  private readonly bridge: Bridge;
  private readonly active = new Set<Admitted>();
  private readonly unsubscribers: (() => void)[] = [];
  private admitting = true;

  constructor(private readonly config: Config, private readonly manifest: BridgeManifest,
    private readonly client: HearthClient, projections: ReadonlyMap<string, EntityProjection>) {
    this.bridge = new Bridge(config.name, bridgeUUID(config.bridgeId));
    this.bridge.disableUnusedIDPurge();
    this.bridge.on('identify', (_paired, callback) => callback());
    for (const item of config.accessories) {
      const accessory = new Accessory(item.name, accessoryUUID(config.bridgeId, item.key));
      accessory.on('identify', (_paired, callback) => callback());
      const service = item.kind === 'light' ? new Service.Lightbulb(item.name, 'primary') : new Service.Outlet(item.name, 'primary');
      // OutletInUse is a library-required default, not Hearth usage evidence.
      // Remove it with the public API rather than fabricate an electrical fact.
      if (item.kind === 'outlet') service.removeCharacteristic(service.getCharacteristic(Characteristic.OutletInUse));
      accessory.addService(service);
      const power = projections.get(item.powerEntity);
      if (!power) throw new BoundaryError('projection_missing');
      const slots: Slot[] = [{ projection: power, characteristic: service.getCharacteristic(Characteristic.On) }];
      if (item.kind === 'light' && item.brightnessEntity) {
        const projection = projections.get(item.brightnessEntity);
        if (!projection || projection.descriptor.kind !== 'brightness') throw new BoundaryError('projection_missing');
        const characteristic = service.getCharacteristic(Characteristic.Brightness);
        characteristic.setProps({ minValue: 0, maxValue: projection.descriptor.maximum, minStep: 1 });
        slots.push({ projection, characteristic });
      }
      const compatible = () => slots.every(slot => {
        const current = slot.projection.current();
        return current.kind !== 'unusable' || !['entity_missing', 'descriptor_changed'].includes(current.reason);
      });
      const update = () => {
        for (const slot of slots) {
          const current = slot.projection.current();
          slot.characteristic.updateValue(compatible() && current.kind === 'usable' ? current.value : communication());
        }
      };
      for (const slot of slots) {
        slot.characteristic.setProps({ perms: [...slot.characteristic.props.perms, Perms.WRITE_RESPONSE] });
        slot.characteristic.on('get', callback => this.handle(item, slot, compatible, callback, 'get'));
        slot.characteristic.on('set', (value, callback) => this.handle(item, slot, compatible, callback, 'set', value));
        this.unsubscribers.push(slot.projection.onChange(update));
      }
      update();
      this.bridge.addBridgedAccessory(accessory);
    }
  }

  private handle(item: AccessoryConfig, slot: Slot, compatible: () => boolean,
    callback: CharacteristicGetCallback | CharacteristicSetCallback, mode: 'get' | 'set', input?: unknown): void {
    if (!this.admitting) { callback(communication()); return; }
    const controller = new AbortController();
    const operation: Operation = { deadline: performance.now() + 8000, signal: controller.signal, state: 'pending' };
    let join: () => void = () => {};
    const done = new Promise<void>(resolve => { join = resolve; });
    let timer: NodeJS.Timeout;
    const complete = (error?: unknown) => {
      if (operation.state !== 'pending') return;
      if (performance.now() >= operation.deadline) error = new HapStatusError(HAPStatus.OPERATION_TIMED_OUT);
      if (!error && !compatible()) error = communication();
      const current = slot.projection.current();
      if (!error && current.kind !== 'usable') error = communication();
      operation.state = 'completed';
      clearTimeout(timer);
      controller.abort();
      try {
        if (error) {
          const mapped = mapError(error);
          diagnostic('hap', mapped.hapStatus === HAPStatus.OPERATION_TIMED_OUT ? 'operation_timeout' : 'operation_failed', 'operation', { accessoryKey: item.key, entityId: slot.projection.descriptor.id });
          callback(mapped);
        } else if (current.kind === 'usable') {
          // No await/microtask boundary between sampling, cache update and callback.
          slot.characteristic.updateValue(current.value);
          callback(null, current.value);
        }
      } finally { this.active.delete(admitted); join(); }
    };
    const admitted: Admitted = { operation, done, fail: complete };
    this.active.add(admitted);
    timer = setTimeout(() => complete(new HapStatusError(HAPStatus.OPERATION_TIMED_OUT)), 8000);
    const work = async () => {
      if (!compatible()) throw communication();
      if (mode === 'set') {
        const descriptor = slot.projection.descriptor;
        let value: boolean | number;
        if (descriptor.kind === 'power') {
          if (typeof input !== 'boolean') throw new HapStatusError(HAPStatus.INVALID_VALUE_IN_REQUEST);
          value = input;
        } else {
          if (typeof input !== 'number' || !Number.isInteger(input) || input < 0 || input > descriptor.maximum) throw new HapStatusError(HAPStatus.INVALID_VALUE_IN_REQUEST);
          const largest = Math.floor(descriptor.maximum / descriptor.commandStep) * descriptor.commandStep;
          value = Math.min(largest, Math.floor(input / descriptor.commandStep + 0.5) * descriptor.commandStep);
        }
        const result = await this.client.setEntity(descriptor.id, value, controller.signal);
        if (operation.state !== 'pending' || performance.now() >= operation.deadline) throw new HapStatusError(HAPStatus.OPERATION_TIMED_OUT);
        diagnostic('hap', 'command_satisfied', 'command', { accessoryKey: item.key, entityId: descriptor.id, commandId: result.commandId });
      }
      if (operation.state !== 'pending') return;
      await slot.projection.refreshAfterNow(controller.signal);
    };
    void work().then(() => complete(), error => complete(error));
  }

  async publish(pairingCode: string, signal: AbortSignal, initialized: () => void): Promise<void> {
    signal.throwIfAborted();
    let listening = false; let advertised = false;
    let resolveEvents: () => void = () => {};
    let rejectEvents: (error: unknown) => void = () => {};
    const events = new Promise<void>((resolve, reject) => { resolveEvents = resolve; rejectEvents = reject; });
    const check = () => { if (listening && advertised) resolveEvents(); };
    const onListening = () => {
      try { initialized(); listening = true; check(); }
      catch (error) { rejectEvents(error); }
    };
    const onAdvertised = () => { advertised = true; check(); };
    const abort = () => rejectEvents(new BoundaryError('hap_startup_timeout'));
    signal.addEventListener('abort', abort, { once: true });
    this.bridge.on('listening', onListening);
    this.bridge.on('advertised', onAdvertised);
    try {
      // publish resolving does NOT establish readiness; event handlers are already installed.
      const publishing = this.bridge.publish({ username: this.manifest.username, setupID: this.manifest.setupId,
        pincode: pairingCode, category: Categories.BRIDGE, port: this.config.hapPort,
        bind: [...this.config.advertiseInterfaces], advertiser: MDNSAdvertiser.CIAO });
      await Promise.all([publishing, events]);
      signal.throwIfAborted();
      await probeHAPListener(this.config.hapPort, signal);
    } finally {
      signal.removeEventListener('abort', abort);
      this.bridge.off('listening', onListening); this.bridge.off('advertised', onAdvertised);
    }
  }

  stopAdmission(): void { this.admitting = false; }
  failOperations(): void { for (const admitted of this.active) admitted.fail(communication()); }
  async close(): Promise<void> {
    this.stopAdmission();
    await Promise.all([...this.active].map(admitted => admitted.done));
    // Callback completion resolves HAP's promise chain, not its internal
    // loopback HTTP transport. A bounded public roundtrip gives queued responses
    // an I/O opportunity before unpublish destroys connections. This is not a
    // remote receipt guarantee, and a broken listener cannot block shutdown.
    await probeHAPListener(this.config.hapPort, AbortSignal.timeout(1000)).catch(() => undefined);
    for (const unsubscribe of this.unsubscribers.splice(0)) unsubscribe();
    await this.bridge.unpublish();
  }
}
