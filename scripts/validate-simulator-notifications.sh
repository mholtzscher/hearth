#!/usr/bin/env bash
# Run only against the existing worktree-managed native simulator stack.
set -euo pipefail
cd "$(dirname "$0")/.."
exec mise exec -- python - "$@" <<'PY'
import json
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def check(condition, message):
    if not condition:
        raise AssertionError(message)


version = subprocess.check_output(["mise", "--version"], text=True).split()[0]
check(tuple(map(int, version.split("."))) >= (2026, 9, 12), "Mise 2026.9.12+ required")
env = json.loads(subprocess.check_output(["mise", "env", "--json"], text=True))
core = "http://127.0.0.1:" + env["SIM_CORE_PORT"]
sim = "http://127.0.0.1:" + env["SIMULATOR_PORT"]
evidence = Path(".data/simulator-stack/notification-validation") / time.strftime("%Y%m%dT%H%M%S")
evidence.mkdir(parents=True, exist_ok=False)
print(f"Core={core} simulator={sim} evidence={evidence}", flush=True)
counter = 0
automations = {}
invocation = uuid.uuid4().hex
definitions = {}
ambiguous_creations = set()
original_entities = []
original_destinations = []
held_attempts = []
core_stopped = False
sim_stopped = False


def save(name, value):
    (evidence / (name + ".json")).write_text(json.dumps(value, indent=2) + "\n")
    return value


