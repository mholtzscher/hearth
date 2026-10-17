import { expect, it, vi } from 'vitest';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { HomeKitBridge, probeHAPListener } from '../dist/hap.js';
import { Bridge } from '@homebridge/hap-nodejs';
import { HearthClient } from '../dist/hearth.js';
import { EntityProjection } from '../dist/projection.js';
import { fixture, powerId, deviceId, observationId, code } from './support/config.js';
import { rmSync } from 'node:fs';
import { setTimeout as delay } from 'node:timers/promises';

// Gate: readiness/monitoring must observe recognizable HAP, not just any socket;
// accepting HTTP200/oversized/partial responses falsely declares readiness;
// cannot be isolated by a healthy real bridge; real HTTP listener, public probe.
it.each(['hap', 'wrong-status', 'wrong-body', 'oversized', 'hung-body'])('recognizes only bounded unauthenticated HAP: %s', async variant => {
  const server = createServer((request, response) => {
    expect({ method: request.method, path: request.url }).toEqual({ method: 'GET', path: '/accessories' });
    response.writeHead(variant === 'wrong-status' ? 200 : 470);
    if (variant === 'hung-body') response.write('{');
    else response.end(variant === 'oversized' ? 'x'.repeat(4097) : JSON.stringify({ status: variant === 'wrong-body' ? 0 : -70401 }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const address = server.address(); if (!address || typeof address === 'string') throw new Error('port missing');
  try {
    const probe = probeHAPListener(address.port, AbortSignal.timeout(2000));
    if (variant === 'hap') await probe;
    else await expect(probe).rejects.toThrow('hap_probe_failed');
  } finally { server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve())); }
});

// Resolving HAP publish does not establish readiness. Exercise the public event
// boundary and a real unauthenticated probe separately from healthy-process tests.
it.each(['delayed', 'missing-listening', 'missing-advertised'])('waits for both public readiness events: %s', async variant => {
  const server = createServer((_request, response) => { response.writeHead(470); response.end('{"status":-70401}'); });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const address = server.address(); if (!address || typeof address === 'string') throw new Error('port missing');
  const f = fixture(); const config = { ...f.config(), hapPort: address.port };
  const client = new HearthClient(config.coreURL);
  const projection = new EntityProjection(client, { descriptor: { id: powerId, deviceId, kind: 'power' }, enabled: true, availability: 'available', state: { observationId, value: false } });
  const hap = new HomeKitBridge(config, { version: 1, stage: 'initialized', bridgeId: config.bridgeId, username: '02:00:00:00:01:02', setupId: 'TEST', pairingCodeDigest: '0'.repeat(64), accessoryKinds: { lamp: 'light' } }, client, new Map([[powerId, projection]]));
  let release: () => void = () => {};
  const publish = vi.spyOn(Bridge.prototype, 'publish').mockImplementation(async function(this: Bridge) {
    release = () => {
      if (variant !== 'missing-listening') this.emit('listening', address.port, '127.0.0.1');
      if (variant !== 'missing-advertised') this.emit('advertised');
    };
  });
  let initialized = 0; let ready = false;
  try {
    const pending = hap.publish(code, AbortSignal.timeout(2000), () => { initialized++; }).then(() => { ready = true; });
    const rejected = variant === 'delayed' ? undefined : expect(pending).rejects.toThrow('hap_startup_timeout');
    await delay(10); expect(ready).toBe(false); expect(initialized).toBe(0);
    release();
    if (variant === 'delayed') { await pending; expect(ready).toBe(true); expect(initialized).toBe(1); }
    else { await rejected; expect(ready).toBe(false); expect(initialized).toBe(variant === 'missing-listening' ? 0 : 1); }
  } finally {
    publish.mockRestore(); await hap.close(); await projection.close();
    server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve()));
    rmSync(f.root, { recursive: true, force: true });
  }
});
