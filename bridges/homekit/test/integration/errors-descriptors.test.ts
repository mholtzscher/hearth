import { expect, it } from 'vitest';
import { setTimeout as delay } from 'node:timers/promises';
import { object } from '../../dist/boundary.js';
import { createStack } from '../support/stack.js';
import { createCoreProxy } from '../support/core-proxy.js';
import { eventually, inventory, runtimeConfig, startCLI } from '../support/runtime.js';
import { WireController } from '../support/wire-controller.js';
import { code } from '../support/config.js';
import { expectWriteSuccess } from '../support/hap-assertions.js';

// Gate: actual HAP status mapping and no POST for invalid delivered values;
// transport classification or retry bugs escape standalone HTTP client tests;
// wire responses and counted forwarded Core attempts, no production test seams.
it('maps upstream rejection/malformed/connection failures and rejects invalid raw values without a Core attempt', async () => {
  const stack = await createStack(); const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined; let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url }); bridge = await startCLI(config.file, true);
    controller = new WireController(config.port); await controller.pair(code); await controller.verify();
    const slots = inventory((await controller.get('/accessories')).body);
    const on = slots.find(item => item.aid === 2 && item.type === '25'); const brightness = slots.find(item => item.aid === 2 && item.type === '8');
    if (!on || !brightness) throw new Error('light missing');
    for (const [slot, value] of [[on, 2], [on, 'false'], [brightness, -1], [brightness, 101], [brightness, 1.5], [brightness, '83']] as const) {
      const raw = await controller.put('/characteristics', { characteristics: [{ aid: slot.aid, iid: slot.iid, value, r: true }] });
      expect(raw.statusCode).toBe(207);
      expect(JSON.parse(raw.body.toString())).toEqual({ characteristics: [{ aid: slot.aid, iid: slot.iid, status: -70410 }] });
    }
    expect(proxy.commands.length).toBe(0);
    const scenarios: { reply: { status: number; body: string } | 'disconnect'; status: number }[] = [
      ...[400, 404, 409, 502, 503, 504].map(status => ({ reply: { status, body: '{"detail":"untrusted operator text"}' }, status: status === 400 ? -70410 : status === 504 ? -70408 : -70402 })),
      { reply: { status: 200, body: '{malformed' }, status: -70402 },
      { reply: { status: 200, body: '{"status":"dispatched"}' }, status: -70402 },
      { reply: 'disconnect', status: -70402 },
    ];
    for (const [index, scenario] of scenarios.entries()) {
      proxy.replyNextCommand(scenario.reply);
      const raw = await controller.put('/characteristics', { characteristics: [{ aid: on.aid, iid: on.iid, value: index % 2 === 0, r: true }] });
      expect(raw.statusCode).toBe(207);
      expect(JSON.parse(raw.body.toString())).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: scenario.status }] });
      expect(proxy.commands.length).toBe(index + 1);
    }
    expect(bridge.stderr).not.toContain('untrusted operator text'); expect(bridge.stderr).not.toContain(code);
    const recovered = await controller.put('/characteristics', { characteristics: [{ aid: on.aid, iid: on.iid, value: false, r: true }] });
    expectWriteSuccess(recovered, on, 0);
    expect(JSON.parse((await controller.get(`/characteristics?id=${on.aid}.${on.iid}`)).body.toString())).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, value: 0 }] });
    expect(proxy.commands.length).toBe(scenarios.length + 1);
  } finally { controller?.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 45_000);

// Gate: runtime descriptor incompatibility latches the WHOLE accessory while
// retaining frozen topology and unrelated outlets; per-Entity helper tests miss
// group admission/topology consequences; real CLI/reconciliation and HAP traffic.
it.each([83, 3])('latches changed brightness maximum %s across the whole light until restart without disrupting an outlet', async maximum => {
  const stack = await createStack(); const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined; let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url }); bridge = await startCLI(config.file, true);
    controller = new WireController(config.port); const pairing = await controller.pair(code); await controller.verify();
    const original = inventory((await controller.get('/accessories')).body);
    const on = original.find(item => item.aid === 2 && item.type === '25'); const brightness = original.find(item => item.aid === 2 && item.type === '8'); const plug = original.find(item => item.aid === 3 && item.type === '25');
    if (!on || !brightness || !plug) throw new Error('topology missing');
    let changedReads = 0;
    proxy.transformReads((path, body) => {
      if (!path.endsWith(config.brightnessId)) return body;
      changedReads++;
      return { ...object(body), support: { state: { maximum }, operations: { set: { step: 5 } } } };
    });
    await eventually(async () => changedReads, count => count > 0); await delay(100);
    const count = proxy.commands.length;
    for (const slot of [on, brightness]) {
      const raw = await controller.get(`/characteristics?id=${slot.aid}.${slot.iid}`);
      expect(JSON.parse(raw.body.toString())).toEqual({ characteristics: [{ aid: slot.aid, iid: slot.iid, status: -70402 }] });
    }
    const refused = await controller.put('/characteristics', { characteristics: [{ aid: on.aid, iid: on.iid, value: true, r: true }] });
    expect(JSON.parse(refused.body.toString())).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: -70402 }] });
    expect(proxy.commands.length).toBe(count);
    const unaffected = await controller.put('/characteristics', { characteristics: [{ aid: plug.aid, iid: plug.iid, value: true, r: true }] });
    expect(JSON.parse(unaffected.body.toString())).toEqual({ characteristics: [{ aid: plug.aid, iid: plug.iid, status: 0, value: 1 }] });
    proxy.transformReads(undefined);
    await delay(1200);
    expect(JSON.parse((await controller.get(`/characteristics?id=${on.aid}.${on.iid}`)).body.toString())).toEqual({ characteristics: [{ aid: on.aid, iid: on.iid, status: -70402 }] });
    // Accessory-database serialization may need defaults for unusable values;
    // assert identity/bounds/topology here, never those defaults as successful State.
    expect(inventory((await controller.get('/accessories')).body).map(item => [item.aid, item.iid, item.type]).sort()).toEqual(original.map(item => [item.aid, item.iid, item.type]).sort());
    controller.close(); controller = undefined; await bridge.stop(); bridge = undefined;
    bridge = await startCLI(config.file, false); controller = new WireController(config.port, pairing); await controller.verify();
    expect((await controller.get(`/characteristics?id=${on.aid}.${on.iid}`)).statusCode).toBe(200);
    expect(JSON.parse((await controller.get(`/characteristics?id=${brightness.aid}.${brightness.iid}`)).body.toString())).toEqual({ characteristics: [{ aid: brightness.aid, iid: brightness.iid, value: 83 }] });
  } finally { controller?.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 45_000);
