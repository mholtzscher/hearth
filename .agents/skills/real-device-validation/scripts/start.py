#!/usr/bin/env python3
"""Start local real-device validation services without changing the shared server."""

import json
import os
from pathlib import Path
import re
import shlex
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[4]
EXIT_MARKER = "REAL_DEVICE_START_PROCESS_EXIT"
STATE_RELPATH = Path(".data/real-device-validation.json")
DASHBOARD_URL = "http://127.0.0.1:5173"
SERVE_HTTP_PORT = 8088
SERVE_PROXY_TARGET = DASHBOARD_URL
# The only mount this workflow creates or removes. `serve_route` accepts no
# other arrangement, so pinning `--set-path` on both the publish and the
# removal keeps a `tailscale serve` invocation from touching a sibling handler
# that another client added in the meantime.
SERVE_MOUNT = "/"
# Every message this script prints starts with this, so operators can tell which task failed.
FAULT = "real-device-start"


def run(*args):
    return subprocess.check_output(args, text=True, cwd=ROOT).strip()


def herdr(*args):
    return json.loads(run("herdr", *args))["result"]


def port_open(host, port):
    try:
        with socket.create_connection((host, port), timeout=3):
            return True
    except OSError:
        return False


def http_read(url):
    with urllib.request.urlopen(url, timeout=2) as response:
        return response.read()


def tailscale_json(*args, fault):
    """Run a read-only Tailscale command and parse its JSON output.

    A missing binary, a stopped daemon, and a rejected command all look like a
    broken tailnet to the caller, so each one fails closed with `fault` prefixed.
    """
    command = ("tailscale",) + args
    try:
        completed = subprocess.run(command, text=True, cwd=ROOT, capture_output=True)
    except OSError as error:
        raise RuntimeError(f"{fault}: {' '.join(command)} could not run: {error}")
    if completed.returncode != 0:
        detail = (completed.stderr or completed.stdout or "").strip().splitlines()
        raise RuntimeError(f"{fault}: {' '.join(command)} failed: {detail[0] if detail else 'no output'}")
    try:
        return json.loads(completed.stdout or "{}")
    except ValueError as error:
        raise RuntimeError(f"{fault}: {' '.join(command)} returned no JSON: {error}")


def tailscale_serve_status(fault):
    """Return the parsed `tailscale serve status --json` document."""
    return tailscale_json("serve", "status", "--json", fault=fault)


def tailscale_dns_name(fault):
    """Return this node's MagicDNS name, refusing to continue without a running tailnet."""
    status = tailscale_json("status", "--json", fault=fault)
    dns_name = ((status.get("Self") or {}).get("DNSName") or "").rstrip(".")
    if status.get("BackendState") != "Running" or not dns_name:
        raise RuntimeError(f"{fault}: Tailscale is not running (state {status.get('BackendState') or 'unknown'})")
    return dns_name


def tailscale_serve_command(*arguments):
    """Build a `tailscale serve` command for the dashboard's tailnet-only HTTP port."""
    return ["tailscale", "serve", "--bg", "--yes", f"--http={SERVE_HTTP_PORT}", *arguments]


def tailscale_serve(*arguments, fault):
    """Run `tailscale serve` for the dashboard port; `off` removes the route.

    Callers pass `--set-path SERVE_MOUNT` so the command can only add or remove
    this workflow's own root handler.
    """
    command = tailscale_serve_command(*arguments)
    try:
        subprocess.run(command, text=True, cwd=ROOT, capture_output=True, check=True)
    except OSError as error:
        raise RuntimeError(f"{fault}: {' '.join(command)} could not run: {error}")
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or error.stdout or "").strip().splitlines()
        raise RuntimeError(f"{fault}: {' '.join(command)} failed: {detail[0] if detail else 'no output'}")


def serve_config_claims_port(config, port):
    """Report whether one ServeConfig layer claims `port`.

    `tailscale serve status --json` nests ephemeral foreground sessions under a
    top-level `Foreground` map, each value a ServeConfig shaped like the
    document root (a foreground config never nests another `Foreground`). A
    claim is a `TCP` entry for the port or any `Web` host whose name ends in
    `:port`.
    """
    config = config or {}
    if str(port) in (config.get("TCP") or {}):
        return True
    return any(host.rsplit(":", 1)[-1] == str(port) for host in (config.get("Web") or {}))


