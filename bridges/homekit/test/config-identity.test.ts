import { afterEach, describe, expect, it } from 'vitest';
import { chmodSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { generateKeyPairSync } from 'node:crypto';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { loadConfig, parseCLI, readPairingCode, validateInterfaces } from '../dist/config.js';
import { accessoryUUID, bridgeUUID, IdentityStore, pairingInfo, prepareDataDirectory } from '../dist/identity.js';
import { brightnessId, code, fixture, powerId } from './support/config.js';

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });
function setup(overrides: Record<string, unknown> = {}) { const f = fixture(overrides); roots.push(f.root); return f; }

// Gate: static operator contract; typo/precedence/normalization regressions;
// no prior package coverage; actual config files/argv/env, no test-only seam.
describe('configuration boundary', () => {
  it('uses flags over environment over YAML over defaults and resolves paths from cwd', () => {
    const f = setup({ name: 'YAML', data_dir: 'relative-store' });
    const config = loadConfig(['--config', f.file, '--name', 'Flag', '--hap-port=52000'], {
      HEARTH_BRIDGE_HOMEKIT_NAME: 'Environment', HEARTH_BRIDGE_HOMEKIT_RECONCILE_INTERVAL: '1.5s',
    });
    expect(config).toMatchObject({ name: 'Flag', hapPort: 52000, reconcileIntervalMs: 1500, dataDir: join(process.cwd(), 'relative-store') });
    expect(loadConfig(['--config', f.file], { HEARTH_BRIDGE_HOMEKIT_NAME: 'Environment' }).name).toBe('Environment');
    expect(f.config()).toMatchObject({ name: 'YAML', hapPort: 51826, reconcileIntervalMs: 5000 });
    expect(parseCLI(['--config', f.file, '--initialize']).initialize).toBe(true);
    expect(() => parseCLI(['pairing-info', '--initialize'])).toThrow('invalid_argument');
  });

  it.each([
    { unknown: true }, { bridge_id: 'Not-a-slug' }, { hap_port: '51826x' }, { hap_port: 1023 },
    { reconcile_interval: '1000.5ms' }, { reconcile_interval: '61s' }, { reconcile_interval: '0.5s' },
    { core_url: 'http://localhost:8080/' }, { core_url: 'http://localhost' }, { core_url: 'https://localhost:8080' },
    { nats_url: 'nats://user@localhost:4222' }, { advertise_interfaces: [] }, { pairing_code: code },
    { accessories: [] }, { accessories: Array.from({ length: 150 }, () => ({ key: 'lamp' })) },
    { accessories: [{ key: 'lamp', name: ' ', kind: 'light', power_entity: powerId }] },
    { accessories: [{ key: 'lamp', name: 'a\n', kind: 'light', power_entity: powerId }] },
    { accessories: [{ key: 'lamp', name: '😀'.repeat(33), kind: 'light', power_entity: powerId }] },
    { accessories: [{ key: 'lamp', name: 'Lamp', kind: 'outlet', power_entity: powerId, brightness_entity: brightnessId }] },
    { accessories: [{ key: 'lamp', name: 'Lamp', kind: 'light', power_entity: powerId, brightness_entity: powerId }] },
    { accessories: [{ key: 'lamp', name: 'Lamp', kind: 'light', power_entity: powerId.toUpperCase() }] },
  ])('rejects malformed/unsafe operator input %#', overrides => {
    const f = setup(overrides);
    expect(() => f.config()).toThrow();
  });

  it.each(['name: one\nname: two\n', 'name: one\n---\nname: two\n', 'name: [unclosed'])('rejects ambiguous YAML %#', content => {
    const f = setup();
    writeFileSync(f.file, content);
    expect(() => f.config()).toThrow('invalid_config_file');
  });

  it.each([['+1s', 1000], ['1.s', 1000], ['1000000μs', 1000], ['1m', 60_000]] as const)('uses existing duration syntax %s with exact whole milliseconds', (input, expected) => {
    expect(setup({ reconcile_interval: input }).config().reconcileIntervalMs).toBe(expected);
  });

  it('rejects an explicit missing file and environment initialization/structured values', () => {
    const f = setup();
    expect(() => loadConfig(['--config', join(f.root, 'absent')], {})).toThrow('invalid_config_file');
    for (const key of ['INITIALIZE', 'ACCESSORIES', 'ADVERTISE_INTERFACES', 'PAIRING_CODE']) {
      expect(() => loadConfig(['--config', f.file], { [`HEARTH_BRIDGE_HOMEKIT_${key}`]: 'value' })).toThrow('unknown_environment_key');
    }
  });

  it('accepts mixed explicit profiles and rejects duplicate keys/entities', () => {
    const entries = [
      { key: 'lamp', name: 'Lamp', kind: 'light', power_entity: powerId },
      { key: 'plug', name: 'Plug', kind: 'outlet', power_entity: brightnessId },
    ];
    expect(setup({ accessories: entries }).config().accessories.map(item => item.kind)).toEqual(['light', 'outlet']);
    expect(() => setup({ accessories: [{ ...entries[0] }, { ...entries[0] }] }).config()).toThrow('duplicate_accessory_key');
    expect(() => setup({ accessories: [entries[0], { ...entries[1], power_entity: powerId }] }).config()).toThrow('duplicate_entity');
    const maximum = Array.from({ length: 149 }, (_, index) => ({ key: `lamp-${index}`, name: 'Lamp', kind: 'light', power_entity: `ent_01900000-0000-7000-8000-${String(index).padStart(12, '0')}` }));
    expect(setup({ accessories: maximum }).config().accessories.length).toBe(149);
  });

  it('reads only a permitted code with at most one LF, rejecting forbidden patterns without disclosing input', () => {
    const f = setup();
    const config = f.config();
    for (const content of [code, `${code}\n`]) {
      writeFileSync(config.pairingCodeFile, content);
      expect(readPairingCode(config)).toBe(code);
    }
    for (const content of ['111-11-111', '123-45-678', '876-54-321', `${code}\r\n`, `${code}\n\n`, '48265193']) {
      writeFileSync(config.pairingCodeFile, content);
      expect(() => readPairingCode(config)).toThrow('invalid_pairing_code');
    }
  });

  it('rejects missing and loopback advertisement interfaces at the OS boundary', () => {
    expect(() => validateInterfaces(setup({ advertise_interfaces: ['hearth-no-such-interface'] }).config())).toThrow('interface_unavailable');
    expect(() => validateInterfaces(setup({ advertise_interfaces: ['lo'] }).config())).toThrow('interface_unavailable');
  });

  it.each(['x'.repeat(59), '界'.repeat(20)])('rejects an oversized UTF-8 bridge label before creating storage', value => {
    const f = setup({ name: value });
    expect(() => f.config()).toThrow('bridge_name_too_long');
    expect(readdirSync(f.root).sort()).toEqual(['code', 'config.yaml']);
    expect(setup({ name: 'x'.repeat(58) }).config().name).toBe('x'.repeat(58));
    expect(setup({ name: '界'.repeat(19) }).config().name).toBe('界'.repeat(19));
  });
});

