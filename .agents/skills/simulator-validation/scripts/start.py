#!/usr/bin/env python3
"""Start a loopback-only simulator validation stack in its own Herdr tab.

This workflow owns everything it starts: a fresh NATS/JetStream on loopback, a
Core process with its own SQLite file, and a scripted simulator. It never
touches the shared homelab, Zigbee2MQTT, Mosquitto, Docker, or the tailnet.
"""

import argparse
from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import secrets
import shlex
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[4]
FAULT = "simulator-start"
STATE_RELPATH = Path(".data/simulator-validation.json")
RUN_DIR_PREFIX = "simulator-validation."
TAB_LABEL_PREFIX = "simulator-validation"

ADAPTER_ID = "simulator"
NATS_URL = "nats://127.0.0.1:4222"
CONTROL_ADDR = "127.0.0.1:8181"
NATS_PORT = 4222
NATS_WEBSOCKET_PORT = 4223
NATS_MONITOR_PORT = 8222
CORE_PORT = 8080
CONTROL_PORT = 8181
DASHBOARD_PORT = 5173

NATS_HEALTH_URL = "http://127.0.0.1:8222/healthz?js-enabled-only=true"
CORE_URL = "http://127.0.0.1:8080"
SIM_URL = "http://127.0.0.1:8181"
DASHBOARD_URL = "http://127.0.0.1:5173"
READINESS_TIMEOUT = 120

# One unique marker per service, printed as a standalone line only after the
# process exits, so readiness can tell a dead service from a slow one.
EXIT_MARKERS = {
    "nats": "SIMULATOR_VALIDATION_NATS_EXITED",
    "core": "SIMULATOR_VALIDATION_CORE_EXITED",
    "simulator": "SIMULATOR_VALIDATION_SIMULATOR_EXITED",
    "dashboard": "SIMULATOR_VALIDATION_DASHBOARD_EXITED",
}


def run(*args):
    return subprocess.check_output(args, text=True, cwd=ROOT).strip()


def herdr(*args):
    return json.loads(run("herdr", *args))["result"]


def session_socket():
    """Return the Herdr session socket this process is attached to, if any."""
    return os.environ.get("HERDR_SOCKET_PATH") or None


def current_time():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def port_open(host, port):
    try:
        with socket.create_connection((host, port), timeout=3):
            return True
    except OSError:
        return False


def read_http(url, timeout=2):
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return response.read()


def read_json(url, timeout=2):
    return json.loads(read_http(url, timeout=timeout))


# --- config generation -----------------------------------------------------


def extract_devices_block(text):
    """Return the indented `devices:` body from a trusted checked-in preset.

    Only checked-in presets are read this way; operator-supplied device files
    never pass through block extraction.
    """
    lines = text.splitlines()
    start = next((index for index, line in enumerate(lines) if line.rstrip() == "devices:"), None)
    if start is None:
        raise RuntimeError(f"{FAULT}: trusted simulator preset has no devices block")
    block = []
    for line in lines[start + 1:]:
        # A non-blank line at column zero starts the next top-level key or comment.
        if line.strip() and not line[:1].isspace():
            break
        block.append(line)
    if not any(line.strip() for line in block):
        raise RuntimeError(f"{FAULT}: trusted simulator preset has an empty devices block")
    return "\n".join(block) + "\n"


def indent_devices_block(text):
    """Indent a custom YAML sequence of Device specs to sit beneath `devices:`.

    The operator file must be a bare sequence, not a whole config, so generated
    transport lines can never be shadowed or overridden. The bytes are indented
    verbatim; there is no YAML parser and no top-level key/value extraction.
    """
    lines = text.splitlines()
    if not any(line.strip() and not line.lstrip().startswith("#") for line in lines):
        raise RuntimeError(f"{FAULT}: custom devices file is empty")
    for line in lines:
        stripped = line.lstrip()
        if not stripped or stripped.startswith("#"):
            continue
        if not line[:1].isspace() and not stripped.startswith("-"):
            raise RuntimeError(
                f"{FAULT}: custom devices file must be a YAML sequence of Device specs, "
                f"not a full config (unexpected top-level line {stripped!r})")
    return "".join(("  " + line if line.strip() else "") + "\n" for line in lines)


def load_devices_block(preset, devices_path):
    """Return the devices block for a trusted preset name or a custom sequence file."""
    if devices_path is not None:
        return indent_devices_block(Path(devices_path).read_text())
    preset_path = ROOT / "configs" / f"simulator.{preset}.example.yaml"
    return extract_devices_block(preset_path.read_text())


