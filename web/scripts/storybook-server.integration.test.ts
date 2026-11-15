import assert from "node:assert/strict";
import http from "node:http";
import test from "node:test";
import { assertLocalPortAvailable, checkIndex } from "./storybook-server.ts";

test("readiness requires a successful nonempty story index, not merely an HTTP listener", async () => {
  let payload: unknown = { entries: {} };
  let status = 200;
  const server = http.createServer((request, response) => {
    assert.equal(request.url, "/index.json");
    response.writeHead(status, { "Content-Type": "application/json" });
    response.end(JSON.stringify(payload));
  });
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as { port: number }).port;
  const url = `http://127.0.0.1:${port}`;
  try {
    await assert.rejects(assertLocalPortAvailable(port), /occupied.*no process was stopped/);
    await assert.rejects(checkIndex(url), /nonempty Storybook/);
    payload = { entries: { "button--basic": { type: "story" } } };
    assert.equal(await checkIndex(url), "button--basic");
    status = 403;
    await assert.rejects(checkIndex(url), /HTTP 403/);
  } finally {
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  }
  await assertLocalPortAvailable(port);
});
