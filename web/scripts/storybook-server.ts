import { execFileSync } from "node:child_process";
import { accessSync, constants, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import net from "node:net";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

export type ServerConfig = {
  port: number;
  tailnet: { host: string; port: number } | null;
};
type ServeStatus = {
  TCP?: Record<string, unknown>;
  Web?: Record<string, { Handlers?: Record<string, { Proxy?: string }> }>;
};

const root = path.resolve(import.meta.dirname, "../..");
const directory = path.join(root, ".data/storybook");
const configFile = path.join(directory, "run.json");
const routeFile = path.join(directory, "route.json");
const logFile = path.join(directory, "server.log");

function command(program: string, args: string[]) {
  return execFileSync(program, args, { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 60_000 });
}

export function parsePort(value: string): number {
  if (!/^\d+$/.test(value) || Number(value) < 1024 || Number(value) > 65535) {
    throw new Error(`Invalid port "${value}": use an integer from 1024 to 65535.`);
  }
  return Number(value);
}

export function parseOptions(args: string[]) {
  const { values } = parseArgs({
    args,
    options: {
      tailscale: { type: "boolean", default: false },
      port: { type: "string", default: "6006" },
      "https-port": { type: "string", default: "6606" },
    },
  });
  const port = parsePort(values.port!);
  const httpsPort = parsePort(values["https-port"]!);
  if (values.tailscale && port === httpsPort) {
    throw new Error("Storybook and Tailscale must use different ports.");
  }
  if (!values.tailscale && values["https-port"] !== "6606") {
    throw new Error("--https-port requires --tailscale.");
  }
  return { port, httpsPort, tailscale: values.tailscale };
}

export function exactHost(host: string): string {
  const normalized = host.replace(/\.$/, "");
  if (!/^[a-z0-9]+(?:[.-][a-z0-9]+)*$/i.test(normalized)) {
    throw new Error(`Invalid Storybook allowed hostname: ${host}`);
  }
  return normalized;
}

function findChromium(): string | undefined {
  if (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH) return process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
  for (const dir of (process.env.PATH ?? "").split(path.delimiter)) {
    for (const name of ["chromium", "chromium-browser"]) {
      const executable = path.join(dir, name);
      try { accessSync(executable, constants.X_OK); return executable; } catch { /* Try the next PATH entry. */ }
    }
  }
}

function readJSON(file: string) {
  try { return JSON.parse(readFileSync(file, "utf8")); }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
    throw error;
  }
}

function readConfig(): ServerConfig {
  const config = readJSON(configFile);
  if (!config) throw new Error("No managed Storybook configuration. Run mise run storybook-start first.");
  parsePort(String(config.port));
  if (config.tailnet) {
    exactHost(config.tailnet.host);
    parsePort(String(config.tailnet.port));
    if (config.port === config.tailnet.port) throw new Error("Storybook and Tailscale must use different ports.");
  }
  return config;
}

function localURL(config: ServerConfig) { return `http://127.0.0.1:${config.port}`; }
function browserURL(config: ServerConfig) {
  return config.tailnet ? `https://${config.tailnet.host}:${config.tailnet.port}` : localURL(config);
}

// A route is ours only while the entire HTTPS listener still has the mapping we created.
export function ownsRoute(status: ServeStatus, config: ServerConfig): boolean {
  if (!config.tailnet) return false;
  const handlers = status.Web?.[`${config.tailnet.host}:${config.tailnet.port}`]?.Handlers;
  return !!handlers && Object.keys(handlers).length === 1 && handlers["/"]?.Proxy === localURL(config);
}

export function assertRouteAvailable(status: ServeStatus, config: ServerConfig, owned: ServerConfig | null) {
  if (owned && JSON.stringify(owned) !== JSON.stringify(config)) {
    throw new Error("An owned Tailscale route has different options. Run storybook-stop before changing startup options.");
  }
  if (!config.tailnet) return;
  if (status.TCP?.[config.tailnet.port] && !(owned && JSON.stringify(owned) === JSON.stringify(config) && ownsRoute(status, config))) {
    throw new Error(`Tailscale port ${config.tailnet.port} already has a service. Choose another --https-port; nothing was replaced.`);
  }
}

export async function checkIndex(url: string) {
  const response = await fetch(`${url}/index.json`, { signal: AbortSignal.timeout(2_000) });
  if (!response.ok) throw new Error(`${url}/index.json returned HTTP ${response.status}`);
  const index = await response.json() as { entries?: Record<string, { type: string }> };
  const story = Object.entries(index.entries ?? {}).find(([, entry]) => entry.type === "story");
  if (!story) throw new Error(`${url} did not return a nonempty Storybook story index.`);
  return story[0];
}

export async function assertLocalPortAvailable(port: number) {
  const server = net.createServer();
  await new Promise<void>((resolve, reject) => {
    server.once("error", () => reject(new Error(`Local port ${port} is occupied. Choose --port; no process was stopped.`)));
    server.listen(port, "127.0.0.1", () => server.close(() => resolve()));
  });
}

function daemonRunning() {
  const daemons = JSON.parse(command("mise", ["daemons", "ls", "--json"])) as { name: string; pid: number | null }[];
  return daemons.some(daemon => daemon.name === "storybook" && daemon.pid !== null);
}

