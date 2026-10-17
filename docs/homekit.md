# Apple Home through the HomeKit bridge

`hearth-bridge-homekit` exports explicitly chosen Hearth lights and plugs to
Apple Home. It is an outgoing bridge, not an importer or Homebridge plugin.
It does not own or register Devices, publish Observations, or claim an Adapter
Session. Core State and Command evidence stay authoritative.

The first release targets Linux amd64/arm64, Node 26/pnpm 11, and a trusted LAN.
The approved [specification](../specs/homekit-bridge.md) lists the acceptance
gates. Do not treat the earlier disposable experiment as production validation.

## Configure and start locally

Install the repository toolchain and util-linux `flock`. Build the package:

```sh
mise run homekit-build
cp configs/homekit.example.yaml configs/homekit.yaml
```

Use the existing HTTP API to discover Entities:

```sh
curl -fsS 'http://127.0.0.1:8080/v1/entities?limit=200'
curl -fsS 'http://127.0.0.1:8080/v1/entities/<entity_id>'
```

Replace the example IDs; follow `next_cursor` for further pages. A `light` needs
a `hearth.power/v1` Entity and may include a `hearth.brightness/v1` Entity from
the same Device. An `outlet` needs only power. Both require `set` support.
Do not reuse an Entity in multiple slots. A compatible disabled/unavailable
Entity or one with no State can be configured, but does not produce successful
reads until usable.

Choose the addressed LAN interface from `ip -brief address`, and configure it
in the YAML-only `advertise_interfaces` list. Configure explicit unauthenticated
HTTP and plain NATS origins with host and port. HTTPS/TLS, broker credentials,
automatic discovery, color, and live configuration reload are not supported.
Keep Core/NATS on loopback or an already trusted private network.

Create a private file containing a unique HomeKit setup code in `XXX-XX-XXX`
form. Do not use repeated digits or ascending/descending trivial codes. The
file may end in one newline. Do not put its contents in Git, YAML, environment,
command arguments, or diagnostics. Give the bridge user read permission, not
other household users. Its path is `pairing_code_file`; paths resolve from the
working directory. Set `data_dir` to a persistent private directory.

For the first start only:

```sh
mise exec -- node bridges/homekit/dist/cli.js --config configs/homekit.yaml --initialize
```

`--initialize` is an explicit authorization to create an identity in a new/empty
directory. It fails if an identity already exists. On every later start, omit it:

```sh
mise exec -- node bridges/homekit/dist/cli.js --config configs/homekit.yaml
```

After the initialized bridge reports ready, stop it with Ctrl-C and explicitly
request pairing information:

```sh
mise exec -- node bridges/homekit/dist/cli.js pairing-info --config configs/homekit.yaml
```

That command prints sensitive pairing information to stdout. Do not send it to
shared logs. It takes the same exclusive storage lock, so it cannot run while
the bridge is serving. Restart without `--initialize`, keeping the displayed
information private. In Apple Home, choose Add Accessory, More Options, select the Hearth
bridge, and enter the setup code. Apple may label it uncertified; this project
does not claim Apple certification. Assign names/rooms in Apple Home as desired.

Settings resolve CLI flags > `HEARTH_BRIDGE_HOMEKIT_` environment > optional
`configs/homekit.yaml` > defaults. For example `--hap-port` overrides
`HEARTH_BRIDGE_HOMEKIT_HAP_PORT`. Use `--config` or
`HEARTH_BRIDGE_HOMEKIT_CONFIG` for a required explicit configuration file.
`accessories` and `advertise_interfaces` stay YAML-only. Unknown/duplicate keys,
multiple YAML documents, and malformed input fail rather than silently changing
the exports. Restart after editing configuration.

The root bridge name must fit 58 UTF-8 bytes, leaving room for HAP's identity
suffix in its mDNS label. Accessory names retain the 64-UTF-16-code-unit limit.
Longer or multibyte root names that exceed the byte limit fail configuration
before a new identity is created; they are never silently shortened.

