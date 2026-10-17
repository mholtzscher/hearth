import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { stringify } from 'yaml';
import { loadConfig } from '../../dist/config.js';

export const powerId = 'ent_01900000-0000-7000-8000-000000000001';
export const brightnessId = 'ent_01900000-0000-7000-8000-000000000002';
export const deviceId = 'dev_01900000-0000-7000-8000-000000000001';
export const observationId = 'obs_01900000-0000-7000-8000-000000000001';
export const code = '482-65-193'; // Public test-only code, never operator data.

export function fixture(overrides: Record<string, unknown> = {}) {
  const root = mkdtempSync(join(tmpdir(), 'hearth-homekit-test-'));
  const yaml = {
    bridge_id: 'fixture', data_dir: join(root, 'data'), pairing_code_file: join(root, 'code'),
    core_url: 'http://127.0.0.1:8080', nats_url: 'nats://127.0.0.1:4222', advertise_interfaces: ['eth0'],
    accessories: [{ key: 'lamp', name: 'Lamp', kind: 'light', power_entity: powerId }], ...overrides,
  };
  const file = join(root, 'config.yaml');
  writeFileSync(file, stringify(yaml), { mode: 0o600 });
  writeFileSync(join(root, 'code'), code, { mode: 0o600 });
  return { root, file, yaml, config: () => loadConfig(['--config', file], {}) };
}
