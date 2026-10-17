import { createServer } from 'node:http';
import type { ServerResponse } from 'node:http';
import { once } from 'node:events';
import { object } from '../../dist/boundary.js';

export type HeldCommand = { body: unknown; result: Record<string, unknown>; release(): void };

/** Real Core forwarding boundary. Controls network delivery, never Core outcomes. */
export async function createCoreProxy(coreURL: string) {
  const commands: HeldCommand[] = [];
  const readResults: { path: string; result: unknown }[] = [];
  const pending = new Set<ServerResponse>();
  let holdCommands = false;
  let holdReads = false;
  const replies: ({ status: number; body: string } | 'disconnect')[] = [];
  let transform: ((path: string, body: unknown) => unknown) | undefined;
  const reads: (() => void)[] = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const bytes = Buffer.concat(chunks);
    try {
      const upstream = await fetch(`${coreURL}${request.url}`, {
        method: request.method ?? 'GET', headers: { 'content-type': 'application/json' },
        ...(bytes.length ? { body: bytes } : {}), signal: AbortSignal.timeout(15_000),
      });
      const original = Buffer.from(await upstream.arrayBuffer());
      let result = original;
      let status = upstream.status;
      if (request.method === 'GET' && transform && upstream.ok) {
        const parsed: unknown = JSON.parse(original.toString());
        result = Buffer.from(JSON.stringify(transform(request.url ?? '', parsed)));
      }
      if (request.method === 'GET' && upstream.ok) {
        const parsed: unknown = JSON.parse(result.toString());
        readResults.push({ path: request.url ?? '', result: parsed });
      }
      const reply = request.method === 'POST' ? replies.shift() : undefined;
      if (reply && reply !== 'disconnect') { status = reply.status; result = Buffer.from(reply.body); }
      const deliver = () => { pending.delete(response); if (reply === 'disconnect') response.destroy(); else { response.writeHead(status, { 'content-type': 'application/json' }); response.end(result); } };
      if (request.method === 'POST') {
        const parsed: unknown = JSON.parse(original.toString());
        const body: unknown = JSON.parse(bytes.toString());
        commands.push({ body, result: object(parsed), release: deliver });
        if (holdCommands) pending.add(response); else deliver();
      } else if (holdReads && request.method === 'GET') { pending.add(response); reads.push(deliver); }
      else deliver();
    } catch { response.destroy(); }
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const address = server.address(); if (!address || typeof address === 'string') throw new Error('missing proxy port');
  return { url: `http://127.0.0.1:${address.port}`, commands, reads, readResults,
    holdCommands(value: boolean) { holdCommands = value; }, holdReads(value: boolean) { holdReads = value; },
    replyNextCommand(reply: { status: number; body: string } | 'disconnect') { replies.push(reply); },
    transformReads(value: ((path: string, body: unknown) => unknown) | undefined) { transform = value; },
    async close() { for (const response of pending) response.destroy(); server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve())); } };
}
