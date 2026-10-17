// A12 owner-boundary check: run the shipped image, not the checkout runtime.
import assert from "node:assert/strict";
import { spawn, execFile, execFileSync } from "node:child_process";
import { randomInt, randomUUID } from "node:crypto";
import { once } from "node:events";
import { createWriteStream } from "node:fs";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { createServer } from "node:net";
import { networkInterfaces } from "node:os";
import { dirname, join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createHomeKitStack } from "./homekit-stack.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const exec = promisify(execFile);
const { HttpClient } = createRequire(join(root, "bridges/homekit/package.json"))("hap-controller");
const image = process.env.HOMEKIT_IMAGE ?? "hearth-bridge-homekit:dev";
const name = `hearth-homekit-smoke-${randomUUID()}`;
const stack = await createHomeKitStack();
let active;
let controller;
let cleaned = false;
let phase = 'image-inspection';

async function bounded(promise, milliseconds) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("cleanup deadline")), milliseconds); }),
    ]);
  } finally { clearTimeout(timer); }
}

async function stopContainer() {
  try {
    await bounded(Promise.resolve(controller?.close()), 2000);
  } finally {
    controller = undefined;
    if (active) {
      const child = active;
      const joined = child.pid !== undefined && child.exitCode === null && child.signalCode === null
        ? new Promise(resolveClose => child.once("close", resolveClose)) : Promise.resolve();
      let removed = false;
      try {
        try {
          await exec("docker", ["stop", "--time", "15", name], { timeout: 20_000 });
        } catch {
          await exec("docker", ["rm", "--force", name], { timeout: 10_000 });
        }
        removed = true;
      } finally {
        try { await bounded(joined, 3000); }
        catch { child.kill("SIGKILL"); await bounded(joined, 3000); }
        // Keep failed-removal ownership for the exit reaper's last bounded attempt.
        if (removed) active = undefined;
      }
    }
  }
}

async function cleanup() {
  if (cleaned) return;
  cleaned = true;
  try { await stopContainer(); }
  finally { await stack.close(); }
}

const terminate = () => { void cleanup().finally(() => process.exit(143)); };
const interrupt = () => { void cleanup().finally(() => process.exit(130)); };
// If the test process exits unexpectedly, remove only its uniquely named container.
const emergency = () => {
  if (active) {
    try { execFileSync("docker", ["rm", "--force", name], { stdio: "ignore", timeout: 10_000 }); }
    catch { /* The daemon may be unavailable during host shutdown. */ }
  }
};
process.on("SIGTERM", terminate);
process.on("SIGINT", interrupt);
process.on("exit", emergency);

