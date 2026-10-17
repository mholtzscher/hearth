/** Safe fixed reason codes are the only error text allowed across diagnostic boundaries. */
export class BoundaryError extends Error {
  constructor(readonly code: string) {
    super(code);
    this.name = 'BoundaryError';
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function object(value: unknown): Record<string, unknown> {
  if (!isRecord(value)) {
    throw new BoundaryError('invalid_object');
  }
  return value;
}

export function text(value: unknown): string {
  if (typeof value !== 'string') throw new BoundaryError('invalid_string');
  return value;
}

export function canonicalId(value: unknown, prefix: 'ent' | 'dev' | 'cmd' | 'obs'): string {
  const id = text(value);
  if (!new RegExp(`^${prefix}_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).test(id)) {
    throw new BoundaryError('invalid_id');
  }
  return id;
}

export function exactKeys(value: Record<string, unknown>, allowed: readonly string[]): void {
  if (Object.keys(value).some(key => !allowed.includes(key))) throw new BoundaryError('unknown_key');
}
