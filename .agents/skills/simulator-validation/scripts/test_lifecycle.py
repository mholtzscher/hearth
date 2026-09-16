"""Simulator-validation lifecycle regressions, isolated from Herdr and the network.

Every test runs against a temporary worktree with mocked Herdr, sockets, clock,
and HTTP, so no service, pane, or port is ever touched.
"""

from contextlib import ExitStack, redirect_stderr, redirect_stdout
import io
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))

import start
import stop

REPO_ROOT = Path(__file__).resolve().parents[4]
TAB_ID = "w1:t2"
WORKSPACE_ID = "w1"
LABEL = "simulator-validation-deadbeef"
PRESET_FILES = ("simulator.scripted.example.yaml", "simulator.full.example.yaml")


class FakeHerdr:
    """A Herdr stand-in that mints stable IDs and records every call."""

    def __init__(self):
        self.calls = []
        self.panes = 0
        self.tabs = 0
        self.reads = {}

    def __call__(self, *args):
        self.calls.append(args)
        if args[:2] == ("tab", "create"):
            self.tabs += 1
            self.panes += 1
            return {"tab": {"tab_id": TAB_ID, "workspace_id": WORKSPACE_ID},
                    "root_pane": {"pane_id": f"{WORKSPACE_ID}:p{self.panes}"}}
        if args[:2] == ("pane", "split"):
            self.panes += 1
            return {"pane": {"pane_id": f"{WORKSPACE_ID}:p{self.panes}"}}
        if args[:2] == ("pane", "read"):
            return self.reads.get(args[2], "")
        return {}


