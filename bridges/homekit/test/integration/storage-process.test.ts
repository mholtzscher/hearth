import { expect, it } from 'vitest';
import { existsSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { object, text } from '../../dist/boundary.js';
import { createStack } from '../support/stack.js';
import { eventually, inventory, runtimeConfig, startCLI } from '../support/runtime.js';
import { WireController } from '../support/wire-controller.js';
import { code } from '../support/config.js';
import { stringify } from 'yaml';

async function runOnce(file: string, args: string[] = []) {
  const child = spawn(process.execPath, ['dist/cli.js', ...args, '--config', file], { stdio: ['ignore', 'pipe', 'pipe'] });
  let stdout = ''; let stderr = '';
  child.stdout.on('data', bytes => { stdout += bytes; }); child.stderr.on('data', bytes => { stderr += bytes; });
  const status = await new Promise<number | null>((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); });
  return { status, stdout, stderr };
}

// Gate: a healthy real runtime must retain its lock and actual HAP identity across
// optional topology changes and reject restoration damage without reset; a mock
// store cannot establish library cache compatibility; CLI/files/public encrypted APIs.
it('holds the lock while serving, reuses optional characteristic IDs, and fails closed on lost/corrupt real storage', async () => {
  const stack = await createStack();
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined; let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack);
    bridge = await startCLI(config.file, true);
    controller = new WireController(config.port); const pairing = await controller.pair(code); await controller.verify();
    const original = inventory((await controller.get('/accessories')).body);
    const conflict = await runOnce(config.file, ['pairing-info']);
    expect(conflict.status).toBe(73); expect(conflict.stderr).toContain('storage_locked'); expect(conflict.stdout).toBe('');
    controller.close(); controller = undefined; await bridge.stop(); bridge = undefined;
    const savedManifest = readFileSync(join(config.values.data_dir, 'bridge.json'));
    const noBrightness = config.values.accessories.map(item => ({ key: item.key, name: item.name, kind: item.kind, power_entity: item.power_entity }));
    writeFileSync(config.file, stringify({ ...config.values, accessories: noBrightness }));
    bridge = await startCLI(config.file, false); controller = new WireController(config.port, pairing); await controller.verify();
    const reduced = inventory((await controller.get('/accessories')).body);
    expect(reduced.some(item => item.type === '8')).toBe(false);
    expect(reduced.filter(item => item.type === '25').map(item => [item.aid, item.iid])).toEqual(original.filter(item => item.type === '25').map(item => [item.aid, item.iid]));
    controller.close(); controller = undefined; await bridge.stop(); bridge = undefined;
    writeFileSync(config.file, stringify(config.values));
    bridge = await startCLI(config.file, false); controller = new WireController(config.port, pairing); await controller.verify();
    expect(inventory((await controller.get('/accessories')).body).map(item => [item.aid, item.iid, item.type]).sort()).toEqual(original.map(item => [item.aid, item.iid, item.type]).sort());
    controller.close(); controller = undefined; await bridge.stop(); bridge = undefined;
    expect(readFileSync(join(config.values.data_dir, 'bridge.json'))).toEqual(savedManifest);
    const suffix = text(object(JSON.parse(savedManifest.toString())).username).replaceAll(':', '');
    const infoFile = join(config.values.data_dir, 'hap', `AccessoryInfo.${suffix}.json`);
    const cacheFile = join(config.values.data_dir, 'hap', `IdentifierCache.${suffix}.json`);
    for (const file of [infoFile, cacheFile, join(config.values.data_dir, 'bridge.json')]) {
      const before = readFileSync(file); rmSync(file);
      expect((await runOnce(config.file)).status).toBe(1);
      expect(existsSync(file)).toBe(false);
      writeFileSync(file, '{malformed', { mode: 0o600 });
      expect((await runOnce(config.file)).status).toBe(1);
      expect(readFileSync(file, 'utf8')).toBe('{malformed');
      writeFileSync(file, before, { mode: 0o600 });
    }
    const initializedAgain = await runOnce(config.file, ['--initialize']);
    expect(initializedAgain.status).toBe(1); expect(initializedAgain.stderr).toContain('initialization_requires_empty_store');
    expect(readFileSync(join(config.values.data_dir, 'bridge.json'))).toEqual(savedManifest);
    const info = await runOnce(config.file, ['pairing-info']);
    expect(info.status).toBe(0); expect(object(JSON.parse(info.stdout)).code).toBe(code); expect(info.stderr).toBe('');
    renameSync(config.values.data_dir, `${config.values.data_dir}-backup`);
    expect((await runOnce(config.file)).status).toBe(1); expect(existsSync(config.values.data_dir)).toBe(false);
    renameSync(`${config.values.data_dir}-backup`, config.values.data_dir);
    bridge = await startCLI(config.file, false); controller = new WireController(config.port, pairing); await controller.verify();
    expect((await controller.get('/accessories')).statusCode).toBe(200);
  } finally { controller?.close(); await bridge?.stop(); await stack.close(); }
}, 90_000);

// Gate: deleting/replacing the HAP directory must be fatal rather than permission
// to regenerate keys; actual serving identity loss differs from startup parsing;
// public directory watch + process exit, with no production fault-injection seam.
it('stops on a removed HAP directory without recreating pairing data', async () => {
  const stack = await createStack(); let bridge: Awaited<ReturnType<typeof startCLI>> | undefined;
  try {
    const config = await runtimeConfig(stack, { reconcile_interval: '60s' }); bridge = await startCLI(config.file, true);
    const hap = join(config.values.data_dir, 'hap'); renameSync(hap, `${hap}-backup`);
    const exited = await Promise.race([bridge.exit, delay(3000).then(() => { throw new Error('identity loss was not fatal'); })]);
    expect(exited.code).toBe(1); expect(bridge.stderr).toContain('identity_integrity_failed'); expect(existsSync(hap)).toBe(false);
    bridge = undefined;
    renameSync(`${hap}-backup`, hap);
    await eventually(async () => (await runOnce(config.file, ['pairing-info'])).status, status => status === 0);
  } finally { await bridge?.stop(); await stack.close(); }
}, 45_000);