def simulator_config_text(devices_block):
    """Prefix trusted local transport lines to an already-indented devices block."""
    return (
        f"adapter_id: {ADAPTER_ID}\n"
        f"nats_url: {NATS_URL}\n"
        f"control_addr: {CONTROL_ADDR}\n"
        "devices:\n"
        f"{devices_block}"
    )


def nats_config_text(run_dir):
    """Render this run's loopback NATS config with its own JetStream store."""
    return (
        f"listen: 127.0.0.1:{NATS_PORT}\n"
        "jetstream {\n"
        f'  store_dir: "{run_dir}/nats"\n'
        "}\n"
        "websocket {\n"
        f"  listen: 127.0.0.1:{NATS_WEBSOCKET_PORT}\n"
        "  no_tls: true\n"
        "}\n"
        f"http: 127.0.0.1:{NATS_MONITOR_PORT}\n"
    )


def hearthd_config_text(run_dir):
    """Render this run's loopback Core config with its own SQLite file and UTC."""
    return (
        f"http_addr: 127.0.0.1:{CORE_PORT}\n"
        f"nats_url: {NATS_URL}\n"
        f"sqlite_path: {run_dir}/hearthd.db\n"
        "household_timezone: UTC\n"
    )


def service_commands(run_dir, dashboard):
    """Return the per-service foreground commands, reusable to restart in place."""
    commands = {
        "nats": f"mise exec -- nats-server -c {shlex.quote(str(run_dir / 'nats.conf'))}",
        "core": (
            "mise exec -- go run ./cmd/hearthd "
            f"-config {shlex.quote(str(run_dir / 'hearthd.yaml'))} --log-format json --log-level debug"
        ),
        "simulator": (
            "mise exec -- go run ./cmd/hearth-simulator "
            f"-config {shlex.quote(str(run_dir / 'simulator.yaml'))} --log-format json --log-level debug"
        ),
    }
    if dashboard:
        commands["dashboard"] = (
            f"HEARTHD_URL={CORE_URL} NATS_MONITOR_URL=http://127.0.0.1:{NATS_MONITOR_PORT} "
            "mise run web-dev"
        )
    return commands


# --- readiness -------------------------------------------------------------


def nats_jetstream_ready():
    """NATS answers its health check with JetStream enabled."""
    body = read_http(NATS_HEALTH_URL)
    if not body.strip():
        return False
    return json.loads(body).get("status") == "ok"


def core_ready():
    read_http(CORE_URL + "/readyz")
    return True


def adapter_runtime_online(adapter_body):
    """Core records an online runtime, regardless of Adapter health status.

    An intentionally unhealthy Adapter (for example the full preset's broken
    Device) is a valid scenario, so only runtime presence and online status are
    required here.
    """
    runtime = (adapter_body.get("health") or {}).get("runtime") or {}
    return bool(runtime.get("id")) and runtime.get("status") == "online"


def core_adapter_runtime_online():
    return adapter_runtime_online(read_json(f"{CORE_URL}/v1/adapters/{ADAPTER_ID}"))


def simulator_ready():
    """The control inventory is non-empty and Core has an online Adapter runtime."""
    inventory = read_json(SIM_URL + "/v1/sim/entities")
    return isinstance(inventory, list) and len(inventory) > 0 and core_adapter_runtime_online()


def dashboard_ready():
    """The dashboard serves and both of its dev proxies reach the local stack."""
    read_http(DASHBOARD_URL + "/")
    read_http(DASHBOARD_URL + "/readyz")
    read_http(DASHBOARD_URL + "/nats-monitor/varz")
    return True


def log_tail(path, lines=20):
    try:
        content = Path(path).read_text(errors="replace").splitlines()
    except OSError:
        return ""
    return "\n".join(content[-lines:])


def readiness_diagnostics(run_dir):
    """Collect stdout/stderr tails so a timed-out gate names the failing service."""
    parts = []
    for service in EXIT_MARKERS:
        for suffix in ("out", "err"):
            path = Path(run_dir) / f"{service}.{suffix}"
            tail = log_tail(path)
            if tail:
                parts.append(f"--- {path.name} ---\n{tail}")
    return "\n".join(parts)


