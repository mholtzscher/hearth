import { afterEach, describe, expect, it } from 'vitest';
import { createRequire } from 'node:module';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { HAPFileStorage as Storage } from '@homebridge/hap-nodejs';

const require = createRequire(import.meta.url);
// A test-only dependency selector permits executing the same owner contract
// against an unpacked upstream tarball as a negative control, never source greps.
const { HAPFileStorage }: { HAPFileStorage: typeof Storage } = require(process.env.HOMEKIT_HAP_PACKAGE ?? '@homebridge/hap-nodejs');
const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

// Gate: installed dependency reports filesystem failures once and does not change
// cached/durable data; omission of mkdir/write/rename/remove catch is credible;
// no application test can isolate this dependency guarantee; public API + real FS.
describe('installed HAP persistence-error API', () => {
  it.each(['mkdir', 'write', 'rename', 'remove'])('reports %s failure synchronously, preserves cache, and rethrows the original filesystem failure', operation => {
    const root = mkdtempSync(join(tmpdir(), 'hearth-hap-patch-')); roots.push(root);
    const dir = join(root, 'hap'); mkdirSync(dir);
    const storage = new HAPFileStorage(); storage.initSync({ dir });
    storage.setItemSync('item', { saved: 1 });
    const notifications: unknown[][] = [];
    storage.setPersistenceErrorHandler((...args) => { notifications.push(args); });
    if (operation === 'mkdir') { rmSync(dir, { recursive: true }); writeFileSync(dir, 'obstruction'); }
    if (operation === 'write') mkdirSync(join(dir, `.item.${process.pid}.tmp`));
    if (operation === 'rename' || operation === 'remove') { rmSync(join(dir, 'item')); mkdirSync(join(dir, 'item')); }
    let thrown: unknown;
    try { if (operation === 'remove') storage.removeItemSync('item'); else storage.setItemSync('item', { saved: 2 }); }
    catch (error) { thrown = error; }
    expect(thrown).toBeInstanceOf(Error);
    expect(thrown).toHaveProperty('code');
    expect(notifications).toEqual([[operation === 'remove' ? 'remove' : 'set']]);
    expect(storage.getItem('item')).toEqual({ saved: 1 });
    if (operation === 'write') expect(JSON.parse(readFileSync(join(dir, 'item'), 'utf8'))).toEqual({ saved: 1 });
  });

  it('preserves successful update/removal and upstream no-handler throw behavior', () => {
    const root = mkdtempSync(join(tmpdir(), 'hearth-hap-patch-')); roots.push(root);
    const storage = new HAPFileStorage(); storage.initSync({ dir: root });
    storage.setItemSync('item', false);
    expect(JSON.parse(readFileSync(join(root, 'item'), 'utf8'))).toBe(false);
    expect(storage.getItem('item')).toBe(false);
    storage.removeItemSync('item');
    expect(storage.getItem('item')).toBeUndefined();
    mkdirSync(join(root, `.item.${process.pid}.tmp`));
    expect(() => storage.setItemSync('item', 0)).toThrow();
    expect(storage.getItem('item')).toBeUndefined();
    rmSync(join(root, `.item.${process.pid}.tmp`), { recursive: true });
    const notifications: string[] = [];
    storage.setPersistenceErrorHandler(operation => { notifications.push(operation); });
    storage.setItemSync('item', 0);
    expect(JSON.parse(readFileSync(join(root, 'item'), 'utf8'))).toBe(0);
    expect(storage.getItem('item')).toBe(0);
    storage.removeItemSync('item');
    expect(storage.getItem('item')).toBeUndefined();
    expect(notifications).toEqual([]);
  });
});
