import { expect, it } from 'vitest';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { stringify } from 'yaml';
import { object } from '../../dist/boundary.js';
import { createStack } from '../support/stack.js';
import { eventually, inventory, json, runtimeConfig, startCLI } from '../support/runtime.js';
import { WireController } from '../support/wire-controller.js';
import { code } from '../support/config.js';
import { createCoreProxy } from '../support/core-proxy.js';

// Gate: actual locked CLI + HAP + Core + NATS lifecycle and retained pairing;
// wrong assembly/order/identity reset/protocol defaults are credible failures;
// helper and dependency-only tests cannot exercise these owners together;
// real executables/encrypted controller/public APIs, no production injection seams.
it('initializes, serves mixed profiles, preserves pairing/IDs across restart, and recovers HTTP and fact delivery', async () => {
  const stack = await createStack();
  const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined;
  let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url });
    bridge = await startCLI(config.file, true);
    controller = new WireController(config.port);
    const pairing = await controller.pair(code); await controller.verify();
    const before = inventory((await controller.get('/accessories')).body);
    const on = before.find(item => item.aid === 2 && item.type === '25');
    const brightness = before.find(item => item.aid === 2 && item.type === '8');
    const outlet = before.find(item => item.aid === 3 && item.type === '25');
    if (!on || !brightness || !outlet) throw new Error('mixed topology missing');
    expect(before.some(item => item.type === '26')).toBe(false);
    expect(brightness.props).toMatchObject({ minValue: 0, maxValue: 100, minStep: 1, value: 83 });
    const set = async (slot: { aid: number; iid: number }, value: boolean | number) => {
      const raw = await controller?.put('/characteristics', { characteristics: [{ aid: slot.aid, iid: slot.iid, value, r: true }] });
      expect(raw?.statusCode).toBe(207); return object(JSON.parse(raw?.body.toString() ?? 'null'));
    };
    expect(await set(on, true)).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, value: 1, status: 0 }] });
    expect(await set(on, true)).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, value: 1, status: 0 }] });
    expect(await set(brightness, 83)).toEqual({ characteristics: [{ aid: brightness.aid, iid: brightness.iid, value: 85, status: 0 }] });
    expect(await set(brightness, 0)).toEqual({ characteristics: [{ aid: brightness.aid, iid: brightness.iid, value: 0, status: 0 }] });
    expect(await set(outlet, false)).toEqual({ characteristics: [{ aid: outlet.aid, iid: outlet.iid, value: 0, status: 0 }] });
    const manifestBefore = JSON.parse(readFileSync(join(config.values.data_dir, 'bridge.json'), 'utf8'));
    controller.close(); controller = undefined;
    expect(await bridge.stop()).toEqual({ code: 0, signal: null }); bridge = undefined;
    writeFileSync(config.file, stringify({ ...config.values, name: 'Renamed bridge', accessories: [...config.values.accessories].reverse().map(item => ({ ...item, name: 'Renamed' })) }));
    bridge = await startCLI(config.file, false);
    controller = new WireController(config.port, pairing); await controller.verify();
    expect(inventory((await controller.get('/accessories')).body).map(item => [item.aid, item.iid, item.type]).sort()).toEqual(before.map(item => [item.aid, item.iid, item.type]).sort());
    expect(JSON.parse(readFileSync(join(config.values.data_dir, 'bridge.json'), 'utf8'))).toEqual(manifestBefore);
    await stack.stopCore();
    const failed = await controller.get(`/characteristics?id=${on.aid}.${on.iid}`);
    expect(object(JSON.parse(failed.body.toString()))).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: -70402 }] });
    await stack.startCore();
    await eventually(async () => {
      const raw = await controller?.get(`/characteristics?id=${on.aid}.${on.iid}`);
      return raw?.statusCode;
    }, status => status === 200);
    await stack.stopNATS();
    // HTTP still has authority while prompt hints are disconnected.
    const read = await controller.get(`/characteristics?id=${brightness.aid}.${brightness.iid}`);
    expect(read.statusCode).toBe(200);
    await stack.startNATS();
    await eventually(async () => bridge?.stderr, log => !!log?.includes('nats_reconnected'));
    expect(bridge.stdout).toBe('');
    expect(bridge.stderr).not.toContain(code);
    // Configuration-only enablement changes have no Observation Fact.
    await json(`${stack.coreURL}/v1/entities/${config.powerId}`, { method: 'PATCH', headers: { 'content-type': 'application/merge-patch+json' }, body: JSON.stringify({ enabled: false }) });
    await eventually(async () => object(await json(`${stack.coreURL}/v1/entities/${config.powerId}`)).enabled, value => value === false);
    // No HAP GET and no Observation Fact can account for this disabled descriptor read.
    await eventually(async () => proxy.readResults.some(read => read.path.endsWith(config.powerId) && object(read.result).enabled === false), value => value);
    const disabled = await controller.get(`/characteristics?id=${on.aid}.${on.iid}`);
    expect(object(JSON.parse(disabled.body.toString()))).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: -70402 }] });
    const disabledAttempts = proxy.commands.length;
    const disabledWrite = await controller.put('/characteristics', { characteristics: [{ aid: on.aid, iid: on.iid, value: false, r: true }] });
    expect(JSON.parse(disabledWrite.body.toString())).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: -70402 }] });
    expect(proxy.commands.length).toBe(disabledAttempts + 1); // Core, not stale projection, owns admission.
    await json(`${stack.coreURL}/v1/entities/${config.powerId}`, { method: 'PATCH', headers: { 'content-type': 'application/merge-patch+json' }, body: JSON.stringify({ enabled: true }) });
    const recovered = await controller.get(`/characteristics?id=${on.aid}.${on.iid}`);
    expect(recovered.statusCode).toBe(200);
    await stack.stopSimulator();
    await eventually(async () => object(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}`)).availability).status, value => value !== 'available', 30_000);
    const unavailable = await controller.get(`/characteristics?id=${on.aid}.${on.iid}`);
    expect(JSON.parse(unavailable.body.toString())).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: -70402 }] });
    const unavailableAttempts = proxy.commands.length;
    const unavailableWrite = await controller.put('/characteristics', { characteristics: [{ aid: on.aid, iid: on.iid, value: true, r: true }] });
    expect(JSON.parse(unavailableWrite.body.toString())).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: -70402 }] });
    expect(proxy.commands.length).toBe(unavailableAttempts + 1);
    await stack.startSimulator();
    await eventually(async () => object(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}`)).availability).status, value => value === 'available', 30_000);
    expect((await controller.get(`/characteristics?id=${on.aid}.${on.iid}`)).statusCode).toBe(200);
  } finally { controller?.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 90_000);
