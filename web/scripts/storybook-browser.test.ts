import {
  chmodSync,
  mkdtempSync,
  mkdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { resolveChromium } from "./storybook-browser.ts";

const executable = (directory: string, name: string) => {
  const file = path.join(directory, name);
  writeFileSync(file, "");
  chmodSync(file, 0o755);
  return file;
};

describe("Storybook Chromium selection", () => {
  const directories: string[] = [];
  const fixture = () => {
    const directory = mkdtempSync(path.join(tmpdir(), "hearth-chromium-"));
    directories.push(directory);
    return directory;
  };
  afterEach(() => {
    for (const directory of directories.splice(0)) {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it.each([true, false])("honors an explicit override (NixOS: %s)", (nixOS) => {
    expect(
      resolveChromium(
        { PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH: "/custom/chrome" },
        nixOS
      )
    ).toBe("/custom/chrome");
  });

  it("keeps Playwright's bundled browser outside NixOS, even with Chromium on PATH", () => {
    const directory = fixture();
    executable(directory, "chromium");
    expect(resolveChromium({ PATH: directory }, false)).toBeUndefined();
  });

  it.each(["chromium", "chromium-browser"])(
    "discovers executable %s on NixOS without an override",
    (name) => {
      const directory = fixture();
      const file = executable(directory, name);
      expect(resolveChromium({ PATH: directory }, true)).toBe(file);
    }
  );

  it("searches in PATH order and skips missing or nonexecutable candidates", () => {
    const directory = fixture();
    const first = path.join(directory, "first");
    const second = path.join(directory, "second");
    mkdirSync(first);
    mkdirSync(second);
    const blocked = path.join(first, "chromium");
    writeFileSync(blocked, "");
    chmodSync(blocked, 0o644);
    const preferred = executable(first, "chromium-browser");
    executable(second, "chromium");
    expect(
      resolveChromium(
        {
          PATH: [path.join(directory, "missing"), first, second].join(
            path.delimiter
          ),
        },
        true
      )
    ).toBe(preferred);
  });

  it("leaves missing-browser reporting to Playwright when no executable is found", () => {
    expect(resolveChromium({ PATH: fixture() }, true)).toBeUndefined();
  });
});