// Test fixture emits the documented HAP on-disk format, using independently
// generated Ed25519 keys. It never pretends to prove live pairing or AID/IID reuse.
function requiredFiles(config: ReturnType<ReturnType<typeof fixture>['config']>, store: IdentityStore) {
  const manifest = store.manifest;
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const publicBytes = publicKey.export({ format: 'der', type: 'spki' }).subarray(-32);
  const seed = privateKey.export({ format: 'der', type: 'pkcs8' }).subarray(-32);
  const suffix = manifest.username.replaceAll(':', '');
  const info = { displayName: 'Fixture', category: 2, pincode: code, signPk: publicBytes.toString('hex'), signSk: Buffer.concat([seed, publicBytes]).toString('hex'), pairedClients: {}, pairedClientsPermission: {}, configVersion: 1, configHash: '', setupID: manifest.setupId };
  const infoFile = join(config.dataDir, 'hap', `AccessoryInfo.${suffix}.json`);
  const cacheFile = join(config.dataDir, 'hap', `IdentifierCache.${suffix}.json`);
  writeFileSync(infoFile, JSON.stringify(info), { mode: 0o600 });
  writeFileSync(cacheFile, JSON.stringify({ cache: { '|nextAID': 3, [`${bridgeUUID(config.bridgeId)}|nextIID`]: 10 } }), { mode: 0o600 });
  return { info, infoFile, cacheFile };
}

