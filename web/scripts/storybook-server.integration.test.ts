import { once } from "node:events";
import http from "node:http";

import { describe, expect, it } from "vitest";

import { assertLocalPortAvailable, checkIndex } from "./storybook-server.ts";

describe("storybook readiness", () => {
  it("readiness requires a successful nonempty story index, not merely an HTTP listener", async () => {
    let payload: unknown = { entries: {} };
    let status = 200;
    const server = http.createServer((request, response) => {
      expect(request.url).toBe("/index.json");
      response.writeHead(status, { "Content-Type": "application/json" });
      response.end(JSON.stringify(payload));
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const { port } = server.address() as { port: number };
    const url = `http://127.0.0.1:${port}`;
    try {
      await expect(assertLocalPortAvailable(port)).rejects.toThrow(
        /occupied.*no process was stopped/u
      );
      await expect(checkIndex(url)).rejects.toThrow(/nonempty Storybook/u);
      payload = { entries: { "button--basic": { type: "story" } } };
      await expect(checkIndex(url)).resolves.toBe("button--basic");
      status = 403;
      await expect(checkIndex(url)).rejects.toThrow(/HTTP 403/u);
    } finally {
      const closed = once(server, "close");
      server.close();
      await closed;
    }
    await assertLocalPortAvailable(port);
  });
});