def serve_route(status, port=SERVE_HTTP_PORT):
    """Describe the node-level HTTP Serve route that claims `port`.

    Serve routes on one port share a single HTTP server, so only an exact root
    proxy is safe to reuse or remove. `status` is a parsed
    `tailscale serve status --json` document; the result is one of:

    - `{"kind": "absent"}` when no Serve route claims the port.
    - `{"kind": "proxy", "host": ..., "proxy": ...}` when one host serves only
      the root path as a proxy to one target.
    - `{"kind": "conflict", "summary": ...}` for every other arrangement, which
      must never be overwritten because nothing here can tell whose it is.

    A foreground session that claims the port is always a conflict, even when
    it looks identical: its config lives only for that client's IPN bus session,
    so this workflow can neither adopt it nor end it.
    """
    status = status or {}
    foreground = {session: config for session, config in (status.get("Foreground") or {}).items()
                  if serve_config_claims_port(config, port)}
    if foreground:
        return {"kind": "conflict", "summary": json.dumps({"Foreground": foreground}, sort_keys=True)}
    tcp = (status.get("TCP") or {}).get(str(port)) or {}
    hosts = {host: config for host, config in (status.get("Web") or {}).items()
             if host.rsplit(":", 1)[-1] == str(port)}
    if not tcp and not hosts:
        return {"kind": "absent"}
    if len(hosts) != 1 or not tcp.get("HTTP"):
        return {"kind": "conflict", "summary": json.dumps(
            {"TCP": {str(port): tcp} if tcp else {}, "Web": hosts}, sort_keys=True)}
    host, config = next(iter(hosts.items()))
    handlers = config.get("Handlers") or {}
    root = handlers.get("/") or {}
    if len(handlers) == 1 and root.get("Proxy") and not root.get("Path") and not root.get("Text"):
        return {"kind": "proxy", "host": host.rsplit(":", 1)[0], "proxy": root["Proxy"]}
    return {"kind": "conflict", "summary": json.dumps(hosts, sort_keys=True)}


def serve_route_matches(route, host, proxy):
    """Report whether `route` is exactly the root proxy to `proxy` served by `host`."""
    return route["kind"] == "proxy" and route["host"] == host and route["proxy"] == proxy


def dashboard_serve_plan(fault):
    """Plan this run's tailnet dashboard route as `{"host", "url", "ownership"}`.

    `ownership` is the record cleanup needs when this run creates the route; it
    is None when an identical route already exists and must be reused untouched.
    Any other route on the port is refused rather than replaced.

    This is a preflight read, not an atomic guard. `tailscale serve` re-reads
    the config itself, so a route that lands after this read and before the
    publish command's own read is invisible here and cannot be preserved; the
    CLI's ETag/If-Match only rejects a change that arrives mid-command. There is
    no CLI flag that makes the write conditional on this inspected config
    (`tailscale debug localapi` cannot send If-Match), so port 8088 must stay
    owned by this workflow and operators must coordinate before serving it.
    """
    dns_name = tailscale_dns_name(fault)
    url = f"http://{dns_name}:{SERVE_HTTP_PORT}/"
    route = serve_route(tailscale_serve_status(fault))
    if route["kind"] == "conflict":
        raise RuntimeError(f"{fault}: tailscale serve port {SERVE_HTTP_PORT} already serves "
                           f"{route['summary']}; refusing to replace it")
    if serve_route_matches(route, dns_name, SERVE_PROXY_TARGET):
        return {"host": dns_name, "url": url, "ownership": None}
    if route["kind"] == "proxy":
        raise RuntimeError(f"{fault}: tailscale serve port {SERVE_HTTP_PORT} already maps "
                           f"{route['host']} to {route['proxy']}; refusing to replace it")
    return {"host": dns_name, "url": url,
            "ownership": {"host": dns_name, "proxy": SERVE_PROXY_TARGET, "url": url}}


def save_state(state):
    """Persist the environment record that cleanup reads."""
    (ROOT / STATE_RELPATH).write_text(json.dumps(state) + "\n")