function removeOwnedRoute() {
  const owned = readJSON(routeFile) as ServerConfig | null;
  if (!owned?.tailnet) return;
  const status = JSON.parse(command("tailscale", ["serve", "status", "--json"])) as ServeStatus;
  if (status.TCP?.[owned.tailnet.port]) {
    if (!ownsRoute(status, owned)) throw new Error("The managed Tailscale listener was changed externally; leaving it untouched. Inspect .data/storybook/route.json.");
    command("tailscale", ["serve", `--https=${owned.tailnet.port}`, "off"]);
  }
  rmSync(routeFile, { force: true });
}

function printResult(config: ServerConfig) {
  console.log(`Local: ${localURL(config)}/`);
  if (config.tailnet) console.log(`Tailnet: ${browserURL(config)}/`);
  console.log(`MCP: ${localURL(config)}/mcp`);
  console.log(`Log: ${logFile}`);
}

async function start(args: string[]) {
  const options = parseOptions(args);
  if (!existsSync(path.join(root, "web/node_modules/storybook/package.json"))) {
    throw new Error("Web dependencies are missing. Run mise run web-install once, then retry.");
  }
  const config: ServerConfig = { port: options.port, tailnet: null };
  let status: ServeStatus = {};
  const owned = readJSON(routeFile) as ServerConfig | null;
  if (options.tailscale) {
    const self = JSON.parse(command("tailscale", ["status", "--json"])).Self;
    if (!self?.DNSName) throw new Error("Tailscale has no local DNS name. Connect Tailscale first.");
    config.tailnet = { host: exactHost(self.DNSName), port: options.httpsPort };
    status = JSON.parse(command("tailscale", ["serve", "status", "--json"]));
  }
  assertRouteAvailable(status, config, owned);
  const running = daemonRunning();
  if (running && JSON.stringify(readConfig()) !== JSON.stringify(config)) {
    throw new Error("This worktree's Storybook is running with different options. Run storybook-stop first.");
  }
  if (!running) {
    await assertLocalPortAvailable(config.port);
    mkdirSync(directory, { recursive: true });
    writeFileSync(configFile, `${JSON.stringify(config, null, 2)}\n`);
    writeFileSync(logFile, "");
  }
  let routeCreated = false;
  try {
    if (!running) command("mise", ["daemons", "start", "storybook", "--quiet"]);
    await checkIndex(localURL(config));
    if (config.tailnet) {
      if (!ownsRoute(status, config)) {
        // Save ownership before dispatch so a failed command remains recoverable.
        writeFileSync(routeFile, `${JSON.stringify(config, null, 2)}\n`);
        routeCreated = true;
        command("tailscale", ["serve", "--bg", `--https=${config.tailnet.port}`, localURL(config)]);
      }
      await checkIndex(browserURL(config));
    }
    printResult(config);
  } catch (error) {
    if (!running) {
      try { command("mise", ["daemons", "stop", "storybook"]); }
      catch (cleanup) { console.error(`Daemon cleanup failed: ${cleanup}`); }
      try { console.error(readFileSync(logFile, "utf8").split("\n").slice(-25).join("\n")); }
      catch { /* The server may have failed before opening its log. */ }
    }
    if (routeCreated) {
      try { removeOwnedRoute(); }
      catch (cleanup) { console.error(`Route cleanup failed: ${cleanup}`); }
    }
    throw error;
  }
}

function serve() {
  const config = readConfig();
  const require = createRequire(import.meta.url);
  const packageFile = require.resolve("storybook/package.json");
  const bin = JSON.parse(readFileSync(packageFile, "utf8")).bin;
  const env: NodeJS.ProcessEnv = {
    ...process.env,
    HEARTH_STORYBOOK_ALLOWED_HOST: config.tailnet?.host ?? "",
    HEARTH_STORYBOOK_HTTPS_PORT: config.tailnet ? String(config.tailnet.port) : "",
  };
  const chromium = findChromium();
  if (chromium) env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH = chromium;
  // Replace the launcher, so pitchfork owns and signals the real Storybook process.
  if (!process.execve) throw new Error("Managed Storybook requires a Unix platform with Node execve support.");
  process.chdir(path.join(root, "web"));
  process.execve(process.execPath, [
    process.execPath, path.resolve(path.dirname(packageFile), bin), "dev",
    "--host", "127.0.0.1", "--port", String(config.port), "--exact-port",
    "--ci", "--no-open", "--no-version-updates",
  ], Object.fromEntries(Object.entries(env).filter((entry): entry is [string, string] => entry[1] !== undefined)));
}

async function main(action: string, args: string[]) {
  switch (action) {
    case "start": await start(args); break;
    case "stop":
      command("mise", ["daemons", "stop", "storybook"]);
      removeOwnedRoute();
      console.log("Stopped this worktree's managed Storybook.");
      break;
    case "ready": await checkIndex(localURL(readConfig())); break;
    case "serve": serve(); break;
    default: throw new Error("Use start [--tailscale] [--port 6006] [--https-port 6606], stop, or ready.");
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv[2], process.argv.slice(3)).catch(error => {
    console.error(`storybook: ${error.message}`);
    if (process.argv[2] === "start") {
      console.error(`Log: ${logFile}`);
    }
    process.exitCode = 1;
  });
}
