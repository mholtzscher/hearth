import { createHash, createPrivateKey, createPublicKey, randomBytes, randomInt } from 'node:crypto';
import { constants, accessSync, closeSync, existsSync, fsyncSync, lstatSync, mkdirSync, openSync, readFileSync, readdirSync, renameSync, statfsSync, unlinkSync, watch, writeFileSync } from 'node:fs';
import type { FSWatcher } from 'node:fs';
import { join } from 'node:path';
import { uuid } from '@homebridge/hap-nodejs';
import { BoundaryError, exactKeys, object, text } from './boundary.js';
import { slug } from './config.js';
import type { Config } from './config.js';

export type BridgeManifest = {
  version: 1; stage: 'initializing' | 'initialized'; bridgeId: string;
  username: string; setupId: string; pairingCodeDigest: string;
  accessoryKinds: Record<string, 'light' | 'outlet'>;
};

function digest(code: string): string { return createHash('sha256').update(code).digest('hex'); }

/** Identity namespaces never depend on display names, ordering, or current Entity mappings. */
export function bridgeUUID(bridgeId: string): string { return uuid.generate(`hearth/homekit/bridge:${bridgeId}`); }
export function accessoryUUID(bridgeId: string, key: string): string { return uuid.generate(`hearth/homekit/${bridgeId}/${key}`); }

function privatePath(path: string, directory: boolean): void {
  const stat = lstatSync(path);
  if (stat.isSymbolicLink() || (directory ? !stat.isDirectory() : !stat.isFile()) || (stat.mode & 0o077) !== 0 || stat.uid !== process.getuid?.()) {
    throw new BoundaryError('insecure_storage');
  }
  accessSync(path, constants.R_OK | constants.W_OK | (directory ? constants.X_OK : 0));
}

function readJSON(path: string): Record<string, unknown> {
  privatePath(path, false);
  const stat = lstatSync(path);
  if (stat.size > 1024 * 1024) throw new BoundaryError('invalid_identity');
  const value: unknown = JSON.parse(readFileSync(path, 'utf8'));
  return object(value);
}

function decodeManifest(input: Record<string, unknown>): BridgeManifest {
  exactKeys(input, ['version', 'stage', 'bridgeId', 'username', 'setupId', 'pairingCodeDigest', 'accessoryKinds']);
  if (input.version !== 1 || (input.stage !== 'initializing' && input.stage !== 'initialized')) throw new BoundaryError('invalid_manifest');
  const username = text(input.username);
  const setupId = text(input.setupId);
  const pairingCodeDigest = text(input.pairingCodeDigest);
  if (!/^(?:[0-9A-F]{2}:){5}[0-9A-F]{2}$/.test(username) || !/^[A-Z0-9]{4}$/.test(setupId) || !/^[0-9a-f]{64}$/.test(pairingCodeDigest)) throw new BoundaryError('invalid_manifest');
  const accessoryKinds: Record<string, 'light' | 'outlet'> = {};
  for (const [key, kind] of Object.entries(object(input.accessoryKinds))) {
    slug(key);
    if (kind !== 'light' && kind !== 'outlet') throw new BoundaryError('invalid_manifest');
    Object.defineProperty(accessoryKinds, key, { value: kind, enumerable: true, writable: true, configurable: true });
  }
  return { version: 1, stage: input.stage, bridgeId: slug(input.bridgeId), username, setupId, pairingCodeDigest, accessoryKinds };
}

function atomicManifest(directory: string, manifest: BridgeManifest): void {
  const temporary = join(directory, `.bridge.${randomBytes(16).toString('hex')}.tmp`);
  let fd: number | undefined;
  try {
    fd = openSync(temporary, 'wx', 0o600);
    writeFileSync(fd, `${JSON.stringify(manifest)}\n`);
    fsyncSync(fd);
    closeSync(fd);
    fd = undefined;
    renameSync(temporary, join(directory, 'bridge.json'));
    const parent = openSync(directory, 'r');
    try { fsyncSync(parent); } finally { closeSync(parent); }
  } catch {
    throw new BoundaryError('manifest_persistence_failed');
  } finally {
    if (fd !== undefined) closeSync(fd);
    try { unlinkSync(temporary); } catch { /* cleanup is best effort, never a replacement identity */ }
  }
}

