import { expect } from 'vitest';

/** Write-response values are optional; when supplied, they must be observed State. */
export function expectWriteSuccess(response: { statusCode: number; body: Buffer }, slot: { aid: number; iid: number }, value: number): void {
  if (response.statusCode === 204) {
    expect(response.body.length).toBe(0);
    return;
  }
  expect(response.statusCode).toBe(207);
  const status = { aid: slot.aid, iid: slot.iid, status: 0 };
  expect(JSON.parse(response.body.toString())).toBeOneOf([
    { characteristics: [status] },
    { characteristics: [{ ...status, value }] },
  ]);
}
