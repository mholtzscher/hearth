#!/usr/bin/env bash
# Run only against the existing worktree-managed simulator stack.
set -euo pipefail
cd "$(dirname "$0")/.."
exec mise exec -- python - "$@" <<'PY'
import json
from pathlib import Path
import subprocess
import threading
import time
import urllib.error
import urllib.request


def check(condition, message):
    if not condition:
        raise AssertionError(message)


version = subprocess.check_output(["mise", "--version"], text=True).split()[0]
check(tuple(map(int, version.split("."))) >= (2026, 9, 12), "Mise 2026.9.12+ required")
env = json.loads(subprocess.check_output(["mise", "env", "--json"], text=True))
core = "http://127.0.0.1:" + env["SIM_CORE_PORT"]
sim = "http://127.0.0.1:" + env["SIMULATOR_PORT"]
evidence = Path(".data/simulator-stack/function-validation") / time.strftime("%Y%m%dT%H%M%S")
evidence.mkdir(parents=True, exist_ok=False)
print(f"Core={core} simulator={sim} evidence={evidence}", flush=True)
counter = 0
counter_lock = threading.Lock()


def save(name, value):
    (evidence / (name + ".json")).write_text(json.dumps(value, indent=2) + "\n")
    return value


def request(base, path, method="GET", body=None, raw_body=None, expected=(200,), timeout=2):
    global counter
    with counter_lock:
        counter += 1
        sequence = counter
    if raw_body is not None:
        data = raw_body.encode()
    elif body is not None:
        data = json.dumps(body).encode()
    else:
        data = None
    request = urllib.request.Request(
        base + path, method=method, data=data, headers={"Content-Type": "application/json"}
    )
    try:
        response = urllib.request.urlopen(request, timeout=timeout)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        text = response.read().decode()
        parsed = json.loads(text) if text else None
        status = response.code
    save(
        f"http-{sequence:04d}",
        {
            "method": method,
            "url": base + path,
            "status": status,
            "response": parsed,
            "raw": text,
            "mutation_may_have_taken_effect": method != "GET",
        },
    )
    check(status in expected, f"{method} {path}: HTTP {status}: {parsed}")
    return parsed, text, status


def poll(name, read, predicate, timeout=30):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = read()
        if predicate(value):
            return save(name, value)
        time.sleep(0.2)
    raise AssertionError(f"{name}: no matching evidence within {timeout} seconds")


adapter = "sim-healthy"
functions, _, _ = request(core, f"/v1/adapters/{adapter}/functions?limit=50")
check({item["function_key"] for item in functions["items"]} >= {
    "inspect", "fail", "not-executed", "unknown", "hold",
}, "default function presets are missing")
descriptor, _, _ = request(core, f"/v1/adapters/{adapter}/functions/inspect")
revision = descriptor["catalog_revision"]
check(descriptor["callable"], "inspect is not callable")


def invoke(key, arguments_raw):
    body = '{"catalog_revision":' + json.dumps(revision) + ',"arguments":' + arguments_raw + "}"
    parsed, _, _ = request(core, f"/v1/adapters/{adapter}/functions/{key}/invocations", "POST", raw_body=body)
    return parsed


exact = '{"sequence":9007199254740993,"tiny":1e-10000}'
echo, echo_text = invoke("inspect", exact)
check(echo["status"] == "succeeded", "inspect did not succeed")
check(echo["result"] == {"sequence": 9007199254740993, "tiny": 0.0}, "inspect result shape changed")
check('"tiny":1e-10000' in echo_text, "inspect did not retain the exact tiny literal")
for key, status, reason in [
    ("fail", "failed", "simulated_failure"),
    ("not-executed", "not_executed", "simulated_unavailable"),
    ("unknown", "unknown", "simulated_unknown"),
]:
    invocation, _ = invoke(key, "{}")
    check(invocation["status"] == status, f"{key} status = {invocation['status']}")
    check(invocation["reason_code"] == reason, f"{key} reason = {invocation['reason_code']}")

held = {}
hold_body = '{"catalog_revision":' + json.dumps(revision) + ',"arguments":{}}'


def hold_call():
    try:
        held["invocation"], _, _ = request(
            core,
            f"/v1/adapters/{adapter}/functions/hold/invocations",
            "POST",
            raw_body=hold_body,
            timeout=40,
        )
    except Exception as error:
        held["error"] = error


worker = threading.Thread(target=hold_call, daemon=True)
worker.start()


def running_hold():
    page, _, _ = request(core, f"/v1/adapters/{adapter}/function-invocations?function_key=hold&limit=50")
    for item in page["items"]:
        if item["status"] == "running":
            return item
    return None


running = poll("held-running", running_hold, lambda value: value is not None)
check(running is not None, "Core never exposed a running held invocation")


def release_hold():
    status, _, _ = request(
        sim,
        f"/v1/sim/functions/invocations/{running['id']}/release",
        "POST",
        expected=(204, 404),
    )
    return status


# Core commits admission before dispatch, so running can precede the adapter's
# releasable hold by a short scheduling window.
poll("release", release_hold, lambda status: status == 204, timeout=10)
worker.join(timeout=20)
check(not worker.is_alive(), "held invocation did not finish after release")
check("error" not in held, f"held invocation failed: {held.get('error')}")
check(held.get("invocation", {}).get("status") == "succeeded", "held invocation did not succeed")
retained, _, _ = request(core, f"/v1/adapter-function-invocations/{running['id']}")
check(retained["status"] == "succeeded" and retained["completed_at"] is not None,
      "Core retained no terminal held evidence")
save("result", {
    "catalog_revision": revision,
    "echo_invocation": echo["id"],
    "held_invocation": running["id"],
    "held_status": retained["status"],
})
print("function validation passed", flush=True)
print(f"evidence: {evidence}", flush=True)
print("stop the stack with mise run simulator-stop when finished", flush=True)
PY