/** Cached allocation counters are not self-healing; a rewind can alias controls. */
function validateIdentifierCache(cache: Record<string, unknown>, bridgeId: string): void {
  const root = bridgeUUID(bridgeId);
  const uuidPattern = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}';
  const keyPattern = new RegExp(`^(?:\\|nextAID|${uuidPattern}(?:\\|nextIID|\\|${uuidPattern}(?:\\|primary)?(?:\\|${uuidPattern})?)?)$`, 'i');
  const aids = new Set<number>();
  const accessoryIds = new Set<string>();
  const groups = new Map<string, { next: number | undefined; raw: Set<number>; wire: Set<number> }>();
  function invalid(): never { throw new BoundaryError('invalid_identifier_cache'); }
  for (const [key, value] of Object.entries(cache)) {
    if (!keyPattern.test(key) || typeof value !== 'number' || !Number.isSafeInteger(value)
      || value < 2 || !Number.isSafeInteger(value + 2_000_000_000)) invalid();
    const parts = key.split('|');
    const accessory = parts[0];
    if (key === '|nextAID') continue;
    if (!accessory) invalid();
    if (parts.length === 1) {
      if (accessory === root || aids.has(value)) invalid();
      aids.add(value); accessoryIds.add(accessory);
      continue;
    }
    let group = groups.get(accessory);
    if (!group) { group = { next: undefined, raw: new Set(), wire: new Set([1]) }; groups.set(accessory, group); }
    if (parts[1] === 'nextIID') { group.next = value; continue; }
    const service = parts.length === 2 || (parts.length === 3 && parts[2] === 'primary');
    const wire = value + (accessory === root && service ? 2_000_000_000 : 0);
    if (group.raw.has(value) || group.wire.has(wire)) invalid();
    group.raw.add(value); group.wire.add(wire);
  }
  const nextAID = cache['|nextAID'];
  if (typeof nextAID !== 'number' || [...aids].some(aid => aid >= nextAID) || !groups.has(root)) invalid();
  for (const [accessory, group] of groups) {
    const next = group.next;
    // Counters advance raw allocated IIDs, not the bridge's offset wire IDs.
    if (next === undefined || [...group.raw].some(iid => iid >= next) || (accessory !== root && !accessoryIds.has(accessory))) invalid();
    // Root service IDs reserve the offset range, including future allocations.
    if (accessory === root && next >= 2_000_000_000) invalid();
  }
  for (const accessory of accessoryIds) if (!groups.has(accessory)) invalid();
}

/**
 * Locked-runtime owner. The caller must hold bridge.lock before calling open and
 * for the entire store lifetime; no method initializes HAP or owns an OS lock.
 * A partial initialization is deliberately not resumable.
 */
export class IdentityStore {
  private manifestValue: BridgeManifest | undefined;
  private hapIdentity: { dev: number; ino: number } | undefined;
  private watcher: FSWatcher | undefined;

  private constructor(private readonly config: Config, private readonly code: string) {}

  static open(config: Config, code: string, initialize: boolean): IdentityStore {
    const store = new IdentityStore(config, code);
    try {
      privatePath(config.dataDir, true);
      // First release is local-filesystem only; reject the common network filesystem types.
      const type = statfsSync(config.dataDir).type >>> 0;
      if ([0x6969, 0xff534d42, 0xfe534d42, 0x517b, 0x564c, 0x5346414f, 0x01021997, 0x00c36400, 0x65735546].includes(type)) throw new BoundaryError('network_storage_unsupported');
      const probe = join(config.dataDir, `.writable.${randomBytes(16).toString('hex')}`);
      const fd = openSync(probe, 'wx', 0o600);
      closeSync(fd);
      unlinkSync(probe);
      const entries = readdirSync(config.dataDir).filter(entry => entry !== 'bridge.lock');
      if (initialize) {
        if (entries.length) throw new BoundaryError('initialization_requires_empty_store');
        return store;
      }
      const manifest = decodeManifest(readJSON(join(config.dataDir, 'bridge.json')));
      if (manifest.stage !== 'initialized') throw new BoundaryError('initialization_incomplete');
      if (manifest.bridgeId !== config.bridgeId) throw new BoundaryError('bridge_id_mismatch');
      if (manifest.pairingCodeDigest !== digest(code)) throw new BoundaryError('pairing_code_changed');
      store.manifestValue = manifest;
      store.checkKinds();
      store.checkIntegrity();
      return store;
    } catch (error) {
      if (error instanceof BoundaryError) throw error;
      throw new BoundaryError('identity_unavailable');
    }
  }

