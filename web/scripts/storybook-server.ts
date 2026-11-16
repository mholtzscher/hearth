import { execFileSync } from "node:child_process";
import { once } from "node:events";
import {
  accessSync,
  constants,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { createRequire } from "node:module";
import net from "node:net";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "../..");
const directory = path.join(root, ".data/storybook");
const logFile = path.join(directory, "server.log");
const opencodeConfigFile = path.join(root, ".opencode/opencode.json");

const command = (program: string, args: string[]) =>
  execFileSync(program, args, {
    cwd: root,
    encoding: "utf-8",
    stdio: ["ignore", "pipe", "pipe"],
    timeout: 60_000,
  });

export const parsePort = (value: string): number => {
  if (!/^\d+$/u.test(value) || Number(value) < 1024 || Number(value) > 65_535) {
    throw new Error(
      `Invalid port "${value}": use an integer from 1024 to 65535.`
    );
  }
  return Number(value);
};

export const resolvePort = (env: NodeJS.ProcessEnv = process.env): number => {
  const value = env.STORYBOOK_PORT;
  if (!value) {
    throw new Error(
      "STORYBOOK_PORT is not set. Run this through mise so [daemons.storybook] supplies the port."
    );
  }
  return parsePort(value);
};

const findChromium = (): string | undefined => {
  if (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH) {
    return process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
  }
  for (const dir of (process.env.PATH ?? "").split(path.delimiter)) {
    for (const name of ["chromium", "chromium-browser"]) {
      const executable = path.join(dir, name);
      try {
        accessSync(executable, constants.X_OK);
        return executable;
      } catch {
        /* Try the next PATH entry. */
      }
    }
  }
};

export const checkIndex = async (url: string) => {
  const response = await fetch(`${url}/index.json`, {
    signal: AbortSignal.timeout(2000),
  });
  if (!response.ok) {
    throw new Error(`${url}/index.json returned HTTP ${response.status}`);
  }
  const index = (await response.json()) as {
    entries?: Record<string, { type: string }>;
  };
  const story = Object.entries(index.entries ?? {}).find(
    ([, entry]) => entry.type === "story"
  );
  if (!story) {
    throw new Error(`${url} did not return a nonempty Storybook story index.`);
  }
  return story[0];
};

export const assertLocalPortAvailable = async (port: number) => {
  const server = net.createServer();
  server.listen(port, "127.0.0.1");
  try {
    await once(server, "listening");
  } catch {
    throw new Error(
      `Local port ${port} is occupied. Stop the other process or redeclare [daemons.storybook] in a local mise.local.toml; no process was stopped.`
    );
  }
  const closed = once(server, "close");
  server.close();
  await closed;
};

const storybookDaemon = () => {
  const daemons = JSON.parse(command("mise", ["daemons", "ls", "--json"])) as {
    name: string;
    pid: number | null;
    port: number | null;
  }[];
  return daemons.find((daemon) => daemon.name === "storybook") ?? null;
};

const localURL = (port: number) => `http://127.0.0.1:${port}`;

const opencodeConfig = (port: number) => ({
  $schema: "https://opencode.ai/config.json",
  mcp: {
    servers: {
      "hearth-storybook": {
        type: "remote",
        url: `${localURL(port)}/mcp`,
      },
    },
  },
});

export const writeOpencodeConfig = (
  port: number,
  file = opencodeConfigFile
) => {
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, `${JSON.stringify(opencodeConfig(port), null, 2)}\n`);
};

export const removeOpencodeConfig = (file = opencodeConfigFile) => {
  rmSync(file, { force: true });
};

const printResult = (port: number) => {
  console.log(`Local: ${localURL(port)}/`);
  console.log(`MCP: ${localURL(port)}/mcp`);
  console.log(`MCP config: ${path.relative(root, opencodeConfigFile)}`);
  console.log(`Log: ${logFile}`);
};

const start = async () => {
  if (!existsSync(path.join(root, "web/node_modules/storybook/package.json"))) {
    throw new Error(
      "Web dependencies are missing. Run mise run web-install once, then retry."
    );
  }
  const port = resolvePort();
  const daemon = storybookDaemon();
  const running = daemon !== null && daemon.pid !== null;
  if (running && daemon?.port !== port) {
    throw new Error(
      `This worktree's Storybook is running on ${daemon?.port ?? "an unknown port"}. Run storybook-stop first.`
    );
  }
  if (!running) {
    removeOpencodeConfig();
    await assertLocalPortAvailable(port);
    mkdirSync(directory, { recursive: true });
    writeFileSync(logFile, "");
  }
  try {
    if (!running) {
      command("mise", ["daemons", "start", "storybook", "--quiet"]);
    }
    await checkIndex(localURL(port));
    writeOpencodeConfig(port);
    printResult(port);
  } catch (error) {
    if (!running) {
      try {
        command("mise", ["daemons", "stop", "storybook"]);
      } catch (error_) {
        console.error(`Daemon cleanup failed: ${error_}`);
      }
      try {
        removeOpencodeConfig();
      } catch (error_) {
        console.error(`MCP config cleanup failed: ${error_}`);
      }
      try {
        console.error(
          readFileSync(logFile, "utf-8").split("\n").slice(-25).join("\n")
        );
      } catch {
        /* The server may have failed before opening its log. */
      }
    }
    throw error;
  }
};

const serve = () => {
  const port = resolvePort();
  const require = createRequire(import.meta.url);
  const packageFile = require.resolve("storybook/package.json");
  const { bin } = JSON.parse(readFileSync(packageFile, "utf-8"));
  const env: NodeJS.ProcessEnv = { ...process.env };
  const chromium = findChromium();
  if (chromium) {
    env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH = chromium;
  }
  // Replace the launcher, so pitchfork owns and signals the real Storybook process.
  if (!process.execve) {
    throw new Error(
      "Managed Storybook requires a Unix platform with Node execve support."
    );
  }
  process.chdir(path.join(root, "web"));
  process.execve(
    process.execPath,
    [
      process.execPath,
      path.resolve(path.dirname(packageFile), bin),
      "dev",
      "--host",
      "127.0.0.1",
      "--port",
      String(port),
      "--exact-port",
      "--ci",
      "--no-open",
      "--no-version-updates",
    ],
    Object.fromEntries(
      Object.entries(env).filter(
        (entry): entry is [string, string] => entry[1] !== undefined
      )
    )
  );
};

const main = async (action: string, args: string[]) => {
  switch (action) {
    case "start": {
      if (args.length > 0) {
        throw new Error(
          "start takes no options. Set the port in mise.toml or mise.local.toml."
        );
      }
      await start();
      break;
    }
    case "stop": {
      command("mise", ["daemons", "stop", "storybook"]);
      removeOpencodeConfig();
      console.log("Stopped this worktree's managed Storybook.");
      break;
    }
    case "ready": {
      await checkIndex(localURL(resolvePort()));
      break;
    }
    case "dev":
    case "serve": {
      serve();
      break;
    }
    default: {
      throw new Error("Use start, stop, ready, dev, or serve.");
    }
  }
};

if (process.argv[1] && path.resolve(process.argv[1]) === import.meta.filename) {
  try {
    await main(process.argv[2], process.argv.slice(3));
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.error(`storybook: ${message}`);
    if (process.argv[2] === "start") {
      console.error(`Log: ${logFile}`);
    }
    process.exitCode = 1;
  }
}
