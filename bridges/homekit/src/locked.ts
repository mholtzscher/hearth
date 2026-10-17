/** Private entry point: invoked only by cli.ts under util-linux flock --no-fork. */
import { HAPStorage } from '@homebridge/hap-nodejs';
import { BoundaryError } from './boundary.js';
import { diagnostic } from './logging.js';
import { loadConfig, parseCLI, readPairingCode } from './config.js';
import type { Config } from './config.js';
import { IdentityStore, pairingInfo } from './identity.js';
import { runRuntime } from './runtime.js';

/** Concrete assembly contract for runtime.ts; not an SDK or a test injection API. */
export type RuntimeContext = {
  config: Config; identity: IdentityStore; pairingCode: string;
  initialize: boolean; signal: AbortSignal;
  prepareHAPStorage(): void;
  fatalStop(reason: string): never;
};

process.umask(0o077);
// No dependency-controlled console/debug text is an approved log channel.
// Preserve only a fixed diagnostic, without names, paths, pairing or wire data.
let vendorWarning = false;
for (const method of ['log', 'info', 'warn', 'error', 'debug'] as const) {
  console[method] = () => {
    if (!vendorWarning) { vendorWarning = true; diagnostic('dependency', 'diagnostic_suppressed', 'dependency'); }
  };
}
const shutdown = new AbortController();
let fatal = false;
const fatalStop = (reason: string): never => {
  fatal = true;
  diagnostic('runtime', reason);
  process.exit(1);
};
// Native listener failures must not escape as unknown exception strings/paths.
process.on('uncaughtException', () => fatalStop('uncaught_exception'));
process.on('unhandledRejection', () => fatalStop('unhandled_rejection'));
let shutdownTimer: NodeJS.Timeout | undefined;
const stop = () => {
  if (fatal || shutdown.signal.aborted) return;
  shutdown.abort();
  shutdownTimer = setTimeout(() => process.exit(1), 10_000);
  shutdownTimer.unref();
};
process.on('SIGTERM', stop);
process.on('SIGINT', stop);
// Parent death (even SIGKILL) closes this pipe. Never orphan a serving runtime.
process.stdin.resume();
process.stdin.on('end', stop);
process.stdin.on('error', stop);

let identity: IdentityStore | undefined;
try {
  const argv = process.argv.slice(2);
  const options = parseCLI(argv);
  const config = loadConfig(argv, process.env);
  if (config.dataDir !== process.env.HEARTH_HOMEKIT_LOCKED_DATA_DIR) {
    throw new BoundaryError('locked_data_directory_mismatch');
  }
  const code = readPairingCode(config);
  identity = IdentityStore.open(config, code, options.initialize);
  if (options.command === 'pairing-info') {
    process.stdout.write(`${JSON.stringify(pairingInfo(identity.manifest, code))}\n`);
  } else {
    // Register before runtime can construct a HAP object. No unknown error/key/path reaches logs.
    const store = identity;
    const prepareHAPStorage = () => {
      // On first start, runtime must call beginInitialization AFTER descriptor
      // discovery and BEFORE this function: storage() creates its directory.
      store.manifest;
      HAPStorage.setCustomStoragePath(`${config.dataDir}/hap`);
      HAPStorage.storage().setPersistenceErrorHandler(() => {
        fatalStop('hap_persistence_failed');
      });
    };
    const context: RuntimeContext = { config, identity, pairingCode: code, initialize: options.initialize, signal: shutdown.signal, prepareHAPStorage, fatalStop };
    await runRuntime(context);
  }
} catch (error) {
  diagnostic('runtime', error instanceof BoundaryError ? error.code : 'startup_failed');
  process.exitCode = 1;
} finally {
  identity?.close();
  if (shutdownTimer) clearTimeout(shutdownTimer);
  process.stdin.destroy();
  process.off('SIGTERM', stop);
  process.off('SIGINT', stop);
}
