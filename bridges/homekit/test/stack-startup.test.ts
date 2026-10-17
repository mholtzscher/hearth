import { expect, it } from 'vitest';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createStack } from './support/stack.js';

// Gate: real fixture process failure must survive teardown as a safe diagnostic;
// previously CI lost the exit cause; no other test owns fixture failure evidence.
it('retains pre-cleanup process exits and safe codes without raw fixture data', async () => {
  const bin = mkdtempSync(join(tmpdir(), 'homekit-failing-fixture-'));
  const previousPath = process.env.PATH;
  let evidence: string | undefined;
  try {
    writeFileSync(join(bin, 'nats-server'), '#!/bin/sh\necho \'{"event":"fixture.failed","error_code":"fixture_failure","message":"private-canary","state":"private-canary"}\'\nexit 42\n', { mode: 0o700 });
    process.env.PATH = `${bin}:${previousPath ?? ''}`;
    const error = await createStack().then(() => { throw new Error('fixture unexpectedly started'); }, error => error as Error);
    expect(error.message).toContain('HomeKit fixture startup failed');
    evidence = error.message.split('private evidence: ')[1];
    if (!evidence) throw new Error('missing fixture evidence directory');
    const verdict = readFileSync(join(evidence, 'startup-verdict.json'), 'utf8');
    expect(JSON.parse(verdict)).toEqual({ status: 'startup_failed', fixtures: [{
      name: 'nats', exit: 42, signal: null, spawnFailed: false, addressInUse: false,
      events: [{ event: 'fixture.failed', error_code: 'fixture_failure' }],
    }] });
    expect(verdict).not.toContain('private-canary');
  } finally {
    if (previousPath === undefined) delete process.env.PATH; else process.env.PATH = previousPath;
    rmSync(bin, { recursive: true, force: true });
    if (evidence) rmSync(evidence, { recursive: true, force: true });
  }
});
