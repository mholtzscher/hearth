#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { join } from 'node:path';
import { BoundaryError } from './boundary.js';
import { loadConfig, parseCLI, readPairingCode, validateInterfaces } from './config.js';
import { prepareDataDirectory } from './identity.js';
import { diagnostic } from './logging.js';

/** Public CLI owns flock supervision; all HAP data access is in its locked child. */
export async function main(argv: readonly string[], env: NodeJS.ProcessEnv): Promise<number> {
  try {
    process.umask(0o077);
    const options = parseCLI(argv);
    const config = loadConfig(argv, env);
    readPairingCode(config);
    if (options.command === 'serve') validateInterfaces(config);
    prepareDataDirectory(config, options.initialize);
    // Vendor debug output can include storage paths, names, and wire data.
    // It is never an allowed diagnostic channel, even when the parent enables DEBUG.
    const childEnv = { ...env };
    delete childEnv.DEBUG;
    delete childEnv.NODE_DEBUG;
    delete childEnv.NODE_DEBUG_NATIVE;
    const child = spawn('flock', [
      '--exclusive', '--nonblock', '--conflict-exit-code', '73', '--no-fork',
      join(config.dataDir, 'bridge.lock'), process.execPath, fileURLToPath(new URL('./locked.js', import.meta.url)), ...argv,
    ], { env: childEnv, stdio: ['pipe', 'inherit', 'inherit'] });
    let stopping = false;
    let killTimer: NodeJS.Timeout | undefined;
    const stop = (signal: NodeJS.Signals) => {
      if (stopping) return;
      stopping = true;
      child.kill(signal);
      // Child has a ten-second graceful budget; keep the lock until it has actually exited.
      killTimer = setTimeout(() => child.kill('SIGKILL'), 11_000);
      killTimer.unref();
    };
    const interrupt = () => stop('SIGINT');
    const terminate = () => stop('SIGTERM');
    process.on('SIGINT', interrupt);
    process.on('SIGTERM', terminate);
    try {
      return await new Promise<number>((resolve) => {
        child.once('error', () => { diagnostic('cli', 'locked_child_failed'); resolve(1); });
        child.once('exit', (code, signal) => {
          if (code === 73) diagnostic('cli', 'storage_locked');
          resolve(code ?? (signal === 'SIGINT' ? 130 : signal === 'SIGTERM' ? 143 : 1));
        });
      });
    } finally {
      if (killTimer) clearTimeout(killTimer);
      process.off('SIGINT', interrupt);
      process.off('SIGTERM', terminate);
      child.stdin.end();
    }
  } catch (error) {
    diagnostic('cli', error instanceof BoundaryError ? error.code : 'startup_failed');
    return 1;
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  process.exitCode = await main(process.argv.slice(2), process.env);
}
