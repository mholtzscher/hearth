# Real-device validation

Run local Core, Zigbee2MQTT adapter, and dashboard through Mise/Pitchfork
against operator-managed homelab NATS, MQTT, and Zigbee2MQTT. Requires Mise
2026.9.12+ and Pitchfork 2.25+. Use the hostname supplied by the user; if
unknown, check ignored `configs/homelab-*.yaml`, then `~/.ssh/config`, then
ask. Do not assume a hostname.

Never start local NATS in this mode or change the shared server, Z2M settings,
pairing, OTA, or coordinator without explicit approval. Reads are safe; API
Commands and `mqtt_pub` actuate physical hardware and require explicit
confirmation for each planned command. Do not run two real-device adapters
against the shared MQTT topics/NATS at the same time. If remote Core `:8081`
answers, stop and ask before starting local Core. Never edit checked-in
example configs or copy secrets into the repository.

## Start

```sh
mise run real-device-start -- HOMELAB
```

The launcher checks the remote Core and shared NATS `:4222`, Mosquitto
`:1883`, monitoring `:8222`, and Z2M frontend `:8082`. It generates ignored
worktree-local configs, starts Core, checks the previous runtime ID in SQLite,
then starts the adapter and dashboard and requires a fresh healthy runtime.
Use the local dashboard URL printed on success. Assigned ports are
`REAL_CORE_PORT` and `REAL_WEB_PORT` from `mise env --json`.

The dashboard is loopback-only. This workflow does **not** publish a Tailscale
Serve route: a fixed `:8088` route cannot safely represent concurrent
worktrees. Do not imply the dashboard is accessible on the tailnet.
On failure, inspect `mise daemons logs real-core` and `mise daemons logs
real-adapter`; stop only this stack with `mise run real-device-stop`.

## Validate and stop

For read-only device inspection or approved physical commands, read
`references/validation.md` after readiness. Browser checks require the
agent-browser skill. NATS WebSocket subscription is optional and does not
determine HTTP readiness. Report the remote host, exact URLs, fresh adapter
runtime, device evidence, warnings, and any blocked checks separately.

```sh
mise run real-device-stop
```

Stop preserves ignored configs and SQLite data, and does not modify remote
services. If the remote Core or another adapter blocks startup, ask before
changing it. After code changes, prefer `mise run validate`; offline safety
checks run under `mise run validation-stack-test`.