## State and failure behavior

- On controls power. Brightness controls only brightness, including at zero;
  it never synthesizes a second power Command.
- Brightness requests snap to the nearest supported command step, with ties
  upward and bounded by the largest valid step not above the maximum. A request
  for 83 at maximum 100/step 5 sends 85. Readback preserves actual accepted State,
  so a device-reported 83 remains 83.
- Each received write is distinct, even at the same value. Concurrent writes
  may overlap; a combined On/Brightness request is not a transaction. No automatic
  command retries, durable queue, or replay occurs.
- Each HAP operation has an eight-second total budget. A timeout or disconnect
  does not prove the action failed to execute. An already admitted Core Command
  may still finish and become visible through later reconciliation.
- Core HTTP reads are authoritative. Live NATS Facts request refreshes; periodic
  reads cover missed facts and availability/enablement changes. A NATS outage
  degrades promptness while HTTP/polling continue. Facts are not a lossless stream
  of physical events, and the bridge does not replay history.
- A missing/changed descriptor makes that accessory incompatible until restart;
  it does not disappear from HomeKit. Unusable reads return errors, not fake
  false/zero values. Apple Home may not display an unsolicited "No Response"
  transition until it interacts with the accessory.

Inspect `GET /v1/entities/<id>`, `GET /v1/entities/<id>/commands`, and
`GET /v1/commands/<command_id>` to distinguish satisfied attempts from requested,
rejected, timed-out, or interrupted attempts. A satisfied Command refers to
Adapter evidence, not independently established physical causation.

## Identity, backups, and recovery

Keep `bridge_id` and accessory `key` values stable. Names and list order can
change without changing accessory identity. A different `kind` needs a new key.
Pointing an existing key at a different compatible Entity is a deliberate
replacement behind that identity. Adding brightness retains existing On identity.
Removing/re-adding an accessory can still lose Apple room/scene assignments;
numeric ID retention cannot guarantee restoration of deleted Apple configuration.

Use one persistent local filesystem data directory per bridge and only one
writer. The public CLI holds a kernel `flock` for the serving lifetime. Do not
delete lock files to bypass a running owner. Shared/network filesystems are not
supported. Pairing keys and identifier cache belong to this directory, not the
configuration. Never copy the disposable experiment's identity into production.
If configuration is replaced during startup and selects a different data
directory, startup fails with `locked_data_directory_mismatch` before opening
identity storage. Restart with the intended configuration; do not bypass the lock.

Back up the **complete directory while stopped**, including the private manifest
and `hap/`. Restore it as a unit with the same bridge configuration and secret.
Do not copy individual pairing/cache files from different backups. Ordinary
startup fails on a missing mount, malformed/half-initialized identity, changed
setup code, or missing required files rather than generating a replacement.
An interrupted initialization needs inspection; do not repeatedly initialize it.

The bridge uses unmodified HAP-NodeJS storage and pairing behavior. A pairing
write failure may return an error while the process continues with in-memory
pairing changes that will not survive restart. Repair storage and restart before
retrying pairing changes; there is no guaranteed immediate stop for these errors.
Companion manifest write failures and detected identity loss still stop the bridge.
Power-loss durability and detection of every external filesystem replacement are
not guaranteed. If intentionally abandoning an identity, remove the old bridge in
Apple Home first, preserve the old data for recovery, and explicitly initialize a
different empty directory. There is no automatic reset command.

SIGTERM/SIGINT stop new operations and drain admitted work within ten seconds.
Allow at least 15 seconds termination grace in the supervisor. Restart does not
replay Commands. Core/NATS outages after startup retain the bridge and pairing;
startup itself requires those dependencies within 30 seconds. The local HAP
listener is probed separately from Core; two consecutive failed probes stop the
process for supervisor restart.

Successful writes may omit the optional response value, including off and zero
brightness. Reads and subscribed notifications still use observed Hearth State;
the bridge does not patch the HomeKit library to change response serialization.

## Network discovery and container deployment