  get manifest(): BridgeManifest {
    if (!this.manifestValue) throw new BoundaryError('identity_not_initialized');
    return structuredClone(this.manifestValue);
  }

  /** Call only after all Core descriptors have been validated, before any HAP object/store access. */
  beginInitialization(): BridgeManifest {
    if (this.manifestValue || readdirSync(this.config.dataDir).some(entry => entry !== 'bridge.lock')) throw new BoundaryError('initialization_requires_empty_store');
    const bytes = randomBytes(6);
    // Locally administered unicast address, not a physical NIC identity.
    bytes[0] = ((bytes[0] ?? 0) | 2) & 0xfe;
    const alphabet = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ';
    const manifest: BridgeManifest = {
      version: 1, stage: 'initializing', bridgeId: this.config.bridgeId,
      username: [...bytes].map(byte => byte.toString(16).padStart(2, '0')).join(':').toUpperCase(),
      setupId: Array.from({ length: 4 }, () => alphabet.charAt(randomInt(alphabet.length))).join(''),
      pairingCodeDigest: digest(this.code), accessoryKinds: {},
    };
    for (const accessory of this.config.accessories) manifest.accessoryKinds[accessory.key] = accessory.kind;
    atomicManifest(this.config.dataDir, manifest);
    this.manifestValue = manifest;
    mkdirSync(join(this.config.dataDir, 'hap'), { mode: 0o700 });
    return this.manifest;
  }

  /** HAP must have established BOTH required files before initialization can commit. */
  commitInitialized(): BridgeManifest {
    if (this.manifest.stage !== 'initializing') throw new BoundaryError('identity_already_initialized');
    this.checkIntegrity();
    const manifest = { ...this.manifest, stage: 'initialized' as const };
    atomicManifest(this.config.dataDir, manifest);
    this.manifestValue = manifest;
    return this.manifest;
  }

  /** Call after descriptor validation. Retired keys remain reserved indefinitely. */
  recordAccessoryKinds(): BridgeManifest {
    this.checkKinds();
    const manifest = this.manifest;
    let changed = false;
    for (const accessory of this.config.accessories) {
      if (!Object.hasOwn(manifest.accessoryKinds, accessory.key)) {
        manifest.accessoryKinds[accessory.key] = accessory.kind;
        changed = true;
      }
    }
    if (changed) {
      atomicManifest(this.config.dataDir, manifest);
      this.manifestValue = manifest;
    }
    return this.manifest;
  }

  private checkKinds(): void {
    const manifest = this.manifest;
    for (const accessory of this.config.accessories) {
      const saved = Object.hasOwn(manifest.accessoryKinds, accessory.key) ? manifest.accessoryKinds[accessory.key] : undefined;
      if (saved !== undefined && saved !== accessory.kind) throw new BoundaryError('accessory_kind_changed');
    }
  }

