import { expect, it } from 'vitest';
import { setTimeout as delay } from 'node:timers/promises';
import { createStack } from '../support/stack.js';
import { createCoreProxy } from '../support/core-proxy.js';
import { eventually, inventory, runtimeConfig, startCLI } from '../support/runtime.js';
import { WireController } from '../support/wire-controller.js';
import { code } from '../support/config.js';

// Gate: shutdown joins admitted work without granting it a fresh budget, and
// parent death must not orphan the locked runtime; helper-only tests cannot prove
// real socket/PID/lock lifetime; OS signals, delayed real Command and encrypted HAP.
it.each(['terminate', 'kill-supervisor'])('joins the locked runtime after %s and never replays admitted Commands on restart', async action => {
  const stack = await createStack(); const proxy = await createCoreProxy(stack.coreURL);
  let bridge: Awaited<ReturnType<typeof startCLI>> | undefined; let controller: WireController | undefined;
  try {
    const config = await runtimeConfig(stack, { core_url: proxy.url, reconcile_interval: '60s' }); bridge = await startCLI(config.file, true);
    controller = new WireController(config.port); const pairing = await controller.pair(code); await controller.verify();
    const on = inventory((await controller.get('/accessories')).body).find(item => item.aid === 2 && item.type === '25');
    if (!on) throw new Error('On missing');
    proxy.holdCommands(true);
    const request = controller.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: true, r: true }] });
    await eventually(async () => proxy.commands.length, count => count === 1);
    const runtimePid = bridge.runtimePid; if (runtimePid === undefined) throw new Error('runtime pid missing');
    const stop = performance.now(); bridge.child.kill(action === 'terminate' ? 'SIGTERM' : 'SIGKILL');
    await delay(250);
    const duringDrain = new WireController(config.port, pairing);
    try {
      await duringDrain.verify();
      const rejected = await duringDrain.put('/characteristics', { characteristics: [{ aid: 2, iid: on.iid, value: false, r: true }] });
      expect(JSON.parse(rejected.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: -70402 }] });
      expect(proxy.commands.length).toBe(1);
    } finally { duringDrain.close(); }
    proxy.commands[0]?.release();
    const response = await Promise.race([request, delay(10_000).then(() => { throw new Error(`admitted response was lost: ${bridge?.stderr}`); })]);
    expect(response.statusCode).toBe(207);
    expect(JSON.parse(response.body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, status: 0, value: 1 }] });
    const exited = await bridge.exit;
    expect(exited).toEqual(action === 'terminate' ? { code: 0, signal: null } : { code: null, signal: 'SIGKILL' });
    await eventually(async () => { try { process.kill(runtimePid, 0); return true; } catch { return false; } }, alive => !alive);
    expect(performance.now() - stop).toBeLessThan(10_000);
    controller.close(); controller = undefined; bridge = undefined;
    proxy.holdCommands(false);
    bridge = await startCLI(config.file, false); controller = new WireController(config.port, pairing); await controller.verify();
    expect(JSON.parse((await controller.get(`/characteristics?id=2.${on.iid}`)).body.toString())).toEqual({ characteristics: [{ aid: 2, iid: on.iid, value: 1 }] });
    expect(proxy.commands.length).toBe(1);
  } finally { controller?.close(); await bridge?.stop(); await proxy.close(); await stack.close(); }
}, 45_000);

// A listening socket can vanish independently of Core. Close the actual public
// Node TCP server after readiness and verify the production monitor's fail-stop.
it('stops after two consecutive failed listener probes without resetting identity', async () => {
  const stack = await createStack(); let bridge: Awaited<ReturnType<typeof startCLI>> | undefined;
  try {
    const config = await runtimeConfig(stack);
    const started = performance.now();
    bridge = await startCLI(config.file, true, {
      NODE_OPTIONS: `--import=${new URL('../support/listener-loss.mjs', import.meta.url).href}`,
      HOMEKIT_FAULT_PORT: String(config.port),
    });
    const runtimePid = bridge.runtimePid; if (runtimePid === undefined) throw new Error('runtime pid missing');
    process.kill(runtimePid, 'SIGUSR2');
    const result = await Promise.race([bridge.exit, delay(15_000).then(() => { throw new Error('listener loss did not terminate runtime'); })]);
    expect(result).toEqual({ code: 1, signal: null });
    expect(bridge.stderr).toContain('hap_listener_failed');
    expect(performance.now() - started).toBeGreaterThan(9000);
    bridge = undefined;
    bridge = await startCLI(config.file, false);
    expect(bridge.stderr).toContain('"event":"ready"');
  } finally { await bridge?.stop(); await stack.close(); }
}, 35_000);

it('bounds a hung dependency cleanup and releases the serving lock only after runtime exit', async () => {
  const stack = await createStack(); let bridge: Awaited<ReturnType<typeof startCLI>> | undefined;
  try {
    const config = await runtimeConfig(stack);
    bridge = await startCLI(config.file, true, {
      NODE_OPTIONS: `--import=${new URL('../support/shutdown-hang.mjs', import.meta.url).href}`,
    });
    const runtimePid = bridge.runtimePid; if (runtimePid === undefined) throw new Error('runtime pid missing');
    const started = performance.now();
    const result = await Promise.race([bridge.stop(), delay(13_000).then(() => { throw new Error('shutdown escalation failed'); })]);
    expect(result).toEqual({ code: 1, signal: null });
    expect(performance.now() - started).toBeGreaterThanOrEqual(9500);
    expect(performance.now() - started).toBeLessThan(12_000);
    await eventually(async () => { try { process.kill(runtimePid, 0); return true; } catch { return false; } }, alive => !alive);
    bridge = undefined;
    bridge = await startCLI(config.file, false);
    expect(bridge.stderr).toContain('"event":"ready"');
  } finally { await bridge?.stop(); await stack.close(); }
}, 35_000);
