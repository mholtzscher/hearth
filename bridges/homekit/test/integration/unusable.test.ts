import { expect, it } from 'vitest';
import { setTimeout as delay } from 'node:timers/promises';
import { object } from '../../dist/boundary.js';
import { createStack } from '../support/stack.js';
import { createCoreProxy } from '../support/core-proxy.js';
import { eventually, inventory, json, records, runtimeConfig, startCLI } from '../support/runtime.js';
import { WireController } from '../support/wire-controller.js';
import { code } from '../support/config.js';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Gate: absent/unavailable State is not false, nor a local command-admission
// rule; a satisfied outcome cannot authorize a successful unusable readback;
// helper/transport tests cannot prove notifications and callback selection;
// controlled valid API variants, actual Core attempt, independent wire observer.
it.each(['unknown-state', 'unavailable'])('never fabricates success/notifications for %s, but submits the valid write exactly once', async variant => {
  const stack = await createStack(); const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined;
  const controllers: WireController[] = [];
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url });
    proxy.transformReads((path, body) => path.endsWith(config.powerId) ? variant === 'unknown-state'
      ? { ...object(body), state: null }
      : { ...object(body), availability: { status: 'unavailable' } } : body);
    bridge = await startCLI(config.file, true);
    const controller = new WireController(config.port); controllers.push(controller); const pairing = await controller.pair(code); await controller.verify();
    const on = inventory((await controller.get('/accessories')).body).find(item => item.aid === 2 && item.type === '25');
    if (!on) throw new Error('On missing');
    const subscriber = new WireController(config.port, pairing); controllers.push(subscriber); await subscriber.verify();
    const rawEvents: Buffer[] = []; subscriber.onEvent(body => rawEvents.push(Buffer.from(body)));
    expect((await subscriber.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, ev: true }] }, true)).statusCode).toBe(204);
    const read = await controller.get(`/characteristics?id=2.${on.iid}`);
    expect(JSON.parse(read.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: -70402 }] });
    const write = await controller.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: true, r: true }] });
    expect(JSON.parse(write.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: -70402 }] });
    expect(proxy.commands.map(command => command.body)).toEqual([{ operation: 'set', parameters: { value: true } }]);
    const history = records(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}/commands`)).items);
    expect(history).toHaveLength(1); expect(history[0]).toMatchObject({ status: 'satisfied' });
    await delay(350); expect(rawEvents).toEqual([]);
    proxy.transformReads(undefined);
    await eventually(async () => rawEvents.flatMap(body => records(object(JSON.parse(body.toString())).characteristics)).some(item => item.aid === 2 && item.iid === on.iid && item.value === 1), value => value);
    const recovered = await controller.get(`/characteristics?id=2.${on.iid}`);
    expect(JSON.parse(recovered.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, value: 1 }] });
  } finally { for (const controller of controllers) controller.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 45_000);

// Native registration without an initial Observation must survive bridge startup
// as unknown State, not as a synthesized false value. No HTTP rewrite supplies it.
it('keeps native Core null State unusable and recovers the same accessory after a real Adapter restart', async () => {
  const stack = await createStack({ unknownPower: true });
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined; let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack);
    const initial = object(await json(`${stack.coreURL}/v1/entities/${config.powerId}`));
    expect(initial.state).toBe(null); expect(object(initial.availability).status).toBe('unavailable');
    bridge = await startCLI(config.file, true); controller = new WireController(config.port);
    await controller.pair(code); await controller.verify();
    const on = inventory((await controller.get('/accessories')).body).find(item => item.aid === 2 && item.type === '25');
    if (!on) throw new Error('On missing');
    expect(JSON.parse((await controller.get(`/characteristics?id=2.${on.iid}`)).body.toString()))
      .toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: -70402 }] });
    await stack.stopSimulator();
    const file = join(stack.directory, 'simulator.yaml');
    const fixture: unknown = JSON.parse(readFileSync(file, 'utf8'));
    const adapter = records(object(fixture).adapters)[0]; if (!adapter) throw new Error('fixture Adapter missing');
    const lamp = records(adapter.devices).find(item => item.binding_key === 'lamp'); if (!lamp) throw new Error('lamp missing');
    const power = records(lamp.entities).find(item => item.key === 'power'); if (!power) throw new Error('power missing');
    // Edit only test-owned configuration, then perform a normal SDK restart.
    power.available = true; delete power.availability_reason;
    writeFileSync(file, JSON.stringify(fixture));
    await stack.startSimulator();
    await eventually(async () => object(await json(`${stack.coreURL}/v1/entities/${config.powerId}`)).state,
      state => state !== null && object(state).value === false);
    expect(JSON.parse((await controller.get(`/characteristics?id=2.${on.iid}`)).body.toString()))
      .toEqual({ characteristics: [{ aid: 2, iid: on.iid, value: 0 }] });
    expect(records(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}/commands`)).items)).toEqual([]);
  } finally { controller?.close(); await bridge?.stop(); await stack.close(); }
}, 45_000);
