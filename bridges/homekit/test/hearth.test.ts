import { afterEach, describe, expect, it } from 'vitest';
import { createServer } from 'node:http';
import type { IncomingMessage, ServerResponse, Server } from 'node:http';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { rmSync } from 'node:fs';
import { HearthClient, validateMappings } from '../dist/hearth.js';
import { brightnessId, deviceId, fixture, observationId, powerId } from './support/config.js';
import { eventually } from './support/runtime.js';

const servers: Server[] = [];
const roots: string[] = [];
afterEach(async () => {
  for (const server of servers.splice(0)) { server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve())); }
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});
async function upstream(handler: (request: IncomingMessage, response: ServerResponse) => void) {
  const server = createServer(handler); servers.push(server); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('missing fixture listener');
  return new HearthClient(`http://127.0.0.1:${address.port}`);
}
function entity(overrides: Record<string, unknown> = {}) {
  return { id: powerId, device_id: deviceId, type: 'hearth.power/v1', support: { state: {}, operations: { set: {} } }, enabled: true, availability: { status: 'available' }, state: { observation_id: observationId, value: false }, ...overrides };
}
const signal = () => AbortSignal.timeout(5000);

// Gate: authoritative API/schema/type correlation and bounded requests; trusting
// JSON, permissive support, redirect replay, or automatic POST retry regressions;
// sole new HTTP owner tests; real loopback HTTP, no injected production transport.
describe('Hearth HTTP boundary', () => {
  // Distinct owner risk: hint bursts and inventory work share an eight-read pool,
  // while foreground access bypasses that pool. No production injection seam.
  it('caps all background reads at eight, removes canceled queue entries, and lets foreground reads bypass the sweep', async () => {
    const waiting: { id: string; response: ServerResponse }[] = [];
    const client = await upstream((request, response) => { waiting.push({ id: request.url?.split('/').at(-1) ?? '', response }); });
    const id = (index: number) => `ent_01900000-0000-7000-8000-${String(index).padStart(12, '0')}`;
    const background = Array.from({ length: 12 }, (_, index) => client.readEntity(id(index + 1), signal(), 'background'));
    const joined = Promise.all(background); void joined.catch(() => {});
    await eventually(async () => waiting.length, count => count >= 8); await delay(20);
    expect(waiting.length).toBe(8);
    const cancel = new AbortController(); const cancelled = client.readEntity(id(13), cancel.signal, 'background');
    const cancellation = expect(cancelled).rejects.toThrow('core_timeout'); cancel.abort(); await cancellation;
    const foreground = client.readEntity(id(14), signal());
    await eventually(async () => waiting.length, count => count === 9);
    expect(waiting[8]?.id).toBe(id(14));
    for (const item of waiting.slice(0, 9)) item.response.end(JSON.stringify(entity({ id: item.id })));
    await foreground;
    await eventually(async () => waiting.length, count => count === 13);
    for (const item of waiting.slice(9)) item.response.end(JSON.stringify(entity({ id: item.id })));
    expect((await joined).map(read => read.descriptor.id)).toEqual(Array.from({ length: 12 }, (_, index) => id(index + 1)));
    expect(waiting.some(item => item.id === id(13))).toBe(false);
  });

  it('reads an actual power descriptor and preserves false despite additive metadata', async () => {
    const client = await upstream((request, response) => {
      expect({ method: request.method, url: request.url }).toEqual({ method: 'GET', url: `/v1/entities/${powerId}` });
      response.end(JSON.stringify(entity({ $schema: 'metadata', extra: true })));
    });
    expect(await client.readEntity(powerId, signal())).toEqual({ descriptor: { id: powerId, deviceId, kind: 'power' }, enabled: true, availability: 'available', state: { observationId, value: false } });
  });

  it('accepts unavailable/disabled/null State and non-step-aligned brightness without rounding it', async () => {
    let body: unknown = entity({ enabled: false, availability: { status: 'unavailable' }, state: null });
    const client = await upstream((_request, response) => response.end(JSON.stringify(body)));
    expect(await client.readEntity(powerId, signal())).toMatchObject({ enabled: false, availability: 'unavailable', state: null });
    body = entity({ id: brightnessId, type: 'hearth.brightness/v1', support: { state: { maximum: 83 }, operations: { set: { step: 5 } } }, state: { observation_id: observationId, value: 83 } });
    expect(await client.readEntity(brightnessId, signal())).toEqual({ descriptor: { id: brightnessId, deviceId, kind: 'brightness', maximum: 83, commandStep: 5 }, enabled: true, availability: 'available', state: { observationId, value: 83 } });
  });

  it.each([
    { id: brightnessId }, { device_id: 'dev_not-canonical' }, { type: 'other/v1' }, { enabled: 1 }, { availability: { status: 'online' } },
    { support: { state: {}, operations: {} } }, { support: { state: {}, operations: { set: {}, typo: {} } } },
    { state: { observation_id: observationId, value: 0 } }, { state: { observation_id: 'obs_invalid', value: false } },
    { type: 'hearth.brightness/v1', support: { state: { maximum: 80 }, operations: { set: { step: 5 } } }, state: { observation_id: observationId, value: 83 } },
    { type: 'hearth.brightness/v1', support: { state: { maximum: 100 }, operations: { set: { step: 0 } } }, state: { observation_id: observationId, value: 10 } },
  ])('rejects incompatible or malformed Entity data %#', async override => {
    const client = await upstream((_request, response) => response.end(JSON.stringify(entity(override))));
    await expect(client.readEntity(powerId, signal())).rejects.toThrow();
  });

  it('posts distinct same-value commands once each and returns satisfied false/zero evidence', async () => {
    const requests: unknown[] = [];
    const client = await upstream((request, response) => {
      let bytes = ''; request.on('data', chunk => { bytes += chunk; });
      request.on('end', () => {
        const body = JSON.parse(bytes); requests.push({ method: request.method, path: request.url, body });
        response.end(JSON.stringify({ command_id: `cmd_01900000-0000-7000-8000-00000000000${requests.length}`, status: 'satisfied', observation_id: observationId, value: body.parameters.value }));
      });
    });
    const first = await client.setEntity(powerId, false, signal());
    const second = await client.setEntity(powerId, false, signal());
    const zero = await client.setEntity(brightnessId, 0, signal());
    expect([first.value, second.value, zero.value]).toEqual([false, false, 0]);
    expect(first.commandId).not.toBe(second.commandId);
    expect(requests).toEqual([
      { method: 'POST', path: `/v1/entities/${powerId}/commands`, body: { operation: 'set', parameters: { value: false } } },
      { method: 'POST', path: `/v1/entities/${powerId}/commands`, body: { operation: 'set', parameters: { value: false } } },
      { method: 'POST', path: `/v1/entities/${brightnessId}/commands`, body: { operation: 'set', parameters: { value: 0 } } },
    ]);
    for (const invalid of [NaN, Infinity, -1, 101, 2.5]) {
      await expect(client.setEntity(brightnessId, invalid, signal())).rejects.toThrow('invalid_command_value');
    }
    expect(requests.length).toBe(3);
  });

  it.each([
    { status: 'dispatched' }, { status: 'satisfied', command_id: 'cmd_bad', observation_id: observationId, value: false },
    { status: 'satisfied', command_id: 'cmd_01900000-0000-7000-8000-000000000001', observation_id: observationId, value: 0 },
  ])('rejects success without correctly correlated satisfied evidence %#', async body => {
    const client = await upstream((_request, response) => response.end(JSON.stringify(body)));
    await expect(client.setEntity(powerId, false, signal())).rejects.toThrow('invalid_command_response');
  });

  it.each([400, 404, 409, 502, 503, 504, 307])('preserves HTTP %s without following redirects, exposing body, or retrying POST', async status => {
    let posts = 0;
    const client = await upstream((_request, response) => { posts++; response.writeHead(status, { location: '/redirected' }); response.end('secret upstream content'); });
    await expect(client.setEntity(powerId, true, signal())).rejects.toMatchObject({ message: 'core_http_error', status });
    expect(posts).toBe(1);
  });

  it.each(['malformed', 'oversized', 'partial-timeout', 'connection-loss'])('bounds %s response work without a second POST', async failure => {
    let posts = 0;
    const client = await upstream((_request, response) => {
      posts++;
      if (failure === 'malformed') response.end('{');
      if (failure === 'oversized') response.end('x'.repeat(1024 * 1024 + 1));
      if (failure === 'partial-timeout') response.write('{');
      if (failure === 'connection-loss') response.destroy();
    });
    await expect(client.setEntity(powerId, false, AbortSignal.timeout(100))).rejects.toThrow();
    expect(posts).toBe(1);
  });

  it('enforces descriptor type and same-Device mapping without requiring current availability', async () => {
    const f = fixture({ accessories: [{ key: 'lamp', name: 'Lamp', kind: 'light', power_entity: powerId, brightness_entity: brightnessId }] }); roots.push(f.root);
    const client = await upstream((request, response) => response.end(JSON.stringify(request.url?.includes(brightnessId)
      ? entity({ id: brightnessId, type: 'hearth.brightness/v1', support: { state: { maximum: 100 }, operations: { set: { step: 5 } } }, state: null })
      : entity({ enabled: false, state: null, availability: { status: 'unavailable' } }))));
    const power = await client.readEntity(powerId, signal()); const brightness = await client.readEntity(brightnessId, signal());
    const reads = new Map([[powerId, power], [brightnessId, brightness]]);
    validateMappings(f.config(), reads);
    reads.set(brightnessId, { ...brightness, descriptor: { ...brightness.descriptor, deviceId: 'dev_01900000-0000-7000-8000-000000000002' } });
    expect(() => validateMappings(f.config(), reads)).toThrow('incompatible_mapping');
  });
});
