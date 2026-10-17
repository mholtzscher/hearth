import { readFileSync, existsSync } from 'node:fs';
import { networkInterfaces } from 'node:os';
import { resolve } from 'node:path';
import { parseDocument } from 'yaml';
import { BoundaryError, canonicalId, exactKeys, object, text } from './boundary.js';

export type AccessoryConfig =
  | { key: string; name: string; kind: 'light'; powerEntity: string; brightnessEntity?: string }
  | { key: string; name: string; kind: 'outlet'; powerEntity: string };

export type Config = {
  bridgeId: string; name: string; dataDir: string; pairingCodeFile: string;
  hapPort: number; advertiseInterfaces: readonly string[];
  coreURL: string; natsURL: string; reconcileIntervalMs: number;
  accessories: readonly AccessoryConfig[];
};

export type CLIOptions = {
  command: 'serve' | 'pairing-info'; initialize: boolean;
  configFile?: string; scalars: Readonly<Record<string, string>>;
};

const scalarKeys = ['bridge_id', 'name', 'data_dir', 'pairing_code_file', 'hap_port', 'core_url', 'nats_url', 'reconcile_interval'];
const prefix = 'HEARTH_BRIDGE_HOMEKIT_';

/** CLI-only authorization is deliberately absent from environment and YAML. */
export function parseCLI(argv: readonly string[]): CLIOptions {
  const args = [...argv];
  const command = args[0] === 'pairing-info' ? (args.shift(), 'pairing-info') : 'serve';
  let initialize = false;
  let configFile: string | undefined;
  const scalars: Record<string, string> = {};
  const seen = new Set<string>();
  while (args.length) {
    const arg = args.shift();
    if (!arg?.startsWith('--')) throw new BoundaryError('invalid_argument');
    const [flag, inline] = arg.slice(2).split(/=(.*)/s);
    if (!flag || seen.has(flag)) throw new BoundaryError('invalid_argument');
    seen.add(flag);
    if (flag === 'initialize') {
      if (inline !== undefined || command !== 'serve') throw new BoundaryError('invalid_argument');
      initialize = true;
      continue;
    }
    const key = flag.replaceAll('-', '_');
    if (key !== 'config' && !scalarKeys.includes(key)) throw new BoundaryError('invalid_argument');
    const value = inline ?? args.shift();
    if (!value || value.startsWith('--')) throw new BoundaryError('invalid_argument');
    if (key === 'config') configFile = value;
    else scalars[key] = value;
  }
  return { command, initialize, scalars, ...(configFile === undefined ? {} : { configFile }) };
}

export function slug(value: unknown): string {
  const result = text(value);
  if (result.length > 64 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(result)) throw new BoundaryError('invalid_key');
  return result;
}

function name(value: unknown): string {
  const result = text(value);
  if (result.length < 1 || result.length > 64 || !result.trim() || /[\p{Cc}]/u.test(result)) {
    throw new BoundaryError('invalid_name');
  }
  return result;
}

function duration(value: unknown): number {
  const input = text(value).replace(/^\+/, '');
  const units: Record<string, bigint> = { h: 3_600_000_000_000n, m: 60_000_000_000n, s: 1_000_000_000n, ms: 1_000_000n, us: 1000n, 'µs': 1000n, 'μs': 1000n, ns: 1n };
  let total = 0n;
  let matched = '';
  for (const part of input.matchAll(/(\d+(?:\.\d*)?|\.\d+)(ms|us|µs|μs|ns|h|m|s)/g)) {
    const unit = part[2];
    if (!unit || units[unit] === undefined) throw new BoundaryError('invalid_duration');
    const [whole, fraction = ''] = (part[1] ?? '').split('.');
    // Match Go duration's integer nanoseconds without floating-point rounding.
    total += BigInt(whole || '0') * units[unit];
    if (fraction) total += BigInt(fraction) * units[unit] / (10n ** BigInt(fraction.length));
    matched += part[0];
  }
  if (matched !== input || total % 1_000_000n !== 0n || total < 1_000_000_000n || total > 60_000_000_000n) throw new BoundaryError('invalid_duration');
  return Number(total / 1_000_000n);
}

function port(value: unknown): number {
  if (typeof value !== 'number' && (typeof value !== 'string' || !/^\d+$/.test(value))) throw new BoundaryError('invalid_port');
  const result = Number(value);
  if (!Number.isInteger(result) || result < 1024 || result > 65535) throw new BoundaryError('invalid_port');
  return result;
}

function origin(value: unknown, protocol: 'http' | 'nats'): string {
  const input = text(value);
  // Inspect the spelling too: URL normalization must not hide paths or an omitted default port.
  if (!new RegExp(`^${protocol}://(?:\\[[0-9a-fA-F:.]+\\]|[a-zA-Z0-9.-]+):[0-9]+$`).test(input)) throw new BoundaryError('invalid_endpoint');
  try {
    const url = new URL(input);
    const rawPort = Number(input.slice(input.lastIndexOf(':') + 1));
    if (!url.hostname || rawPort < 1 || rawPort > 65535 || url.username || url.password) throw new Error();
  } catch { throw new BoundaryError('invalid_endpoint'); }
  return input;
}