class SimulatorLifecycleTest(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.root = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        (self.root / "configs").mkdir()
        (self.root / ".data").mkdir()
        (self.root / "web/node_modules").mkdir(parents=True)
        for name in PRESET_FILES:
            (self.root / "configs" / name).write_text((REPO_ROOT / "configs" / name).read_text())
        self.stack.enter_context(patch.object(start, "ROOT", self.root))
        self.stack.enter_context(patch.object(stop, "ROOT", self.root))
        self.stack.enter_context(patch.dict(os.environ, HERDR_ENV="1", HERDR_WORKSPACE_ID=WORKSPACE_ID,
                                            HERDR_SOCKET_PATH="/tmp/herdr.sock"))
        self.output = io.StringIO()
        self.stack.enter_context(redirect_stdout(self.output))
        self.stack.enter_context(redirect_stderr(self.output))
        self.run = self.stack.enter_context(patch.object(start, "run", return_value=""))
        self.ports = self.stack.enter_context(patch.object(start, "port_open", return_value=False))
        self.validate_config = self.stack.enter_context(
            patch.object(start, "validate_simulator_config", return_value=None))
        self.herdr = FakeHerdr()
        self.stack.enter_context(patch.object(start, "herdr", side_effect=self.herdr))
        self.stop_herdr = self.stack.enter_context(patch.object(stop, "herdr"))

    # --- helpers -----------------------------------------------------------

    def read_state(self):
        return json.loads((self.root / ".data/simulator-validation.json").read_text())

    def run_dir_of(self):
        return Path(self.read_state()["run_dir"])

    def start_successfully(self, preset="scripted", devices_path=None, dashboard=False, events=None):
        events = events if events is not None else []

        def fake_launch(pane, service, command, run_dir):
            record = self.read_state()
            self.assertIn(service, record["panes"], "pane must be recorded before its launch")
            self.assertEqual(record["panes"][service], pane)
            events.append(("launch", service))

        def fake_wait(label, check, panes=(), timeout=start.READINESS_TIMEOUT, run_dir=None):
            events.append(("ready", label))

        with patch.object(start, "launch", side_effect=fake_launch), \
                patch.object(start, "wait_ready", side_effect=fake_wait):
            start.start(preset, devices_path, dashboard)
        return events

    def write_custom_devices(self, text, name="custom-devices.yaml"):
        path = self.root / name
        path.write_text(text)
        return path

    # --- preflight ---------------------------------------------------------

    def test_preflight_failures_never_reserve_or_launch(self):
        cases = {
            "outside Herdr": {"HERDR_ENV": "0"},
            "missing workspace": {"HERDR_WORKSPACE_ID": ""},
            "blank workspace": {"HERDR_WORKSPACE_ID": "  "},
        }
        for name, environment in cases.items():
            with self.subTest(name=name), patch.dict(os.environ, environment):
                with self.assertRaises(RuntimeError):
                    start.start("scripted", None, False)
                self.assertEqual(self.herdr.calls, [])
                self.assertFalse((self.root / ".data/simulator-validation.json").exists())

    def test_occupied_ports_release_reserved_state_without_launch(self):
        for host, port in [("127.0.0.1", start.NATS_PORT), ("::1", start.CORE_PORT)]:
            with self.subTest(host=host, port=port):
                self.ports.side_effect = lambda candidate_host, candidate_port: (
                    candidate_host == host and candidate_port == port)
                with self.assertRaisesRegex(RuntimeError, "already occupied"):
                    start.start("scripted", None, False)
                self.assertEqual(self.herdr.calls, [])
                self.assertFalse((self.root / ".data/simulator-validation.json").exists())
                self.assertEqual(
                    [path for path in (self.root / ".data").glob("simulator-validation.*")
                     if path.suffix != ".lock"], [])

    def test_missing_tool_and_ignored_path_fail_before_launch(self):
        def missing_tool(*args):
            if args[:2] == ("mise", "which"):
                raise subprocess.CalledProcessError(1, "mise")
            return ""

        self.run.side_effect = missing_tool
        with self.assertRaisesRegex(RuntimeError, "unavailable through mise"):
            start.start("scripted", None, False)
        self.assertEqual(self.herdr.calls, [])
        self.assertFalse((self.root / ".data/simulator-validation.json").exists())

        def unignored(*args):
            if args[:2] == ("git", "check-ignore"):
                raise subprocess.CalledProcessError(1, "git")
            return ""

        self.run.side_effect = unignored
        with self.assertRaises(subprocess.CalledProcessError):
            start.start("scripted", None, False)
        self.assertEqual(self.herdr.calls, [])
        self.assertFalse((self.root / ".data/simulator-validation.json").exists())

    def test_invalid_device_list_fails_before_any_tab(self):
        self.validate_config.side_effect = RuntimeError(
            "simulator-start: simulator config is invalid:\n"
            "validate config '/tmp/config.yaml': devices[0] entities[0]: "
            "invalid support: missing property 'set'")
        with self.assertRaisesRegex(RuntimeError, "invalid support"):
            start.start("scripted", None, False)
        self.assertEqual(self.herdr.calls, [])
        self.assertFalse((self.root / ".data/simulator-validation.json").exists())
        self.assertEqual(
            [path for path in (self.root / ".data").glob("simulator-validation.*")
             if path.suffix != ".lock"], [])

    def test_generated_config_is_validated_before_the_tab(self):
        devices = self.write_custom_devices(
            "- binding_key: custom-light\n"
            "  name: Custom light\n"
            "  kind: light\n"
            "  entities:\n"
            "    - key: power\n"
            "      name: Power\n"
            "      type: hearth.power/v1\n"
            "      support: {state: {}, operations: {set: {}}}\n"
            "      initial: false\n")
        self.start_successfully(devices_path=devices)
        self.validate_config.assert_called_once()
        validated_text = self.validate_config.call_args.args[0]
        self.assertIn("binding_key: custom-light", validated_text)
        self.assertEqual(validated_text, (self.run_dir_of() / "simulator.yaml").read_text())
        self.assertEqual(self.herdr.calls[0][:2], ("tab", "create"))

    def test_existing_record_blocks_a_competing_start(self):
        record = {"version": 1, "status": "reserved", "worktree": str(self.root)}
        (self.root / ".data/simulator-validation.json").write_text(json.dumps(record))
        with self.assertRaisesRegex(RuntimeError, "ownership record"):
            start.start("scripted", None, False)
        self.assertEqual(self.herdr.calls, [])
        self.assertEqual(self.read_state(), record)

    # --- config generation -------------------------------------------------

    def test_default_and_preset_configs_are_local_and_isolated(self):
        for preset, marker in [("scripted", "simulated-light"), ("full", "demo-broken")]:
            with self.subTest(preset=preset):
                start.clear_state()
                self.start_successfully(preset=preset)
                run_dir = self.run_dir_of()
                simulator = (run_dir / "simulator.yaml").read_text()
                self.assertTrue(simulator.startswith(
                    "adapter_id: simulator\nnats_url: nats://127.0.0.1:4222\n"
                    "control_addr: 127.0.0.1:8181\ndevices:\n"))
                self.assertIn(marker, simulator)
                self.assertEqual(self.read_state()["mode"], preset)

    def test_generated_configs_use_only_owned_loopback_infrastructure(self):
        self.start_successfully()
        run_dir = self.run_dir_of()
        nats = (run_dir / "nats.conf").read_text()
        self.assertIn("listen: 127.0.0.1:4222", nats)
        self.assertIn(f'store_dir: "{run_dir}/nats"', nats)
        self.assertIn("listen: 127.0.0.1:4223", nats)
        self.assertIn("http: 127.0.0.1:8222", nats)
        core = (run_dir / "hearthd.yaml").read_text()
        self.assertIn("http_addr: 127.0.0.1:8080", core)
        self.assertIn(f"sqlite_path: {run_dir}/hearthd.db", core)
        self.assertIn("household_timezone: UTC", core)
        simulator = (run_dir / "simulator.yaml").read_text()
        for foreign in ("wanda", "tailscale", "mqtt", "mosquitto", "docker", "homelab"):
            with self.subTest(foreign=foreign):
                self.assertNotIn(foreign, (nats + core + simulator).lower())
        self.assertTrue(run_dir.name.startswith(start.RUN_DIR_PREFIX))
        self.assertEqual(run_dir.parent, self.root / ".data")

    def test_two_starts_use_distinct_run_directories(self):
        self.start_successfully()
        first = self.read_state()["run_dir"]
        start.clear_state()
        self.start_successfully()
        second = self.read_state()["run_dir"]
        self.assertNotEqual(first, second)

    def test_custom_devices_are_confined_below_generated_transport(self):
        custom = self.write_custom_devices(
            "# operator devices\n"
            "- binding_key: my-sensor\n"
            "  name: My sensor # keep this comment\n"
            "  kind: sensor\n"
            "  entities:\n"
            "    - key: temperature\n"
            "      name: Temperature\n"
            "      type: hearth.temperature/v1\n"
            "      support: {state: {unit: mCel}, operations: {}}\n"
            "      initial: 21000\n")
        self.start_successfully(preset="scripted", devices_path=custom)
        run_dir = self.run_dir_of()
        simulator = (run_dir / "simulator.yaml").read_text()
        self.assertEqual(simulator.count("nats_url:"), 1)
        self.assertTrue(simulator.startswith(
            "adapter_id: simulator\nnats_url: nats://127.0.0.1:4222\n"
            "control_addr: 127.0.0.1:8181\ndevices:\n"))
        self.assertIn("  - binding_key: my-sensor", simulator)
        self.assertIn("    name: My sensor # keep this comment", simulator)
        record = self.read_state()
        self.assertEqual(record["mode"], "custom")
        self.assertEqual(record["devices_path"], str(custom.resolve()))

    def test_custom_full_config_is_rejected_without_parsing(self):
        for text in (
            "adapter_id: hijack\nnats_url: nats://example.invalid:4222\ndevices: []\n",
            "nats_url: nats://host:4222\n- binding_key: x\n",
        ):
            with self.subTest(text=text.splitlines()[0]):
                path = self.write_custom_devices(text)
                with self.assertRaisesRegex(RuntimeError, "sequence of Device specs"):
                    start.start("scripted", path, False)
                self.assertEqual(self.herdr.calls, [])
                self.assertFalse((self.root / ".data/simulator-validation.json").exists())

    def test_indent_devices_block_preserves_operator_bytes(self):
        block = start.indent_devices_block("  - binding_key: x\n    name: 'a: b'\n")
        self.assertEqual(block, "    - binding_key: x\n      name: 'a: b'\n")
        self.assertEqual(start.indent_devices_block("- binding_key: x\n"), "  - binding_key: x\n")

    def test_parse_args_defaults_and_mutual_exclusion(self):
        defaults = start.parse_args([])
        self.assertIsNone(defaults.preset)
        self.assertIsNone(defaults.devices)
        self.assertFalse(defaults.dashboard)
        self.assertEqual(start.parse_args(["--preset", "full"]).preset, "full")
        self.assertTrue(start.parse_args(["--dashboard"]).dashboard)
        with self.assertRaises(SystemExit):
            start.parse_args(["--devices", "x.yaml", "--preset", "full"])

    def test_cli_defaults_to_the_scripted_preset(self):
        with patch.object(start, "start") as start_mock:
            start.main([])
            start_mock.assert_called_once_with("scripted", None, False)
        with patch.object(start, "start") as start_mock:
            start.main(["--preset", "full", "--dashboard"])
            start_mock.assert_called_once_with("full", None, True)
        with patch.object(start, "start") as start_mock:
            start.main(["--devices", "custom.yaml"])
            self.assertEqual(start_mock.call_args.args, ("scripted", Path("custom.yaml"), False))

    # --- ordering and records ---------------------------------------------

    def test_start_orders_services_and_records_ownership(self):
        events = self.start_successfully()
        self.assertEqual(events, [
            ("launch", "nats"), ("ready", "NATS JetStream"),
            ("launch", "core"), ("ready", "Core readiness"),
            ("launch", "simulator"), ("ready", "simulator registration"),
        ])
        record = self.read_state()
        self.assertEqual(record["root_pane_id"], f"{WORKSPACE_ID}:p1")
        self.assertEqual(record["pane_ids"], [f"{WORKSPACE_ID}:p{index}" for index in (1, 2, 3)])
        self.assertTrue(record["tab_label"].startswith(start.TAB_LABEL_PREFIX))
        self.assertNotEqual(record["tab_label"], start.TAB_LABEL_PREFIX)
        self.assertEqual(record["session_socket"], "/tmp/herdr.sock")
        for call in self.herdr.calls:
            if call[:2] in (("tab", "create"), ("pane", "split")):
                self.assertIn("--no-focus", call)
        output = self.output.getvalue()
        self.assertIn("Ready for validation:", output)
        self.assertIn("mise run simulator-stop", output)
        self.assertIn("In-place restart:", output)
        self.assertIn("core pane", output)
        self.assertIn("simulator pane", output)
        self.assertIn(f"Run directory: {Path(record['run_dir']).relative_to(start.ROOT)}\n", output)
        self.assertIn("Logs in run directory: <service>.out and <service>.err\n", output)
        # The agent output compressor truncates long lines: key setup details
        # must not share a giant summary line with endpoints/restart commands.
        for line in output.splitlines():
            self.assertLessEqual(len(line), 140, line)

    def test_active_lifecycle_blocks_start_and_stop_without_clearing_record(self):
        record = {"status": "reserved", "worktree": str(start.ROOT)}
        start.reserve_state(record)
        with start.lifecycle_lock():
            for operation in (lambda: start.start("scripted", None, False), stop.stop):
                with self.assertRaisesRegex(RuntimeError, "lifecycle operation is active"):
                    operation()
                self.assertEqual(self.read_state(), record)
        self.assertEqual(self.herdr.calls, [])
        self.stop_herdr.assert_not_called()
        # Releasing the lock permits cleanup of a crashed pre-tab reservation.
        stop.stop()
        self.assertIsNone(start.read_state())

    def test_failed_start_releases_reserved_state_before_any_tab(self):
        with patch.object(start, "check_ports_free", side_effect=RuntimeError("port race")):
            with self.assertRaisesRegex(RuntimeError, "port race"):
                start.start("scripted", None, False)
        self.assertEqual(self.herdr.calls, [])
        self.assertFalse((self.root / ".data/simulator-validation.json").exists())
        self.assertIn("released reserved", self.output.getvalue())

    def test_failed_start_retains_record_after_tab(self):
        with patch.object(start, "launch"), \
                patch.object(start, "wait_ready", side_effect=RuntimeError("nats failed")):
            with self.assertRaisesRegex(RuntimeError, "nats failed"):
                start.start("scripted", None, False)
        record = self.read_state()
        self.assertEqual(record["tab_id"], TAB_ID)
        self.assertEqual(record["root_pane_id"], f"{WORKSPACE_ID}:p1")
        self.assertIn("retained", self.output.getvalue())
        self.assertIn("mise run simulator-stop", self.output.getvalue())
        self.assertNotIn("Ready for validation:", self.output.getvalue())

    # --- readiness ---------------------------------------------------------

    def test_process_exit_is_detected_before_the_full_timeout(self):
        clock = [0]
        self.run.return_value = "compile failed\n" + start.EXIT_MARKERS["core"]
        with patch.object(start.time, "monotonic", side_effect=lambda: clock[0]), \
                patch.object(start.time, "sleep", side_effect=lambda seconds: clock.__setitem__(0, clock[0] + seconds)):
            with self.assertRaisesRegex(RuntimeError, "process exited"):
                start.wait_ready("core", lambda: False, [("w1:p3", start.EXIT_MARKERS["core"])], timeout=60)
        self.assertLess(clock[0], 60)

    def test_timeout_is_bounded_and_ignores_an_echoed_marker(self):
        clock = [0]
        self.run.return_value = f"bash -c 'printf {start.EXIT_MARKERS['core']}'"
        with patch.object(start.time, "monotonic", side_effect=lambda: clock[0]), \
                patch.object(start.time, "sleep", side_effect=lambda seconds: clock.__setitem__(0, clock[0] + seconds)):
            with self.assertRaisesRegex(RuntimeError, "timed out after 7s"):
                start.wait_ready("core", lambda: False, [("w1:p3", start.EXIT_MARKERS["core"])], timeout=7)
        self.assertEqual(clock[0], 7)

    def test_launch_wraps_in_bash_and_redirects_both_streams(self):
        run_dir = self.root / "run dir with spaces"
        run_dir.mkdir()
        start.launch("w1:p9", "core", "printf 'hello world'", run_dir)
        args = self.run.call_args.args
        self.assertEqual(args[:4], ("herdr", "pane", "run", "w1:p9"))
        result = subprocess.run(shlex.split(args[4]), text=True, capture_output=True)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout.strip(), start.EXIT_MARKERS["core"])
        self.assertNotIn("&", args[4])
        self.assertEqual((run_dir / "core.out").read_text(), "hello world")
        self.assertEqual((run_dir / "core.err").read_text(), "")

        start.launch("w1:p9", "simulator", "false", run_dir)
        failed = subprocess.run(shlex.split(self.run.call_args.args[4]), text=True, capture_output=True)
        self.assertEqual(failed.returncode, 1)
        self.assertEqual(failed.stdout.strip(), start.EXIT_MARKERS["simulator"])

    def test_simulator_gate_requires_inventory_and_online_runtime_not_health(self):
        unhealthy_online = {"health": {"status": "unhealthy",
                                       "runtime": {"id": "runtime-1", "status": "online"}}}
        self.assertTrue(start.adapter_runtime_online(unhealthy_online))
        self.assertTrue(start.adapter_runtime_online(
            {"health": {"status": "unknown", "runtime": {"id": "runtime-1", "status": "online"}}}))
        self.assertFalse(start.adapter_runtime_online(
            {"health": {"status": "healthy", "runtime": {"id": "runtime-1", "status": "offline"}}}))
        self.assertFalse(start.adapter_runtime_online(
            {"health": {"status": "healthy", "runtime": {"status": "online"}}}))

        def fake_read_json(url, timeout=2):
            if url.endswith("/v1/sim/entities"):
                return [{"entity_id": "ent_1"}]
            return unhealthy_online

        with patch.object(start, "read_json", side_effect=fake_read_json):
            self.assertTrue(start.simulator_ready())

        def empty_inventory(url, timeout=2):
            return []

        with patch.object(start, "read_json", side_effect=empty_inventory):
            self.assertFalse(start.simulator_ready())

    # --- optional dashboard ------------------------------------------------

    def test_dashboard_is_optional(self):
        events = self.start_successfully(dashboard=False)
        calls = [call.args for call in self.run.call_args_list]
        self.assertNotIn(("mise", "run", "web-install"), calls)
        self.assertEqual(self.herdr.panes, 3)
        self.assertNotIn(("ready", "dashboard"), events)
        record = self.read_state()
        self.assertFalse(record["dashboard"])
        self.assertNotIn("dashboard", record["commands"])

    def test_dashboard_installs_dependencies_only_when_absent(self):
        import shutil
        shutil.rmtree(self.root / "web/node_modules")
        events = self.start_successfully(dashboard=True)
        calls = [call.args for call in self.run.call_args_list]
        self.assertIn(("mise", "run", "web-install"), calls)
        self.assertEqual(self.herdr.panes, 4)
        self.assertIn(("ready", "dashboard"), events)
        record = self.read_state()
        self.assertTrue(record["dashboard"])
        self.assertEqual(record["commands"]["dashboard"],
                         "HEARTHD_URL=http://127.0.0.1:8080 "
                         "NATS_MONITOR_URL=http://127.0.0.1:8222 mise run web-dev")
        self.assertIn("Dashboard: http://127.0.0.1:5173/", self.output.getvalue())

    def test_dashboard_skips_install_when_dependencies_present(self):
        self.start_successfully(dashboard=True)
        calls = [call.args for call in self.run.call_args_list]
        self.assertNotIn(("mise", "run", "web-install"), calls)

    def test_dashboard_ports_are_only_reserved_when_requested(self):
        self.start_successfully(dashboard=False)
        self.assertNotIn(start.DASHBOARD_PORT, start.required_ports(False))
        self.assertIn(start.DASHBOARD_PORT, start.required_ports(True))

    def test_dashboard_ready_checks_proxies(self):
        requested = []

        def fake_read_http(url, timeout=2):
            requested.append(url)
            return b"ok"

        with patch.object(start, "read_http", side_effect=fake_read_http):
            self.assertTrue(start.dashboard_ready())
        self.assertEqual(requested, [start.DASHBOARD_URL + "/", start.DASHBOARD_URL + "/readyz",
                                     start.DASHBOARD_URL + "/nats-monitor/varz"])

    # --- stop --------------------------------------------------------------

    def pane_entry(self, pane_id, tab_id=TAB_ID, cwd=None):
        return {"pane_id": pane_id, "tab_id": tab_id, "cwd": str(cwd if cwd is not None else self.root)}

    def write_record(self, dashboard=False, **overrides):
        run_dir = self.root / ".data" / "simulator-validation.abc123"
        run_dir.mkdir(parents=True, exist_ok=True)
        record = {
            "version": 1, "status": "running", "worktree": str(self.root),
            "session_socket": "/tmp/herdr.sock", "workspace_id": WORKSPACE_ID,
            "tab_id": TAB_ID, "tab_label": LABEL, "run_dir": str(run_dir),
            "mode": "scripted", "devices_path": None, "dashboard": dashboard,
            "adapter_id": "simulator",
            "panes": {"nats": "w1:p1", "core": "w1:p2", "simulator": "w1:p3"},
            "pane_ids": ["w1:p1", "w1:p2", "w1:p3"], "root_pane_id": "w1:p1",
            "commands": {"nats": "nats", "core": "core", "simulator": "simulator"},
            "created_at": "2026-01-01T00:00:00Z",
        }
        if dashboard:
            record["panes"]["dashboard"] = "w1:p4"
            record["pane_ids"].append("w1:p4")
        record.update(overrides)
        (self.root / ".data/simulator-validation.json").write_text(json.dumps(record))
        return record

    def stub_stop_herdr(self, tab_label=LABEL, panes=None, confirm=()):
        panes = panes if panes is not None else [self.pane_entry(pane_id) for pane_id in
                                                  ("w1:p1", "w1:p2", "w1:p3")]
        self.stop_herdr.side_effect = [
            {"tabs": [{"tab_id": TAB_ID, "label": tab_label}]},
            {"panes": panes},
            {},
            {"tabs": list(confirm)},
        ]

    def stop_call_args(self):
        return [call.args for call in self.stop_herdr.call_args_list]

    def test_stop_is_idempotent_and_closes_only_the_owned_tab(self):
        stop.stop()
        self.assertEqual(self.stop_herdr.call_args_list, [])
        self.assertIn("no recorded environment", self.output.getvalue())

        record = self.write_record()
        run_dir = Path(record["run_dir"])
        (run_dir / "nats.out").write_text("evidence")
        self.stub_stop_herdr()
        stop.stop()
        self.assertIn(("tab", "close", TAB_ID), self.stop_call_args())
        self.assertFalse((self.root / ".data/simulator-validation.json").exists())
        self.assertTrue((run_dir / "nats.out").exists(), "run data must be retained")

        self.stop_herdr.reset_mock()
        self.stop_herdr.side_effect = None
        stop.stop()
        self.assertEqual(self.stop_herdr.call_args_list, [])

    def test_stop_refuses_ownership_mismatch_and_retains_record(self):
        self.write_record()
        self.stub_stop_herdr(tab_label="someone-else")
        with self.assertRaisesRegex(RuntimeError, "does not match recorded"):
            stop.stop()
        self.assertTrue((self.root / ".data/simulator-validation.json").exists())
        self.assertNotIn(("tab", "close", TAB_ID), self.stop_call_args())

    def test_stop_refuses_worktree_session_and_herdr_context_mismatch(self):
        self.write_record(worktree="/somewhere/else")
        with self.assertRaisesRegex(RuntimeError, "worktree mismatch"):
            stop.stop()
        (self.root / ".data/simulator-validation.json").unlink()

        self.write_record(session_socket="/tmp/other.sock")
        with self.assertRaisesRegex(RuntimeError, "session"):
            stop.stop()
        (self.root / ".data/simulator-validation.json").unlink()

        self.write_record()
        with patch.dict(os.environ, HERDR_ENV="0"):
            with self.assertRaisesRegex(RuntimeError, "run inside Herdr"):
                stop.stop()
        self.assertEqual(self.stop_herdr.call_args_list, [])

    def test_stop_refuses_moved_missing_and_unexpected_panes(self):
        cases = {
            "moved pane": [self.pane_entry("w1:p1"), self.pane_entry("w1:p2", tab_id="w1:t9"),
                           self.pane_entry("w1:p3")],
            "missing pane": [self.pane_entry("w1:p1"), self.pane_entry("w1:p2")],
            "unexpected pane": [self.pane_entry("w1:p1"), self.pane_entry("w1:p2"),
                                self.pane_entry("w1:p3"), self.pane_entry("w1:p7")],
            "changed cwd": [self.pane_entry("w1:p1", cwd="/elsewhere"), self.pane_entry("w1:p2"),
                            self.pane_entry("w1:p3")],
        }
        for name, panes in cases.items():
            with self.subTest(name=name):
                self.write_record()
                self.stub_stop_herdr(panes=panes)
                with self.assertRaises(RuntimeError):
                    stop.stop()
                self.assertTrue((self.root / ".data/simulator-validation.json").exists())
                self.assertNotIn(("tab", "close", TAB_ID), self.stop_call_args())
                (self.root / ".data/simulator-validation.json").unlink()

    def test_stop_releases_reserved_state_without_a_tab(self):
        self.write_record(tab_id=None, tab_label=None, panes={}, pane_ids=[], root_pane_id=None)
        stop.stop()
        self.assertFalse((self.root / ".data/simulator-validation.json").exists())
        self.assertEqual(self.stop_herdr.call_args_list, [])
        self.assertIn("released reserved", self.output.getvalue())

    def test_stop_clears_record_when_the_owned_tab_is_already_closed(self):
        self.write_record()

        def herdr(*args):
            if args[:2] == ("tab", "list"):
                return {"tabs": []}
            if args[:2] == ("pane", "get"):
                raise subprocess.CalledProcessError(1, "herdr")
            return {}

        self.stop_herdr.side_effect = herdr
        stop.stop()
        self.assertFalse((self.root / ".data/simulator-validation.json").exists())
        self.assertIn("already closed", self.output.getvalue())

    def test_stop_retains_record_when_a_recorded_pane_survived(self):
        self.write_record()

        def herdr(*args):
            if args[:2] == ("tab", "list"):
                return {"tabs": []}
            if args[:2] == ("pane", "get"):
                return {"pane": {"pane_id": args[2]}}
            return {}

        self.stop_herdr.side_effect = herdr
        with self.assertRaisesRegex(RuntimeError, "still exist"):
            stop.stop()
        self.assertTrue((self.root / ".data/simulator-validation.json").exists())

    def test_stop_retains_record_when_closure_is_not_confirmed(self):
        self.write_record()
        self.stub_stop_herdr(confirm=[{"tab_id": TAB_ID, "label": LABEL}])
        with self.assertRaisesRegex(RuntimeError, "closure not confirmed"):
            stop.stop()
        self.assertTrue((self.root / ".data/simulator-validation.json").exists())


if __name__ == "__main__":
    unittest.main()