def wait_ready(label, check, panes=(), timeout=READINESS_TIMEOUT, run_dir=None):
    """Poll `check` until it passes, failing early when an owned service exits.

    `panes` is a sequence of (pane_id, exit_marker) pairs. A dead service is
    detected from its pane output even while the HTTP check is failing, and a
    timeout reports the last check error plus service log tails.
    """
    deadline = time.monotonic() + timeout
    next_exit_check = time.monotonic()
    last_error = None
    while time.monotonic() < deadline:
        if time.monotonic() >= next_exit_check:
            for pane, marker in panes:
                output = run("herdr", "pane", "read", pane, "--source", "recent-unwrapped", "--lines", "120")
                if marker in output.splitlines():
                    raise RuntimeError(f"{FAULT}: {label} process exited\n{output}")
            next_exit_check = time.monotonic() + 5
        try:
            if check():
                return
        except (OSError, urllib.error.URLError, ValueError, KeyError) as error:
            last_error = error
        time.sleep(1)
    detail = readiness_diagnostics(run_dir) if run_dir is not None else ""
    raise RuntimeError(
        f"{FAULT}: {label} readiness timed out after {timeout}s; last error: {last_error}\n{detail}")


# --- preflight -------------------------------------------------------------


def check_herdr_context():
    """Require an explicit Herdr workspace so tab creation never targets the UI."""
    if os.environ.get("HERDR_ENV") != "1":
        raise RuntimeError(f"{FAULT}: run inside Herdr")
    if not (os.environ.get("HERDR_WORKSPACE_ID") or "").strip():
        raise RuntimeError(f"{FAULT}: HERDR_WORKSPACE_ID is required to target one explicit Herdr workspace")


def workspace_id():
    return os.environ["HERDR_WORKSPACE_ID"].strip()


def required_ports(dashboard):
    ports = [NATS_PORT, NATS_WEBSOCKET_PORT, NATS_MONITOR_PORT, CORE_PORT, CONTROL_PORT]
    if dashboard:
        ports.append(DASHBOARD_PORT)
    return ports


def check_ports_free(dashboard):
    """Every default port must be free on both loopback families before any launch."""
    for port in required_ports(dashboard):
        for host in ("127.0.0.1", "::1"):
            if port_open(host, port):
                raise RuntimeError(
                    f"{FAULT}: loopback port {port} is already occupied on {host}; "
                    "do not start competing services")


def check_required_tools(dashboard):
    """Resolve every needed tool through mise before creating a tab."""
    tools = ["nats-server", "go"]
    if dashboard:
        tools.append("pnpm")
    for tool in tools:
        try:
            run("mise", "which", tool)
        except (subprocess.CalledProcessError, OSError) as error:
            raise RuntimeError(f"{FAULT}: required tool {tool!r} is unavailable through mise: {error}")


def check_ignored_paths():
    """Fail closed unless the run directory and record are gitignored."""
    for path in (STATE_RELPATH, Path(".data") / (RUN_DIR_PREFIX + "example")):
        run("git", "check-ignore", "-q", str(path))


def validate_simulator_config(config_text):
    """Reject an invalid Device list before any run directory or tab exists.

    The simulator binary validates the generated configuration against the
    authoritative Entity-type schemas, so a bad --devices file fails here with
    its real reason instead of after NATS and Core are already running.
    """
    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as handle:
        handle.write(config_text)
        config_path = Path(handle.name)
    try:
        result = subprocess.run(
            ["mise", "exec", "--", "go", "run", "./cmd/hearth-simulator",
             "-config", str(config_path), "-validate-config"],
            cwd=ROOT, text=True, capture_output=True, timeout=READINESS_TIMEOUT)
    except subprocess.TimeoutExpired as error:
        raise RuntimeError(
            f"{FAULT}: simulator config validation timed out after {READINESS_TIMEOUT}s") from error
    finally:
        config_path.unlink(missing_ok=True)
    if result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip()
        raise RuntimeError(f"{FAULT}: simulator config is invalid:\n{detail}")


def make_run_dir():
    """Create a unique ignored run directory under .data."""
    data_dir = ROOT / ".data"
    data_dir.mkdir(parents=True, exist_ok=True)
    return Path(tempfile.mkdtemp(prefix=RUN_DIR_PREFIX, dir=data_dir))


# --- ownership record ------------------------------------------------------


def state_path():
    return ROOT / STATE_RELPATH


@contextmanager
def lifecycle_lock():
    """Serialize startup/cleanup, including pre-tab reservations.

    The lock file persists; unlinking it would let contenders lock different
    inodes. Kernel ownership ends automatically if a launcher crashes.
    """
    path = state_path().with_suffix(".lock")
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a") as handle:
        try:
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError("simulator lifecycle operation is active; wait for it to finish before retrying")
        try:
            yield
        finally:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


def read_state():
    path = state_path()
    if not path.exists():
        return None
    return json.loads(path.read_text())


