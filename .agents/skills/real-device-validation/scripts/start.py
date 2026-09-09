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
    if (ROOT / ".data/real-device-validation.json").exists():
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
    stage("preflight passed")

    prepare_config(Path("configs/homelab-hearthd.yaml"),
                   f"http_addr: 127.0.0.1:8080\nnats_url: nats://{host}:4222\nsqlite_path: .data/homelab-hearthd.db\nhousehold_timezone: UTC\n")
    prepare_config(Path("configs/homelab-zigbee2mqtt.yaml"),
                   f"adapter_id: zigbee2mqtt\nnats_url: nats://{host}:4222\nmqtt:\n  url: tcp://{host}:1883\n  base_topic: zigbee2mqtt\n")
    run("git", "check-ignore", "-q", ".data/real-device-validation.json")
    (ROOT / ".data").mkdir(exist_ok=True)
    if not (ROOT / "web/node_modules").is_dir():
        subprocess.run(["mise", "run", "web-install"], cwd=ROOT, check=True)
    stage("local files prepared")

    try:
        created = herdr("tab", "create", "--label", "real-device-validation", "--cwd", str(ROOT), "--no-focus")
        tab = created["tab"]["tab_id"]
        core = created["root_pane"]["pane_id"]
        panes.append(core)
        (ROOT / ".data/real-device-validation.json").write_text(json.dumps({
            "tab_id": tab, "root_pane_id": core, "worktree": str(ROOT),
            "workspace_id": created["tab"]["workspace_id"],
        }) + "\n")
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
        stage("all startup gates passed")
        print(f"Ready for validation: http://127.0.0.1:5173/ — tab {tab}", flush=True)
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