// Gate: fail-closed restart/initialization is the storage owner's invariant;
// implicit reset, early commit, and key-kind reuse are credible regressions;
// no other tests cover this new owner; only real filesystem/public owner methods.
describe('identity storage boundary', () => {
  it('requires explicit initialization, persists the crash marker first, and cannot resume a half store', () => {
    const f = setup(); const config = f.config();
    expect(() => prepareDataDirectory(config, false)).toThrow('identity_unavailable');
    prepareDataDirectory(config, true);
    const store = IdentityStore.open(config, code, true);
    expect(readdirSync(config.dataDir)).toEqual(['bridge.lock']);
    const manifest = store.beginInitialization();
    expect(JSON.parse(readFileSync(join(config.dataDir, 'bridge.json'), 'utf8'))).toEqual(manifest);
    expect(manifest).toMatchObject({ version: 1, stage: 'initializing', bridgeId: 'fixture', accessoryKinds: { lamp: 'light' } });
    expect(() => store.commitInitialized()).toThrow('identity_unavailable');
    expect(() => IdentityStore.open(config, code, false)).toThrow('initialization_incomplete');
    requiredFiles(config, store);
    // A crash after both HAP files exist still cannot bypass the manifest commit.
    expect(() => IdentityStore.open(config, code, false)).toThrow('initialization_incomplete');
    expect(() => IdentityStore.open(config, code, true)).toThrow('initialization_requires_empty_store');
  });

  it('commits only a complete store and preserves identity across rename/repoint/reorder and retired keys', () => {
    const config = setup().config(); prepareDataDirectory(config, true);
    const store = IdentityStore.open(config, code, true); store.beginInitialization();
    requiredFiles(config, store);
    const manifest = store.commitInitialized();
    const original = config.accessories[0];
    if (!original) throw new Error('fixture accessory missing');
    const changed = { ...config, name: 'Renamed', accessories: [{ key: 'new', name: 'New', kind: 'outlet' as const, powerEntity: brightnessId }, { ...original, name: 'Other', powerEntity: brightnessId }] };
    const reopened = IdentityStore.open(changed, code, false);
    expect(reopened.manifest).toEqual(manifest);
    expect(reopened.recordAccessoryKinds().accessoryKinds).toEqual({ lamp: 'light', new: 'outlet' });
    const retired = IdentityStore.open({ ...config, accessories: [] }, code, false).recordAccessoryKinds();
    expect(retired.accessoryKinds).toEqual({ lamp: 'light', new: 'outlet' });
    expect(() => IdentityStore.open({ ...config, accessories: [{ key: 'lamp', name: 'Plug', kind: 'outlet', powerEntity: powerId }] }, code, false)).toThrow('accessory_kind_changed');
    expect(accessoryUUID('fixture', 'lamp')).toBe(accessoryUUID(changed.bridgeId, 'lamp'));
    expect(bridgeUUID('fixture')).not.toBe(accessoryUUID('bridge', 'fixture'));
    expect(pairingInfo(manifest, code)).toEqual({ code, setupURI: `X-HM://00249O1RD${manifest.setupId}` });
  });

  it.each(['missing-info', 'missing-cache', 'malformed-info', 'bad-keys', 'bad-cache', 'changed-code', 'changed-bridge', 'public-files', 'replaced-directory'])('fails closed on %s without changing the manifest', failure => {
    const config = setup().config(); prepareDataDirectory(config, true);
    const store = IdentityStore.open(config, code, true); store.beginInitialization();
    const files = requiredFiles(config, store); store.commitInitialized();
    const before = readFileSync(join(config.dataDir, 'bridge.json'));
    let candidate = config; let secret = code;
    switch (failure) {
      case 'missing-info': rmSync(files.infoFile); break;
      case 'missing-cache': rmSync(files.cacheFile); break;
      case 'malformed-info': writeFileSync(files.infoFile, '{'); break;
      case 'bad-keys': writeFileSync(files.infoFile, JSON.stringify({ ...files.info, signPk: '00'.repeat(32) })); break;
      case 'bad-cache': writeFileSync(files.cacheFile, JSON.stringify({ cache: { invalid: -1 } })); break;
      case 'changed-code': secret = '394-62-851'; break;
      case 'changed-bridge': candidate = { ...config, bridgeId: 'other' }; break;
      case 'public-files': chmodSync(files.infoFile, 0o644); break;
      case 'replaced-directory': {
        renameSync(join(config.dataDir, 'hap'), join(config.dataDir, 'old-hap'));
        mkdirSync(join(config.dataDir, 'hap'), { mode: 0o700 });
        expect(() => store.checkIntegrity()).toThrow('hap_directory_replaced');
        break;
      }
    }
    expect(() => IdentityStore.open(candidate, secret, false)).toThrow();
    expect(readFileSync(join(config.dataDir, 'bridge.json'))).toEqual(before);
  });

  it('requires private storage and rejects initialization of an unrelated nonempty directory', () => {
    const config = setup().config(); prepareDataDirectory(config, true);
    writeFileSync(join(config.dataDir, 'unrelated'), 'keep');
    expect(() => IdentityStore.open(config, code, true)).toThrow('initialization_requires_empty_store');
    expect(readFileSync(join(config.dataDir, 'unrelated'), 'utf8')).toBe('keep');
    chmodSync(config.dataDir, 0o755);
    expect(() => IdentityStore.open(config, code, true)).toThrow('insecure_storage');
  });

  // Rewound allocation counters can route a new accessory to an existing AID.
  // Validate the restored cache at its owner before HAP can allocate from it.
  it.each(['aid-rewind', 'aid-collision', 'iid-rewind', 'iid-collision', 'iid-missing-counter', 'root-offset', 'root-future-offset'])('rejects corrupt identifier allocation %s without repairing the store', failure => {
    const config = setup().config(); prepareDataDirectory(config, true);
    const store = IdentityStore.open(config, code, true); store.beginInitialization();
    const files = requiredFiles(config, store); store.commitInitialized();
    const first = accessoryUUID(config.bridgeId, 'lamp');
    const second = accessoryUUID(config.bridgeId, 'other');
    const service = '00000043-0000-1000-8000-0026BB765291';
    const characteristic = '00000025-0000-1000-8000-0026BB765291';
    const bridge = bridgeUUID(config.bridgeId);
    const cache: Record<string, number> = {
      '|nextAID': 4, [first]: 2, [second]: 3,
      [`${bridge}|nextIID`]: 10,
      [`${first}|nextIID`]: 4, [`${first}|${service}|primary`]: 2, [`${first}|${service}|primary|${characteristic}`]: 3,
      [`${second}|nextIID`]: 2,
    };
    if (failure === 'aid-rewind') cache['|nextAID'] = 3;
    if (failure === 'aid-collision') cache[second] = 2;
    if (failure === 'iid-rewind') cache[`${first}|nextIID`] = 3;
    if (failure === 'iid-collision') cache[`${first}|${service}|primary|${characteristic}`] = 2;
    if (failure === 'iid-missing-counter') delete cache[`${first}|nextIID`];
    if (failure === 'root-offset') {
      cache[`${bridge}|${service}`] = 2;
      cache[`${bridge}|${service}|${characteristic}`] = 2_000_000_002;
      cache[`${bridge}|nextIID`] = 2_000_000_003;
    }
    if (failure === 'root-future-offset') {
      cache[`${bridge}|${service}`] = 2;
      cache[`${bridge}|nextIID`] = 2_000_000_002;
    }
    writeFileSync(files.cacheFile, JSON.stringify({ cache }));
    const before = readFileSync(files.cacheFile);
    expect(() => IdentityStore.open(config, code, false)).toThrow('invalid_identifier_cache');
    expect(readFileSync(files.cacheFile)).toEqual(before);
  });

  // Gate: actual public CLI must acquire the OS lock before identity access;
  // omission/blocking flock or implicit initialization fails; distinct process risk;
  // real competing util-linux process, no configurable runtime injection.
  it('rejects a competing CLI before reading or changing a corrupt store', async () => {
    const f = setup(); const config = f.config(); prepareDataDirectory(config, true);
    writeFileSync(join(config.dataDir, 'bridge.json'), 'corrupt', { mode: 0o600 });
    const locker = spawn('flock', ['--exclusive', '--no-fork', join(config.dataDir, 'bridge.lock'), process.execPath, '-e', 'process.stdout.write("locked\\n");setInterval(()=>{},1000)'], { stdio: ['ignore', 'pipe', 'pipe'] });
    try {
      await once(locker.stdout, 'data');
      const child = spawn(process.execPath, ['dist/cli.js', 'pairing-info', '--config', f.file], { stdio: ['ignore', 'pipe', 'pipe'] });
      let stderr = ''; child.stderr.on('data', chunk => { stderr += chunk; });
      const [status] = await once(child, 'exit');
      expect(status).toBe(73);
      expect(stderr).toContain('storage_locked');
      expect(stderr).not.toContain(code);
      expect(readFileSync(join(config.dataDir, 'bridge.json'), 'utf8')).toBe('corrupt');
    } finally { locker.kill('SIGKILL'); }
  });

  it('prints explicit pairing-info for an initialized store without a listener or mutations', async () => {
    const f = setup(); const config = f.config(); prepareDataDirectory(config, true);
    const store = IdentityStore.open(config, code, true); store.beginInitialization(); requiredFiles(config, store); store.commitInitialized();
    const before = readFileSync(join(config.dataDir, 'bridge.json'));
    const child = spawn(process.execPath, ['dist/cli.js', 'pairing-info', '--config', f.file], { stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = ''; let stderr = '';
    child.stdout.on('data', chunk => { stdout += chunk; }); child.stderr.on('data', chunk => { stderr += chunk; });
    const [status] = await once(child, 'exit');
    expect({ status, stderr }).toEqual({ status: 0, stderr: '' });
    expect(JSON.parse(stdout)).toEqual(pairingInfo(store.manifest, code));
    expect(readFileSync(join(config.dataDir, 'bridge.json'))).toEqual(before);
  });
});