  /** Recheck during reconciliation; disappearance is fatal, never permission to regenerate. */
  checkIntegrity(): void {
    try {
      privatePath(this.config.dataDir, true);
      const disk = decodeManifest(readJSON(join(this.config.dataDir, 'bridge.json')));
      if (JSON.stringify(disk) !== JSON.stringify(this.manifest)) throw new BoundaryError('manifest_changed');
      const hap = join(this.config.dataDir, 'hap');
      privatePath(hap, true);
      const current = lstatSync(hap);
      if (this.hapIdentity && (current.dev !== this.hapIdentity.dev || current.ino !== this.hapIdentity.ino)) throw new BoundaryError('hap_directory_replaced');
      this.hapIdentity = { dev: current.dev, ino: current.ino };
      const suffix = this.manifest.username.replaceAll(':', '');
      const info = readJSON(join(hap, `AccessoryInfo.${suffix}.json`));
      if (info.pincode !== this.code || info.setupID !== this.manifest.setupId || info.category !== 2) throw new BoundaryError('invalid_hap_identity');
      const publicHex = text(info.signPk);
      const secretHex = text(info.signSk);
      if (!/^[0-9a-f]{64}$/i.test(publicHex) || !/^[0-9a-f]{128}$/i.test(secretHex) || secretHex.slice(64) !== publicHex) throw new BoundaryError('invalid_hap_keys');
      // Verify the seed really derives this public key, not merely equal trailing bytes.
      const seed = Buffer.from(secretHex.slice(0, 64), 'hex');
      const privateDER = Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), seed]);
      const derived = createPublicKey(createPrivateKey({ key: privateDER, format: 'der', type: 'pkcs8' })).export({ format: 'der', type: 'spki' });
      if (!derived.subarray(-32).equals(Buffer.from(publicHex, 'hex'))) throw new BoundaryError('invalid_hap_keys');
      if (typeof info.displayName !== 'string' || typeof info.configHash !== 'string' || !Number.isInteger(info.configVersion) || typeof info.configVersion !== 'number' || info.configVersion < 1 || info.configVersion > 65535) throw new BoundaryError('invalid_hap_identity');
      const clients = object(info.pairedClients);
      const permissions = object(info.pairedClientsPermission);
      for (const [client, key] of Object.entries(clients)) {
        if (!client || typeof key !== 'string' || !/^[0-9a-f]{64}$/i.test(key) || (permissions[client] !== 0 && permissions[client] !== 1)) throw new BoundaryError('invalid_hap_pairings');
      }
      if (Object.keys(permissions).some(client => !(client in clients))) throw new BoundaryError('invalid_hap_pairings');
      if (Object.keys(clients).length && !Object.values(permissions).includes(1)) throw new BoundaryError('invalid_hap_pairings');
      const cache = object(readJSON(join(hap, `IdentifierCache.${suffix}.json`)).cache);
      validateIdentifierCache(cache, this.config.bridgeId);
      // Validate all other owned HAP files too: no permissive symlink or public-secret escape.
      for (const file of readdirSync(hap)) privatePath(join(hap, file), false);
    } catch (error) {
      if (error instanceof BoundaryError) throw error;
      throw new BoundaryError('identity_unavailable');
    }
  }

  /** Directory watch is supplementary to reconciliation, not a transactional loss detector. */
  watchIntegrity(onFatal: () => void): void {
    if (this.watcher) throw new BoundaryError('identity_watch_already_started');
    this.watcher = watch(this.config.dataDir, (_event, filename) => {
      if (filename !== null && filename !== 'hap' && filename !== 'bridge.json') return;
      try { this.checkIntegrity(); } catch { onFatal(); }
    });
    this.watcher.on('error', onFatal);
  }

  close(): void { this.watcher?.close(); this.watcher = undefined; }
}

/** Public supervisor may prepare only the directory/lock, never read HAP data. */
export function prepareDataDirectory(config: Config, initialize: boolean): void {
  try {
    if (!existsSync(config.dataDir)) {
      if (!initialize) throw new BoundaryError('identity_unavailable');
      mkdirSync(config.dataDir, { recursive: true, mode: 0o700 });
    }
    privatePath(config.dataDir, true);
    const lock = join(config.dataDir, 'bridge.lock');
    if (!existsSync(lock)) {
      const fd = openSync(lock, 'wx', 0o600);
      closeSync(fd);
    }
    privatePath(lock, false);
  } catch (error) {
    if (error instanceof BoundaryError) throw error;
    throw new BoundaryError('storage_unavailable');
  }
}

/** HAP setup payload: category Bridge (2), IP bit 28, eight decimal code digits. No listener needed. */
export function pairingInfo(manifest: BridgeManifest, code: string): { code: string; setupURI: string } {
  const payload = (2n << 31n) | (1n << 28n) | BigInt(code.replaceAll('-', ''));
  return { code, setupURI: `X-HM://${payload.toString(36).toUpperCase().padStart(9, '0')}${manifest.setupId}` };
}
