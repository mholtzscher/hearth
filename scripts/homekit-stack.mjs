// Test support only: real, isolated executable boundaries, never household daemons.
import { spawn } from "node:child_process";
import { once } from "node:events";
import { createWriteStream } from "node:fs";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const activeStacks = new Set();
const reapOnExit = () => {
  for (const stack of activeStacks) stack.kill();
};
const stopOnSignal = signal => {
  Promise.allSettled([...activeStacks].map(stack => stack.close()))
    .finally(() => process.exit(signal === "SIGINT" ? 130 : 143));
};
const interrupt = () => stopOnSignal("SIGINT");
const terminate = () => stopOnSignal("SIGTERM");

function installReaper(stack) {
  if (activeStacks.size === 0) {
    process.on("exit", reapOnExit);
    process.on("SIGINT", interrupt);
    process.on("SIGTERM", terminate);
  }
  activeStacks.add(stack);
}

function removeReaper(stack) {
  activeStacks.delete(stack);
  if (activeStacks.size === 0) {
    process.removeListener("exit", reapOnExit);
    process.removeListener("SIGINT", interrupt);
    process.removeListener("SIGTERM", terminate);
  }
}

async function freePort() {
  const server = createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("no TCP port");
  const port = address.port;
  await new Promise((resolveClose, reject) => server.close(error => error ? reject(error) : resolveClose()));
  return port;
}

async function ready(url, child) {
  const deadline = performance.now() + 30_000;
  while (performance.now() < deadline) {
    if (child.fixtureSpawnFailed || child.exitCode !== null || child.signalCode !== null) {
      throw new Error(`${child.fixtureName} exited before readiness (exit=${child.exitCode}, signal=${child.signalCode}, spawn_failed=${!!child.fixtureSpawnFailed})`);
    }
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(500) });
      await response.arrayBuffer();
      if (response.ok) return;
    } catch { /* A fixture has not opened its listener yet. */ }
    await delay(50);
  }
  throw new Error("fixture readiness deadline");
}

/** Starts a completely private stack and returns joined stop/restart controls.
 * No real provider key or provider HTTP endpoint is used. Evidence stays private
 * under .data/ for failures, rather than copying State or pairing into git.
 */
