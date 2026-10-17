import { fork } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Disposable dependency process; IPC controls only test-owned observations/upstream waits. */
export async function startHAPFixture(setter: 'callback' | 'ordinary' = 'callback') {
  const directory = mkdtempSync(join(tmpdir(), 'hearth-hap-wire-'));
  const child = fork(fileURLToPath(new URL('./hap-server.mjs', import.meta.url)), [], {
    env: { ...process.env, HOMEKIT_TEST_STORE: directory, HOMEKIT_TEST_SETTER: setter }, stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
  });
  let diagnostics = '';
  child.stderr?.on('data', chunk => { diagnostics += chunk; });
  const message = (matches: (value: Record<string, unknown>) => boolean, timeout = 5000) => new Promise<Record<string, unknown>>((resolve, reject) => {
    const cleanup = () => { clearTimeout(timer); child.off('message', receive); child.off('exit', exited); child.off('error', failed); };
    const receive = (value: unknown) => { if (record(value) && matches(value)) { cleanup(); resolve(value); } };
    const exited = () => { cleanup(); reject(new Error(`HAP fixture exited: ${diagnostics}`)); };
    const failed = (error: Error) => { cleanup(); reject(error); };
    const timer = setTimeout(() => { cleanup(); reject(new Error(`HAP fixture IPC timeout: ${diagnostics}`)); }, timeout);
    child.on('message', receive); child.once('exit', exited); child.once('error', failed);
  });
  const close = async () => {
    try {
      if (child.exitCode === null && child.signalCode === null) {
        const exited = once(child, 'exit');
        if (child.connected) child.send('stop');
        const kill = setTimeout(() => child.kill('SIGKILL'), 3000);
        try { await exited; } finally { clearTimeout(kill); }
      }
    } finally { rmSync(directory, { recursive: true, force: true }); }
  };
  let sequence = 0;
  const command = async (body: { action: 'observe'; characteristic: string; value: boolean | number } | { action: 'release' }) => {
    const id = ++sequence;
    const acknowledged = message(value => value.event === 'ack' && value.id === id);
    child.send({ ...body, id });
    await acknowledged;
  };
  try {
    const listening = await message(value => typeof value.port === 'number', 10_000);
    if (typeof listening.port !== 'number') throw new Error('missing HAP port');
    return { child, directory, port: listening.port, close,
      waitForSet(key: string) { return message(value => value.event === 'set-started' && value.characteristic === key); },
      observe(characteristic: string, value: boolean | number) { return command({ action: 'observe', characteristic, value }); },
      releaseCommands() { return command({ action: 'release' }); } };
  } catch (error) { await close(); throw error; }
}