function path(value: unknown): string {
  const input = text(value);
  if (!input.trim() || input.includes('\0')) throw new BoundaryError('invalid_path');
  return resolve(input);
}

/** Parses all static input. OS interface eligibility is checked separately at startup. */
export function loadConfig(argv: readonly string[], env: NodeJS.ProcessEnv): Config {
  const options = parseCLI(argv);
  const selected = options.configFile ?? env[`${prefix}CONFIG`];
  const configPath = selected ?? 'configs/homekit.yaml';
  let yaml: Record<string, unknown> = {};
  if (selected !== undefined || existsSync(configPath)) {
    try {
      const document = parseDocument(readFileSync(configPath, 'utf8'), { uniqueKeys: true, strict: true });
      if (document.errors.length) throw new Error();
      const value: unknown = document.toJS({ maxAliasCount: 0 });
      yaml = object(value);
    } catch { throw new BoundaryError('invalid_config_file'); }
  }
  exactKeys(yaml, [...scalarKeys, 'accessories', 'advertise_interfaces']);
  const values: Record<string, unknown> = { name: 'Hearth', hap_port: 51826, reconcile_interval: '5s', ...yaml };
  for (const [key, value] of Object.entries(env)) {
    if (!key.startsWith(prefix) || value === undefined) continue;
    const field = key.slice(prefix.length).toLowerCase();
    if (field === 'config') continue;
    if (!scalarKeys.includes(field)) throw new BoundaryError('unknown_environment_key');
    values[field] = value;
  }
  Object.assign(values, options.scalars);
  if (!Array.isArray(values.accessories) || values.accessories.length < 1 || values.accessories.length > 149) throw new BoundaryError('invalid_accessories');
  const keys = new Set<string>();
  const entities = new Set<string>();
  const accessories = values.accessories.map((input: unknown): AccessoryConfig => {
    const item = object(input);
    exactKeys(item, ['key', 'name', 'kind', 'power_entity', 'brightness_entity']);
    const key = slug(item.key);
    if (keys.has(key)) throw new BoundaryError('duplicate_accessory_key');
    keys.add(key);
    const powerEntity = canonicalId(item.power_entity, 'ent');
    const kind = item.kind;
    if (kind !== 'light' && kind !== 'outlet') throw new BoundaryError('invalid_accessory_kind');
    const brightnessEntity = item.brightness_entity === undefined ? undefined : canonicalId(item.brightness_entity, 'ent');
    if (kind === 'outlet' && brightnessEntity !== undefined) throw new BoundaryError('outlet_brightness');
    for (const entity of [powerEntity, brightnessEntity]) {
      if (entity === undefined) continue;
      if (entities.has(entity)) throw new BoundaryError('duplicate_entity');
      entities.add(entity);
    }
    const base = { key, name: name(item.name), powerEntity };
    return kind === 'outlet' ? { ...base, kind } : { ...base, kind, ...(brightnessEntity === undefined ? {} : { brightnessEntity }) };
  });
  const interfaces = values.advertise_interfaces;
  if (!Array.isArray(interfaces) || !interfaces.length || interfaces.some((entry: unknown) => typeof entry !== 'string' || !/^[a-zA-Z0-9_.:-]+$/.test(entry))) {
    throw new BoundaryError('invalid_interfaces');
  }
  const advertiseInterfaces = interfaces.map((entry: unknown) => text(entry));
  if (new Set(advertiseInterfaces).size !== advertiseInterfaces.length) throw new BoundaryError('invalid_interfaces');
  const bridgeName = name(values.name);
  if (Buffer.byteLength(bridgeName, 'utf8') > 58) throw new BoundaryError('bridge_name_too_long');
  return {
    bridgeId: slug(values.bridge_id), name: bridgeName, dataDir: path(values.data_dir), pairingCodeFile: path(values.pairing_code_file),
    hapPort: port(values.hap_port), advertiseInterfaces, coreURL: origin(values.core_url, 'http'), natsURL: origin(values.nats_url, 'nats'),
    reconcileIntervalMs: duration(values.reconcile_interval), accessories,
  };
}

/** Node's address inventory does not expose IFF_UP; Linux sysfs is the authoritative flag check. */
export function validateInterfaces(config: Config): void {
  const inventory = networkInterfaces();
  for (const name of config.advertiseInterfaces) {
    try {
      const addresses = inventory[name];
      const flags = Number.parseInt(readFileSync(`/sys/class/net/${name}/flags`, 'utf8').trim(), 16);
      if (!addresses?.some(address => !address.internal) || !(flags & 1) || (flags & 8)) throw new Error();
    } catch { throw new BoundaryError('interface_unavailable'); }
  }
}

export function readPairingCode(config: Config): string {
  let code: string;
  try { code = readFileSync(config.pairingCodeFile, 'utf8'); }
  catch { throw new BoundaryError('pairing_code_unreadable'); }
  if (!/^\d{3}-\d{2}-\d{3}\n?$/.test(code)) throw new BoundaryError('invalid_pairing_code');
  code = code.replace(/\n$/, '');
  const digits = code.replaceAll('-', '');
  if (/^(\d)\1{7}$/.test(digits) || digits === '12345678' || digits === '87654321') throw new BoundaryError('invalid_pairing_code');
  return code;
}
