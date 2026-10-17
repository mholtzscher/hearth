import { expect, it } from 'vitest';
import { object } from '../../dist/boundary.js';
import { createStack } from '../support/stack.js';
import { createCoreProxy } from '../support/core-proxy.js';
import { inventory, json, records, runtimeConfig, startCLI } from '../support/runtime.js';
import { WireController } from '../support/wire-controller.js';
import { code } from '../support/config.js';
import { expectWriteSuccess } from '../support/hap-assertions.js';

// Gate: descriptor-driven translation must not round State, invent a positive
// command below one step, or synthesize power; credible rescaling/step-precision
// bugs are not caught by raw dependency tests; real HAP and Core command records.
// The proxy supplies alternate *valid API descriptors*; this is not evidence that
// the fixed real simulator itself advertised maximum 83/3 or step 10.
it.each([
  { maximum: 100, step: 5, requested: 83, initial: 83, expected: 85 },
  { maximum: 83, step: 5, requested: 83, initial: 83, expected: 80 },
  { maximum: 3, step: 5, requested: 3, initial: 3, expected: 0 },
  { maximum: 100, step: 10, requested: 5, initial: 83, expected: 10 },
])('preserves State precision and translates brightness $requested at maximum $maximum step $step to $expected', async scenario => {
  const stack = await createStack(); const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined; let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url });
    proxy.transformReads((path, body) => {
      if (!path.endsWith(config.brightnessId)) return body;
      const entity = object(body); const state = object(entity.state);
      return { ...entity, support: { state: { maximum: scenario.maximum }, operations: { set: { step: scenario.step } } },
        state: { ...state, value: typeof state.value === 'number' ? Math.min(scenario.maximum, state.value) : state.value } };
    });
    bridge = await startCLI(config.file, true); controller = new WireController(config.port); await controller.pair(code); await controller.verify();
    const brightness = inventory((await controller.get('/accessories')).body).find(item => item.aid === 2 && item.type === '8');
    if (!brightness) throw new Error('brightness missing');
    expect(brightness.props).toMatchObject({ minValue: 0, maxValue: scenario.maximum, minStep: 1, value: scenario.initial });
    expect(JSON.parse((await controller.get(`/characteristics?id=2.${brightness.iid}`)).body.toString())).toEqual({ characteristics: [{ aid: 2, iid: brightness.iid, value: scenario.initial }] });
    const invalid = await controller.put('/characteristics', { characteristics: [{ aid: 2, iid: brightness.iid, value: scenario.maximum + 1, r: true }] });
    expect(JSON.parse(invalid.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: brightness.iid, status: -70410 }] });
    expect(proxy.commands.length).toBe(0);
    const raw = await controller.put('/characteristics', { characteristics: [{ aid: 2, iid: brightness.iid, value: scenario.requested, r: true }] });
    expectWriteSuccess(raw, brightness, scenario.expected);
    expect(JSON.parse((await controller.get(`/characteristics?id=2.${brightness.iid}`)).body.toString())).toEqual({ characteristics: [{ aid: 2, iid: brightness.iid, value: scenario.expected }] });
    expect(proxy.commands.map(command => command.body)).toEqual([{ operation: 'set', parameters: { value: scenario.expected } }]);
    const history = records(object(await json(`${stack.coreURL}/v1/entities/${config.brightnessId}/commands`)).items);
    expect(history).toHaveLength(1); expect(history[0]).toMatchObject({ parameters: { value: scenario.expected }, status: 'satisfied' });
    expect(records(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}/commands`)).items)).toEqual([]);
    expect(object(object(await json(`${stack.coreURL}/v1/entities/${config.powerId}`)).state).value).toBe(false);
  } finally { controller?.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 45_000);
