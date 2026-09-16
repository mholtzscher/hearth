"""Simulator smoke regressions, isolated from Herdr and the network.

Every test runs against a temporary worktree with a mocked Herdr, transport,
and clock, so no service, pane, or port is ever touched. The cases pin the
safety properties of smoke.py: it refuses without mutating, it fails on wrong
Command or Entity Event evidence, it correlates the exact publish-response
Observation and Event IDs, it bounds every poll, it restores pause state, and
it preserves raw evidence plus summary.json.
"""

import io
import json
import os
from contextlib import ExitStack, redirect_stdout
from pathlib import Path
import tempfile
import unittest
import urllib.parse
from unittest.mock import patch

import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))

import smoke

POWER_ID = "ent-power"
EVENTS_ID = "ent-events"
TAB_ID = "w1:t2"
WORKSPACE_ID = "w1"
LABEL = "simulator-validation-deadbeef"
SOCKET = "/tmp/herdr.sock"
OLD_RECEIVED = "2026-09-15T20:36:02.372522901Z"
NEW_RECEIVED = "2026-09-15T20:36:23.909030987Z"


class FakeClock:
    """Deterministic monotonic budget plus a fixed, non-advancing wall clock."""

    def __init__(self, wall=1_700_000_000.0, mono=0.0):
        self.wall = wall
        self.mono = mono

    def monotonic(self):
        return self.mono

    def sleep(self, seconds):
        self.mono += seconds

    def time(self):
        return self.wall


class FakeResponse:
    def __init__(self, status, payload, headers=None):
        self.status = status
        self.headers = headers if headers is not None else {"content-type": "application/json"}
        self._body = payload if isinstance(payload, bytes) else json.dumps(payload).encode()

    def read(self):
        return self._body


class FakeHerdr:
    """Return one owned tab and its recorded panes for stop's ownership helper."""

    def __init__(self, *, root, pane_ids, tab_id=TAB_ID, label=LABEL, workspace=WORKSPACE_ID,
                 extra_panes=()):
        self.root = root
        self.pane_ids = list(pane_ids)
        self.tab_id = tab_id
        self.label = label
        self.workspace = workspace
        self.extra_panes = list(extra_panes)

    def __call__(self, *args):
        if args[:2] == ("tab", "list"):
            return {"tabs": [{"tab_id": self.tab_id, "label": self.label,
                              "workspace_id": self.workspace}]}
        if args[:2] == ("pane", "list"):
            panes = [{"pane_id": pane_id, "tab_id": self.tab_id, "cwd": str(self.root)}
                     for pane_id in self.pane_ids]
            panes.extend(self.extra_panes)
            return {"panes": panes}
        return {}


