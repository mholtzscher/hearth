---
name: real-device-validation
description: Validate Hearth against REAL Zigbee devices through the homelab's shared NATS and Zigbee2MQTT dev environment, and collect real device payloads for fixtures. Use this skill whenever the user mentions real devices, real hardware, the homelab, physical lights or sensors, validating against Zigbee2MQTT, capturing live device data, or debugging behavior the simulator cannot reproduce. Do NOT use it for pure simulator runs, unit tests, or local-loopback development.
---

# Real-Device Validation

Run local `hearthd`, the Zigbee2MQTT adapter, and the debug dashboard against
shared homelab NATS + Zigbee2MQTT. Read these safety rules, then run the startup
task; do not reconstruct its steps with separate tool calls.

## Safety and topology

- Use the hostname supplied by the user. If unknown, check ignored
  `configs/homelab-*.yaml` with shell path tests, then `~/.ssh/config`, then
  ask. Never assume a hostname. No SSH access, tunnel, or VPN is required.
- The operator-managed server provides NATS `:4222`, MQTT `:1883`, NATS
  monitoring `:8222`, and the Z2M frontend `:8082`. Its MQTT base topic is
  `zigbee2mqtt`, availability enabled, global optimistic `false`.
- Never start local NATS in real-device mode. Never change the shared server,
  Z2M settings, pairing, names, OTA, or coordinator without explicit approval.
- If the remote core at `:8081` answers, stop and ask before starting a local
  core. Occupied local ports also block startup; never kill another session.
- Reads are safe. API commands and `mqtt_pub` actuate physical hardware:
  announce each planned command and obtain explicit user confirmation first.
- Never edit `configs/*.example.yaml`. Reuse ignored homelab configs and
  `.data`; never copy network keys, PAN IDs, or database internals into the repo.

## Start

From this worktree, inside Herdr:

```sh
mise run real-device-start -- HOMELAB
```

For a user-specified Wanda host, substitute `wanda`. Allow the tool call enough
time for compilation and dependency installation (e.g. 240 seconds). The task
uses Python 3, Bash, Git, Herdr, and the mise-managed Go/frontend tools.

The bundled `scripts/start.py` performs preflight, checks ignored configs,
creates `.data`, installs missing dashboard dependencies, and creates a named
Herdr tab without changing focus. It starts core → waits for readiness → starts
adapter and dashboard together. Vite binds `127.0.0.1:5173` with strict-port
checking. It rejects stale adapter health from a reused database, inspects
process exits during polling, and checks the API and NATS monitoring proxies.

Existing configs must match the task's minimal templates; on mismatch, review
rather than overwrite. On failure, the task prints pane logs and the cleanup
command for only its own tab. Do not blindly rerun while that tab is running.

On success, **immediately relay “Ready for validation” with the dashboard URL
and tab ID**. Report relevant adapter warnings printed by the task. Its elapsed
times measure script execution; when benchmarking, also measure prompt-to-ready
including skill loading and tool overhead. Do not launch competing live runs
for benchmarking.

## Validate after readiness

Only now load `references/validation.md` for device inspection, approved
physical commands, fixture capture, or troubleshooting. Load browser tooling
only when performing browser smoke tests, not as a startup prerequisite.

A live NATS WebSocket subscription is optional and separate from the working
HTTP monitoring proxy. Report a failed subscription separately; do not delay
readiness or start local NATS to fix it.

## Cleanup

```sh
mise run real-device-stop
```

Startup records tab ownership in ignored `.data/real-device-validation.json`.
Teardown verifies the worktree, tab label, and original root pane before closing
that tab. Repeated teardown is safe; an already-closed tab clears the stale
record. An ownership mismatch stops without closing anything. Environments
started manually before this task have no record: inspect their IDs and close
only the confirmed validation tab with `herdr tab close <tab-id>`.

Herdr reaps the tab's pane processes. The shared server, local configs, and
SQLite/evidence files remain untouched. Run teardown before starting again.
