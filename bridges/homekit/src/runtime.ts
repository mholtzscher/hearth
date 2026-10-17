import { BoundaryError } from './boundary.js';
import { diagnostic } from './logging.js';
import { startFactHints } from './facts.js';
import { HomeKitBridge, probeHAPListener } from './hap.js';
import { HearthClient, validateMappings } from './hearth.js';
import type { EntityRead } from './hearth.js';
import { EntityProjection } from './projection.js';
import type { RuntimeContext } from './locked.js';

/** Manual assembly: this companion does not own Devices, Sessions, or executable intent. */
export async function runRuntime(context: RuntimeContext): Promise<void> {
  const { config, identity, signal, fatalStop } = context;
  const startup = AbortSignal.any([signal, AbortSignal.timeout(30_000)]);
  const client = new HearthClient(config.coreURL);
  const projections = new Map<string, EntityProjection>();
  let facts: Awaited<ReturnType<typeof startFactHints>> | undefined;
  let hap: HomeKitBridge | undefined;
  let stopping = false;
  let reconcileTimer: NodeJS.Timeout | undefined;
  let monitorTimer: NodeJS.Timeout | undefined;
  let pass: Promise<void> = Promise.resolve();
  let monitor: Promise<void> = Promise.resolve();
  const background = new AbortController();
  const stop = () => {
    stopping = true;
    hap?.stopAdmission();
    if (reconcileTimer) clearTimeout(reconcileTimer);
    if (monitorTimer) clearTimeout(monitorTimer);
    background.abort();
  };
  signal.addEventListener('abort', stop, { once: true });
  const batch = async (work: (id: string) => Promise<void>, batchSignal: AbortSignal) => {
    const ids = config.accessories.flatMap(item => item.kind === 'light' && item.brightnessEntity ? [item.powerEntity, item.brightnessEntity] : [item.powerEntity]);
    let next = 0;
    await Promise.all(Array.from({ length: Math.min(8, ids.length) }, async () => {
      while (!batchSignal.aborted) {
        const id = ids[next++];
        if (id === undefined) return;
        await work(id);
      }
    }));
    batchSignal.throwIfAborted();
  };
  try {
    startup.throwIfAborted();
    const reads = new Map<string, EntityRead>();
    await batch(async id => { reads.set(id, await client.readEntity(id, startup)); }, startup);
    validateMappings(config, reads);
    facts = await startFactHints(config, id => { if (!stopping) projections.get(id)?.hint(); }, startup);
    for (const [id, read] of reads) projections.set(id, new EntityProjection(client, read));
    await batch(async id => { await projections.get(id)?.refreshAfterNow(startup, 'background'); }, startup);
    if (context.initialize) identity.beginInitialization();
    else identity.recordAccessoryKinds();
    context.prepareHAPStorage();
    hap = new HomeKitBridge(config, identity.manifest, client, projections);
    await hap.publish(context.pairingCode, startup, () => {
      try {
        if (context.initialize) identity.commitInitialized();
        else identity.checkIntegrity();
      } catch { fatalStop('identity_persistence_failed'); }
    });
    identity.watchIntegrity(() => fatalStop('identity_integrity_failed'));
    diagnostic('runtime', 'ready', 'ready');
    const reconcile = () => {
      if (stopping) return;
      pass = (async () => {
        try { identity.checkIntegrity(); } catch { fatalStop('identity_integrity_failed'); }
        await batch(async id => { await projections.get(id)?.refreshAfterNow(background.signal, 'background').catch(() => undefined); }, background.signal);
      })().catch(() => {
        if (!stopping) diagnostic('runtime', 'reconcile_failed');
      }).finally(() => {
        if (!stopping) reconcileTimer = setTimeout(reconcile, config.reconcileIntervalMs);
      });
    };
    reconcileTimer = setTimeout(reconcile, config.reconcileIntervalMs);
    let probeFailures = 0;
    const probe = () => {
      if (stopping) return;
      monitor = probeHAPListener(config.hapPort, background.signal).then(() => { probeFailures = 0; }, () => {
        if (!stopping && ++probeFailures >= 2) fatalStop('hap_listener_failed');
      }).finally(() => { if (!stopping) monitorTimer = setTimeout(probe, 5000); });
    };
    monitorTimer = setTimeout(probe, 5000);
    if (!signal.aborted) await new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }));
  } catch (error) {
    hap?.failOperations();
    if (error instanceof BoundaryError && error.code === 'manifest_persistence_failed') fatalStop('identity_persistence_failed');
    throw error;
  } finally {
    stop();
    // Joining is different from cancellation: admitted callbacks retain their
    // original budgets, while background waiters stop scheduling new work.
    const shutdownTimer = setTimeout(() => fatalStop('shutdown_timeout'), 10_000);
    try {
      await Promise.all([pass, monitor]);
      await hap?.close();
      await facts?.close();
      await Promise.all([...projections.values()].map(projection => projection.close()));
    } finally {
      clearTimeout(shutdownTimer);
      signal.removeEventListener('abort', stop);
      identity.close();
    }
  }
}