class Stack:
    """A configurable in-memory stand-in for the loopback control and Core APIs."""

    def __init__(self):
        self.power_id = POWER_ID
        self.events_id = EVENTS_ID
        self.power_value = True
        self.power_observation = "obs-initial"
        self.baseline_observation = "obs-baseline"
        # What Core reports once the baseline publication lands; differs from
        # baseline_observation only to prove the exact-ID correlation.
        self.core_baseline_observation = "obs-baseline"
        self.paused = {POWER_ID: False, EVENTS_ID: False}
        self.command_status = "satisfied"
        self.outcome_observation_id = "obs-outcome"
        self.final_observation_id = "obs-outcome"
        self.final_value = True
        self.history_disposition = "applied"
        self.history_has_outcome = True
        self.events_state = None
        self.new_event_name = "single_press"
        self.new_event_disposition = "accepted"
        self.new_event_received_at = NEW_RECEIVED
        self.inject_event = True
        self.injected_event_id = "evt-new-1"
        self.extra_events = []
        # Publish-response ID fields: None omits the field, a value overrides it,
        # and the *_wrong_field knobs inject the opposite Entity's ID field.
        self.state_publish_id = self.baseline_observation
        self.state_publish_wrong_field = None
        self.event_publish_id = self.injected_event_id
        self.event_publish_wrong_field = None
        self.resume_fails = False
        self.inventory_override = None
        self.events = [
            {"event_id": "evt-old-1", "entity_id": EVENTS_ID, "name": "double_press",
             "disposition": "accepted", "emitted_at": OLD_RECEIVED, "received_at": OLD_RECEIVED,
             "recorded_at": OLD_RECEIVED},
            {"event_id": "evt-old-2", "entity_id": EVENTS_ID, "name": "single_press",
             "disposition": "accepted", "emitted_at": OLD_RECEIVED, "received_at": OLD_RECEIVED,
             "recorded_at": OLD_RECEIVED},
        ]
        self.history = [
            {"observation_id": "obs-initial", "value": True, "disposition": "applied",
             "adapter_received_at": OLD_RECEIVED, "observed_at": OLD_RECEIVED},
        ]
        self.commands = {}

    # --- payloads ----------------------------------------------------------

    def power_entity(self):
        return {"id": self.power_id, "type": smoke.POWER_TYPE,
                "state": {"value": self.power_value, "observation_id": self.power_observation,
                          "adapter_received_at": OLD_RECEIVED, "observed_at": OLD_RECEIVED}}

    def events_entity(self):
        return {"id": self.events_id, "type": smoke.EVENTS_TYPE, "state": self.events_state}

    def snapshot(self, entity_id, current=None):
        if entity_id == self.power_id:
            return {"binding_key": smoke.POWER_BINDING_KEY, "key": smoke.POWER_KEY,
                    "entity_id": entity_id, "entity_type": smoke.POWER_TYPE,
                    "event_source": False, "paused": self.paused[entity_id],
                    "current": self.power_value}
        return {"binding_key": smoke.EVENTS_BINDING_KEY, "key": smoke.EVENTS_KEY,
                "entity_id": entity_id, "entity_type": smoke.EVENTS_TYPE,
                "event_source": True, "paused": self.paused[entity_id],
                "current": current if current is not None else "single_press"}

    def inventory(self):
        if self.inventory_override is not None:
            return self.inventory_override
        return [
            {"binding_key": smoke.POWER_BINDING_KEY, "key": smoke.POWER_KEY,
             "entity_id": self.power_id, "entity_type": smoke.POWER_TYPE,
             "event_source": False, "paused": self.paused[self.power_id],
             "current": self.power_value},
            {"binding_key": smoke.EVENTS_BINDING_KEY, "key": smoke.EVENTS_KEY,
             "entity_id": self.events_id, "entity_type": smoke.EVENTS_TYPE,
             "event_source": True, "paused": self.paused[self.events_id],
             "current": "single_press"},
        ]

    # --- routing -----------------------------------------------------------

    def handle(self, method, path, query, body):
        if path == "/v1/sim/entities" and method == "GET":
            return 200, self.inventory()
        if path.startswith("/v1/sim/entities/") and method == "POST":
            parts = path.split("/")
            entity_id, action = parts[4], parts[5]
            if action == "pause":
                self.paused[entity_id] = True
                return 200, self.snapshot(entity_id)
            if action == "resume":
                if self.resume_fails:
                    return 500, {"error": "resume refused"}
                self.paused[entity_id] = False
                return 200, self.snapshot(entity_id)
            if action == "publish":
                return self._publish(entity_id, body or {})
        if path == f"/v1/entities/{self.power_id}/commands" and method == "POST":
            return self._execute_command()
        if path.startswith("/v1/commands/") and method == "GET":
            command = self.commands.get(path.rsplit("/", 1)[1])
            if command is None:
                return 404, {"error": "not found"}
            return 200, command
        if path.endswith("/events") and method == "GET":
            return 200, {"items": list(self.events)}
        if path.endswith("/state/history") and method == "GET":
            return 200, {"items": list(self.history)}
        if path.startswith("/v1/entities/") and method == "GET":
            entity_id = path.rsplit("/", 1)[1]
            if entity_id == self.power_id:
                return 200, self.power_entity()
            return 200, self.events_entity()
        return 404, {"error": f"unhandled {method} {path}"}

    def _publish(self, entity_id, body):
        if entity_id == self.power_id:
            self.power_value = body.get("value")
            self.power_observation = self.core_baseline_observation
            payload = self.snapshot(entity_id)
            if self.state_publish_id is not None:
                payload["observation_id"] = self.state_publish_id
            if self.state_publish_wrong_field is not None:
                payload["event_id"] = self.state_publish_wrong_field
            return 200, payload
        if self.inject_event:
            self.events.insert(0, {
                "event_id": self.injected_event_id, "entity_id": entity_id,
                "name": self.new_event_name, "disposition": self.new_event_disposition,
                "emitted_at": self.new_event_received_at,
                "received_at": self.new_event_received_at,
                "recorded_at": self.new_event_received_at,
            })
        for event in self.extra_events:
            self.events.insert(0, dict(event))
        payload = self.snapshot(entity_id, current=body.get("name"))
        if self.event_publish_id is not None:
            payload["event_id"] = self.event_publish_id
        if self.event_publish_wrong_field is not None:
            payload["observation_id"] = self.event_publish_wrong_field
        return 200, payload

    def _execute_command(self):
        outcome = self.outcome_observation_id
        self.commands["cmd-1"] = {
            "id": "cmd-1", "entity_id": self.power_id, "operation": "set",
            "parameters": {"value": True}, "status": self.command_status,
            "requested_at": OLD_RECEIVED, "completed_at": NEW_RECEIVED,
            "outcome_observation_id": outcome if self.command_status == "satisfied" else None,
        }
        if self.command_status == "satisfied":
            self.power_value = self.final_value
            self.power_observation = self.final_observation_id
            if self.history_has_outcome:
                self.history.insert(0, {"observation_id": outcome, "value": self.final_value,
                                        "disposition": self.history_disposition,
                                        "adapter_received_at": NEW_RECEIVED,
                                        "observed_at": NEW_RECEIVED})
        return 200, {"command_id": "cmd-1", "status": self.command_status,
                     "observation_id": outcome if self.command_status == "satisfied" else None,
                     "value": self.final_value if self.command_status == "satisfied" else None}


