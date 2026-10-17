import { expect, it } from 'vitest';
import { createServer } from 'node:http';
import type { ServerResponse } from 'node:http';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { HearthClient } from '../dist/hearth.js';
import type { EntityRead } from '../dist/hearth.js';
import { EntityProjection } from '../dist/projection.js';
import { deviceId, observationId, powerId } from './support/config.js';
import { eventually } from './support/runtime.js';

const initial: EntityRead = { descriptor: { id: powerId, deviceId, kind: 'power' }, enabled: true, availability: 'available', state: { observationId, value: false } };
function body(value: boolean, observation: string) {
  return { id: powerId, device_id: deviceId, type: 'hearth.power/v1', support: { state: {}, operations: { set: {} } }, enabled: true, availability: { status: 'available' }, state: { observation_id: observation, value } };
}
async function controlled() {
  const requests: ServerResponse[] = [];
  const server = createServer((_request, response) => { requests.push(response); });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const address = server.address(); if (!address || typeof address === 'string') throw new Error('missing listener');
  const projection = new EntityProjection(new HearthClient(`http://127.0.0.1:${address.port}`), initial);
  return { requests, projection, async close() {
    await projection.close(); server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve()));
  } };
}

// Gate: the public per-Entity barrier/coalescing/cancellation contract; returning
// an already active refresh or canceling another waiter is a credible failure;
// HAP alone cannot distinguish the exact generation barrier; real delayed HTTP.
it('coalesces hints and waits for a post-invocation generation without granting waiter cancellation ownership', async () => {
  const f = await controlled();
  const changes: unknown[] = [];
  const unsubscribe = f.projection.onChange(value => changes.push(value.kind === 'usable' ? value.value : value.reason));
  try {
    const first = f.projection.refreshAfterNow(AbortSignal.timeout(5000));
    await eventually(async () => f.requests.length, count => count === 1);
    const cancelled = new AbortController();
    const canceledWait = f.projection.refreshAfterNow(cancelled.signal);
    const cancelledAssertion = expect(canceledWait).rejects.toThrow('refresh_cancelled');
    let secondDone = false;
    const second = f.projection.refreshAfterNow(AbortSignal.timeout(5000)).then(() => { secondDone = true; });
    for (let i = 0; i < 1000; i++) f.projection.hint();
    cancelled.abort(); await cancelledAssertion;
    expect(f.requests.length).toBe(1);
    f.requests[0]?.end(JSON.stringify(body(true, 'obs_01900000-0000-7000-8000-000000000002')));
    await first;
    await eventually(async () => f.requests.length, count => count === 2);
    expect(secondDone).toBe(false);
    expect(f.projection.current()).toMatchObject({ kind: 'usable', refresh: 1, value: true });
    f.requests[1]?.end(JSON.stringify(body(false, 'obs_01900000-0000-7000-8000-000000000003')));
    await second;
    expect(f.projection.current()).toMatchObject({ kind: 'usable', refresh: 2, value: false, observationId: 'obs_01900000-0000-7000-8000-000000000003' });
    expect(changes).toEqual([true, false]);
    await delay(20); expect(f.requests.length).toBe(2);
  } finally { unsubscribe(); await f.close(); }
});

// Gate: timed-out old HTTP results must lose commit capability, including late
// errors; a missing generation fence regresses a recovered projection; distinct
// from ordinary coalescing; actual aborted/delayed HTTP, no private cache reads.
it.each(['success', 'failure'])('fences a late %s after timeout and joins closure without further notifications', async late => {
  const f = await controlled();
  const changes: unknown[] = []; f.projection.onChange(value => changes.push(value.kind === 'usable' ? value.value : value.reason));
  try {
    const first = f.projection.refreshAfterNow(AbortSignal.timeout(5000));
    await expect(first).rejects.toThrow('core_timeout');
    expect(f.projection.current()).toMatchObject({ kind: 'unusable', refresh: 1, reason: 'read_failed' });
    const recovered = f.projection.refreshAfterNow(AbortSignal.timeout(5000));
    await eventually(async () => f.requests.length, count => count === 2);
    f.requests[1]?.end(JSON.stringify(body(false, 'obs_01900000-0000-7000-8000-000000000004'))); await recovered;
    if (late === 'success') f.requests[0]?.end(JSON.stringify(body(true, observationId)));
    else f.requests[0]?.destroy(new Error('late transport failure'));
    await delay(30);
    expect(f.projection.current()).toMatchObject({ kind: 'usable', refresh: 2, value: false, observationId: 'obs_01900000-0000-7000-8000-000000000004' });
    await f.projection.close(); f.projection.hint(); await delay(20);
    expect(changes).toEqual(['read_failed', false]);
    expect(f.requests.length).toBe(2);
  } finally { await f.close(); }
});