export async function createHomeKitStack({ unknownPower = false } = {}) {
  await mkdir(join(root, ".data"), { recursive: true, mode: 0o700 });
  const directory = await mkdtemp(join(root, ".data/homekit-integration-"));
  const [natsPort, monitorPort, corePort, simulatorPort] = await Promise.all(
    Array.from({ length: 4 }, freePort),
  );
  const natsURL = `nats://127.0.0.1:${natsPort}`;
  const coreURL = `http://127.0.0.1:${corePort}`;
  const simulatorURL = `http://127.0.0.1:${simulatorPort}`;
  const processes = new Map();
  const fixtureEnv = Object.fromEntries(Object.entries(process.env).filter(
    ([key]) => !key.startsWith("HEARTHD_") && !key.startsWith("HEARTH_SIMULATOR_"),
  ));
  const keyPath = join(directory, "fixture-key");
  await writeFile(keyPath, "integration-placeholder-never-sent\n", { mode: 0o600 });
  const coreConfig = join(directory, "core.yaml");
  await writeFile(coreConfig, JSON.stringify({
    http_addr: `127.0.0.1:${corePort}`,
    nats_url: natsURL,
    sqlite_path: join(directory, "core.db"),
    household_timezone: "UTC",
    agent: { api_key_file: keyPath, base_url: "http://127.0.0.1:1/v1" },
  }), { mode: 0o600 });
  const simulatorConfig = join(directory, "simulator.yaml");
  const controllable = (key, type, support, initial) => ({
    key, name: key, type, support, initial,
    commands: { set: { behavior: "accept-and-publish", apply_parameters: true } },
  });
  const power = () => controllable("power", "hearth.power/v1", { state: {}, operations: { set: {} } }, false);
  await writeFile(simulatorConfig, JSON.stringify({
    nats_url: natsURL,
    control_addr: `127.0.0.1:${simulatorPort}`,
    adapters: [{
      adapter_id: "homekit-test",
      devices: [
        {
          binding_key: "lamp", name: "Integration lamp", kind: "light", health: "healthy",
          entities: [
            { ...power(), ...(unknownPower ? { available: false, availability_reason: "adapter.lamp.measurement_stale" } : {}) },
            controllable("brightness", "hearth.brightness/v1", {
            state: { maximum: 100 }, operations: { set: { step: 5 } },
          }, 83)],
        },
        { binding_key: "plug", name: "Integration plug", kind: "relay", health: "healthy", entities: [power()] },
      ],
    }],
  }), { mode: 0o600 });
  const natsConfig = join(directory, "nats.conf");
  await writeFile(natsConfig, [
    `listen: "127.0.0.1:${natsPort}"`,
    `http: "127.0.0.1:${monitorPort}"`,
    `jetstream { store_dir: ${JSON.stringify(join(directory, "nats"))} }`,
  ].join("\n"), { mode: 0o600 });

  function launch(name, command, args) {
    if (processes.has(name)) throw new Error("fixture already running");
    const log = createWriteStream(join(directory, `${name}.log`), { flags: "a", mode: 0o600 });
    const child = spawn(command, args, {
      cwd: root, env: fixtureEnv, stdio: ["ignore", "pipe", "pipe"],
    });
    child.fixtureName = name;
    child.fixtureLogFinished = new Promise(resolveLog => log.once("finish", resolveLog));
    child.stdout.pipe(log, { end: false });
    child.stderr.pipe(log, { end: false });
    // Capture spawn failures immediately, not as an unhandled EventEmitter error.
    child.once("error", () => { child.fixtureSpawnFailed = true; });
    child.once("close", () => log.end());
    processes.set(name, child);
    return child;
  }

  async function stop(name, signal = "SIGTERM") {
    const child = processes.get(name);
    if (!child) return;
    if (!child.fixtureSpawnFailed && child.exitCode === null && child.signalCode === null) {
      const closed = once(child, "close").catch(() => {});
      child.kill(signal);
      const killTimer = setTimeout(() => child.kill("SIGKILL"), 15_000);
      try { await closed; } finally { clearTimeout(killTimer); }
    }
    await child.fixtureLogFinished;
    processes.delete(name);
  }

  async function startNATS() {
    const child = launch("nats", "nats-server", ["-c", natsConfig]);
    await ready(`http://127.0.0.1:${monitorPort}/healthz?js-enabled-only=true`, child);
  }

  async function startCore() {
    const child = launch("core", join(root, ".data/homekit-bin/hearthd"), ["--config", coreConfig, "--log-format", "json"]);
    await ready(`${coreURL}/readyz`, child);
  }

  async function startSimulator() {
    const child = launch("simulator", join(root, ".data/homekit-bin/hearth-simulator"), [
      "--config", simulatorConfig, "--control-addr", `127.0.0.1:${simulatorPort}`, "--log-format", "json",
    ]);
    await ready(`${simulatorURL}/v1/sim/entities`, child);
  }

  const stack = {
    directory, natsURL, coreURL, simulatorURL,
    startNATS, startCore, startSimulator,
    stopNATS: () => stop("nats"),
    stopCore: () => stop("core"),
    stopSimulator: () => stop("simulator"),
    kill() {
      for (const child of processes.values()) child.kill("SIGKILL");
    },
    async close() {
      // Reverse dependency order. Every stopped process is joined before return.
      for (const name of ["simulator", "core", "nats"]) await stop(name);
      removeReaper(stack);
    },
  };
  installReaper(stack);
  try {
    await startNATS();
    await startCore();
    await startSimulator();
    return stack;
  } catch (error) {
    // Snapshot exits before teardown adds its own termination signals.
    const exits = [...processes].map(([name, child]) => ({
      name, exit: child.exitCode, signal: child.signalCode, spawnFailed: !!child.fixtureSpawnFailed,
    }));
    await stack.close();
    // Retain only machine event codes, never raw logs, State, names or secrets.
    const fixtures = await Promise.all(exits.map(async status => {
      const log = await readFile(join(directory, `${status.name}.log`), "utf8");
      const events = log.split("\n").flatMap(line => {
        try {
          const record = JSON.parse(line);
          const codes = Object.fromEntries(["event", "error_code", "stage"].flatMap(key =>
            typeof record?.[key] === "string" && /^[a-z][a-z0-9_.]{0,95}$/.test(record[key]) ? [[key, record[key]]] : []));
          return Object.keys(codes).length ? [codes] : [];
        } catch { return []; }
      }).slice(-32);
      return { ...status, addressInUse: log.includes("address already in use"), events };
    }));
    const verdict = { status: "startup_failed", fixtures };
    await writeFile(join(directory, "startup-verdict.json"), JSON.stringify(verdict), { mode: 0o600 });
    console.error(`HomeKit fixture startup verdict: ${JSON.stringify(verdict)}`);
    throw new Error(`HomeKit fixture startup failed; private evidence: ${directory}`, { cause: error });
  }
}
