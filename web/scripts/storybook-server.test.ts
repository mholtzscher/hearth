import assert from "node:assert/strict";
import test from "node:test";
import { assertRouteAvailable, exactHost, ownsRoute, parseOptions, parsePort } from "./storybook-server.ts";
import { EventEmitter } from "node:events";
import { installStoryObserver, type StoryObserverScope } from "./storybook-observer.ts";

test("startup defaults and custom local/HTTPS ports stay separate", () => {
  assert.deepEqual(parseOptions([]), { port: 6006, httpsPort: 6606, tailscale: false });
  assert.deepEqual(parseOptions(["--tailscale", "--port", "6016", "--https-port", "6616"]), {
    port: 6016, httpsPort: 6616, tailscale: true,
  });
  assert.throws(() => parseOptions(["--tailscale", "--https-port", "6006"]), /different ports/);
  assert.throws(() => parseOptions(["--https-port", "6616"]), /requires --tailscale/);
  assert.throws(() => parseOptions(["--unknown"]), /Unknown option/);
});

test("ports and exact hostnames reject unsafe or ambiguous input", () => {
  for (const port of ["0", "80", "-1", "65536", "6006junk", "NaN"]) assert.throws(() => parsePort(port), /Invalid port/);
  assert.equal(exactHost("desktop.example.ts.net."), "desktop.example.ts.net");
  for (const host of ["*", ".ts.net", "https://desktop.ts.net", "desktop:6606", "foo/bar", ""]) {
    assert.throws(() => exactHost(host), /Invalid Storybook allowed hostname/);
  }
});

const config = { port: 6016, tailnet: { host: "desktop.example.ts.net", port: 6616 } };
const status = {
  TCP: { "6616": { HTTPS: true } },
  Web: { "desktop.example.ts.net:6616": { Handlers: { "/": { Proxy: "http://127.0.0.1:6016" } } } },
};

test("a preexisting listener is not adopted even if its target happens to match", () => {
  assert.throws(() => assertRouteAvailable(status, config, null), /already has a service/);
  assert.doesNotThrow(() => assertRouteAvailable({}, config, null));
  assert.doesNotThrow(() => assertRouteAvailable(status, config, config));
});

test("a retained route must be cleaned up before changing a stopped server's options", () => {
  assert.throws(() => assertRouteAvailable({}, { ...config, port: 6017 }, config), /storybook-stop/);
  assert.throws(() => assertRouteAvailable({}, { ...config, tailnet: { ...config.tailnet, port: 6617 } }, config), /storybook-stop/);
  assert.throws(() => assertRouteAvailable({}, { ...config, tailnet: null }, config), /storybook-stop/);
});

test("cleanup ownership requires the unchanged listener, target, and full path set", () => {
  assert.equal(ownsRoute(status, config), true);
  assert.equal(ownsRoute(status, { ...config, port: 6017 }), false);
  assert.equal(ownsRoute(status, { ...config, tailnet: null }), false);
  const changed = structuredClone(status);
  Object.assign(changed.Web["desktop.example.ts.net:6616"].Handlers, { "/other": { Proxy: "http://127.0.0.1:9090" } });
  assert.equal(ownsRoute(changed, config), false);
  assert.throws(() => assertRouteAvailable(changed, config, config), /already has a service/);
});

test("browser observer retains fast completion and rejects caught play or report failures", () => {
  const scope = {} as StoryObserverScope;
  installStoryObserver(scope);
  const channel = new EventEmitter();
  scope.__STORYBOOK_ADDONS_CHANNEL__ = channel;
  channel.emit("storyFinished", { status: "success" });
  assert.deepEqual(scope.__HEARTH_STORY_CHECK__, { status: "success", errors: [] });
  // Storybook can log a caught play exception without a pageerror or error display.
  channel.emit("playFunctionThrewException", { message: "assertion failed" });
  channel.emit("storyFinished", { status: "success" });
  assert.deepEqual(scope.__HEARTH_STORY_CHECK__.errors, ["playFunctionThrewException"]);
  channel.emit("storyFinished", { status: "error" });
  assert.equal(scope.__HEARTH_STORY_CHECK__.status, "error");
});