class FakeTransport:
    def __init__(self, stack):
        self.stack = stack
        self.calls = []

    def __call__(self, request, timeout=None):
        method = request.get_method()
        parts = urllib.parse.urlsplit(request.full_url)
        body = json.loads(request.data.decode()) if request.data else None
        self.calls.append({"method": method, "path": parts.path, "body": body,
                           "timeout": timeout})
        status, payload = self.stack.handle(method, parts.path,
                                            urllib.parse.parse_qs(parts.query), body)
        return FakeResponse(status, payload)


class SmokeTest(unittest.TestCase):
    def setUp(self):
        self.exit_stack = ExitStack()
        self.addCleanup(self.exit_stack.close)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / ".data").mkdir()
        self.run_dir = self.root / ".data" / "simulator-validation.test"
        self.run_dir.mkdir()
        self.tab_id = TAB_ID
        self.label = LABEL
        self.pane_ids = ["w1:p1", "w1:p2", "w1:p3"]
        self.write_record()
        self.stack = Stack()
        self.transport = FakeTransport(self.stack)
        self.herdr = FakeHerdr(root=self.root, pane_ids=self.pane_ids)
        self.clock = FakeClock()
        self.exit_stack.enter_context(patch.object(smoke.start, "ROOT", self.root))
        self.exit_stack.enter_context(patch.object(smoke.start, "session_socket", lambda: SOCKET))
        self.exit_stack.enter_context(patch.object(smoke, "herdr", self.herdr))
        self.exit_stack.enter_context(patch.object(smoke, "open_url", self.transport))
        self.output = io.StringIO()
        self.exit_stack.enter_context(redirect_stdout(self.output))

    # --- helpers -----------------------------------------------------------

    def write_record(self, **overrides):
        record = {
            "version": 1, "status": "running", "worktree": str(self.root),
            "session_socket": SOCKET, "workspace_id": WORKSPACE_ID, "tab_id": self.tab_id,
            "tab_label": self.label, "run_dir": str(self.run_dir), "mode": "scripted",
            "devices_path": None, "adapter_id": "simulator",
            "panes": {"nats": "w1:p1", "core": "w1:p2", "simulator": "w1:p3"},
            "pane_ids": list(self.pane_ids), "root_pane_id": "w1:p1",
            "commands": {}, "created_at": "2026-01-01T00:00:00Z",
        }
        record.update(overrides)
        (self.root / ".data" / "simulator-validation.json").write_text(json.dumps(record))
        return record

    def smoke(self, **overrides):
        params = {
            "clock": self.clock, "poll_interval": 0.05, "http_read_timeout": 1.0,
            "http_mutation_timeout": 1.0, "baseline_timeout": 2.0, "command_timeout": 2.0,
            "event_timeout": 2.0,
        }
        params.update(overrides)
        return smoke.Smoke(**params)

    def run_smoke(self, runner):
        return runner.execute()

    def smoke_dirs(self):
        return sorted(self.run_dir.glob("smoke.*"))

    def summary_file(self):
        directories = self.smoke_dirs()
        self.assertTrue(directories, "a smoke.* evidence directory must be created")
        return directories[0] / "summary.json"

    def calls(self):
        return [(call["method"], call["path"]) for call in self.transport.calls]

    # --- happy path --------------------------------------------------------

    def test_default_stack_passes_and_preserves_evidence(self):
        summary = self.run_smoke(self.smoke())
        self.assertTrue(summary["passed"], summary["failures"])
        self.assertEqual(summary["status"], "passed")
        self.assertIn("power_entity_id", summary["ids"])
        self.assertEqual(summary["ids"]["command_status"], "satisfied")
        self.assertEqual(summary["ids"]["outcome_observation_id"], "obs-outcome")
        self.assertEqual(summary["ids"]["baseline_observation_id"], "obs-baseline")
        self.assertEqual(summary["ids"]["published_event_id"], "evt-new-1")
        self.assertEqual(summary["ids"]["injected_event_id"], "evt-new-1")
        self.assertEqual(summary["ids"]["injected_event_name"], "single_press")
        self.assertGreater(summary["elapsed_seconds"], -1)

        directory = self.smoke_dirs()[0]
        self.assertEqual(len(self.smoke_dirs()), 1)
        self.assertTrue((directory / "summary.json").exists())
        self.assertTrue(list(directory.glob("*.request.json")), "raw requests must be saved")
        self.assertTrue(list(directory.glob("*.response.json")), "raw responses must be saved")
        self.assertEqual(json.loads((directory / "summary.json").read_text())["passed"], True)

        # Mutations run paused and in order, then both tickers are resumed.
        paths = [path for _, path in self.calls()]
        self.assertIn(f"/v1/sim/entities/{POWER_ID}/pause", paths)
        self.assertIn(f"/v1/entities/{POWER_ID}/commands", paths)
        self.assertLess(paths.index(f"/v1/sim/entities/{POWER_ID}/pause"),
                        paths.index(f"/v1/entities/{POWER_ID}/commands"))
        self.assertEqual(summary["cleanup"]["errors"], [])
        self.assertEqual(sorted(summary["cleanup"]["restored"]), sorted([POWER_ID, EVENTS_ID]))

    def test_publish_response_event_id_is_correlated_exactly(self):
        summary = self.run_smoke(self.smoke())
        self.assertTrue(summary["passed"], summary["failures"])
        self.assertEqual(summary["ids"]["published_event_id"], "evt-new-1")
        self.assertEqual(summary["ids"]["injected_event_id"], "evt-new-1")
        self.assertEqual(summary["ids"]["injected_event_disposition"], "accepted")
        self.assertNotIn("limitations", summary)

    def test_missing_state_observation_id_cannot_pass(self):
        self.stack.state_publish_id = None
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("omitted observation_id" in failure
                            for failure in summary["failures"]), summary["failures"])
        self.assertEqual(summary["cleanup"]["restored"], [POWER_ID])

    def test_baseline_observation_id_must_match_core(self):
        self.stack.core_baseline_observation = "obs-other"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("did not appear in Core" in failure
                            for failure in summary["failures"]), summary["failures"])

    def test_state_publish_with_event_id_cannot_pass(self):
        self.stack.state_publish_id = None
        self.stack.state_publish_wrong_field = "evt-mixup"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("with event_id but no observation_id" in failure
                            for failure in summary["failures"]), summary["failures"])

    def test_publish_with_both_ids_cannot_pass(self):
        self.stack.state_publish_wrong_field = "evt-both"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("carries both" in failure
                            for failure in summary["failures"]), summary["failures"])

    def test_missing_event_id_in_publish_response_cannot_pass(self):
        self.stack.event_publish_id = None
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("omitted event_id" in failure
                            for failure in summary["failures"]), summary["failures"])

    def test_event_publish_with_observation_id_cannot_pass(self):
        self.stack.event_publish_id = None
        self.stack.event_publish_wrong_field = "obs-mixup"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("with observation_id but no event_id" in failure
                            for failure in summary["failures"]), summary["failures"])

    # --- command failures --------------------------------------------------

    def test_wrong_command_status_cannot_pass(self):
        self.stack.command_status = "rejected"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("rejected" in failure for failure in summary["failures"]),
                        summary["failures"])
        # The Command fails before the event ticker is paused, so only power resumes.
        self.assertEqual(summary["cleanup"]["restored"], [POWER_ID])

    def test_stale_state_outcome_id_cannot_pass(self):
        self.stack.final_observation_id = "obs-stale"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("does not match" in failure for failure in summary["failures"]),
                        summary["failures"])

    def test_missing_applied_history_entry_cannot_pass(self):
        self.stack.history_has_outcome = False
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("absent from State history" in failure for failure in summary["failures"]),
                        summary["failures"])

    def test_command_timeout_is_bounded(self):
        self.stack.command_status = "accepted"
        summary = self.run_smoke(self.smoke(command_timeout=0.2))
        self.assertFalse(summary["passed"])
        self.assertTrue(any("terminal status within" in failure for failure in summary["failures"]),
                        summary["failures"])
        self.assertLess(self.clock.mono, 1.0, "polling must be bounded")
        self.assertIn(POWER_ID, summary["cleanup"]["restored"])

    # --- entity event failures ---------------------------------------------

    def test_rejected_event_cannot_pass(self):
        self.stack.new_event_disposition = "rejected"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("expected accepted" in failure for failure in summary["failures"]),
                        summary["failures"])

    def test_wrong_event_name_cannot_pass(self):
        self.stack.new_event_name = "double_press"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("name is" in failure for failure in summary["failures"]),
                        summary["failures"])

    def test_wrong_event_id_cannot_pass(self):
        self.stack.event_publish_id = "evt-other"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("was not recorded accepted" in failure
                            for failure in summary["failures"]), summary["failures"])

    def test_missing_injected_event_cannot_pass(self):
        self.stack.inject_event = False
        summary = self.run_smoke(self.smoke(event_timeout=0.2))
        self.assertFalse(summary["passed"])
        self.assertTrue(any("was not recorded accepted" in failure
                            for failure in summary["failures"]), summary["failures"])

    def test_unrelated_same_name_events_do_not_substitute_or_fail(self):
        self.stack.extra_events = [{
            "event_id": "evt-unrelated", "entity_id": EVENTS_ID, "name": "single_press",
            "disposition": "accepted", "emitted_at": NEW_RECEIVED, "received_at": NEW_RECEIVED,
            "recorded_at": NEW_RECEIVED,
        }]
        summary = self.run_smoke(self.smoke())
        self.assertTrue(summary["passed"], summary["failures"])
        self.assertEqual(summary["ids"]["injected_event_id"], "evt-new-1")

    def test_unrelated_same_name_event_cannot_substitute(self):
        self.stack.inject_event = False
        self.stack.extra_events = [{
            "event_id": "evt-unrelated", "entity_id": EVENTS_ID, "name": "single_press",
            "disposition": "accepted", "emitted_at": NEW_RECEIVED, "received_at": NEW_RECEIVED,
            "recorded_at": NEW_RECEIVED,
        }]
        summary = self.run_smoke(self.smoke(event_timeout=0.2))
        self.assertFalse(summary["passed"])
        self.assertTrue(any("was not recorded accepted" in failure
                            for failure in summary["failures"]), summary["failures"])

    def test_non_null_event_state_cannot_pass(self):
        self.stack.events_state = {"value": "unexpected"}
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("must be null" in failure for failure in summary["failures"]),
                        summary["failures"])

    # --- refusal without mutation ------------------------------------------

    def test_missing_record_refuses_without_mutation(self):
        (self.root / ".data" / "simulator-validation.json").unlink()
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("no simulator ownership record" in failure
                            for failure in summary["failures"]), summary["failures"])
        self.assertEqual(self.transport.calls, [])

    def test_malformed_record_refuses_without_mutation(self):
        (self.root / ".data" / "simulator-validation.json").write_text("[]")
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("malformed" in failure for failure in summary["failures"]),
                        summary["failures"])
        self.assertEqual(self.transport.calls, [])

    def test_foreign_worktree_refuses_without_mutation(self):
        self.write_record(worktree="/somewhere/else")
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("worktree" in failure for failure in summary["failures"]),
                        summary["failures"])
        self.assertEqual(self.transport.calls, [])

    def test_ownership_mismatch_refuses_without_mutation(self):
        self.herdr.extra_panes = [{"pane_id": "w1:p9", "tab_id": TAB_ID, "cwd": str(self.root)}]
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("ownership verification failed" in failure
                            for failure in summary["failures"]), summary["failures"])
        self.assertEqual(self.transport.calls, [])

    def test_full_preset_record_refuses_without_mutation(self):
        self.write_record(mode="full")
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("default scripted stack required" in failure
                            for failure in summary["failures"]), summary["failures"])
        self.assertEqual(self.transport.calls, [])

    def test_custom_devices_record_refuses_without_mutation(self):
        self.write_record(mode="custom", devices_path=str(self.root / "custom.yaml"))
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("default scripted stack required" in failure
                            for failure in summary["failures"]), summary["failures"])
        self.assertEqual(self.transport.calls, [])

    def test_non_default_inventory_refuses_without_mutation(self):
        self.stack.inventory_override = [
            {"binding_key": "demo-light", "key": "power", "entity_id": "ent-demo",
             "entity_type": "hearth.power/v1", "event_source": False, "paused": False,
             "current": True},
        ]
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(any("default scripted stack required" in failure
                            for failure in summary["failures"]), summary["failures"])
        mutated = [path for _, path in self.calls() if path.endswith(("/pause", "/commands"))]
        self.assertEqual(mutated, [], "identity rejection must happen before mutation")

    def test_wrong_entity_type_refuses_without_mutation(self):
        self.stack.inventory_override = [
            {"binding_key": smoke.POWER_BINDING_KEY, "key": smoke.POWER_KEY,
             "entity_id": POWER_ID, "entity_type": "hearth.switch/v1", "event_source": False,
             "paused": False, "current": True},
            {"binding_key": smoke.EVENTS_BINDING_KEY, "key": smoke.EVENTS_KEY,
             "entity_id": EVENTS_ID, "entity_type": smoke.EVENTS_TYPE, "event_source": True,
             "paused": False, "current": "single_press"},
        ]
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        mutated = [path for _, path in self.calls() if path.endswith(("/pause", "/commands"))]
        self.assertEqual(mutated, [])

    # --- pause restoration -------------------------------------------------

    def test_only_originally_unpaused_entities_are_resumed(self):
        self.stack.paused[EVENTS_ID] = True
        summary = self.run_smoke(self.smoke())
        self.assertTrue(summary["passed"], summary["failures"])
        self.assertEqual(summary["cleanup"]["restored"], [POWER_ID])
        self.assertEqual(summary["ids"]["original_paused"], {"power": False, "events": True})
        self.assertEqual(summary["cleanup"]["original_paused"],
                         {"power": False, "events": True})

    def test_restore_happens_on_failure(self):
        self.stack.command_status = "internal_failure"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertEqual(summary["cleanup"]["restored"], [POWER_ID])

    def test_cleanup_failure_fails_the_run(self):
        self.stack.resume_fails = True
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        self.assertTrue(summary["cleanup"]["errors"])
        self.assertIn("CLEANUP-ERROR", self.output.getvalue())

    # --- evidence ----------------------------------------------------------

    def test_evidence_and_summary_preserved_on_failure(self):
        self.stack.command_status = "rejected"
        summary = self.run_smoke(self.smoke())
        self.assertFalse(summary["passed"])
        directory = self.smoke_dirs()[0]
        saved = json.loads((directory / "summary.json").read_text())
        self.assertFalse(saved["passed"])
        self.assertIn("failures", saved)
        response_files = sorted(directory.glob("*command*.response.json"))
        self.assertTrue(response_files, "the failing command response must be preserved")
        recorded = json.loads(response_files[-1].read_text())
        self.assertEqual(recorded["status"], 200)

    def test_elapsed_and_steps_are_reported(self):
        summary = self.run_smoke(self.smoke())
        self.assertIn("elapsed_seconds", summary)
        self.assertTrue(summary["steps"])
        self.assertTrue(all(set(step) >= {"name", "status", "detail"} for step in summary["steps"]))
        names = [step["name"] for step in summary["steps"]]
        self.assertEqual(names[:3], ["ownership", "default-scenario", "inventory"])
        self.assertIn("event-verification", names)
        self.assertIn("evidence", self.output.getvalue())


if __name__ == "__main__":
    unittest.main()