def write_state_atomic(record):
    """Replace the ownership record atomically so a crash never truncates it."""
    path = state_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(record) + "\n")
    os.replace(temporary, path)


def reserve_state(record):
    """Create the ownership record exclusively so two starts cannot both own a stack."""
    path = state_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o644)
    with os.fdopen(descriptor, "w") as handle:
        handle.write(json.dumps(record) + "\n")


def clear_state():
    state_path().unlink(missing_ok=True)


def record_pane(record, service, pane_id):
    """Persist a freshly created pane before its service is launched."""
    record["panes"][service] = pane_id
    if pane_id not in record["pane_ids"]:
        record["pane_ids"].append(pane_id)
    write_state_atomic(record)


# --- launch ----------------------------------------------------------------


def launch(pane, service, command, run_dir):
    """Run one service as an explicit Bash wrapper with its own exit marker.

    Bash is explicit because Herdr panes may use Nushell, and the marker is a
    standalone line only after the process exits, never its echoed command.
    """
    marker = EXIT_MARKERS[service]
    out_path = shlex.quote(str(Path(run_dir) / f"{service}.out"))
    err_path = shlex.quote(str(Path(run_dir) / f"{service}.err"))
    wrapped = f"{command} >{out_path} 2>{err_path}; code=$?; printf '\\n{marker}\\n'; exit $code"
    run("herdr", "pane", "run", pane, "bash -c " + shlex.quote(wrapped))


def restart_instructions(record):
    """Describe the in-place restart command for the restarted-by-hand services."""
    parts = []
    for service in ("core", "simulator"):
        pane = record.get("panes", {}).get(service)
        command = record.get("commands", {}).get(service)
        if pane and command:
            parts.append(f"{service} pane {pane}: {command}")
    return " — ".join(parts)


# --- start -----------------------------------------------------------------


def start(preset, devices_path, dashboard):
    """Start the owned stack without racing another startup or cleanup."""
    check_herdr_context()
    with lifecycle_lock():
        return start_owned_stack(preset, devices_path, dashboard)


