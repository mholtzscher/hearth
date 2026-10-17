import { expect, it } from 'vitest';
import { setTimeout as delay } from 'node:timers/promises';
import { object, text } from '../../dist/boundary.js';
import { createStack } from '../support/stack.js';
import { createCoreProxy } from '../support/core-proxy.js';
import { eventually, inventory, json, records, runtimeConfig, startCLI } from '../support/runtime.js';
import { WireController } from '../support/wire-controller.js';
import { code } from '../support/config.js';

// Gate: stale requested-value publication, deduplication and POST serialization
// are credible regressions of the real HAP completion boundary; dependency-only
// and projection tests cannot prove encrypted responses/notifications/Core history;
// independent controllers and a forwarding latency boundary, no production hooks.
it('never regresses newer physical State after held/overlapping Commands and restores fact notifications after NATS reconnect', async () => {
  const stack = await createStack(); const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined;
  const controllers: WireController[] = [];
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url, reconcile_interval: '60s' });
    bridge = await startCLI(config.file, true);
    const first = new WireController(config.port); controllers.push(first);
    const pairing = await first.pair(code); await first.verify();
    const subscriber = new WireController(config.port, pairing); controllers.push(subscriber); await subscriber.verify();
    const slots = inventory((await first.get('/accessories')).body);
    const on = slots.find(item => item.aid === 2 && item.type === '25'); const brightness = slots.find(item => item.aid === 2 && item.type === '8');
    if (!on || !brightness) throw new Error('light missing');
    const rawEvents: Buffer[] = [];
    subscriber.onEvent(body => rawEvents.push(Buffer.from(body)));
    expect((await subscriber.put('/characteristics', { characteristics: [on, brightness].map(slot => ({ aid: slot.aid, iid: slot.iid, ev: true })) }, true)).statusCode).toBe(204);
    const events = (iid: number) => rawEvents.flatMap(bytes => {
      const parsed: unknown = JSON.parse(bytes.toString());
      return records(object(parsed).characteristics).toReversed().filter(item => item.aid === 2 && item.iid === iid).map(item => item.value);
    });
    const publish = async (id: string, value: boolean | number) => {
      const accepted = object(await json(`${stack.simulatorURL}/v1/sim/entities/${id}/publish`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ value }) }));
      const observation = text(accepted.observation_id);
      await eventually(async () => object(object(await json(`${stack.coreURL}/v1/entities/${id}`)).state).observation_id, current => current === observation);
      return observation;
    };
    proxy.holdCommands(true);
    const cases = [
      { slot: on, id: config.powerId, requested: true, physical: false, wire: 0 },
      { slot: on, id: config.powerId, requested: false, physical: true, wire: 1 },
      { slot: brightness, id: config.brightnessId, requested: 83, physical: 0, wire: 0 },
      { slot: brightness, id: config.brightnessId, requested: 0, physical: 83, wire: 83 },
    ];
    for (const scenario of cases) {
      rawEvents.length = 0;
      const count = proxy.commands.length;
      const set = first.put('/characteristics', { characteristics: [{ aid: 2, iid: scenario.slot.iid, value: scenario.requested, r: true }] });
      await eventually(async () => proxy.commands.length, value => value === count + 1);
      await publish(scenario.id, scenario.physical);
      await eventually(async () => events(scenario.slot.iid), values => values.at(-1) === scenario.wire, 5000);
      const deliveredBeforeRelease = events(scenario.slot.iid).length;
      // Release on a microtask after the physical notification proved projection commit.
      queueMicrotask(() => proxy.commands[count]?.release());
      const response = await set;
      expect(response.statusCode).toBe(207);
      expect(JSON.parse(response.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: scenario.slot.iid, status: 0, value: scenario.wire }] });
      await delay(350);
      expect(events(scenario.slot.iid).slice(deliveredBeforeRelease).every(value => value === scenario.wire)).toBe(true);
      expect(events(scenario.slot.iid).at(-1)).toBe(scenario.wire);
      const read = await first.get(`/characteristics?id=2.${scenario.slot.iid}`);
      expect(JSON.parse(read.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: scenario.slot.iid, value: scenario.wire }] });
    }
    const second = new WireController(config.port, pairing); controllers.push(second); await second.verify();
    const count = proxy.commands.length;
    const a = first.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: false, r: true }] });
    const b = second.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: false, r: true }] });
    await eventually(async () => proxy.commands.length, length => length === count + 2);
    const earlier = proxy.commands[count]; const later = proxy.commands[count + 1];
    if (!earlier || !later) throw new Error('overlapping attempts missing');
    expect(earlier.body).toEqual({ operation: 'set', parameters: { value: false } });
    expect(later.body).toEqual({ operation: 'set', parameters: { value: false } });
    expect(earlier.result.command_id).not.toBe(later.result.command_id);
    later.release(); expect((await b).statusCode).toBe(207);
    earlier.release(); expect((await a).statusCode).toBe(207);
    const history = records(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}/commands`)).items);
    expect(history.map(item => item.id)).toEqual(expect.arrayContaining([earlier.result.command_id, later.result.command_id]));
    // Opposing requests may both satisfy, but a delayed response must sample the
    // latest State rather than resurrect the earlier successful request.
    const oppositeCount = proxy.commands.length;
    const turnOn = first.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: true, r: true }] });
    await eventually(async () => proxy.commands.length, length => length === oppositeCount + 1);
    const turnOff = second.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: false, r: true }] });
    await eventually(async () => proxy.commands.length, length => length === oppositeCount + 2);
    const heldOn = proxy.commands[oppositeCount]; const heldOff = proxy.commands[oppositeCount + 1];
    if (!heldOn || !heldOff) throw new Error('opposing attempts missing');
    expect(heldOn.result.status).toBe('satisfied'); expect(heldOff.result.status).toBe('satisfied');
    heldOff.release();
    expect(JSON.parse((await turnOff).body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: 0, value: 0 }] });
    heldOn.release();
    expect(JSON.parse((await turnOn).body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: 0, value: 0 }] });
    await stack.stopNATS();
    expect((await first.get(`/characteristics?id=2.${on.iid}`)).statusCode).toBe(200);
    await stack.startNATS();
    await eventually(async () => bridge?.stderr, log => !!log?.includes('nats_reconnected'));
    rawEvents.length = 0;
    await publish(config.powerId, true);
    await eventually(async () => events(on.iid), values => values.at(-1) === 1, 5000);
    // The 60s poll cannot account for this reconnect notification, and no HAP GET ran.
    expect(rawEvents.length).toBeGreaterThan(0);
  } finally { for (const controller of controllers) controller.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 90_000);

// Gate: the setter's eight seconds must include the final refresh, not restart
// after a seven-second successful POST; credible timer-reset/late callback bugs;
// distinct timing contract from ordinary newer-State ordering; real wire elapsed
// time and real Core evidence, forwarding proxy only delays delivery.
it.each(['post', 'final-refresh'])('bounds a held %s to one eight-second budget, without retry or late success', async phase => {
  const stack = await createStack(); const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined; let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url, reconcile_interval: '60s' });
    bridge = await startCLI(config.file, true);
    controller = new WireController(config.port); await controller.pair(code); await controller.verify();
    const on = inventory((await controller.get('/accessories')).body).find(item => item.aid === 2 && item.type === '25');
    if (!on) throw new Error('On missing');
    proxy.holdCommands(true);
    const start = performance.now();
    const pending = controller.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: true, r: true }] });
    await eventually(async () => proxy.commands.length, count => count === 1);
    const accepted = proxy.commands[0]; if (!accepted) throw new Error('Core result missing');
    expect(accepted.result.status).toBe('satisfied');
    if (phase === 'final-refresh') {
      await delay(7000 - (performance.now() - start));
      proxy.holdReads(true); accepted.release();
    }
    const response = await pending; const elapsed = performance.now() - start;
    expect(elapsed).toBeGreaterThanOrEqual(7800); expect(elapsed).toBeLessThan(8800);
    expect(JSON.parse(response.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: -70408 }] });
    if (phase === 'post') {
      const published = object(await json(`${stack.simulatorURL}/v1/sim/entities/${config.powerId}/publish`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ value: false }) }));
      await eventually(async () => object(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}`)).state).observation_id, current => current === published.observation_id);
      accepted.release();
    } else {
      for (const release of proxy.reads) release(); proxy.holdReads(false);
    }
    await delay(400);
    expect(proxy.commands.length).toBe(1);
    const history = records(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}/commands`)).items);
    expect(history).toEqual(expect.arrayContaining([expect.objectContaining({ id: accepted.result.command_id, status: 'satisfied' })]));
    const recovered = await controller.get(`/characteristics?id=2.${on.iid}`);
    expect(JSON.parse(recovered.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, value: phase === 'post' ? 0 : 1 }] });
  } finally { controller?.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 45_000);
