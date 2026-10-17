import { expect, it } from 'vitest';
import { mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { generateKeyPairSync } from 'node:crypto';
import { join } from 'node:path';
import { WireController } from './support/wire-controller.js';
import { startHAPFixture } from './support/hap-fixture.js';
import { eventually, inventory } from './support/runtime.js';

function record(input: unknown): Record<string, unknown> {
  if (typeof input !== 'object' || input === null || Array.isArray(input)) throw new Error('invalid HAP object');
  return Object.fromEntries(Object.entries(input));
}
function records(input: unknown): Record<string, unknown>[] {
  if (!Array.isArray(input)) throw new Error('invalid HAP collection');
  return input.map(record);
}

// Gate: literal false/zero write responses on the encrypted wire; the upstream
// truthiness check drops them; controller methods otherwise echo request values;
// installed dependency process + independent public protocol transport, no HAP internals.
it('returns literal true/false/zero/positive values for r:true and omits unrequested responses on encrypted HTTP', async () => {
  const fixture = await startHAPFixture();
  let controller: WireController | undefined;
  try {
    controller = new WireController(fixture.port);
    await controller.pair('482-65-193');
    await controller.verify();
    const inventory = await controller.get('/accessories');
    expect(inventory.statusCode).toBe(200);
    const parsed: unknown = JSON.parse(inventory.body.toString());
    const database = record(parsed);
    const light = records(database.accessories).find(item => item.aid !== 1);
    if (!light) throw new Error('light missing');
    const characteristics = records(light.services).flatMap(service => records(service.characteristics));
    const on = characteristics.find(item => item.type === '25');
    const brightness = characteristics.find(item => item.type === '8');
    if (!on || !brightness) throw new Error('characteristics missing');
    expect(light.aid).toBe(2);
    // The pinned library's public outgoing formatter encodes bool as HAP 1/0.
    // Assert those literal raw values, not a high-level controller's bool echo.
    const cases: [Record<string, unknown>, boolean | number, number][] = [[on, true, 1], [on, false, 0], [brightness, 0, 0], [brightness, 83, 83]];
    for (const [characteristic, value, wireValue] of cases) {
      const raw = await controller.put('/characteristics', { characteristics: [{ aid: light.aid, iid: characteristic.iid, value, r: true }] });
      expect(raw.statusCode).toBe(207);
      expect(JSON.parse(raw.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: characteristic.iid, status: 0, value: wireValue }] });
    }
    for (const flag of [{}, { r: false }]) {
      const raw = await controller.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: false, ...flag }] });
      expect(raw.statusCode).toBe(204);
      expect(raw.body.length).toBe(0);
    }
  } finally {
    controller?.close();
    await fixture.close();
  }
}, 20_000);

// Gate: prove why the persistence hook is needed, not merely that its API exists.
// Removing registration can leave a route-catching server alive after a failed
// save; positive production process tests cannot establish that counterfactual.
// Real authenticated pairing traffic + OS write failure, no production seam.
it('negative control: without a persistence handler a pairing save fails but the encrypted server remains alive', async () => {
  const fixture = await startHAPFixture();
  const controller = new WireController(fixture.port);
  try {
    await controller.pair('482-65-193'); await controller.verify();
    const infoName = readdirSync(fixture.directory).find(name => name.startsWith('AccessoryInfo.'));
    if (!infoName || fixture.child.pid === undefined) throw new Error('initialized pairing storage missing');
    const file = join(fixture.directory, infoName); const saved = readFileSync(file);
    // Same genuine temporary-file EISDIR fault as the production fail-stop tests.
    mkdirSync(join(fixture.directory, `.${infoName}.${fixture.child.pid}.tmp`), { mode: 0o700 });
    const key = generateKeyPairSync('ed25519').publicKey.export({ format: 'der', type: 'spki' }).subarray(-32);
    const response = await controller.addPairingResponse('fixture-not-persisted', key);
    expect(response.statusCode).toBe(500);
    expect(JSON.parse(response.body.toString())).toEqual({ status: -70403 });
    expect(readFileSync(file)).toEqual(saved);
    expect(fixture.child.exitCode).toBe(null);
    // A fresh encrypted request succeeds AFTER the failed save: a missing API at
    // startup, authorization denial, or disconnected socket cannot fake this proof.
    const stillServing = await controller.get('/accessories');
    expect(stillServing.statusCode).toBe(200);
    expect(inventory(stillServing.body).some(item => item.aid === 2 && item.type === '25')).toBe(true);
  } finally { controller.close(); await fixture.close(); }
}, 20_000);

// Gate: conventional async setters reintroduce a requested-value notification
// after a newer authoritative update. Production callbacks already pass the
// opposite invariant; this counterfactual proves that the unsafe baseline really
// exhibits the forbidden wire behavior. Public handlers/updateValue/IPC only.
it.each([
  { key: 'on', type: '25', requested: true, physical: false, requestedWire: 1, physicalWire: 0 },
  { key: 'brightness', type: '8', requested: 83, physical: 0, requestedWire: 83, physicalWire: 0 },
])('negative control: an ordinary async $key setter notifies its request after newer State', async scenario => {
  const fixture = await startHAPFixture('ordinary');
  const writer = new WireController(fixture.port);
  let subscriber: WireController | undefined;
  try {
    const pairing = await writer.pair('482-65-193'); await writer.verify();
    subscriber = new WireController(fixture.port, pairing); await subscriber.verify();
    const slot = inventory((await writer.get('/accessories')).body).find(item => item.aid === 2 && item.type === scenario.type);
    if (!slot) throw new Error('negative-control characteristic missing');
    const rawEvents: Buffer[] = [];
    subscriber.onEvent(body => rawEvents.push(Buffer.from(body)));
    const chronological = () => rawEvents.flatMap(bytes => {
      const parsed: unknown = JSON.parse(bytes.toString());
      // Retain the raw bytes; normalize the pinned library's reversed batches
      // without changing an event object or touching any library listener.
      return records(record(parsed).characteristics).toReversed()
        .filter(item => item.aid === slot.aid && item.iid === slot.iid).map(item => item.value);
    });
    expect((await subscriber.put('/characteristics', { characteristics: [{ aid: slot.aid, iid: slot.iid, ev: true }] }, true)).statusCode).toBe(204);
    const entered = fixture.waitForSet(scenario.key);
    const write = writer.put('/characteristics', { characteristics: [{ aid: slot.aid, iid: slot.iid, value: scenario.requested }] });
    await entered;
    await fixture.observe(scenario.key, scenario.requested);
    await eventually(async () => chronological(), values => values.at(-1) === scenario.requestedWire, 3000);
    await fixture.observe(scenario.key, scenario.physical);
    await eventually(async () => chronological(), values => values.at(-1) === scenario.physicalWire, 3000);
    expect(chronological()).toEqual([scenario.requestedWire, scenario.physicalWire]);
    await fixture.releaseCommands();
    expect((await write).statusCode).toBe(204);
    await eventually(async () => chronological(), values => values.length === 3, 3000);
    // Expected regression observation: the third notification is forbidden in
    // production. Neither IPC observation above sent this third/requested value.
    expect(chronological()).toEqual([scenario.requestedWire, scenario.physicalWire, scenario.requestedWire]);
    const authoritative = await writer.get(`/characteristics?id=${slot.aid}.${slot.iid}`);
    expect(JSON.parse(authoritative.body.toString())).toEqual({ characteristics: [{ aid: slot.aid, iid: slot.iid, value: scenario.physicalWire }] });
  } finally { writer.close(); subscriber?.close(); await fixture.close(); }
}, 20_000);