Interface selection limits mDNS advertisement, **not necessarily TCP binding**.
The HAP library can listen on `0.0.0.0`. Use an operator-managed firewall allowing
UDP 5353 multicast and the configured HAP TCP port only from the trusted LAN.
Never forward this port from the internet, widen Core/NATS exposure, or run the
container privileged. The bridge never edits firewall rules or installs a
multicast reflector.

Check discovery and TCP separately from another LAN machine, for example:

```sh
timeout 10s avahi-browse --resolve --terminate _hap._tcp
nc -vz -w 3 <bridge-lan-address> 51826
```

A local `advertised` log/event does not prove an iPhone can discover the bridge.
Check VLAN isolation, Wi-Fi multicast policy, firewall, and reachable interface
addresses before resetting pairing. Host listener probes prove neither external
LAN reachability nor continuing multicast delivery.

The release image is
`ghcr.io/mholtzscher/hearth/hearth-bridge-homekit`. Pin a repository release tag.
Use **Linux host networking**, not Docker Desktop or ordinary NAT port publishing.
Provide a data directory owned by the image's unprivileged user (UID/GID 1000), a
read-only configuration, and a read-only setup-code file. In the container YAML,
set `data_dir: /var/lib/hearth-homekit` and
`pairing_code_file: /run/secrets/homekit-code`.

```sh
docker run --name hearth-homekit --network host --read-only \
  --cap-drop ALL --security-opt no-new-privileges --stop-timeout 15 \
  -v /srv/hearth/homekit-data:/var/lib/hearth-homekit \
  -v /srv/hearth/homekit.yaml:/etc/hearth/homekit.yaml:ro \
  -v /srv/hearth/homekit-code:/run/secrets/homekit-code:ro \
  ghcr.io/mholtzscher/hearth/hearth-bridge-homekit:<release-tag> \
  --config /etc/hearth/homekit.yaml
```

Add `--initialize` only on the first, explicitly authorized start. To request
pairing information, stop the bridge and run the same mounted image with
`pairing-info --config ...` as its arguments, then restart without `--initialize`.
Do not initialize or replace the store. Host networking
lets existing host-loopback Core/NATS remain unchanged. On an IPv6/segmented LAN,
the operator remains responsible for multicast and TCP firewall policy.

## Production acceptance record

Automatic checks are `mise run homekit-test`, `mise run homekit-integration`,
`mise run homekit-container-smoke`, and the integrated `mise run validate`.
The container smoke builds the image, pairs over encrypted HAP, executes a Core
Command, and repeats authenticated control after restarting the same private
volume. It verifies the default image user is `node` and maps the running user to
the nonroot host UID for private bind-mount portability in CI. Integration
uses private loopback Core/NATS/simulator processes and a synthetic local-only
agent key; no provider requests or household deployment are needed.

A13 in the spec requires a separate operator check with an iPhone:

1. Pair production builds exposing an on/off light, a dimmable light, and a plug.
2. Exercise true/false, brightness zero/positive/stepped values, and rapid controls.
   Retain Core Command IDs/statuses and matching Observation IDs; do not record
   setup codes, pairing files, household names/addresses, or raw State traces.
3. Publish simulated physical changes without touching Home controls and confirm
   tile updates. Cover both directions and a brightness update.
4. Restart with the same data. Verify control, reverse updates, and **room
   assignment** without re-pairing.
5. Stop Core, exercise controls, record the actual failure presentation, restart
   Core, and verify authoritative recovery without re-pairing. Repeat a NATS
   interruption, allowing periodic reconciliation to operate.
6. Record tested revision, iOS version, profiles, outcomes, and remaining gaps in
   a sanitized acceptance report. Earlier prototype results do not satisfy this
production gate. Stop any temporary stack and remove temporary firewall rules.

The initial production-build check is recorded in
[HomeKit acceptance results](homekit-acceptance.md), including validation limits.

For a household simulator session, use `mise run simulator-start` and stop with
`mise run simulator-stop`. Do not replace household pairing data for a test.