try {
  assert.notEqual(process.getuid(), 0, "container smoke must run as a nonroot host user");
  const inspected = JSON.parse((await exec("docker", ["image", "inspect", image])).stdout)[0];
  assert.equal(inspected.Config.User, "node", "image default is the unprivileged Node user");
  phase = 'configuration';
  const iface = Object.entries(networkInterfaces()).find(([, addresses]) => addresses?.some(address => !address.internal))?.[0];
  assert.ok(iface, "an addressed LAN interface is required");
  const listener = createServer().listen(0, "127.0.0.1");
  await once(listener, "listening");
  const port = listener.address().port;
  await new Promise(resolveClose => listener.close(resolveClose));
  const data = join(stack.directory, "bridge");
  await mkdir(data, { mode: 0o700 });
  let digits;
  do { digits = String(randomInt(20_000_000, 80_000_000)); } while (/^(\d)\1{7}$/.test(digits));
  const code = `${digits.slice(0, 3)}-${digits.slice(3, 5)}-${digits.slice(5)}`;
  const codeFile = join(stack.directory, "pairing-code");
  await writeFile(codeFile, `${code}\n`, { mode: 0o600 });
  const entities = await (await fetch(`${stack.simulatorURL}/v1/sim/entities`)).json();
  const entity = (binding, key) => {
    const found = entities.find(item => item.binding_key === binding && item.key === key);
    assert.ok(found, "fixture entity exists");
    return found.entity_id;
  };
  const power = entity("lamp", "power");
  const configFile = join(stack.directory, "bridge.yaml");
  await writeFile(configFile, JSON.stringify({
    bridge_id: "container-smoke", name: "Container smoke", data_dir: "/var/lib/hearth-homekit",
    pairing_code_file: "/run/secrets/homekit-code", hap_port: port,
    advertise_interfaces: [iface], core_url: stack.coreURL, nats_url: stack.natsURL,
    reconcile_interval: "1s",
    accessories: [
      { key: "lamp", name: "Lamp", kind: "light", power_entity: power, brightness_entity: entity("lamp", "brightness") },
      { key: "plug", name: "Plug", kind: "outlet", power_entity: entity("plug", "power") },
    ],
  }), { mode: 0o600 });

  async function start(initialize) {
    const log = createWriteStream(join(stack.directory, "container.log"), { flags: "a", mode: 0o600 });
    active = spawn("docker", [
      "run", "--rm", "--name", name, "--network", "host", "--read-only",
      "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--stop-timeout", "15",
      // Map the nonroot host UID so private bind mounts work on CI as well as UID1000 hosts.
      "--user", `${process.getuid()}:${process.getgid()}`,
      "-v", `${data}:/var/lib/hearth-homekit`,
      "-v", `${configFile}:/etc/hearth/homekit.yaml:ro`,
      "-v", `${codeFile}:/run/secrets/homekit-code:ro`,
      image, "--config", "/etc/hearth/homekit.yaml", ...(initialize ? ["--initialize"] : []),
    ], { stdio: ["ignore", "pipe", "pipe"] });
    active.stdout.pipe(log, { end: false });
    active.stderr.pipe(log, { end: false });
    active.once("close", () => log.end());
    let spawnFailed = false;
    active.once("error", () => { spawnFailed = true; });
    const end = performance.now() + 35_000;
    while (performance.now() < end) {
      if (spawnFailed || active.exitCode !== null || active.signalCode !== null) throw new Error("container failed startup");
      try {
        const manifest = JSON.parse(await readFile(join(data, "bridge.json"), "utf8"));
        const response = await fetch(`http://127.0.0.1:${port}/accessories`, { signal: AbortSignal.timeout(500) });
        const result = await response.json();
        if (manifest.stage === "initialized" && response.status === 470 && result.status === -70401) return manifest;
      } catch { /* Waiting for persisted initialization and the actual HAP socket. */ }
      await delay(100);
    }
    throw new Error("container readiness deadline");
  }

  phase = 'initial-start';
  const manifest = await start(true);
  phase = 'pairing';
  controller = new HttpClient(manifest.username, "127.0.0.1", port);
  await controller.pairSetup(code);
  const pairing = controller.getLongTermData();
  phase = 'inventory';
  const before = await controller.getAccessories();
  const identity = database => database.accessories.map(accessory => ({
    aid: accessory.aid,
    services: accessory.services.map(service => ({
      type: service.type, iid: service.iid,
      // HAP serializes asynchronous reads in completion order. Identity is the
      // exact AID/service-IID/characteristic-IID/type binding, not array position.
      characteristics: service.characteristics.map(characteristic => ({ type: characteristic.type, iid: characteristic.iid }))
        .sort((a, b) => a.iid - b.iid),
    })).sort((a, b) => a.iid - b.iid),
  })).sort((a, b) => a.aid - b.aid);
  assert.equal(before.accessories.length, 3, "bridge, dimmable light, and plug");
  const firstAccessory = before.accessories.find(accessory => accessory.aid !== 1);
  const on = firstAccessory.services.flatMap(service => service.characteristics).find(characteristic =>
    characteristic.type.toUpperCase() === "25" || characteristic.type.toUpperCase().startsWith("00000025-"));
  assert.ok(on, "light On characteristic exists");
  const key = `${firstAccessory.aid}.${on.iid}`;
  phase = 'command';
  await controller.setCharacteristics({ [key]: true });
  const state = await (await fetch(`${stack.coreURL}/v1/entities/${power}`)).json();
  assert.equal(state.state.value, true, "shipped image writes a real Core Command");
  phase = 'command-evidence';
  const commands = await (await fetch(`${stack.coreURL}/v1/entities/${power}/commands`)).json();
  assert.equal(commands.items.length, 1);
  assert.equal(commands.items[0].status, "satisfied");
  assert.equal(commands.items[0].outcome_observation_id, state.state.observation_id);
  phase = 'stop';
  await stopContainer();
  phase = 'restart';
  const restarted = await start(false);
  assert.equal(restarted.username, manifest.username);
  controller = new HttpClient(restarted.username, "127.0.0.1", port, pairing);
  phase = 'restart-inventory';
  assert.deepEqual(identity(await controller.getAccessories()), identity(before), "pairing and AID/IIDs survive image restart");
  phase = 'restart-command';
  await controller.setCharacteristics({ [key]: false });
  const after = await (await fetch(`${stack.coreURL}/v1/entities/${power}`)).json();
  assert.equal(after.state.value, false);
  await writeFile(join(stack.directory, "container-verdict.json"), JSON.stringify({
    status: "passed", image, checks: ["nonroot", "encrypted-pairing", "real-command", "persistent-restart", "stable-identifiers"],
  }), { mode: 0o600 });
  console.log("PASS HomeKit nonroot host-network image: encrypted pairing, real Commands, persistent restart, stable AID/IIDs");
} catch (error) {
  await writeFile(join(stack.directory, "container-verdict.json"), JSON.stringify({
    status: "failed", phase, reason: error instanceof assert.AssertionError ? error.message : "runtime_or_transport_failure",
  }), { mode: 0o600 });
  console.error(`FAIL HomeKit container smoke at ${phase}; private evidence: ${stack.directory}`);
  if (error instanceof assert.AssertionError) console.error(error.message);
  process.exitCode = 1;
} finally {
  await cleanup();
  process.off("SIGTERM", terminate);
  process.off("SIGINT", interrupt);
  process.off("exit", emergency);
}
