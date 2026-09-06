"""Real-device lifecycle regressions, isolated from Herdr and the network."""

from contextlib import ExitStack, redirect_stdout, redirect_stderr
import io
import json
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import start
import stop


class RealDeviceLifecycleTest(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.root = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        (self.root / "configs").mkdir()
        (self.root / "web/node_modules").mkdir(parents=True)
        self.stack.enter_context(patch.object(start, "ROOT", self.root))
        self.stack.enter_context(patch.object(stop, "ROOT", self.root))
        self.stack.enter_context(patch.dict(start.os.environ, HERDR_ENV="1"))
        self.output = io.StringIO()
        self.stack.enter_context(redirect_stdout(self.output))
        self.stack.enter_context(redirect_stderr(self.output))
        self.run = self.stack.enter_context(patch.object(start, "run", return_value=""))
        self.ports = self.stack.enter_context(patch.object(start, "port_open", side_effect=lambda h, p: h == "wanda" and p in (4222, 1883)))
        self.http = self.stack.enter_context(patch.object(start, "http_read", return_value=b"ok"))
        self.herdr = self.stack.enter_context(patch.object(start, "herdr"))
        self.stop_herdr = self.stack.enter_context(patch.object(stop, "herdr"))

    def record(self):
        (self.root / ".data").mkdir(exist_ok=True)
        state = {"tab_id": "w1:t2", "root_pane_id": "w1:p3", "workspace_id": "w1", "worktree": str(self.root)}
        path = self.root / ".data/real-device-validation.json"
        path.write_text(json.dumps(state))
        return path

    def test_preflight_failures_never_launch_or_write_configs(self):
        cases = [
            ("outside Herdr", "wanda", "0", lambda h, p: False),
            ("invalid host", "wanda;touch bad", "1", lambda h, p: False),
            ("occupied IPv4", "wanda", "1", lambda h, p: h == "127.0.0.1"),
            ("occupied IPv6", "wanda", "1", lambda h, p: h == "::1"),
            ("remote core", "wanda", "1", lambda h, p: h == "wanda" and p == 8081),
            ("unreachable NATS", "wanda", "1", lambda h, p: False),
        ]
        for name, host, env, ports in cases:
            with self.subTest(name=name), patch.dict(start.os.environ, HERDR_ENV=env):
                self.ports.side_effect = ports
                with self.assertRaises(RuntimeError):
                    start.start(host)
                self.herdr.assert_not_called()
                self.assertEqual(list((self.root / "configs").iterdir()), [])

    def test_configs_must_be_ignored_and_never_overwritten(self):
        path = Path("configs/homelab-hearthd.yaml")
        self.run.side_effect = subprocess.CalledProcessError(1, "git")
        with self.assertRaises(subprocess.CalledProcessError):
            start.prepare_config(path, "expected\n")
        self.assertFalse((self.root / path).exists())
        self.run.side_effect = None
        start.prepare_config(path, "expected\n")
        start.prepare_config(path, "expected\n")
        with self.assertRaisesRegex(RuntimeError, "config differs"):
            start.prepare_config(path, "wrong-host\n")
        self.assertEqual((self.root / path).read_text(), "expected\n")

    def test_stale_health_does_not_pass_new_adapter_gate(self):
        for status, runtime_status, runtime_id, expected in [
            ("healthy", "online", "old", False),
            ("unknown", "online", "new", False),
            ("healthy", "offline", "new", False),
            ("healthy", "online", "new", True),
        ]:
            with self.subTest(status=status, runtime=runtime_id):
                self.http.return_value = json.dumps({"health": {"status": status, "runtime": {"id": runtime_id, "status": runtime_status}}}).encode()
                self.assertEqual(start.adapter_ready("old"), expected)

    def test_process_exit_is_detected_before_full_timeout(self):
        clock = [0]
        def sleep(seconds):
            clock[0] += seconds
        self.run.return_value = "compiler failed\n" + start.EXIT_MARKER
        with patch.object(start.time, "monotonic", side_effect=lambda: clock[0]), patch.object(start.time, "sleep", side_effect=sleep):
            with self.assertRaisesRegex(RuntimeError, "process exited"):
                start.wait_ready("core", lambda: False, ("w1:p3",))
        self.assertEqual(clock[0], 5)

    def test_readiness_timeout_is_bounded_and_ignores_echoed_marker(self):
        clock = [0]
        self.run.return_value = f"bash -c 'printf {start.EXIT_MARKER}'"
        def sleep(seconds):
            clock[0] += seconds
        with patch.object(start.time, "monotonic", side_effect=lambda: clock[0]), patch.object(start.time, "sleep", side_effect=sleep):
            with self.assertRaisesRegex(RuntimeError, "timed out"):
                start.wait_ready("core", lambda: False, ("w1:p3",), timeout=7)
        self.assertEqual(clock[0], 7)

    def test_launch_handles_empty_herdr_output_and_shell_quoting(self):
        start.launch("w1:p3", "false")
        args = self.run.call_args.args
        self.assertEqual(args[:4], ("herdr", "pane", "run", "w1:p3"))
        # Execute only the harmless shell wrapper, not Herdr or a real service.
        result = subprocess.run(shlex.split(args[4]), text=True, capture_output=True)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout.strip(), start.EXIT_MARKER)

    def test_start_orders_services_and_records_owned_tab(self):
        self.herdr.side_effect = [
            {"tab": {"tab_id": "w1:t2", "workspace_id": "w1"}, "root_pane": {"pane_id": "w1:p3"}},
            {"pane": {"pane_id": "w1:p4"}},
            {"pane": {"pane_id": "w1:p5"}},
        ]
        self.http.return_value = b'{"health":{"runtime":{"id":"old"}}}'
        events = []
        def launch(pane, command):
            self.assertTrue((self.root / ".data").is_dir())
            events.append(command)
        def wait(label, check, panes):
            events.append(label)
        with patch.object(start, "launch", side_effect=launch), patch.object(start, "wait_ready", side_effect=wait):
            start.start("wanda")
        self.assertLess(events.index("core readiness"), events.index("go run ./cmd/hearth-adapter-zigbee2mqtt -config configs/homelab-zigbee2mqtt.yaml"))
        self.assertLess(events.index("NATS_MONITOR_URL=http://wanda:8222 mise run web-dev"), events.index("adapter"))
        self.assertIn("API proxy", events)
        self.assertIn("monitor proxy", events)
        self.assertIn("Ready for validation:", self.output.getvalue())
        state = json.loads((self.root / ".data/real-device-validation.json").read_text())
        self.assertEqual(state["root_pane_id"], "w1:p3")
        for call in self.herdr.call_args_list:
            self.assertIn("--no-focus", call.args)
        with self.assertRaisesRegex(RuntimeError, "saved environment exists"):
            start.start("wanda")

    def test_start_failure_retains_record_and_prints_cleanup(self):
        self.herdr.return_value = {"tab": {"tab_id": "w1:t2", "workspace_id": "w1"}, "root_pane": {"pane_id": "w1:p3"}}
        with patch.object(start, "wait_ready", side_effect=RuntimeError("core failed")):
            with self.assertRaisesRegex(RuntimeError, "core failed"):
                start.start("wanda")
        self.assertTrue((self.root / ".data/real-device-validation.json").exists())
        self.assertIn("mise run real-device-stop", self.output.getvalue())
        self.assertNotIn("Ready for validation:", self.output.getvalue())

    def test_stop_is_idempotent_and_closes_only_verified_tab(self):
        stop.stop()
        self.stop_herdr.assert_not_called()
        state_path = self.record()
        self.stop_herdr.side_effect = [
            {"tabs": [{"tab_id": "w1:t2", "label": "real-device-validation"}]},
            {"panes": [{"pane_id": "w1:p3", "tab_id": "w1:t2", "cwd": str(self.root)}]},
            {},
        ]
        stop.stop()
        self.assertEqual(self.stop_herdr.call_args.args, ("tab", "close", "w1:t2"))
        self.assertFalse(state_path.exists())
        self.stop_herdr.reset_mock()
        stop.stop()
        self.stop_herdr.assert_not_called()

    def test_stop_refuses_ownership_mismatch_and_retains_record(self):
        state_path = self.record()
        self.stop_herdr.side_effect = [
            {"tabs": [{"tab_id": "w1:t2", "label": "unrelated"}]},
            {"panes": []},
        ]
        with self.assertRaisesRegex(RuntimeError, "ownership check failed"):
            stop.stop()
        self.assertTrue(state_path.exists())
        self.assertFalse(any(call.args[:2] == ("tab", "close") for call in self.stop_herdr.call_args_list))

    def test_stop_refuses_wrong_worktree_or_non_herdr_context(self):
        state_path = self.record()
        with patch.dict(start.os.environ, HERDR_ENV="0"):
            with self.assertRaisesRegex(RuntimeError, "run inside Herdr"):
                stop.stop()
        state = json.loads(state_path.read_text())
        state["worktree"] = "/another/worktree"
        state_path.write_text(json.dumps(state))
        with self.assertRaisesRegex(RuntimeError, "worktree mismatch"):
            stop.stop()
        self.stop_herdr.assert_not_called()
        self.assertTrue(state_path.exists())

    def test_stop_clears_record_when_tab_already_closed(self):
        state_path = self.record()
        self.stop_herdr.return_value = {"tabs": []}
        stop.stop()
        self.assertFalse(state_path.exists())
        self.assertEqual(self.stop_herdr.call_count, 1)


if __name__ == "__main__":
    unittest.main()