def enable_serve_route(record, fault):
    """Publish the recorded dashboard route, then confirm it landed exactly.

    Pinning `--set-path SERVE_MOUNT` keeps the command from replacing a sibling
    handler, but it still replaces whatever occupies `/` on the port at the
    moment of the CLI's own read (see `dashboard_serve_plan`).
    """
    tailscale_serve("--set-path", SERVE_MOUNT, SERVE_PROXY_TARGET, fault=fault)
    if not serve_route_matches(serve_route(tailscale_serve_status(fault)), record["host"], record["proxy"]):
        raise RuntimeError(f"{fault}: tailscale serve did not publish {record['url']} exactly; "
                           f"inspect tailscale serve status")


def prepare_config(path, expected):
    run("git", "check-ignore", "-q", str(path))
    full_path = ROOT / path
    if full_path.exists():
        # Deliberately fail closed on custom YAML rather than guessing its meaning.
        if full_path.read_text().strip() != expected.strip():
            raise RuntimeError(f"real-device-start: config differs from expected template: {path}; review it before retrying")
    else:
        full_path.write_text(expected)


def wait_ready(label, check, panes=(), timeout=60):
    deadline = time.monotonic() + timeout
    next_logs = time.monotonic() + 5
    while time.monotonic() < deadline:
        if time.monotonic() >= next_logs:
            for pane in panes:
                output = run("herdr", "pane", "read", pane, "--source", "recent-unwrapped", "--lines", "120")
                if EXIT_MARKER in output.splitlines():
                    raise RuntimeError(f"real-device-start: {label} process exited\n{output}")
            next_logs = time.monotonic() + 5
        try:
            if check():
                return
        except (OSError, urllib.error.URLError, ValueError, KeyError):
            pass
        time.sleep(1)
    raise RuntimeError(f"real-device-start: {label} readiness timed out after {timeout}s")


def launch(pane, command):
    # Bash is explicit because Herdr panes may use Nushell. The marker is a
    # standalone output line only after the process exits, never its echoed command.
    wrapped = f"{command}; code=$?; printf '\\n{EXIT_MARKER}\\n'; exit $code"
    run("herdr", "pane", "run", pane, "bash -c " + shlex.quote(wrapped))


def adapter_ready(previous_runtime):
    health = json.loads(http_read("http://127.0.0.1:8080/v1/adapters/zigbee2mqtt"))["health"]
    runtime = health.get("runtime") or {}
    return (health["status"] == "healthy" and runtime.get("status") == "online"
            and bool(runtime.get("id")) and runtime["id"] != previous_runtime)


