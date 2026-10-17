import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { networkInterfaces } from 'node:os';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { stringify } from 'yaml';
import { object, text } from '../../dist/boundary.js';
import type { Stack } from './stack.js';
import { code } from './config.js';

export function records(input: unknown): Record<string, unknown>[] {
  if (!Array.isArray(input)) throw new Error('expected collection');
  return input.map(object);
}
export async function json(url: string, init?: RequestInit): Promise<unknown> {
  const response = await fetch(url, { ...init, signal: AbortSignal.timeout(3000) });
  if (!response.ok) throw new Error(`fixture HTTP ${response.status}`);
  const value: unknown = await response.json(); return value;
}
export async function eventually<T>(read: () => Promise<T>, accept: (value: T) => boolean, timeout = 10_000): Promise<T> {
  const deadline = performance.now() + timeout;
  let last: unknown;
  while (performance.now() < deadline) {
    try { const value = await read(); if (accept(value)) return value; last = value; } catch (error) { last = error; }
    await delay(40);
  }
  throw new Error('eventual fixture condition timed out', { cause: last });
}
export async function freePort(): Promise<number> {
  const server = createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const address = server.address(); if (!address || typeof address === 'string') throw new Error('port missing');
  await new Promise<void>(resolve => server.close(() => resolve())); return address.port;
}
export async function runtimeConfig(stack: Stack, overrides: Record<string, unknown> = {}) {
  const inventory = records(await json(`${stack.simulatorURL}/v1/sim/entities`));
  const id = (binding: string, key: string) => text(inventory.find(item => item.binding_key === binding && item.key === key)?.entity_id);
  const lan = Object.entries(networkInterfaces()).find(([name, addresses]) => {
    const flags = Number.parseInt(readFileSync(`/sys/class/net/${name}/flags`, 'utf8').trim(), 16);
    return !!addresses?.some(address => !address.internal) && !!(flags & 1) && !(flags & 8);
  })?.[0];
  if (!lan) throw new Error('runtime process tests require a local non-loopback interface');
  const port = await freePort();
  const file = join(stack.directory, 'bridge.yaml');
  const values = { bridge_id: 'integration', name: 'Integration', data_dir: join(stack.directory, 'bridge'), pairing_code_file: join(stack.directory, 'code'),
    hap_port: port, advertise_interfaces: [lan], core_url: stack.coreURL, nats_url: stack.natsURL, reconcile_interval: '1s',
    accessories: [
      { key: 'lamp', name: 'Lamp', kind: 'light', power_entity: id('lamp', 'power'), brightness_entity: id('lamp', 'brightness') },
      { key: 'plug', name: 'Plug', kind: 'outlet', power_entity: id('plug', 'power') },
    ], ...overrides };
  writeFileSync(file, stringify(values), { mode: 0o600 });
  writeFileSync(join(stack.directory, 'code'), code, { mode: 0o600 });
  return { file, values, port, powerId: id('lamp', 'power'), brightnessId: id('lamp', 'brightness'), plugId: id('plug', 'power') };
}

export async function startCLI(file: string, initialize: boolean, extraEnv: NodeJS.ProcessEnv = {}) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('HEARTH_BRIDGE_HOMEKIT_')));
  const child = spawn(process.execPath, ['dist/cli.js', '--config', file, ...(initialize ? ['--initialize'] : [])], { env: { ...env, ...extraEnv }, stdio: ['ignore', 'pipe', 'pipe'] });
  let stderr = ''; let stdout = ''; let runtimePid: number | undefined;
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => {
    stderr += chunk;
    for (const line of stderr.trim().split('\n')) {
      try { const parsed: unknown = JSON.parse(line); const data = object(parsed); if (data.event === 'ready' && typeof data.pid === 'number') runtimePid = data.pid; } catch { /* partial line */ }
    }
  });
  const exit = new Promise<{ code: number | null; signal: NodeJS.Signals | null }>((resolve, reject) => {
    child.once('error', reject); child.once('exit', (code, signal) => resolve({ code, signal }));
  });
  try {
    const deadline = performance.now() + 35_000;
    while (runtimePid === undefined) {
      if (child.exitCode !== null || child.signalCode !== null) throw new Error(`runtime exited: ${stderr}`);
      if (performance.now() >= deadline) throw new Error('runtime readiness timeout');
      await delay(25);
    }
  } catch (error) { child.kill('SIGTERM'); await exit; throw new Error(`bridge startup failed: ${stderr}`, { cause: error }); }
  return { child, exit, get runtimePid() { return runtimePid; }, get stderr() { return stderr; }, get stdout() { return stdout; },
    async stop() { if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM'); return exit; } };
}

export type WireCharacteristic = { aid: number; iid: number; type: string; value: unknown; props: Record<string, unknown> };
export function inventory(body: Buffer): WireCharacteristic[] {
  const parsed: unknown = JSON.parse(body.toString());
  return records(object(parsed).accessories).flatMap(accessory => records(accessory.services).flatMap(service => records(service.characteristics).map(characteristic => {
    if (typeof accessory.aid !== 'number' || typeof characteristic.iid !== 'number') throw new Error('invalid HAP identity');
    return { aid: accessory.aid, iid: characteristic.iid, type: text(characteristic.type), value: characteristic.value, props: characteristic };
  })));
}
