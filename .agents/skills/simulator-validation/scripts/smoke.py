#!/usr/bin/env python3
"""Bounded default-only smoke validation for a running simulator stack.

Requires a stack already started by ``mise run simulator-start`` with its
default scripted Devices. This script never starts or stops services. It
verifies the recorded ownership and the default Entity identities, drives
exactly one Command and one manual Entity Event through the loopback control
API, and asserts Core's durable Command, State, and Entity Event evidence.
Control publishes return the canonical Observation/Event ID Core will record,
so the baseline and the injected event are correlated by that exact ID instead
of by history differencing or arrival time.

Raw requests and responses, including error responses, are saved under a
unique ``smoke.*`` directory inside the run directory, together with a
``summary.json`` describing pass/fail, steps, IDs, and elapsed time.
"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parent))

import start
import stop

# Fixed loopback endpoints owned by `mise run simulator-start`.
CORE_URL = "http://127.0.0.1:8080"
SIM_URL = "http://127.0.0.1:8181"

# Default scripted preset identities; anything else is rejected as non-default.
POWER_BINDING_KEY = "simulated-light"
POWER_KEY = "power"
POWER_TYPE = "hearth.power/v1"
EVENTS_BINDING_KEY = "simulated-button"
EVENTS_KEY = "events"
EVENTS_TYPE = "hearth.enumevent/v1"
EVENT_NAME = "single_press"

# Non-terminal Command statuses; anything else ends the durable poll.
NON_TERMINAL_COMMAND_STATUSES = frozenset({"requested", "accepted"})

# A control publish response is the flat EntityInfo snapshot plus exactly one
# of these canonical ID fields: State publishes return STATE_PUBLISH_ID_FIELD
# and event publishes return EVENT_PUBLISH_ID_FIELD.
STATE_PUBLISH_ID_FIELD = "observation_id"
EVENT_PUBLISH_ID_FIELD = "event_id"


class SmokeFailure(Exception):
    """A bounded check failed; the run stops instead of retrying a mutation."""


def herdr(*args):
    """Resolve a Herdr command through start.py so tests can replace it."""
    return start.herdr(*args)


class SystemClock:
    """Real monotonic budgeting, sleep, and wall time."""

    def monotonic(self):
        return time.monotonic()

    def sleep(self, seconds):
        time.sleep(seconds)

    def time(self):
        return time.time()


def format_epoch(epoch):
    """Render an epoch second as an RFC 3339 UTC timestamp."""
    if epoch is None:
        return None
    return datetime.fromtimestamp(epoch, timezone.utc).isoformat().replace("+00:00", "Z")


def _nonempty_id(value):
    """Return a trimmed canonical ID string, or None when absent or blank."""
    return value.strip() if isinstance(value, str) and value.strip() else None


def _parse_json(text):
    try:
        return json.loads(text)
    except (TypeError, ValueError):
        return None


def _headers_dict(headers):
    if headers is None:
        return {}
    try:
        return {str(key): str(value) for key, value in headers.items()}
    except AttributeError:
        return dict(headers)


def open_url(request, timeout):
    """Single transport seam so tests can replace HTTP without a live service."""
    return urllib.request.urlopen(request, timeout=timeout)


@dataclass
class HttpResult:
    status: object
    headers: dict
    text: str
    data: object
    error: object = None

    def explain(self):
        if self.error:
            return f"network error: {self.error}"
        return f"status {self.status}: {self.text[:200]}"


class Evidence:
    """Numbered raw request/response capture inside one smoke run directory."""

    def __init__(self, directory):
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        self._sequence = 0

    def next_label(self, slug):
        self._sequence += 1
        safe = re.sub(r"[^a-z0-9]+", "-", (slug or "call").lower()).strip("-") or "call"
        return f"{self._sequence:02d}-{safe}"

    def write(self, name, value):
        path = self.directory / name
        path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
        return path

    def record(self, label, request, response):
        self.write(label + ".request.json", request)
        self.write(label + ".response.json", response)


class HttpClient:
    """Loopback HTTP that always records what it sent and received.

    Mutations are never retried; callers poll GET endpoints with a bounded
    deadline instead.
    """

    def __init__(self, evidence, base_url, read_timeout, mutation_timeout):
        self.evidence = evidence
        self.base_url = base_url
        self.read_timeout = read_timeout
        self.mutation_timeout = mutation_timeout

    def get(self, path, name):
        return self._request("GET", path, None, name, self.read_timeout)

    def post(self, path, body, name):
        return self._request("POST", path, body, name, self.mutation_timeout)

    def _request(self, method, path, body, name, timeout):
        url = self.base_url + path
        label = self.evidence.next_label(name)
        payload = None if body is None else json.dumps(body).encode("utf-8")
        request = urllib.request.Request(url, data=payload, method=method)
        if payload is not None:
            request.add_header("content-type", "application/json")
        status = None
        headers = {}
        text = ""
        data = None
        error = None
        try:
            response = open_url(request, timeout)
            status = getattr(response, "status", None)
            headers = _headers_dict(getattr(response, "headers", None))
            text = response.read().decode("utf-8", "replace")
            data = _parse_json(text)
        except urllib.error.HTTPError as http_error:
            status = http_error.code
            headers = _headers_dict(http_error.headers)
            text = http_error.read().decode("utf-8", "replace")
            data = _parse_json(text)
        except (urllib.error.URLError, OSError, ValueError) as transport_error:
            error = str(transport_error)
        self.evidence.record(
            label,
            {"method": method, "url": url, "body": body},
            {"status": status, "headers": headers, "body": text, "error": error},
        )
        return HttpResult(status=status, headers=headers, text=text, data=data, error=error)


class Smoke:
    """One bounded, default-only Command plus Entity Event validation run."""

    def __init__(
        self,
        clock=None,
        poll_interval=0.5,
        http_read_timeout=5.0,
        http_mutation_timeout=10.0,
        baseline_timeout=20.0,
        command_timeout=30.0,
        event_timeout=30.0,
    ):
        self.clock = clock if clock is not None else SystemClock()
        self.poll_interval = poll_interval
        self.http_read_timeout = http_read_timeout
        self.http_mutation_timeout = http_mutation_timeout
        self.baseline_timeout = baseline_timeout
        self.command_timeout = command_timeout
        self.event_timeout = event_timeout

        self.record = None
        self.run_dir = None
        self.smoke_dir = None
        self.evidence = None
        self.core = None
        self.sim = None
        self.power_id = None
        self.events_id = None
        self.original_paused = {"power": False, "events": False}
        self.paused_by_us = []
        self.steps = []
        self.ids = {}
        self.cleanup = {"restored": [], "errors": [], "original_paused": {}}
        self._published_event_id = None
        self.started = 0.0
        self.started_at = None
        self.passed = False
        self.exit_code = 1

    # --- steps ---------------------------------------------------------------

    def _step(self, name, action):
        """Run one named check, recording its pass/fail for the summary."""
        try:
            detail = action()
        except SmokeFailure as error:
            self.steps.append({"name": name, "status": "failed", "detail": str(error)})
            raise
        self.steps.append({"name": name, "status": "passed", "detail": detail})
        return detail

    # --- ownership -----------------------------------------------------------

    def _load_record(self):
        """Require a valid local ownership record created by simulator-start."""
        if not start.state_path().exists():
            raise SmokeFailure(
                "no simulator ownership record; run `mise run simulator-start` first")
        record = start.read_state()
        if not isinstance(record, dict):
            raise SmokeFailure("malformed simulator ownership record")
        if record.get("worktree") != str(start.ROOT):
            raise SmokeFailure(
                f"ownership record worktree {record.get('worktree')!r} is not this worktree "
                f"{str(start.ROOT)!r}")
        recorded_socket = record.get("session_socket")
        current_socket = start.session_socket()
        if recorded_socket and current_socket and recorded_socket != current_socket:
            raise SmokeFailure(
                f"ownership record session {recorded_socket} does not match current session "
                f"{current_socket}")
        if not record.get("tab_id"):
            raise SmokeFailure("ownership record has no tab; the stack is not running")
        run_dir = record.get("run_dir")
        if not run_dir or not Path(run_dir).is_dir():
            raise SmokeFailure("ownership record run_dir is missing")
        return record

    def _verify_ownership(self):
        """Verify the owned tab and panes before any mutation, without stopping."""
        record = self.record
        workspace = record["workspace_id"]
        try:
            tabs = herdr("tab", "list", "--workspace", workspace)["tabs"]
        except (subprocess.CalledProcessError, KeyError, ValueError, OSError) as error:
            raise SmokeFailure(f"cannot list Herdr tabs: {error}")
        tab = next((item for item in tabs if item.get("tab_id") == record["tab_id"]), None)
        if tab is None:
            raise SmokeFailure(f"owned tab {record['tab_id']} is not present")
        try:
            panes = herdr("pane", "list", "--workspace", workspace)["panes"]
        except (subprocess.CalledProcessError, KeyError, ValueError, OSError) as error:
            raise SmokeFailure(f"cannot list Herdr panes: {error}")
        try:
            stop.verify_tab_ownership(record, tab, panes)
        except RuntimeError as error:
            raise SmokeFailure(f"ownership verification failed: {error}")
        return f"tab {record['tab_id']} with {len(stop.recorded_pane_ids(record))} recorded panes"

    def _check_default_scenario(self):
        """Reject the full preset, custom devices, or any non-default record."""
        mode = self.record.get("mode")
        devices_path = self.record.get("devices_path")
        if mode != "scripted" or devices_path:
            raise SmokeFailure(
                f"default scripted stack required: record mode={mode!r} "
                f"devices_path={devices_path!r}; start without --preset full or --devices")
        return f"mode={mode}"

    # --- inventory -----------------------------------------------------------

    def _discover_entities(self):
        """Discover canonical IDs and require the exact default identities."""
        response = self.sim.get("/v1/sim/entities", "sim-entities")
        if response.status != 200 or not isinstance(response.data, list):
            raise SmokeFailure(f"control inventory unavailable: {response.explain()}")
        entries = [entry for entry in response.data if isinstance(entry, dict)]
        power = [entry for entry in entries
                 if entry.get("binding_key") == POWER_BINDING_KEY and entry.get("key") == POWER_KEY]
        events = [entry for entry in entries
                  if entry.get("binding_key") == EVENTS_BINDING_KEY and entry.get("key") == EVENTS_KEY]
        found = sorted({(entry.get("binding_key"), entry.get("key"), entry.get("entity_type"))
                        for entry in entries})
        if (len(power) != 1 or power[0].get("entity_type") != POWER_TYPE
                or power[0].get("event_source") is not False
                or not isinstance(power[0].get("entity_id"), str)):
            raise SmokeFailure(
                "default scripted stack required: expected exactly one "
                f"{POWER_BINDING_KEY}/{POWER_KEY} State Entity of type {POWER_TYPE}; found {found!r}")
        if (len(events) != 1 or events[0].get("entity_type") != EVENTS_TYPE
                or events[0].get("event_source") is not True
                or not isinstance(events[0].get("entity_id"), str)):
            raise SmokeFailure(
                "default scripted stack required: expected exactly one "
                f"{EVENTS_BINDING_KEY}/{EVENTS_KEY} event Entity of type {EVENTS_TYPE}; "
                f"found {found!r}")
        self.power_id = power[0]["entity_id"]
        self.events_id = events[0]["entity_id"]
        self.original_paused = {
            "power": power[0].get("paused") is True,
            "events": events[0].get("paused") is True,
        }
        self.cleanup["original_paused"] = dict(self.original_paused)
        self.ids["power_entity_id"] = self.power_id
        self.ids["events_entity_id"] = self.events_id
        self.ids["original_paused"] = dict(self.original_paused)
        return f"power={self.power_id} events={self.events_id}"

    # --- pause and baseline --------------------------------------------------

    def _pause_entity(self, entity_id, slug, role):
        """Pause one ticker unless it was already paused before this run."""
        if self.original_paused.get(role):
            return f"{slug} was already paused; left paused"
        response = self.sim.post(f"/v1/sim/entities/{entity_id}/pause", None, f"{slug}-pause")
        if response.status != 200 or not isinstance(response.data, dict):
            raise SmokeFailure(f"pause {slug} failed: {response.explain()}")
        if response.data.get("paused") is not True:
            raise SmokeFailure(f"pause {slug} did not confirm paused")
        self.paused_by_us.append(entity_id)
        return f"{slug} paused"

    def _establish_baseline(self):
        """Publish a false State and correlate its exact returned Observation in Core."""
        publish = self.sim.post(
            f"/v1/sim/entities/{self.power_id}/publish", {"value": False},
            "power-baseline-publish")
        baseline = self._require_publish_id(
            publish, STATE_PUBLISH_ID_FIELD, "a State Entity", "baseline publish")
        self.ids["baseline_observation_id"] = baseline
        self._wait_baseline(baseline)
        return f"baseline observation {baseline}"

    def _require_publish_id(self, response, expected_field, entity_label, action):
        """Return the one canonical ID a successful publish response must carry.

        A State Entity must return ``observation_id`` and an event Entity must
        return ``event_id``; a response with the wrong field, both fields, or
        neither fails instead of faking a correlation.
        """
        if response.status != 200 or not isinstance(response.data, dict):
            raise SmokeFailure(f"{action} failed: {response.explain()}")
        body = response.data
        other_field = (EVENT_PUBLISH_ID_FIELD if expected_field == STATE_PUBLISH_ID_FIELD
                       else STATE_PUBLISH_ID_FIELD)
        expected = _nonempty_id(body.get(expected_field))
        other = _nonempty_id(body.get(other_field))
        if expected and other:
            raise SmokeFailure(
                f"{action} response carries both {STATE_PUBLISH_ID_FIELD} and "
                f"{EVENT_PUBLISH_ID_FIELD}; expected exactly one")
        if other:
            raise SmokeFailure(
                f"{action} response is a flat EntityInfo snapshot with {other_field} but no "
                f"{expected_field}; {entity_label} must publish {expected_field}")
        if not expected:
            raise SmokeFailure(
                f"{action} response omitted {expected_field}; without it the publication "
                "cannot be correlated with Core")
        return expected

    def _wait_baseline(self, baseline_id):
        """Wait until Core's power State carries the exact returned Observation ID."""
        deadline = self.clock.monotonic() + self.baseline_timeout
        last = None
        while True:
            response = self.core.get(f"/v1/entities/{self.power_id}", "power-baseline")
            state = (response.data or {}).get("state") if isinstance(response.data, dict) else None
            observation = (state or {}).get("observation_id")
            if response.status == 200 and isinstance(state, dict) and observation == baseline_id:
                if state.get("value") is not False:
                    raise SmokeFailure(
                        f"baseline observation {baseline_id} has Core value "
                        f"{state.get('value')!r}, expected false")
                self.ids["baseline_core_value"] = state.get("value")
                return baseline_id
            last = observation
            if self.clock.monotonic() >= deadline:
                raise SmokeFailure(
                    f"baseline observation {baseline_id} did not appear in Core within "
                    f"{self.baseline_timeout}s; last Core observation {last!r}")
            self.clock.sleep(self.poll_interval)

    # --- command -------------------------------------------------------------

    def _run_command(self):
        """Dispatch one Command and prove its durable satisfied outcome."""
        requested_at = self.clock.time()
        self.ids["command_requested_at"] = format_epoch(requested_at)
        response = self.core.post(
            f"/v1/entities/{self.power_id}/commands",
            {"operation": "set", "parameters": {"value": True}}, "power-command")
        if response.status != 200 or not isinstance(response.data, dict):
            raise SmokeFailure(f"command request failed: {response.explain()}")
        command_id = response.data.get("command_id")
        if not command_id:
            raise SmokeFailure("command response omitted command_id")
        self.ids["command_id"] = command_id
        record = self._poll_command(command_id)
        status = record.get("status")
        self.ids["command_status"] = status
        if status != "satisfied":
            raise SmokeFailure(
                f"command {command_id} finished {status!r}, expected satisfied")
        outcome = record.get("outcome_observation_id")
        if not outcome:
            raise SmokeFailure("satisfied command omitted outcome_observation_id")
        self.ids["outcome_observation_id"] = outcome
        self._assert_final_state(outcome)
        self._assert_applied_history(outcome)
        return f"command {command_id} satisfied with outcome {outcome}"

    def _poll_command(self, command_id):
        deadline = self.clock.monotonic() + self.command_timeout
        while True:
            response = self.core.get(f"/v1/commands/{command_id}", "command-durable")
            record = response.data if isinstance(response.data, dict) else {}
            status = record.get("status")
            self.ids["last_command_status"] = status
            if response.status == 200 and status not in NON_TERMINAL_COMMAND_STATUSES and status:
                return record
            if self.clock.monotonic() >= deadline:
                raise SmokeFailure(
                    f"command {command_id} reached no terminal status within "
                    f"{self.command_timeout}s; last status {status!r}")
            self.clock.sleep(self.poll_interval)

    def _assert_final_state(self, outcome):
        response = self.core.get(f"/v1/entities/{self.power_id}", "power-final")
        if response.status != 200 or not isinstance(response.data, dict):
            raise SmokeFailure(f"cannot read final power State: {response.explain()}")
        state = response.data.get("state") or {}
        if state.get("value") is not True:
            raise SmokeFailure(f"final power State is {state.get('value')!r}, expected true")
        if state.get("observation_id") != outcome:
            raise SmokeFailure(
                f"final State observation_id {state.get('observation_id')!r} does not match "
                f"command outcome_observation_id {outcome!r}")
        self.ids["final_observation_id"] = state.get("observation_id")

    def _assert_applied_history(self, outcome):
        response = self.core.get(
            f"/v1/entities/{self.power_id}/state/history?limit=50", "power-state-history")
        if response.status != 200 or not isinstance(response.data, dict):
            raise SmokeFailure(f"cannot read State history: {response.explain()}")
        items = response.data.get("items") or []
        match = next((item for item in items if item.get("observation_id") == outcome), None)
        if match is None:
            raise SmokeFailure(f"outcome observation {outcome} is absent from State history")
        self.ids["outcome_history_disposition"] = match.get("disposition")
        self.ids["outcome_history_value"] = match.get("value")
        if match.get("disposition") != "applied":
            raise SmokeFailure(
                f"outcome observation {outcome} disposition is {match.get('disposition')!r}, "
                "expected applied")
        if match.get("value") is not True:
            raise SmokeFailure(
                f"outcome observation {outcome} value is {match.get('value')!r}, expected true")

    # --- entity event --------------------------------------------------------

    def _check_event_state_null(self, moment):
        response = self.core.get(f"/v1/entities/{self.events_id}", f"events-entity-{moment}")
        if response.status != 200 or not isinstance(response.data, dict):
            raise SmokeFailure(f"cannot read events Entity ({moment}): {response.explain()}")
        state = response.data.get("state")
        if state is not None:
            raise SmokeFailure(f"event Entity State must be null ({moment}), got {state!r}")
        return f"State null {moment}"

    def _inject_event(self):
        """Publish exactly one manual event and require its canonical Event ID."""
        response = self.sim.post(
            f"/v1/sim/entities/{self.events_id}/publish", {"name": EVENT_NAME},
            "event-publish")
        event_id = self._require_publish_id(
            response, EVENT_PUBLISH_ID_FIELD, "an event Entity", "manual event publish")
        if response.data.get("current") != EVENT_NAME:
            raise SmokeFailure(
                f"manual event publish did not confirm {EVENT_NAME}: "
                f"{response.data.get('current')!r}")
        self._published_event_id = event_id
        self.ids["published_event_id"] = event_id
        return f"published {EVENT_NAME} as event {event_id}"

    def _verify_event(self):
        """Prove the exact published Event ID landed in Core as accepted."""
        event = self._poll_injected_event()
        self.ids["injected_event_id"] = event.get("event_id")
        self.ids["injected_event_name"] = event.get("name")
        self.ids["injected_event_disposition"] = event.get("disposition")
        self.ids["injected_event_received_at"] = event.get("received_at")
        self._check_event_state_null("after")
        return (f"accepted event {event.get('event_id')} received {event.get('received_at')}")

    def _poll_injected_event(self):
        """Poll Core until the exact published Event ID is recorded accepted.

        Only the returned ``event_id`` selects a match: unrelated events, even
        accepted ones with the same name, never substitute and never fail the
        run once the expected event arrives.
        """
        expected_id = self._published_event_id
        deadline = self.clock.monotonic() + self.event_timeout
        while True:
            items = self._event_items("events-after")
            ids = sorted(item["event_id"] for item in items
                         if isinstance(item.get("event_id"), str))
            self.ids["event_ids_after"] = ids
            match = next((item for item in items if item.get("event_id") == expected_id), None)
            if match is not None:
                if match.get("name") != EVENT_NAME:
                    raise SmokeFailure(
                        f"event {expected_id} name is {match.get('name')!r}, "
                        f"expected {EVENT_NAME!r}")
                if match.get("disposition") != "accepted":
                    raise SmokeFailure(
                        f"event {expected_id} disposition is {match.get('disposition')!r}, "
                        "expected accepted")
                return match
            if self.clock.monotonic() >= deadline:
                raise SmokeFailure(
                    f"event {expected_id} was not recorded accepted in Core within "
                    f"{self.event_timeout}s; Core ids {ids}")
            self.clock.sleep(self.poll_interval)

    def _event_items(self, name):
        response = self.core.get(
            f"/v1/entities/{self.events_id}/events?limit=50", name)
        if response.status != 200 or not isinstance(response.data, dict):
            raise SmokeFailure(f"cannot read Entity Event history: {response.explain()}")
        return [item for item in (response.data.get("items") or []) if isinstance(item, dict)]

    # --- cleanup -------------------------------------------------------------

    def _restore(self):
        """Resume only the entities this run paused, recording any failure."""
        for entity_id in list(self.paused_by_us):
            try:
                response = self.sim.post(
                    f"/v1/sim/entities/{entity_id}/resume", None, "resume")
                if response.status != 200:
                    raise RuntimeError(response.explain())
                self.cleanup["restored"].append(entity_id)
            except Exception as error:  # noqa: BLE001 - cleanup must report every failure
                self.cleanup["errors"].append(f"resume {entity_id}: {error}")
        self.paused_by_us = []

    # --- orchestration and summary -------------------------------------------

    def execute(self):
        """Run the smoke checks, always restoring pause state, then summarize."""
        self.started = self.clock.monotonic()
        self.started_at = format_epoch(self.clock.time())
        try:
            self.record = self._load_record()
        except SmokeFailure as error:
            self.steps.append({"name": "ownership-record", "status": "failed",
                               "detail": str(error)})
            self.passed = False
            return self.finalize()

        self.run_dir = Path(self.record["run_dir"])
        self.smoke_dir = Path(tempfile.mkdtemp(prefix="smoke.", dir=self.run_dir))
        self.evidence = Evidence(self.smoke_dir)
        self.core = HttpClient(self.evidence, CORE_URL, self.http_read_timeout,
                               self.http_mutation_timeout)
        self.sim = HttpClient(self.evidence, SIM_URL, self.http_read_timeout,
                              self.http_mutation_timeout)
        try:
            self._step("ownership", self._verify_ownership)
            self._step("default-scenario", self._check_default_scenario)
            self._step("inventory", self._discover_entities)
            self._step("power-pause",
                       lambda: self._pause_entity(self.power_id, "power", "power"))
            self._step("power-baseline", self._establish_baseline)
            self._step("command-outcome", self._run_command)
            self._step("event-state-null-before",
                       lambda: self._check_event_state_null("before"))
            self._step("events-pause",
                       lambda: self._pause_entity(self.events_id, "events", "events"))
            self._step("event-injection", self._inject_event)
            self._step("event-verification", self._verify_event)
            self.passed = True
        except SmokeFailure:
            self.passed = False
        except Exception as error:  # noqa: BLE001 - record unexpected failures, keep evidence
            self.steps.append({"name": "unexpected", "status": "failed",
                               "detail": f"{type(error).__name__}: {error}"})
            self.passed = False
        finally:
            self._restore()
        return self.finalize()

    def finalize(self):
        """Write summary.json, print a short report, and set the exit code."""
        elapsed = round(self.clock.monotonic() - self.started, 3)
        cleanup_ok = not self.cleanup["errors"]
        passed = bool(self.passed and cleanup_ok)
        self.exit_code = 0 if passed else 1
        failed_steps = [step for step in self.steps if step["status"] == "failed"]
        summary = {
            "schema": "simulator-smoke/v1",
            "status": "passed" if passed else "failed",
            "passed": passed,
            "started_at": self.started_at,
            "finished_at": format_epoch(self.clock.time()),
            "elapsed_seconds": elapsed,
            "worktree": str(start.ROOT),
            "tab_id": (self.record or {}).get("tab_id"),
            "run_dir": str(self.run_dir) if self.run_dir else None,
            "smoke_dir": str(self.smoke_dir) if self.smoke_dir else None,
            "mode": (self.record or {}).get("mode"),
            "endpoints": {"core": CORE_URL, "simulator": SIM_URL},
            "steps": self.steps,
            "ids": self.ids,
            "cleanup": self.cleanup,
            "failures": [f"{step['name']}: {step['detail']}" for step in failed_steps],
        }
        if self.evidence is not None:
            self.evidence.write("summary.json", summary)

        passed_steps = len(self.steps) - len(failed_steps)
        print(f"smoke: {summary['status']} elapsed={elapsed:.2f}s "
              f"steps={passed_steps}/{len(self.steps)}", flush=True)
        for step in failed_steps:
            print(f"smoke: FAIL {step['name']}: {step['detail']}", flush=True)
        for error in self.cleanup["errors"]:
            print(f"smoke: CLEANUP-ERROR {error}", flush=True)
        if self.smoke_dir is not None:
            try:
                relative = os.path.relpath(self.smoke_dir, start.ROOT)
            except ValueError:
                relative = str(self.smoke_dir)
            print(f"smoke: summary {relative}/summary.json", flush=True)
            print(f"smoke: raw evidence {relative}", flush=True)
        return summary


def parse_args(argv):
    parser = argparse.ArgumentParser(
        prog="smoke.py",
        description="Bounded default-only smoke validation of a running simulator stack")
    return parser.parse_args(argv)


def main(argv=None):
    parse_args(argv)
    smoke = Smoke()
    summary = smoke.execute()
    sys.exit(0 if summary["passed"] else 1)


if __name__ == "__main__":
    main()