def http(base, path, method="GET", body=None, expected=(200,), deadline=None):
    global counter
    counter += 1
    request = urllib.request.Request(base + path, method=method,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Content-Type": "application/json"})
    try:
        timeout = 2
        if deadline is not None:
            remaining = deadline - time.monotonic()
            check(remaining > 0, "HTTP request budget exhausted before " + path)
            timeout = min(timeout, remaining)
        response = urllib.request.urlopen(request, timeout=timeout)
    except urllib.error.HTTPError as error:
        response = error
    except Exception as error:
        save(f"http-{counter:04d}", {"method": method, "url": base + path,
            "request": body, "error": str(error), "mutation_may_have_taken_effect": method != "GET"})
        raise
    with response:
        raw = response.read().decode()
        result = json.loads(raw) if raw else None
        status = response.code
    if deadline is not None:
        check(time.monotonic() < deadline, "HTTP request budget exhausted during " + path)
    save(f"http-{counter:04d}", {"method": method, "url": base + path,
        "request": body, "status": status, "response": result})
    check(status in expected, f"{method} {path}: HTTP {status}: {result}")
    return result


def poll(name, read, predicate, deadline=None):
    # Reserve the final request's two-second timeout inside the 90-second budget.
    if deadline is None:
        deadline = time.monotonic() + 88
    while time.monotonic() < deadline:
        result = read()
        if predicate(result):
            return save(name, result)
        time.sleep(min(0.5, max(0, deadline - time.monotonic())))
    raise AssertionError(f"{name}: no matching evidence within 90 seconds")


def lifecycle(action, daemon):
    subprocess.run(["mise", "daemons", action, daemon], check=True, timeout=90)


def captures(cursor=0):
    page = http(sim, f"/v1/sim/notifications/requests?adapter_id=sim-healthy&after_sequence={cursor}&limit=1000")
    check(not page["has_more"], "Capture page incomplete; cannot prove count/absence")
    check(len(page["metadata"]) == 1, "Missing Adapter retention metadata")
    meta = page["metadata"][0]
    check(meta["latest_sequence"] >= cursor, "Capture runtime reset unexpectedly")
    check(meta["highest_dropped_sequence"] <= cursor, "Capture window lost entries; absence is not evidence")
    return page


def capture_head():
    page = http(sim, "/v1/sim/notifications/requests?adapter_id=sim-healthy&limit=1")
    return page["metadata"][0]["latest_sequence"]


def attempts(key):
    items = []
    cursor = None
    seen = set()
    deadline = time.monotonic() + 88
    while time.monotonic() < deadline:
        query = {"destination_id": destinations[key], "limit": 200}
        if cursor:
            query["cursor"] = cursor
        page = http(core, "/v1/notifications?" + urllib.parse.urlencode(query))
        items.extend(page["items"])
        cursor = page.get("next_cursor")
        if not cursor:
            return {x["id"] for x in items}
        check(cursor not in seen, "Attempt pagination cursor repeated")
        seen.add(cursor)
    raise AssertionError("Attempt pagination exceeded 90 seconds; count evidence incomplete")


def cause_event(record):
    return record.get("cause", {}).get("fact", {}).get("event_id")


def history(automation):
    result = http(core, f"/v1/automations/{automation}/history?limit=200")
    check(not result.get("next_cursor"), "Temporary Automation history unexpectedly paginated")
    return result


def detail(automation, run):
    return http(core, f"/v1/automations/{automation}/history/{run}")["run"]


def publish(name):
    publication = http(sim, f"/v1/sim/entities/{events}/publish", "POST", {"name": name})
    event = publication["event_id"]
    save("publication-" + event, publication)
    poll("event-" + event,
        lambda: http(core, f"/v1/entities/{events}/events?limit=200"),
        lambda page: any(x["event_id"] == event and x["disposition"] == "accepted" for x in page["items"]))
    return event


def triggered(automation, event, kind="run"):
    page = poll("history-" + event, lambda: history(automation),
        lambda page: any(cause_event(x) == event and x["kind"] == kind for x in page["items"]))
    matches = [x for x in page["items"] if cause_event(x) == event]
    check(len(matches) == 1 and matches[0]["kind"] == kind, "Event did not produce exactly one expected history entry")
    return matches[0]


def create(key):
    notification = {"id": "notify", "kind": "notification", "destination_id": destinations[key],
        "title": "Native simulator validation", "message": "Exact message: " + key}
    definition = {"name": "Temporary native notification validation " + invocation + " " + key, "enabled": True,
        "triggers": [{"id": "selected", "kind": "entity_event", "entity_id": events, "event_name": "single_press"},
                     {"id": "unselected", "kind": "entity_event", "entity_id": events, "event_name": "double_press"}],
        "steps": [{"id": "route", "kind": "if",
            "conditions": {"id": "selected-trigger", "kind": "trigger", "trigger_ids": ["selected"]},
            "then": [notification, {"id": "power", "kind": "command", "entity_id": power,
                "operation": "set", "parameters": {"value": True}}]}]}
    # Record ownership before POST: a committed create can lose its response.
    # Never retry POST. Cleanup discovers the exact invocation definition instead.
    definitions[definition["name"]] = definition
    ambiguous_creations.add(definition["name"])
    save("invocation", {"marker": invocation, "definitions": definitions})
    record = http(core, "/v1/automations", "POST", definition, (201,))
    automations[record["id"]] = record["revision"]
    ambiguous_creations.discard(definition["name"])
    save("definition-" + key, record)
    return record["id"]


def remove(automation):
    http(core, f"/v1/automations/{automation}?expected_revision={automations[automation]}", "DELETE", expected=(204,))
    del automations[automation]


def discover_owned(deadline):
    # Follow every page; broad name prefixes or pre-existing IDs are not ownership.
    cursor = None
    seen = set()
    found = set()
    while time.monotonic() < deadline:
        query = {"limit": 200}
        if cursor:
            query["cursor"] = cursor
        page = http(core, "/v1/automations?" + urllib.parse.urlencode(query), deadline=deadline)
        for record in page["items"]:
            definition = record["definition"]
            name = definition["name"]
            if name not in definitions:
                continue
            check(definition == definitions[name] and record["revision"] == 1,
                "Owned definition changed; refusing to delete: " + record["id"])
            automations[record["id"]] = record["revision"]
            found.add(name)
        cursor = page.get("next_cursor")
        if not cursor:
            ambiguous_creations.difference_update(found)
            return not ambiguous_creations
        check(cursor not in seen, "Automation pagination cursor repeated")
        seen.add(cursor)
    raise AssertionError("Automation discovery exceeded 90 seconds; cleanup ownership incomplete")


def terminal(automation, run, status):
    return poll("run-" + run, lambda: detail(automation, run), lambda x: x["status"] == status)


def assert_send(run, key, cursor, outcome, reason=""):
    check(len(run["notifications"]) == 1, "Reached notification evidence missing/duplicated")
    execution = run["notifications"][0]
    attempt = http(core, "/v1/notifications/" + execution["attempt_id"])
    check(attempt["id"] == execution["attempt_id"] and attempt["status"] == outcome, "Wrong authoritative attempt outcome")
    check(attempt.get("reason_code", "") == reason, "Wrong authoritative attempt reason")
    check(attempt["destination_id"] == destinations[key] and attempt["title"] == "Native simulator validation"
        and attempt["message"] == "Exact message: " + key, "Attempt snapshot mismatch")
    check(execution.get("outcome") == outcome, "Run does not retain attempt outcome")
    check(execution.get("reason_code", "") == reason, "Run does not retain exact attempt reason")
    page = poll("captures-" + attempt["id"], lambda: captures(cursor),
        lambda x: any(c["attempt_id"] == attempt["id"] and c["finished_at"] is not None for c in x["items"]))
    matches = [x for x in page["items"] if x["attempt_id"] == attempt["id"]]
    check(len(page["items"]) == 1 and len(matches) == 1, "Expected independently counted one capture")
    capture = matches[0]
    check(capture["destination_id"] == attempt["destination_id"] and capture["message"] ==
        {"Title": attempt["title"], "Text": attempt["message"]}, "SDK capture differs from Core attempt")
    save("attempt-" + attempt["id"], attempt)


try:
    http(core, "/readyz")
    original_entities = save("original-entities", http(sim, "/v1/sim/entities"))
    original_destinations = save("original-destinations", http(sim, "/v1/sim/notifications/destinations"))
    destinations = {x["key"]: x["destination_id"] for x in original_destinations if x["adapter_id"] == "sim-healthy"}
    check(set(destinations) >= {"accepted", "rejected", "not-sent", "unknown", "held"}, "Native preset destinations missing")
    for key, outcome, reason in [("accepted", "accepted", ""), ("rejected", "rejected", "provider_rejected"),
        ("not-sent", "not_sent", "adapter_busy"), ("unknown", "unknown", "provider_error"), ("held", "accepted", "")]:
        http(sim, f"/v1/sim/notifications/destinations/{destinations[key]}/behavior", "PUT",
            {"outcome": outcome, "reason_code": reason, "hold": key == "held"})
        destination = http(core, "/v1/notification-destinations/" + destinations[key])
        check(destination["id"] == destinations[key] and destination["available"], "Core destination unavailable/mismatched")
    events = next(x["entity_id"] for x in original_entities if x["adapter_id"] == "sim-healthy" and
        x["binding_key"] == "simulated-button" and x["key"] == "events")
    power = next(x["entity_id"] for x in original_entities if x["adapter_id"] == "sim-healthy" and
        x["binding_key"] == "simulated-light" and x["key"] == "power")
    # Pause all outputs: restart scenarios must not introduce unrelated tickers.
    for entity in original_entities:
        http(sim, f"/v1/sim/entities/{entity['entity_id']}/pause", "POST")
    for key, outcome, reason in [("accepted", "accepted", ""), ("rejected", "rejected", "provider_rejected"),
        ("not-sent", "not_sent", "adapter_busy"), ("unknown", "unknown", "provider_error")]:
        automation = create(key)
        cursor = capture_head()
        before = attempts(key)
        unselected = triggered(automation, publish("double_press"))
        no_send = terminal(automation, unselected["id"], "succeeded")
        check(no_send["notifications"] == [] and all(s["status"] == "not_attempted" for s in no_send["steps"]),
            "Unselected leaves produced execution evidence")
        check(captures(cursor)["items"] == [], "Unselected notification reached provider")
        check(attempts(key) == before, "Unselected notification created an authoritative attempt")
        baseline = http(sim, f"/v1/sim/entities/{power}/publish", "POST", {"value": False})
        poll("baseline-" + key, lambda: http(core, f"/v1/entities/{power}"),
            lambda x: x["state"]["observation_id"] == baseline["observation_id"])
        selected = triggered(automation, publish("single_press"))
        run = terminal(automation, selected["id"], "succeeded" if outcome == "accepted" else "failed")
        assert_send(run, key, cursor, outcome, reason)
        check(attempts(key) - before ==
            {run["notifications"][0]["attempt_id"]}, "Unexpected authoritative notification attempts")
        step = run["steps"][0]
        if outcome == "accepted":
            check(step["status"] == "satisfied", "Accepted notification did not permit satisfied Command")
            command = http(core, "/v1/commands/" + step["verified_command_id"])
            check(command["id"] == step["verified_command_id"] and command["status"] == "satisfied", "Wrong Command evidence")
        else:
            check(step["status"] == "not_attempted", "Failed notification allowed later Command")
        remove(automation)
        print("PASS selected/unselected " + outcome, flush=True)

    # A held notification retains the Automation slot until explicit release.
    automation = create("held")
    cursor = capture_head()
    before = attempts("held")
    started = triggered(automation, publish("single_press"))
    page = poll("held-capture", lambda: captures(cursor), lambda x: len(x["items"]) == 1)
    attempt_id = page["items"][0]["attempt_id"]
    held_attempts.append(attempt_id)
    check(page["items"][0]["finished_at"] is None, "Held capture already completed")
    running = detail(automation, started["id"])
    check(running["status"] == "running" and running["notifications"][0]["attempt_id"] == attempt_id
        and running["notifications"][0]["status"] == "running", "Held send not linked to active Run")
    busy = triggered(automation, publish("single_press"), "skip")
    check(busy["reason"] == "automation_busy", "Held send did not retain busy admission")
    check(len(captures(cursor)["items"]) == 1, "Busy event generated a second send")
    check(attempts("held") - before == {attempt_id}, "Busy event created a second authoritative attempt")
    http(sim, "/v1/sim/notifications/requests/" + attempt_id + "/release", "POST", expected=(204,))
    run = terminal(automation, started["id"], "succeeded")
    assert_send(run, "held", cursor, "accepted")
    check(run["steps"][0]["status"] == "satisfied", "Released notification did not permit satisfied Command")
    remove(automation)
    print("PASS held/busy/release", flush=True)

    # Graceful managed Core restart interrupts a held Run, never resumes it.
    automation = create("held")
    cursor = capture_head()
    before = attempts("held")
    started = triggered(automation, publish("single_press"))
    page = poll("restart-held-capture", lambda: captures(cursor), lambda x: len(x["items"]) == 1)
    attempt_id = page["items"][0]["attempt_id"]
    held_attempts.append(attempt_id)
    check(detail(automation, started["id"])["notifications"][0]["attempt_id"] == attempt_id, "Restart attempt link mismatch")
    core_stopped = True
    lifecycle("stop", "sim-core")
    lifecycle("start", "sim-core")
    core_stopped = False
    run = terminal(automation, started["id"], "interrupted")
    check(run["notifications"][0]["status"] == "interrupted"
        and run["notifications"][0].get("reason_code") == "core_restarted"
        and run["steps"][0]["status"] == "not_attempted",
        "Restart resumed a notification or later Command")
    attempt = save("restart-attempt", http(core, "/v1/notifications/" + attempt_id))
    check(attempt["id"] == attempt_id and attempt["status"] == "unknown" and attempt["reason_code"] == "core_restarted",
        "Restart did not retain authoritative unknown/core_restarted attempt")
    for _ in range(6):
        check(len(captures(cursor)["items"]) == 1, "Core restart resent provider work")
        check(detail(automation, started["id"])["status"] == "interrupted", "Interrupted Run resumed")
        time.sleep(0.5)
    check(attempts("held") - before == {attempt_id}, "Restart created another authoritative attempt")
    remove(automation)
    print("PASS managed Core restart interruption/no resend", flush=True)

    # Restart only the managed simulator, retaining SQLite and JetStream.
    sim_stopped = True
    lifecycle("stop", "simulator")
    lifecycle("start", "simulator")
    sim_stopped = False
    restarted = save("restarted-destinations", http(sim, "/v1/sim/notifications/destinations"))
    check({(x["adapter_id"], x["key"]): x["destination_id"] for x in restarted} ==
        {(x["adapter_id"], x["key"]): x["destination_id"] for x in original_destinations}, "Simulator restart changed destination IDs")
    check(captures()["items"] == [], "Captures unexpectedly persisted across simulator restart")
    print("PASS managed simulator restart stable destination identities", flush=True)
    save("result", {"status": "passed", "core_url": core, "simulator_url": sim})
except BaseException as error:
    save("result", {"status": "failed", "error": str(error)})
    raise
finally:
    # Retain all evidence and storage. Restore every changed control, even on failure.
    errors = []
    if core_stopped:
        try:
            lifecycle("start", "sim-core")
        except Exception as error:
            errors.append(str(error))
    if sim_stopped:
        try:
            lifecycle("start", "simulator")
        except Exception as error:
            errors.append(str(error))
    for attempt in held_attempts:
        try:
            http(sim, "/v1/sim/notifications/requests/" + attempt + "/release", "POST", expected=(204, 404))
        except Exception as error:
            errors.append(str(error))
    if definitions:
        try:
            # Pagination and repeated scans share this one budget. Each HTTP
            # request is capped by what remains, rather than starting another 88s.
            discovery_deadline = time.monotonic() + 88
            poll("cleanup-discovery", lambda: discover_owned(discovery_deadline),
                lambda complete: complete, deadline=discovery_deadline)
        except Exception as error:
            errors.append(str(error))
    for automation in list(automations):
        try:
            poll("cleanup-run-" + automation, lambda: history(automation),
                lambda x: not any(r.get("status") == "running" for r in x["items"]))
            remove(automation)
        except Exception as error:
            errors.append(str(error))
    for destination in original_destinations:
        try:
            http(sim, f"/v1/sim/notifications/destinations/{destination['destination_id']}/behavior",
                "PUT", destination["behavior"])
        except Exception as error:
            errors.append(str(error))
    for entity in original_entities:
        try:
            action = "pause" if entity["paused"] else "resume"
            http(sim, f"/v1/sim/entities/{entity['entity_id']}/{action}", "POST")
        except Exception as error:
            errors.append(str(error))
    save("cleanup", {"errors": errors, "remaining_automations": automations,
        "ambiguous_creations": sorted(ambiguous_creations), "invocation": invocation})
    print(f"Cleanup errors={len(errors)}; evidence={evidence}", flush=True)
    if errors:
        raise RuntimeError("Cleanup incomplete: " + "; ".join(errors))
PY
