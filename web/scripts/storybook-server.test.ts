import { describe, expect, it } from "vitest";

import {
  assertRouteAvailable,
  exactHost,
  ownsRoute,
  parseOptions,
  parsePort,
} from "./storybook-server.ts";

describe("storybook server startup", () => {
  it("startup defaults and custom local/HTTPS ports stay separate", () => {
    expect(parseOptions([])).toStrictEqual({
      port: 6006,
      httpsPort: 6606,
      tailscale: false,
    });
    expect(
      parseOptions(["--tailscale", "--port", "6016", "--https-port", "6616"])
    ).toStrictEqual({
      port: 6016,
      httpsPort: 6616,
      tailscale: true,
    });
    expect(() => parseOptions(["--tailscale", "--https-port", "6006"])).toThrow(
      /different ports/u
    );
    expect(() => parseOptions(["--https-port", "6616"])).toThrow(
      /requires --tailscale/u
    );
    expect(() => parseOptions(["--unknown"])).toThrow(/Unknown option/u);
  });

  it("ports and exact hostnames reject unsafe or ambiguous input", () => {
    for (const port of ["0", "80", "-1", "65536", "6006junk", "NaN"]) {
      expect(() => parsePort(port)).toThrow(/Invalid port/u);
    }
    expect(exactHost("desktop.example.ts.net.")).toBe("desktop.example.ts.net");
    for (const host of [
      "*",
      ".ts.net",
      "https://desktop.ts.net",
      "desktop:6606",
      "foo/bar",
      "",
    ]) {
      expect(() => exactHost(host)).toThrow(
        /Invalid Storybook allowed hostname/u
      );
    }
  });
});

const config = {
  port: 6016,
  tailnet: { host: "desktop.example.ts.net", port: 6616 },
};
const status = {
  TCP: { "6616": { HTTPS: true } },
  Web: {
    "desktop.example.ts.net:6616": {
      Handlers: { "/": { Proxy: "http://127.0.0.1:6016" } },
    },
  },
};

describe("storybook route ownership", () => {
  it("a preexisting listener is not adopted even if its target happens to match", () => {
    expect(() => assertRouteAvailable(status, config, null)).toThrow(
      /already has a service/u
    );
    expect(() => assertRouteAvailable({}, config, null)).not.toThrow();
    expect(() => assertRouteAvailable(status, config, config)).not.toThrow();
  });

  it("a retained route must be cleaned up before changing a stopped server's options", () => {
    expect(() =>
      assertRouteAvailable({}, { ...config, port: 6017 }, config)
    ).toThrow(/storybook-stop/u);
    expect(() =>
      assertRouteAvailable(
        {},
        { ...config, tailnet: { ...config.tailnet, port: 6617 } },
        config
      )
    ).toThrow(/storybook-stop/u);
    expect(() =>
      assertRouteAvailable({}, { ...config, tailnet: null }, config)
    ).toThrow(/storybook-stop/u);
  });

  it("cleanup ownership requires the unchanged listener, target, and full path set", () => {
    expect(ownsRoute(status, config)).toBeTruthy();
    expect(ownsRoute(status, { ...config, port: 6017 })).toBeFalsy();
    expect(ownsRoute(status, { ...config, tailnet: null })).toBeFalsy();
    const changed = structuredClone(status);
    Object.assign(changed.Web["desktop.example.ts.net:6616"].Handlers, {
      "/other": { Proxy: "http://127.0.0.1:9090" },
    });
    expect(ownsRoute(changed, config)).toBeFalsy();
    expect(() => assertRouteAvailable(changed, config, config)).toThrow(
      /already has a service/u
    );
  });
});
