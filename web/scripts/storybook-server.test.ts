import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import {
  parsePort,
  removeOpencodeConfig,
  resolvePort,
  writeOpencodeConfig,
} from "./storybook-server.ts";

describe("storybook server startup", () => {
  it("uses the port mise assigns and rejects missing or unsafe values", () => {
    expect(resolvePort({ STORYBOOK_PORT: "6016" })).toBe(6016);
    expect(() => resolvePort({})).toThrow(/STORYBOOK_PORT.*mise/u);
    for (const port of ["0", "80", "-1", "65536", "6006junk", "NaN"]) {
      expect(() => parsePort(port)).toThrow(/Invalid port/u);
      expect(() => resolvePort({ STORYBOOK_PORT: port })).toThrow(
        /Invalid port/u
      );
    }
  });
});

describe("generated OpenCode config", () => {
  const directories: string[] = [];

  afterEach(() => {
    for (const directory of directories.splice(0)) {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it("publishes only this checkout's MCP endpoint and removes it with the server", () => {
    const directory = mkdtempSync(path.join(tmpdir(), "hearth-opencode-"));
    directories.push(directory);
    const file = path.join(directory, ".opencode/opencode.json");

    writeOpencodeConfig(6016, file);
    expect(readFileSync(file, "utf-8")).toBe(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "hearth-storybook": {
        "type": "remote",
        "url": "http://127.0.0.1:6016/mcp"
      }
    }
  }
}
`);

    writeOpencodeConfig(6026, file);
    expect(
      JSON.parse(readFileSync(file, "utf-8")).mcp.servers["hearth-storybook"]
        .url
    ).toBe("http://127.0.0.1:6026/mcp");

    removeOpencodeConfig(file);
    removeOpencodeConfig(file);
    expect(existsSync(file)).toBeFalsy();
  });
});
