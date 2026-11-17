# Connect ESPHome devices to Hearth

This guide is for an operator connecting ESPHome Native API devices to Hearth
and checking discovery without sending device Commands. Run commands from the
repository root. Device Builder remains responsible for firmware and updates;
no Home Assistant bridge is required.

Home Assistant can remain connected during device-by-device migration if the
firmware permits another Native API client. Check `api.max_connections` and
leave capacity for both systems.

## Configure devices and credentials

Copy the example without overwriting an existing configuration:

```sh
cp -n configs/esphome.example.yaml configs/esphome.yaml
```

Edit `configs/esphome.yaml` for your devices. The example enables mDNS
discovery and contains illustrative MAC addresses, a blocklist, and key paths;
replace or remove the examples. The blocklist wins over discovery and static
entries and makes previously owned Entities unavailable without deleting them.
Do not run two Adapter instances with the same `adapter_id` against one Core.

For discovery, enable `discovery.enabled`. Add MAC-keyed `devices` entries
without addresses for encryption keys or include/exclude overrides. Discovery
uses resolved IP addresses and needs multicast on the host's interfaces.
Container bridge networks and VLANs may need host networking or an
operator-managed mDNS reflector.

For a static device, specify its colon-separated MAC and `address`. Use a fixed
IP or unicast DNS name, not a configured `.local` address. A static address
remains authoritative even when discovery is enabled; the default port is 6053.
Keep `nats_url` in the example when using the managed task below.

Encryption is the default. For each encrypted device, put its base64
`api.encryption.key` in a separate private file readable by the Adapter and set
`encryption_key_file` to the path. Keep the key out of Git, YAML values,
command arguments, shell history, and logs. Restart the Adapter after changing
configuration or rotating a key. Password-protected Native API devices are
unsupported.

Plaintext exposes readings and controls to the network. Use it only on a trusted
network, with explicit `insecure_plaintext: true` on a static device or
`discovery.allow_insecure_plaintext: true` for discovered devices. A discovery
permission does not authorize static plaintext. mDNS and MAC addresses are not
authentication. An encrypted advertisement without a key is skipped, and a
failed encrypted connection never falls back to plaintext.

## Start and inspect

The optional Adapter shares this worktree's simulator Core, NATS, database, and
dashboard. The simulator's checked-in Core selects OpenCode Go, so supply
`HEARTH_OPENCODE_GO_API_KEY` through the process supervisor or secret manager
before starting. If you change the provider, supply its credential instead.
Keep the value out of YAML, command arguments, and shell history.

```sh
mise run esphome-start
mise daemons status esphome-adapter
mise env --json | jq '{SIM_CORE_PORT, SIM_WEB_PORT}'
```

The task starts the simulator stack if needed and substitutes its NATS port in
an ignored runtime copy of the config. It does not change the source config.
Physical and simulated Devices share the simulator database and remain there
after the Adapter stops. Use the printed Core port in these read-only requests:

```sh
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/adapters/esphome'
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/devices'
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/entities'
```

Replace the placeholder with the printed port.
Each list response holds one page. If it has `next_cursor`, repeat the same
request, keeping its filters, with `cursor=<next_cursor>` added to the query string.
Inspect Entity support, State, and availability before any separately
authorized operation. To stop the Adapter, then the remaining simulator stack:

```sh
mise run esphome-stop
mise run simulator-stop
```

The second command stops the remaining simulator stack. A device outage affects
its Entity availability, not necessarily Adapter health. A missing mDNS
advertisement alone does not interrupt a live connection; after an Adapter
restart, discovered Entities remain unavailable until rediscovered.

Identity uses MAC, platform, and upstream entity name. Address changes preserve
identity, but a firmware entity rename creates a new Entity and leaves the old
one unavailable. Retarget dashboards and Automations after a rename. Overrides
cannot enable unsupported types. See [architecture](../architecture/architecture.md),
the [Entity schemas](../../entitytypes/), and
[ESPHome thermostats](esphome-thermostat.md) for capabilities and operation
details.

## Understand control evidence and safety

An ESPHome socket write permits Command acceptance; matching reported State
can satisfy it. For an already-matching value, the Adapter can use cached
evidence from the current live subscription. Neither case proves a physical
effect. Reconnects do not replay Commands. Synthetic Native API tests do not
establish physical auto-discovery or device behavior; the thermostat guide
describes its unverified physical discovery and control.

Ratgdo cover position can be timing-estimated and needs travel calibration.
An initial idle/zero report or a satisfied position Command does not prove the
door is closed or secure. A `stop` Command is dispatched, not proof of stopped
motion, and delayed broker acknowledgements can prevent its native write before
the deadline. Stop clears earlier target matchers, so earlier accepted position
Commands may time out. It is not an emergency-stop guarantee.

Ratgdo **Lock remotes** is remote lockout, not a physical deadbolt. Firmware can
report a requested change without confirming enforcement. Do not treat a
satisfied Command as proof that remotes are disabled. No live garage-door or
lockout Command validated this implementation.

## Troubleshoot

- For an undiscovered device, check multicast reachability, VLAN or container
  boundaries, the blocklist, and its advertised MAC. A static address avoids
  mDNS but needs its own credential or explicit plaintext opt-in.
- For `encryption_key_required`, supply the correct private key file and
  restart. Never work around an encrypted connection failure by assuming
  plaintext is allowed.
- Check per-device availability and Adapter logs for omitted capabilities.
  Deep-sleep-capable firmware, sub-devices, and assumed-state switches, covers,
  and locks are unsupported. For climate-specific omissions and evidence, use
  the thermostat guide.
