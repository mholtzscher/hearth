"""Real-device lifecycle regressions, isolated from Herdr, Tailscale, and the network."""

from contextlib import ExitStack, redirect_stdout, redirect_stderr
import copy
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


def route_document(proxy=start.SERVE_PROXY_TARGET, host="node.ts.net"):
    """A live `tailscale serve status --json` document for the dashboard's HTTP port."""
    port = start.SERVE_HTTP_PORT
    return {"TCP": {str(port): {"HTTP": True}},
            "Web": {f"{host}:{port}": {"Handlers": {"/": {"Proxy": proxy}}}}}


def foreground_document(config=None, host="node.ts.net", proxy=start.SERVE_PROXY_TARGET):
    """A status document whose port is claimed only by one foreground session."""
    if config is None:
        config = route_document(proxy=proxy, host=host)
    return {"Foreground": {"session-1": config}}


def empty_document():
    """A live Serve status with no node-level routes at all."""
    return {"TCP": {}, "Web": {}}


# The two `tailscale serve` invocations this workflow is allowed to make.
PUBLISH_ARGUMENTS = ("--set-path", start.SERVE_MOUNT, start.SERVE_PROXY_TARGET)
REMOVE_ARGUMENTS = ("--set-path", start.SERVE_MOUNT, "off")


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
        # The fake tailnet: a mutable Serve document plus the only two commands
        # this workflow is allowed to send to `tailscale serve`.
        self.serve_status = empty_document()
        self.serve_commands = []
        self.dns = self.stack.enter_context(patch.object(start, "tailscale_dns_name", return_value="node.ts.net"))
        self.serve_status_mock = self.stack.enter_context(
            patch.object(start, "tailscale_serve_status", side_effect=self.fake_serve_status))
        self.stop_serve_status = self.stack.enter_context(
            patch.object(stop, "tailscale_serve_status", side_effect=self.fake_serve_status))
        self.serve = self.stack.enter_context(patch.object(start, "tailscale_serve", side_effect=self.apply_serve_command))
        self.stop_serve = self.stack.enter_context(patch.object(stop, "tailscale_serve", side_effect=self.apply_serve_command))

    def fake_serve_status(self, fault):
        return copy.deepcopy(self.serve_status)

    def apply_serve_command(self, *arguments, fault):
        """Apply a `tailscale serve` command to the fake status, rejecting anything else."""
        self.serve_commands.append((arguments, fault))
        if arguments == PUBLISH_ARGUMENTS:
            self.serve_status = route_document()
        elif arguments == REMOVE_ARGUMENTS:
            self.serve_status = empty_document()
        else:
            raise AssertionError(f"unexpected tailscale serve arguments: {arguments}")

    def record(self, serve=None):
        (self.root / ".data").mkdir(exist_ok=True)
        state = {"tab_id": "w1:t2", "root_pane_id": "w1:p3", "workspace_id": "w1", "worktree": str(self.root)}
        if serve is not None:
            state["serve"] = serve
        path = self.root / ".data/real-device-validation.json"
        path.write_text(json.dumps(state))
        return path

    def owned_serve(self, host="node.ts.net", proxy=start.SERVE_PROXY_TARGET):
        return {"host": host, "proxy": proxy, "url": f"http://{host}:{start.SERVE_HTTP_PORT}/"}

    def read_state(self):
        return json.loads((self.root / ".data/real-device-validation.json").read_text())

    def start_successfully(self, events):
        """Run a fully mocked startup that reaches readiness, recording ordered events."""
        self.herdr.side_effect = [
            {"tab": {"tab_id": "w1:t2", "workspace_id": "w1"}, "root_pane": {"pane_id": "w1:p3"}},
            {"pane": {"pane_id": "w1:p4"}},
            {"pane": {"pane_id": "w1:p5"}},
        ]
        self.http.return_value = b'{"health":{"runtime":{"id":"old"}}}'

        def launch(pane, command):
            self.assertTrue((self.root / ".data").is_dir())
            events.append(command)

        def wait(label, check, panes):
            events.append(label)

        def serve(*arguments, fault):
            events.append(f"tailscale serve {' '.join(arguments)}")
            self.apply_serve_command(*arguments, fault=fault)

        self.serve.side_effect = serve
        with patch.object(start, "launch", side_effect=launch), patch.object(start, "wait_ready", side_effect=wait):
            start.start("wanda")
        return events

    def stop_with_tab(self, events):
        """Mock Herdr so the recorded tab verifies, recording ordered events."""
        def herdr(*args):
            events.append(args)
            if args[:2] == ("tab", "list"):
                return {"tabs": [{"tab_id": "w1:t2", "label": "real-device-validation"}]}
            if args[:2] == ("pane", "list"):
                return {"panes": [{"pane_id": "w1:p3", "tab_id": "w1:t2", "cwd": str(self.root)}]}
            return {}

        self.stop_herdr.side_effect = herdr

        def serve(*arguments, fault):
            events.append(("tailscale serve", *arguments))
            self.apply_serve_command(*arguments, fault=fault)

        self.stop_serve.side_effect = serve

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
        events = []
        self.start_successfully(events)
        self.assertLess(events.index("core readiness"), events.index("go run ./cmd/hearth-adapter-zigbee2mqtt -config configs/homelab-zigbee2mqtt.yaml"))
        self.assertLess(events.index("NATS_MONITOR_URL=http://wanda:8222 mise run web-dev"), events.index("adapter"))
        self.assertIn("API proxy", events)
        self.assertIn("monitor proxy", events)
        self.assertIn("Ready for validation:", self.output.getvalue())
        state = self.read_state()
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

    def test_serve_route_accepts_only_an_exact_root_proxy(self):
        port = str(start.SERVE_HTTP_PORT)
        self.assertEqual(start.serve_route(empty_document()), {"kind": "absent"})
        self.assertEqual(start.serve_route({}), {"kind": "absent"})
        self.assertEqual(start.serve_route(route_document()),
                         {"kind": "proxy", "host": "node.ts.net", "proxy": start.SERVE_PROXY_TARGET})
        other_port = route_document()
        other_port["Web"] = {key.replace(f":{start.SERVE_HTTP_PORT}", ":8443"): value for key, value in other_port["Web"].items()}
        other_port["TCP"] = {"8443": {"HTTP": True}}
        self.assertEqual(start.serve_route(other_port), {"kind": "absent"})
        conflicts = {
            "tcp forwarder": {"TCP": {port: {"TCPForward": "127.0.0.1:5173"}}},
            "no handlers": {"TCP": {port: {"HTTP": True}}},
            "path handler": {"TCP": {port: {"HTTP": True}}, "Web": {f"node.ts.net:{port}": {"Handlers": {"/": {"Path": "/srv"}}}}},
            "second host": {**route_document(), "Web": {**route_document()["Web"], f"other.ts.net:{port}": {"Handlers": {"/": {"Proxy": start.SERVE_PROXY_TARGET}}}}},
            "second mount": {**route_document(), "Web": {f"node.ts.net:{port}": {"Handlers": {"/": {"Proxy": start.SERVE_PROXY_TARGET}, "/api": {"Proxy": "http://127.0.0.1:8080"}}}}},
        }
        for name, document in conflicts.items():
            with self.subTest(name=name):
                self.assertEqual(start.serve_route(document)["kind"], "conflict")

    def test_serve_route_treats_any_foreground_claim_as_conflict(self):
        port = str(start.SERVE_HTTP_PORT)
        # A foreground session cannot be adopted or ended by this workflow, so
        # even an identical-looking claim is refused rather than reused.
        conflicts = {
            "identical root proxy": foreground_document(),
            "other proxy": foreground_document(proxy="http://127.0.0.1:9999"),
            "other host": foreground_document(host="other.ts.net"),
            "tcp forwarder": foreground_document({"TCP": {port: {"TCPForward": "127.0.0.1:5173"}}}),
            "background route plus foreground": {**route_document(), **foreground_document()},
        }
        for name, document in conflicts.items():
            with self.subTest(name=name):
                self.assertEqual(start.serve_route(document)["kind"], "conflict", document)
        self.assertEqual(start.serve_route(foreground_document({})), {"kind": "absent"})
        other_port = foreground_document({
            "TCP": {"8443": {"HTTP": True}},
            "Web": {"node.ts.net:8443": {"Handlers": {"/": {"Proxy": start.SERVE_PROXY_TARGET}}}}})
        self.assertEqual(start.serve_route(other_port), {"kind": "absent"})

    def test_start_publishes_tailnet_route_and_records_ownership_first(self):
        events = []
        self.start_successfully(events)
        self.assertEqual(self.serve_commands, [(PUBLISH_ARGUMENTS, "real-device-start")])
        self.assertLess(events.index("monitor proxy"), events.index(f"tailscale serve {' '.join(PUBLISH_ARGUMENTS)}"))
        state = self.read_state()
        self.assertEqual(state["serve"], self.owned_serve())
        output = self.output.getvalue()
        self.assertIn("Ready for validation: http://127.0.0.1:5173/", output)
        self.assertIn("tailnet http://node.ts.net:8088/", output)

    def test_start_records_ownership_before_publishing(self):
        recorded = []
        def serve(*arguments, fault):
            recorded.append(self.read_state())
            self.apply_serve_command(*arguments, fault=fault)
        self.serve.side_effect = serve
        with patch.object(start, "launch"), patch.object(start, "wait_ready"):
            self.herdr.side_effect = [
                {"tab": {"tab_id": "w1:t2", "workspace_id": "w1"}, "root_pane": {"pane_id": "w1:p3"}},
                {"pane": {"pane_id": "w1:p4"}},
                {"pane": {"pane_id": "w1:p5"}},
            ]
            self.http.return_value = b'{"health":{"runtime":{"id":"old"}}}'
            start.start("wanda")
        self.assertEqual(recorded, [{"tab_id": "w1:t2", "root_pane_id": "w1:p3", "workspace_id": "w1",
                                     "worktree": str(self.root), "serve": self.owned_serve()}])

    def test_start_reuses_identical_tailnet_route_without_owning_it(self):
        self.serve_status = route_document()
        events = []
        self.start_successfully(events)
        self.assertEqual(self.serve_commands, [])
        self.assertNotIn("serve", self.read_state())
        output = self.output.getvalue()
        self.assertIn("reusing existing tailnet route http://node.ts.net:8088/", output)
        self.assertIn("tailnet http://node.ts.net:8088/", output)

    def test_start_fails_closed_before_any_tab_when_tailscale_is_unavailable(self):
        self.dns.side_effect = RuntimeError("real-device-start: tailscale status --json could not run: missing")
        with self.assertRaisesRegex(RuntimeError, "could not run"):
            start.start("wanda")
        self.dns.side_effect = None
        self.serve_status_mock.side_effect = RuntimeError("real-device-start: tailscale serve status --json failed: no output")
        with self.assertRaisesRegex(RuntimeError, "failed"):
            start.start("wanda")
        self.herdr.assert_not_called()
        self.assertEqual(self.serve_commands, [])
        self.assertEqual(list((self.root / "configs").iterdir()), [])

    def test_start_refuses_conflicting_tailnet_route_before_any_tab(self):
        cases = {
            "other proxy": route_document(proxy="http://127.0.0.1:9999"),
            "other host": route_document(host="other.ts.net"),
            "complex route": {**route_document(), "Web": {f"node.ts.net:{start.SERVE_HTTP_PORT}": {"Handlers": {"/": {"Text": "hello"}}}}},
            "foreground session": foreground_document(),
        }
        for name, document in cases.items():
            with self.subTest(name=name):
                self.serve_status = document
                with self.assertRaisesRegex(RuntimeError, "real-device-start: .*refusing to replace it"):
                    start.start("wanda")
                self.herdr.assert_not_called()
                self.assertEqual(self.serve_commands, [])
                self.assertEqual(list((self.root / "configs").iterdir()), [])

    def test_start_retains_ownership_record_when_publishing_fails(self):
        self.serve.side_effect = RuntimeError("real-device-start: tailscale serve failed: locked")
        with patch.object(start, "launch"), patch.object(start, "wait_ready"):
            self.herdr.side_effect = [
                {"tab": {"tab_id": "w1:t2", "workspace_id": "w1"}, "root_pane": {"pane_id": "w1:p3"}},
                {"pane": {"pane_id": "w1:p4"}},
                {"pane": {"pane_id": "w1:p5"}},
            ]
            self.http.return_value = b'{"health":{"runtime":{"id":"old"}}}'
            with self.assertRaisesRegex(RuntimeError, "tailscale serve failed"):
                start.start("wanda")
        self.assertEqual(self.read_state()["serve"], self.owned_serve())
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

    def test_stop_removes_only_the_owned_tailnet_route(self):
        state_path = self.record(serve=self.owned_serve())
        self.serve_status = route_document()
        events = []
        self.stop_with_tab(events)
        stop.stop()
        self.assertEqual(self.serve_commands, [(REMOVE_ARGUMENTS, "real-device-stop")])
        self.assertLess(events.index(("tailscale serve", *REMOVE_ARGUMENTS)), events.index(("tab", "close", "w1:t2")))
        self.assertFalse(state_path.exists())
        self.assertIn("tailnet route removed", self.output.getvalue())

    def test_stop_refuses_a_changed_tailnet_route_without_closing(self):
        cases = {
            "other proxy": route_document(proxy="http://127.0.0.1:9999"),
            "conflict": {**route_document(), "Web": {f"node.ts.net:{start.SERVE_HTTP_PORT}": {"Handlers": {"/": {"Text": "hello"}}}}},
            "foreground session": foreground_document(),
        }
        for name, document in cases.items():
            with self.subTest(name=name):
                state_path = self.record(serve=self.owned_serve())
                self.serve_status = document
                events = []
                self.stop_with_tab(events)
                with self.assertRaisesRegex(RuntimeError, "refusing to remove it; nothing closed"):
                    stop.stop()
                self.assertTrue(state_path.exists())
                self.assertEqual(self.serve_commands, [])
                self.assertNotIn(("tab", "close", "w1:t2"), events)
                state_path.unlink()

    def test_stop_closes_normally_when_the_owned_route_is_already_gone(self):
        state_path = self.record(serve=self.owned_serve())
        self.stop_herdr.side_effect = [
            {"tabs": [{"tab_id": "w1:t2", "label": "real-device-validation"}]},
            {"panes": [{"pane_id": "w1:p3", "tab_id": "w1:t2", "cwd": str(self.root)}]},
            {},
        ]
        stop.stop()
        self.assertEqual(self.serve_commands, [])
        self.assertFalse(state_path.exists())

    def test_stop_leaves_manual_routes_alone_for_legacy_records(self):
        state_path = self.record()
        self.serve_status = route_document()
        events = []
        self.stop_with_tab(events)
        stop.stop()
        self.assertEqual(self.serve_commands, [])
        self.assertIn(("tab", "close", "w1:t2"), events)
        self.assertFalse(state_path.exists())

    def test_stop_clears_owned_route_when_tab_already_closed(self):
        state_path = self.record(serve=self.owned_serve())
        self.serve_status = route_document()
        self.stop_herdr.return_value = {"tabs": []}
        stop.stop()
        self.assertEqual(self.serve_commands, [(REMOVE_ARGUMENTS, "real-device-stop")])
        self.assertFalse(state_path.exists())
        self.assertIn("tailnet route removed", self.output.getvalue())

    def test_stop_keeps_everything_open_when_tailscale_is_unreachable(self):
        state_path = self.record(serve=self.owned_serve())
        events = []
        self.stop_with_tab(events)
        self.stop_serve_status.side_effect = RuntimeError("real-device-stop: tailscale serve status --json failed: no output")
        with self.assertRaisesRegex(RuntimeError, "nothing closed; retry once Tailscale is reachable"):
            stop.stop()
        self.assertTrue(state_path.exists())
        self.assertEqual(self.serve_commands, [])
        self.assertNotIn(("tab", "close", "w1:t2"), events)

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