def start_owned_stack(preset, devices_path, dashboard):
    """Record ownership before each service launch while holding lifecycle_lock."""
    started = time.monotonic()
    tab_id = None
    record = None

    def stage(label):
        print(f"{FAULT}: {label} elapsed={time.monotonic() - started:.1f}s", flush=True)

    check_herdr_context()
    if state_path().exists():
        raise RuntimeError(f"{FAULT}: ownership record {STATE_RELPATH} exists; run mise run simulator-stop first")
    check_ignored_paths()
    reserved = {"version": 1, "status": "reserved", "worktree": str(ROOT),
                "session_socket": session_socket(), "created_at": current_time()}
    try:
        reserve_state(reserved)
    except FileExistsError:
        raise RuntimeError(
            f"{FAULT}: another start reserved {STATE_RELPATH}; run mise run simulator-stop first")
    try:
        check_ports_free(dashboard)
        check_required_tools(dashboard)
        devices_block = load_devices_block(preset, devices_path)
        simulator_text = simulator_config_text(devices_block)
        validate_simulator_config(simulator_text)
        run_dir = make_run_dir()
        (run_dir / "nats.conf").write_text(nats_config_text(run_dir))
        (run_dir / "hearthd.yaml").write_text(hearthd_config_text(run_dir))
        (run_dir / "simulator.yaml").write_text(simulator_text)
        commands = service_commands(run_dir, dashboard)
        if dashboard and not (ROOT / "web/node_modules").is_dir():
            run("mise", "run", "web-install")
        stage("preflight and configs ready")

        label = f"{TAB_LABEL_PREFIX}-{secrets.token_hex(4)}"
        created = herdr("tab", "create", "--workspace", workspace_id(), "--label", label,
                        "--cwd", str(ROOT), "--no-focus")
        tab_id = created["tab"]["tab_id"]
        record = {
            "version": 1, "status": "running", "worktree": str(ROOT),
            "session_socket": session_socket(), "workspace_id": created["tab"]["workspace_id"],
            "tab_id": tab_id, "tab_label": label, "run_dir": str(run_dir),
            "mode": "custom" if devices_path else preset,
            "devices_path": str(Path(devices_path).resolve()) if devices_path else None,
            "dashboard": bool(dashboard), "adapter_id": ADAPTER_ID, "panes": {}, "pane_ids": [],
            "root_pane_id": created["root_pane"]["pane_id"], "commands": commands,
            "created_at": current_time(),
        }
        record_pane(record, "nats", record["root_pane_id"])
        stage("tab created")

        launch(record["panes"]["nats"], "nats", commands["nats"], run_dir)
        wait_ready("NATS JetStream", nats_jetstream_ready,
                   [(record["panes"]["nats"], EXIT_MARKERS["nats"])], run_dir=run_dir)
        stage("NATS JetStream ready")

        core_pane = herdr("pane", "split", "--pane", record["root_pane_id"], "--direction", "right",
                          "--cwd", str(ROOT), "--no-focus")["pane"]["pane_id"]
        record_pane(record, "core", core_pane)
        launch(core_pane, "core", commands["core"], run_dir)
        wait_ready("Core readiness", core_ready, [(core_pane, EXIT_MARKERS["core"])], run_dir=run_dir)
        stage("Core ready")

        simulator_pane = herdr("pane", "split", "--pane", core_pane, "--direction", "down",
                               "--cwd", str(ROOT), "--no-focus")["pane"]["pane_id"]
        record_pane(record, "simulator", simulator_pane)
        launch(simulator_pane, "simulator", commands["simulator"], run_dir)
        wait_ready("simulator registration", simulator_ready,
                   [(core_pane, EXIT_MARKERS["core"]), (simulator_pane, EXIT_MARKERS["simulator"])],
                   run_dir=run_dir)
        stage("simulator registered")

        if dashboard:
            dashboard_pane = herdr("pane", "split", "--pane", simulator_pane, "--direction", "down",
                                   "--cwd", str(ROOT), "--no-focus")["pane"]["pane_id"]
            record_pane(record, "dashboard", dashboard_pane)
            launch(dashboard_pane, "dashboard", commands["dashboard"], run_dir)
            wait_ready("dashboard", dashboard_ready,
                       [(dashboard_pane, EXIT_MARKERS["dashboard"]), (core_pane, EXIT_MARKERS["core"])],
                       run_dir=run_dir)
            stage("dashboard ready")

        # Short, separate lines survive agent output compression; long joined
        # summaries hid the run directory during real skill evaluations.
        print("Ready for validation:", flush=True)
        print(f"Core: {CORE_URL}", flush=True)
        print(f"Simulator control: {SIM_URL}", flush=True)
        if dashboard:
            print(f"Dashboard: {DASHBOARD_URL}/", flush=True)
        print(f"Tab: {tab_id} ({record['tab_label']})", flush=True)
        print(f"Run directory: {run_dir.relative_to(ROOT)}", flush=True)
        print("Configs in run directory: nats.conf, hearthd.yaml, simulator.yaml", flush=True)
        print("Logs in run directory: <service>.out and <service>.err", flush=True)
        print(f"In-place restart: commands in {STATE_RELPATH}", flush=True)
        for service in ("core", "simulator"):
            print(f"{service} pane: {record['panes'][service]}", flush=True)
        print("Cleanup: mise run simulator-stop", flush=True)
    except Exception:
        if tab_id is None:
            clear_state()
            print(f"{FAULT}: startup failed before creating a tab; released reserved {STATE_RELPATH}",
                  file=sys.stderr, flush=True)
        else:
            run_dir_text = record.get("run_dir") if record else "unknown"
            panes = record.get("panes", {}) if record else {}
            print(f"{FAULT}: startup failed after tab {tab_id}; retained {STATE_RELPATH}; "
                  f"logs {run_dir_text}; cleanup: mise run simulator-stop", file=sys.stderr, flush=True)
            for pane in panes.values():
                try:
                    print(run("herdr", "pane", "read", pane, "--source", "recent-unwrapped", "--lines", "120"),
                          file=sys.stderr)
                except (subprocess.CalledProcessError, OSError):
                    pass
        raise


def parse_args(argv):
    parser = argparse.ArgumentParser(
        prog="start.py",
        description="Start a loopback-only simulator validation stack in its own Herdr tab")
    parser.add_argument("--preset", choices=("scripted", "full"), default=None,
                        help="checked-in devices preset (default: scripted)")
    parser.add_argument("--devices", default=None,
                        help="YAML file containing only a sequence of Device specs")
    parser.add_argument("--dashboard", action="store_true",
                        help="also start the dashboard dev server on loopback 5173")
    args = parser.parse_args(argv)
    if args.devices and args.preset:
        parser.error("--devices is mutually exclusive with an explicit --preset")
    return args


def main(argv=None):
    args = parse_args(argv)
    start(args.preset or "scripted", Path(args.devices) if args.devices else None, args.dashboard)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
