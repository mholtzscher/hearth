import { expect, it } from 'vitest';
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

// Gate: conventional async setters reintroduce a requested-value notification
// after a newer authoritative update. Production callbacks already pass the
// opposite invariant; this counterfactual proves that the unsafe baseline really
// exhibits the forbidden wire behavior. Public handlers/updateValue/IPC only.
it.each([
  { key: 'on', type: '25', requested: true, physical: false, requestedWire: 1, physicalWire: 0 },
  { key: 'brightness', type: '8', requested: 83, physical: 0, requestedWire: 83, physicalWire: 0 },
])('negative control: an ordinary async $key setter notifies its request after newer State', async scenario => {
  const fixture = await startHAPFixture();
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