def start(host):
    started = time.monotonic()
    tab = None
    panes = []

    def stage(label):
        print(f"real-device-start: {label} elapsed={time.monotonic() - started:.1f}s", flush=True)

    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.-]*", host):
        raise RuntimeError("real-device-start: supply a hostname or IPv4 address, not a URL")
    if os.environ.get("HERDR_ENV") != "1":
        raise RuntimeError("real-device-start: run inside Herdr")
    if (ROOT / STATE_RELPATH).exists():
        raise RuntimeError("real-device-start: saved environment exists; run mise run real-device-stop first")
    for port in (8080, 5173):
        if port_open("127.0.0.1", port) or port_open("::1", port):
            raise RuntimeError(f"real-device-start: local port {port} is occupied; do not start competing services")
    if port_open(host, 8081):
        raise RuntimeError("real-device-start: remote core port 8081 answers; ask the operator before proceeding")
    http_read(f"http://{host}:8222/varz")
    http_read(f"http://{host}:8082/")
    for port in (4222, 1883):
        if not port_open(host, port):
            raise RuntimeError(f"real-device-start: {host}:{port} is unreachable")
    # Plan the tailnet route before any Herdr tab exists, so a missing Tailscale
    # or an occupied Serve port fails closed without leaving a tab to diagnose.
    dashboard_serve_plan(FAULT)
    stage("preflight passed")

    agent_api_key = Path.home() / ".local/share/agenix/hearth-openai-api-key"
    prepare_config(Path("configs/homelab-hearthd.yaml"),
                   f"http_addr: 127.0.0.1:8080\nnats_url: nats://{host}:4222\nsqlite_path: .data/homelab-hearthd.db\nhousehold_timezone: UTC\nagent:\n  api_key_file: {agent_api_key}\n  reasoning_effort: none\n")
    prepare_config(Path("configs/homelab-zigbee2mqtt.yaml"),
                   f"adapter_id: zigbee2mqtt\nnats_url: nats://{host}:4222\nmqtt:\n  url: tcp://{host}:1883\n  base_topic: zigbee2mqtt\n")
    run("git", "check-ignore", "-q", str(STATE_RELPATH))
    (ROOT / ".data").mkdir(exist_ok=True)
    if not (ROOT / "web/node_modules").is_dir():
        subprocess.run(["mise", "run", "web-install"], cwd=ROOT, check=True)
    stage("local files prepared")

    try:
        created = herdr("tab", "create", "--label", "real-device-validation", "--cwd", str(ROOT), "--no-focus")
        tab = created["tab"]["tab_id"]
        core = created["root_pane"]["pane_id"]
        panes.append(core)
        state = {"tab_id": tab, "root_pane_id": core, "worktree": str(ROOT),
                 "workspace_id": created["tab"]["workspace_id"]}
        save_state(state)
        print(f"real-device-start: tab={tab} core={core}", flush=True)
        launch(core, "go run ./cmd/hearthd -config configs/homelab-hearthd.yaml")
        wait_ready("core health", lambda: http_read("http://127.0.0.1:8080/healthz"), (core,))
        wait_ready("core readiness", lambda: http_read("http://127.0.0.1:8080/readyz"), (core,))
        stage("core ready")

        # A reused SQLite database may still expose the previous runtime's health.
        try:
            health = json.loads(http_read("http://127.0.0.1:8080/v1/adapters/zigbee2mqtt"))["health"]
            previous_runtime = (health.get("runtime") or {}).get("id")
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise
            previous_runtime = None

        adapter = herdr("pane", "split", "--pane", core, "--direction", "right", "--cwd", str(ROOT), "--no-focus")["pane"]["pane_id"]
        panes.append(adapter)
        launch(adapter, "go run ./cmd/hearth-adapter-zigbee2mqtt -config configs/homelab-zigbee2mqtt.yaml")
        dashboard = herdr("pane", "split", "--pane", adapter, "--direction", "down", "--cwd", str(ROOT), "--no-focus")["pane"]["pane_id"]
        panes.append(dashboard)
        launch(dashboard, f"NATS_MONITOR_URL=http://{host}:8222 mise run web-dev")
        print(f"real-device-start: adapter={adapter} dashboard={dashboard}", flush=True)

        wait_ready("adapter", lambda: adapter_ready(previous_runtime), (adapter, dashboard))
        stage("adapter healthy (new runtime)")
        print(run("herdr", "pane", "read", adapter, "--source", "recent-unwrapped", "--lines", "120"), flush=True)
        wait_ready("dashboard", lambda: http_read("http://127.0.0.1:5173/"), (dashboard,))
        wait_ready("API proxy", lambda: http_read("http://127.0.0.1:5173/readyz"), (core, dashboard))
        wait_ready("monitor proxy", lambda: http_read("http://127.0.0.1:5173/nats-monitor/varz"), (dashboard,))

        # Replan against the live Serve config: another client may have changed it
        # while the services compiled. Only this run's own route is ever published.
        plan = dashboard_serve_plan(FAULT)
        if plan["ownership"]:
            # Record ownership before mutating, so cleanup still removes a route
            # whose publish command failed after the Serve config changed.
            state["serve"] = plan["ownership"]
            save_state(state)
            enable_serve_route(plan["ownership"], FAULT)
            print(f"real-device-start: published tailnet route {plan['url']}", flush=True)
        else:
            print(f"real-device-start: reusing existing tailnet route {plan['url']}", flush=True)
        stage("all startup gates passed")
        print(f"Ready for validation: {DASHBOARD_URL}/ — tailnet {plan['url']} — tab {tab}", flush=True)
    except Exception:
        # Leave only this run's tab available for diagnosis; never kill other work.
        if tab:
            print(f"real-device-start: failed; inspect tab {tab}; cleanup: mise run real-device-stop (tab {tab})", file=sys.stderr)
            for pane in panes:
                try:
                    print(run("herdr", "pane", "read", pane, "--source", "recent-unwrapped", "--lines", "120"), file=sys.stderr)
                except subprocess.CalledProcessError:
                    pass
        raise


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: start.py HOST")
    try:
        start(sys.argv[1])
    except (RuntimeError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
