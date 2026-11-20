# HomeKit Adapter implementation spec

**Status:** Approved for implementation (Michael, 2026-11-19, Plannotator review `pn-4d2df2`); implementation in progress
**Type:** Feature plan
**Effort:** XL, approximately 7 to 10 focused days at 60% confidence
**Date:** 2026-11-19
**Baseline:** `main` at `422276f`
**Depends on:** the Adapter SDK Session, Adapter Functions, Adapter health, Core HTTP Device and Entity reads, Core HTTP Commands, and the Device Fact stream
**Target evidence:** the household's iPhone and Apple home hub on the trusted LAN. Not yet collected: HomeKit behavior in this spec comes from upstream source and documentation, not from this household.

## Implementation checkpoint

- **Playbook:** mstack Spec implementation. **Scope:** this approved Go Adapter spec; the closed TypeScript bridge PR #180 is not the delivery branch.
- **Worktree:** `/home/michael/code/worktrees/hearth/feat-homekit-adapter`, branch `feat/homekit-adapter`, based on `origin/main` at `422276f`.
- **Completion checklist:** the D1–D12 dependencies and A1–A17 checks below remain the contract. D12 is conditional on a confirmed library defect.
- **Evidence:** the original `main` checkout is clean after moving the spec. Baseline Discord and SDK unit tests passed. After integrating D3–D5, focused tests with `-tags=integration -count=1`, lint and vet passed for `./cmd/hearth-adapter-homekit`, `./internal/app/homekit`, and `./internal/adapters/homekit`. Real NATS and SQLite verified Session claim/release on cancellation. Real JetStream verified tail delivery, rejecting a mismatched subject, and reporting consumer deletion. An HTTP socket fixture verified 201 Entities, canonical units and one Command POST per call. Unit tests verified stale-Fact rejection, setup URI encoding, identity retention and store fault handling, and explicit roles.
- **Current step:** D1–D10 and D12 implemented on [draft PR #411](https://github.com/mholtzscher/hearth/pull/411). Local automated acceptance is verified. A17 requires a household operator; the software controller is not a substitute.
- **Next action:** verify the published revision's CI and complete A17 with the household operator. Start with a single test light on the target Linux host. Do not use an existing pairing store for the acceptance run.
- **Contract decisions:** keep the existing Core API and preserve only the structured failure fields Core sends. Michael confirmed that `pairing_info` returns the setup code and URI only while unpaired, accepts the resulting 30-day Function history exposure, and does not log either credential. Michael chose WindowCovering for garage covers; defer GarageDoorOpener until Hearth has obstruction evidence.
- **Autonomous continuation:** Production Core verified 201 Entities, HTTP and MCP management, successful reset, and HTTP recovery after consumer deletion within 5 seconds, before the normal 60-second resync. `/tmp/opencode/hearth-homekit-wire.rZNe76/run-3.log` records race-enabled software pairing, encrypted writes and notifications, old-controller revocation on reset, and graceful SIGTERM. CI passed `backend` and `web` on the initial draft revision, `c326e8e`. Full local validation passed with the acceptance follow-up (`/tmp/opencode/hearth-homekit-validate-acceptance.log`). It covers a combined exposure reload, health faults and recovery, and schema and retained history for all five Functions. Decision trail: `/tmp/opencode/hearth-homekit-decisions.tsv`.
- **Container verification:** Linux amd64 non-root host-network deployment passed the same software pairing, encrypted write/event, reset and shutdown checks (`/tmp/opencode/hearth-homekit-wire.rZNe76/run-docker.log`). Arm64 image build passed; arm64 runtime not tested.
- **Runtime review:** Reproduced four findings: an unreadable purifier mode with only `active` mapped, contradictory cover target events during movement, delayed shutdown with an admitted mutation, and listing blocked while HAP was stopped. Focused race tests and full validation passed after the fixes (`/tmp/opencode/hearth-homekit-validate-reviewed.log`). A failed pairing reset can still make pairing-status reads busy until recovery. A notification already in flight can overlap a new write; the cover regression exercises Observations processed after admission. Startup still exits on a failed initial inventory read or HAP bind. The household operator plans to run A17 separately.

## Problem

Hearth has no native way to control household Devices from an iPhone. The household wants to use the Apple Home app, Siri and Apple home-hub remote access, which means implementing the HomeKit Accessory Protocol (HAP) as a bridge that presents selected Hearth Devices as HomeKit accessories.

The Agent must be able to add, change and remove the accessories the bridge exposes, report pairing state and reset pairing. Adapter Functions already give the Agent and Automations one validated, evidence-retaining way to call Adapter-owned capabilities, so the bridge is an Adapter that registers Functions.

The shape is not obvious because this Adapter runs in the opposite direction from every existing one:

- It registers no Devices. It reads other Adapters' Devices and State, which the SDK deliberately cannot do (`docs/architecture/architecture.md:72`), so it also acts as an ordinary client of Core's HTTP API and Device Fact stream.
- HomeKit requires stable accessory and characteristic identifiers plus pairing keys that survive restarts. Losing them breaks pairing and the household's rooms, scenes and Home automations. Every existing first-party companion is stateless (`docs/architecture/architecture.md:318`).
- Hearth splits one physical light into `power`, `brightness`, `colortemp` and `colorhs` Entities, and a Levoit purifier into three `power` and two `enumsetting` Entities. HomeKit wants one accessory with typed services.
- `POST /v1/entities/{entity_id}/commands` returns only when the Command reaches its outcome, up to 30 seconds for locks and 60 seconds for covers. HomeKit expects a prompt answer to a characteristic write.

## Decision and scope

Add `hearth-adapter-homekit`, a Go process with two roles:

```text
iPhone / Apple home hub
    <-- HAP over TCP + mDNS -->  hearth-adapter-homekit
        --> Adapter SDK Session (claim, heartbeat, health, Functions) --> Core NATS --> hearthd
        --> Core HTTP /v1 (Devices, Entities, Commands) ------------------------------> hearthd
        <-- Device Fact stream HEARTH_DEVICE_FACTS_V1 (observation family) <---------- hearthd
```

1. **Adapter Session.** It claims an Adapter instance, reports Adapter health, and registers five Functions: `list_accessories`, `expose_device`, `unexpose_device`, `pairing_info` and `reset_pairing`. It registers no Devices or Entities.
2. **Core client.** It reads Devices and Entities over HTTP with canonical units. It applies live State from an ephemeral JetStream consumer on observation Facts. It sends iPhone writes as Commands over HTTP.

Use [`github.com/brutella/hap`](https://github.com/brutella/hap) v0.0.35 (Apache-2.0). Its last commit was October 2024, and it has open reports about bridged state synchronization and pairing listing. If the implementation or real-device acceptance hits a library defect, copy the library into `internal/third_party/hap` with its license and notice, rewrite the imports, and fix it there. The household expects this to happen.

The process keeps one data directory that only it writes. It holds HAP pairing keys, the setup code, the exposure list, and accessory and service identifier allocations. Back up the directory with Hearth. This is an explicit exception to the stateless-companion rule, recorded in a new ADR (D1).

### V1 includes

- Accessory kinds: Lightbulb, Switch, Outlet, LockMechanism, WindowCovering, Thermostat, AirPurifier (with AirQualitySensor and FilterMaintenance), and sensor accessories with TemperatureSensor, HumiditySensor, ContactSensor, MotionSensor, OccupancySensor and LeakSensor services.
- One bridge per Adapter instance, at most 149 exposed accessories.
- Exposure controlled only through Functions. Only Devices that the Agent or an operator explicitly exposes appear in HomeKit.
- Live State through Device Facts, plus a periodic full HTTP resync for availability, enablement, support and names.
- "No Response" in the Home app when the backing Entity is unavailable, disabled, missing or has null State.
- Prompt write responses with background Commands. A failed Command restores the characteristic to the current State.
- Light write coalescing and ordering.
- Pairing by the 8-digit setup code or the `X-HM://` setup URI.
- Docker deployment with host networking and a persistent volume.

### V1 defers

- Fans, thermostat fan modes, battery services, cameras, TVs, doorbells, security systems and Adaptive Lighting. Hearth has no fan Entity type.
- GarageDoorOpener. Expose garage covers as WindowCovering while Hearth lacks obstruction evidence.
- HomeKit Secure Video, HomeKit Bluetooth, Thread and Matter.
- Several bridges per process, or automatic splitting past 149 accessories. Run a second Adapter instance instead.
- Exposing whole areas or kinds automatically.
- Rendering the setup QR code. `pairing_info` returns the `X-HM://` URI, which any QR tool can render.
- Showing HomeKit as a source of the Command in Hearth history. Commands from the bridge look like any other HTTP Commands.
- Authentication on Core HTTP or NATS. The deployment remains a trusted-network deployment.

## Solution space and trade-offs

| Option | Exposes to callers | Hides | Verdict |
| --- | --- | --- | --- |
| Core module hosting HAP | Nothing new | HAP inside `hearthd` | Rejected: puts pairing credentials and an external protocol connection inside Core (`architecture.md:17`) |
| Stateless standalone client with a YAML allowlist | A static allowlist edited by the operator | All HAP detail | Rejected: the Agent cannot configure it, and health is invisible in Hearth |
| Adapter with Functions that is also a Core client | Five Functions with JSON Schemas | HAP, identifier persistence, sync, reloads, Command timing | **Chosen** |
| Homebridge plugin in TypeScript | Homebridge UI and plugin config | HAP via the mature HAP-NodeJS | Fallback only: adds a Node runtime and puts the integration outside the Go tests and Functions |
| Matter bridge | Matter commissioning | Cross-ecosystem control | Deferred: no mature Go Matter bridge library, and Apple requires a home hub for Matter |

## Operator and configuration contract

The CLI follows the Discord and Pirate Weather Adapters: `urfave/cli/v3`, `platform.LoadYAML`, flags that override `HEARTH_ADAPTER_HOMEKIT_*` environment variables, which override YAML.

```yaml
# configs/homekit.example.yaml
adapter_id: homekit
nats_url: nats://127.0.0.1:4222
core_url: http://127.0.0.1:8080
data_dir: /var/lib/hearth-adapter-homekit
bridge_name: Hearth
listen_port: 21063
interfaces: []              # empty advertises on every interface
resync_interval: 60s
temperature_display_unit: celsius
```

| Field | Rule |
| --- | --- |
| `adapter_id` | Required subject-safe slug (`internal/platform/config/validate.go`) |
| `nats_url` | NATS URL; default `nats://127.0.0.1:4222` |
| `core_url` | Required `http` or `https` URL with a host and no path other than `/`; the client appends `/v1` |
| `data_dir` | Required absolute path. The process creates it with mode `0700` if it is missing and fails at startup if it cannot write it |
| `bridge_name` | 1 to 64 characters; default `Hearth`. HomeKit shows it while pairing |
| `listen_port` | 1024 to 65535; default `21063`. A fixed port keeps firewall rules simple |
| `interfaces` | Optional network interface names for mDNS announcements |
| `resync_interval` | `15s` to `10m`; default `60s` |
| `temperature_display_unit` | `celsius` or `fahrenheit`; default `celsius`. Sets the Thermostat `TemperatureDisplayUnits` characteristic. The Home app converts on its own |

The configuration has no setup-code field. The process generates a valid HAP setup code and a 4-character setup ID on first start and stores them in `data_dir`. Retrieve the setup code and URI with `pairing_info` while the bridge is unpaired. Neither credential is logged.

Run the release image with host networking so that mDNS multicast and the HAP port reach the LAN:

```yaml
services:
  hearth-adapter-homekit:
    image: ghcr.io/mholtzscher/hearth/hearth-adapter-homekit
    network_mode: host
    volumes:
      - ./homekit.yaml:/config/homekit.yaml:ro
      - hearth-homekit-data:/var/lib/hearth-adapter-homekit
    command: ["--config", "/config/homekit.yaml"]
```

UDP 5353 and the configured TCP port must reach the iPhone and the home hub. An mDNS record can be visible while the advertised TCP address is unreachable, so the guide documents both checks.

## Startup order and process ownership

1. Validate configuration and open `data_dir`.
2. Claim the Adapter Session with `adapter.Connect`. Fencing then guarantees one active runtime per Adapter instance, so the store needs no file lock.
3. Load the store. Generate the setup identity if absent.
4. Start the Fact consumer, buffering Facts.
5. Read the full inventory over HTTP with `units=canonical`.
6. Apply buffered Facts with the ordering rule in [State projection](#state-projection).
7. Build accessories from the exposures and start the HAP server.
8. Start the Function server. The catalog is fixed per Session.
9. Report `healthy` once the HAP server listens, the inventory read succeeded and the Fact consumer is active.
10. Run the resync loop, Fact application and the Command runner until the context ends.

Shutdown stops new HAP writes, cancels and joins the Command runner, stops the HAP server, stops the Fact consumer, closes the Function server and releases the Session. Background Commands already admitted by Core continue in Core after the HTTP caller disconnects, and the bridge does not wait for their outcomes.

## Identity and persistence

The data directory has two parts:

```text
/var/lib/hearth-adapter-homekit/
├── hap/            # brutella/hap FsStore: bridge identity, long-term keys, paired controllers, config version
└── bridge.json     # exposures, AID and service-slot allocations, setup code and setup ID
```

`bridge.json` is written atomically: write a temporary file in the same directory, `fsync` it, rename it over the old file, then `fsync` the directory. A failed write leaves the previous file unchanged and fails the Function that caused it with `store_write_failed`.

Identifier rules:

- The bridge accessory has AID 1. Exposed Devices receive AIDs from a counter starting at 2. The counter only increases, and AIDs are never reused.
- `unexpose_device` moves the exposure's AID and service slots into a `retired` map. Exposing the same Device again restores them, so Home can reattach its room and scene assignments.
- Each service in an accessory has a persisted slot keyed by `role/entity_id`. The service IID is `slot * 100`, and each characteristic IID is the service IID plus a fixed offset from a per-characteristic table in code. AccessoryInformation always uses slot 0. The offset table is append-only.
- A Device's canonical ID is the accessory serial number. The Device display name or the exposure's `name` override is the initial accessory name. After pairing, Home keeps its own names, so a later Hearth rename does not rename the accessory in Home.

`reset_pairing` stops the current HAP server and atomically persists a new pairing generation, setup code, and setup ID. The new server uses a new private directory (`hap-1/`, `hap-2/`, and so on); older generations are retired and removed. Exposures and identifier allocations stay in `bridge.json`. If the file rename has happened but directory sync fails, the Function returns an uncertain outcome rather than claiming rollback.

## Exposures, roles and planning

An exposure maps one Hearth Device to one HomeKit accessory. Each of the Device's mapped Entities fills a role.

| Kind | Default for Device kind | Roles (required in bold) | Entity types |
| --- | --- | --- | --- |
| `lightbulb` | `light` | **`on`**, `brightness`, `color_temperature`, `color` | `power/v1`, `brightness/v1`, `colortemp/v1`, `colorhs/v1` |
| `switch` | `relay` | **`on`** (one or more) | `power/v1` |
| `outlet` | none | **`on`** (one or more) | `power/v1` |
| `lock` | `lock` | **`lock`** | `lock/v1` |
| `window_covering` | `cover` | **`position`** | `cover/v1` |
| `thermostat` | `thermostat` | **`thermostat`** | `thermostat/v1` |
| `air_purifier` | `air-purifier` | **`active`**, `mode`, `speed`, `child_lock`, `pm25`, `filter_life` | `power/v1`, `enumsetting/v1`, `enumsetting/v1`, `power/v1`, `numericsensor/v1` with quantity kind `particulate_matter`, `numericsensor/v1` with unit `%` |
| `sensor` | `sensor` | at least one of `temperature`, `humidity`, `contact`, `motion`, `occupancy`, `leak` (each repeatable) | `temperature/v1`, `relativehumidity/v1`, `binarysensor/v1` for the last four |

Compatible kind overrides: any Device with a `power/v1` Entity may use `switch` or `outlet`. Every other kind needs a Device of its default kind.

Role inference runs when `expose_device` omits `entities`:

1. For each role, collect the Device's Entities whose type, and for numeric sensors whose quantity kind or unit, match the role.
2. A role with exactly one candidate that no other role claims is assigned. Repeatable roles (`on` for switches and outlets, `temperature` and `humidity` for sensors) take every candidate.
3. Two roles with the same Entity type, such as `active` and `child_lock`, or `mode` and `speed`, are ambiguous. Report them as unresolved.
4. A `binarysensor/v1` Entity is always unresolved because Hearth does not record its sensor class.
5. Entities that match no role are listed as unmapped and ignored.

Core's HTTP API does not expose Adapter Entity keys, so names are the only hint for an ambiguous role. The Agent reads the unresolved roles and Entity names from `list_accessories` and calls `expose_device` with explicit `entities`. The bridge never guesses from names.

The resolved assignments are stored with the exposure. Entities added to the Device later are not exposed until `expose_device` is called again.

## Accessory mappings

All conversions use canonical units. Every characteristic is backed by exactly one Entity.

### Lightbulb

| Characteristic | From State | Write |
| --- | --- | --- |
| `On` | `power` boolean | `set {"value": bool}` on `on` |
| `Brightness` (0 to 100, step 1) | `brightness` integer | `set {"value": n}` |
| `ColorTemperature` (mireds) | `colortemp.value` | `set {"value": mireds}`; props `minValue`/`maxValue` from support `minimum`/`maximum` |
| `Hue` (0 to 360), `Saturation` (0 to 100) | `colorhs.hue`, `colorhs.saturation` | One `set {"hue", "saturation"}` per coalesced batch. A missing half comes from the latest State. Hue 360 becomes 359 |

Writes for one accessory within a 25 ms window form one batch. If the batch sets `On` to false, only `power set false` is sent. Otherwise `power set true` goes first when present, followed by brightness and then the color write. If a batch has both color temperature and hue/saturation, the last one received wins, matching the Home app's single color picker.

### Switch and outlet

Each `on` Entity is one `Switch` or `Outlet` service. `OutletInUse` mirrors `On` because Hearth has no power-draw evidence for the relay.

### Lock

| Hearth `lock/v1` State | `LockCurrentState` | `LockTargetState` when no write is pending |
| --- | --- | --- |
| `locked` | secured (1) | secured |
| `unlocked` | unsecured (0) | unsecured |
| `locking` | the previous current state | secured |
| `unlocking` | the previous current state | unsecured |
| `jammed` | jammed (2) | unchanged |
| `unknown` | unknown (3) | unchanged |

A `LockTargetState` write sends `set {"value": "locked" | "unlocked"}`.

### Window covering

`WindowCovering`: `CurrentPosition` comes from `position`. `PositionState` is increasing for `opening`, decreasing for `closing` and stopped for `idle`. `TargetPosition` holds the pending write until the Command completes and otherwise mirrors `position`. A `TargetPosition` write sends `set {"position": n}`. A `HoldPosition` write sends `stop` when support lists `stop`; otherwise the characteristic is omitted.

Garage covers use the same service. The bridge does not claim physical closure or expose an obstruction characteristic.

### Thermostat

Temperatures convert between milli-Celsius and Celsius as `c = mCel / 1000` and `mCel = round(c * 1000)`, with a step of 0.1. Temperature props come from support `temperature_minimum` and `temperature_maximum`.

| Hearth | HomeKit |
| --- | --- |
| `mode` `off`, `heat`, `cool` | `TargetHeatingCoolingState` off, heat, cool |
| `mode` `heat_cool` or `auto` | auto |
| `mode` `dry` or `fan_only` | cool. These modes cannot be selected from HomeKit |
| `activity` `heating` or `preheating` | `CurrentHeatingCoolingState` heat |
| `activity` `cooling` | cool |
| any other or missing `activity` | off |
| `current_temperature` | `CurrentTemperature` |
| `target_temperature` | `TargetTemperature`; a write sends `set_target` |
| `target_temperature_low` and `target_temperature_high` | `HeatingThresholdTemperature` and `CoolingThresholdTemperature`, present when support lists `set_range`. Writes in one 25 ms batch send one `set_range`, with any missing half taken from State |
| `current_humidity`, `target_humidity` | `CurrentRelativeHumidity`, plus `TargetRelativeHumidity` when support lists `set_humidity` |

`TargetHeatingCoolingState` valid values include only modes in support `modes`. A HomeKit auto write sends `set_mode heat_cool` when supported and `auto` otherwise. `TemperatureDisplayUnits` comes from configuration and is read-only.

### Air purifier

| Characteristic | Source | Write |
| --- | --- | --- |
| `Active` | `active` | `set {"value": bool}` |
| `CurrentAirPurifierState` | inactive when `active` is false, otherwise purifying | none |
| `TargetAirPurifierState` | auto when `mode` is `auto`, otherwise manual | auto sends `mode set "auto"`. Manual sends `mode set "manual"` when it is a choice, otherwise the first choice that is not `auto` |
| `RotationSpeed` (step `100 / n`) | `speed` choice at index `i` of `n` support choices gives `round(100 * (i + 1) / n)` | the speed `s` selects choice `ceil(s * n / 100) - 1`, clamped to `0..n-1`. Speed 0 sends nothing; HomeKit sends `Active` separately |
| `LockPhysicalControls` | `child_lock` | `set {"value": bool}` |
| `AirQualitySensor.PM2_5Density` and `AirQuality` | `pm25` µg/m³. Air quality: excellent ≤ 12, good ≤ 35, fair ≤ 55, inferior ≤ 150, poor above | none |
| `FilterMaintenance.FilterLifeLevel` and `FilterChangeIndication` | `filter_life` percent. Change indication when ≤ 10 | none |

Speed uses the order of support `choices`. Levoit lists `1` through `4`, which gives 25, 50, 75 and 100. The `sleep` mode reads as manual. Levoit's optimistic Observations and later stale polls can make the Home tile flip back and forth. The guide states this limitation, which belongs to the Levoit Adapter.

If `mode` is not assigned, the target mode reads Manual but is not writable. This describes the bridge's controls, not an observed physical mode.

### Sensors

`temperature` gives a `TemperatureSensor` in Celsius. `humidity` gives a `HumiditySensor`. `contact`, `motion`, `occupancy` and `leak` give the matching sensor service.

`binarysensor/v1` defines true only as the upstream "on" reading, and upstream conventions disagree. An ESPHome door sensor is on when open, while Zigbee2MQTT reports `contact: true` when closed. No Hearth Adapter reports contact sensors yet. Each binary sensor assignment therefore has an optional `inverted` flag, default false:

| Role | `inverted: false` | `inverted: true` |
| --- | --- | --- |
| `contact` | true means open: `ContactSensorState` not detected (1) | true means closed: `ContactSensorState` detected (0) |
| `motion`, `occupancy`, `leak` | true means detected | true means not detected |

`inverted` is rejected with `entity_type_mismatch` on any role that is not backed by `binarysensor/v1`.

## State projection

The bridge keeps the latest State per backed Entity:

```text
latest[entity_id] = {value, observation_id, observed_at, availability, enabled}
```

- The inventory read sets every field.
- An observation Fact sets `value`, `observation_id` and `observed_at` when the Fact's `observed_at` is not earlier than the stored one. Core assigns `observed_at` on receipt, so it agrees with Core receive order except across clock adjustments (`docs/reference/state-changes.md:20`). An earlier Fact is dropped.
- A Fact for an Entity that no exposure backs is ignored.
- Facts carry no availability or enablement. The resync loop reads them every `resync_interval`.
- A resync that finds a changed value, availability, enablement, support or Device presence updates the projection. A support change that alters an accessory's characteristics, or a Device or Entity that appears or disappears, triggers a rebuild.

The Fact consumer is an ephemeral, ordered JetStream consumer on `HEARTH_DEVICE_FACTS_V1`. It filters to `hearth.v1.core.fact.entity.*.observation.*`, uses `DeliverNewPolicy` and leaves no durable state on the broker. It decodes envelopes with `internal/contracts/v1/natswire` and validates payloads against `contracts/v1` `ObservationFactSchemaID`. After any consumer interruption, the bridge re-creates the consumer and runs a full resync, because Facts published during the gap are lost.

A characteristic is reachable when its Entity exists, is enabled, has non-null State and has availability `available` or `unknown`. Reads of an unreachable characteristic return HAP status `-70402`. In practice the Home app then shows "No Response" for the accessory.

## Writes and Command runner

A HAP characteristic write follows these steps:

1. If the backing Entity is unreachable, return `-70402` at once.
2. Validate the value against the characteristic's props and return `-70410` if invalid.
3. Add the write to the accessory's coalescing batch, then return success to the iPhone. This tells HomeKit that Hearth accepted the request, not that the device changed.
4. When the batch window closes, the runner sends Commands over HTTP in the batch's order. Each HTTP request has a timeout of the operation's deadline plus 5 seconds.
5. A satisfied or dispatched Command needs no further action. The Fact for its linked Observation updates the characteristic.
6. On any other result (504 `outcome_timeout`, 503, 409 `entity_disabled`, 502, 400, transport failure), the runner logs a warning with `command_id` when known and restores the Entity's target and current characteristics from the latest State. It does not retry.

The runner owns every background Command goroutine and joins them at shutdown. At most 32 Commands run at once. Beyond that, writes return `-70403` (resource busy) without being queued.

## Accessory reloads

`brutella/hap` cannot add or remove accessories while it runs. Exposure changes and rebuilds therefore stop the HAP server and start a new one with the same store. The library hashes the accessory database and increases the configuration number, which makes controllers reload it. Rebuild requests within 2 seconds are combined. During a reload, accessories show "No Response" briefly.

A Device or Entity that disappears from Core stays exposed with every characteristic unreachable. `list_accessories` reports it as `missing`. The bridge never removes an exposure on its own.

## Adapter health

One goroutine serializes `SetHealth` reports, following the Ecowitt queue pattern.

| Status | Condition |
| --- | --- |
| `healthy` | HAP server listening, last inventory read succeeded within `3 * resync_interval`, and the Fact consumer active |
| `unhealthy` `adapter.hearth-adapter-homekit.hap_server_failed` | HAP server failed to start or exited |
| `unhealthy` `adapter.hearth-adapter-homekit.core_unreachable` | No successful inventory read within `3 * resync_interval` |
| `unhealthy` `adapter.hearth-adapter-homekit.facts_unavailable` | The Fact consumer cannot be created or has failed for longer than `resync_interval` |

Pairing state is not health. An unpaired bridge is healthy.

## Functions

All Functions use the SDK `FunctionDescriptor` with a 10,000 ms timeout, except that `expose_device`, `unexpose_device` and `reset_pairing` use 30,000 ms to include a reload. Function results are retained in SQLite for 30 days.

### `list_accessories`

Input `{}`. Output:

```json
{
  "paired": true,
  "accessory_count": 2,
  "accessory_limit": 149,
  "accessories": [
    {"device_id": "dev_…", "name": "Kitchen light", "kind": "lightbulb", "aid": 2,
     "status": "reachable", "entities": [{"entity_id": "ent_…", "role": "on"}]}
  ],
  "candidates": [
    {"device_id": "dev_…", "name": "Bedroom purifier", "device_kind": "air-purifier",
     "default_kind": "air_purifier", "allowed_kinds": ["air_purifier", "switch", "outlet"],
     "proposed": [{"entity_id": "ent_…", "role": "pm25"}],
     "unresolved": [{"role": "active", "reason": "ambiguous",
                     "candidates": [{"entity_id": "ent_…", "name": "Power"}, {"entity_id": "ent_…", "name": "Child lock"}]}],
     "unmapped": [{"entity_id": "ent_…", "name": "Display"}]}
  ]
}
```

`status` is `reachable`, `partially_unreachable`, `unreachable` or `missing`. `candidates` lists unexposed Devices whose kind has a default accessory kind or that have a `power/v1` Entity. Unresolved reasons are `missing_required`, `ambiguous` and `class_required`. The output schema bounds arrays to 512 items. Failure: `not_executed` `core_unreachable` when the latest inventory is older than `3 * resync_interval`.

### `expose_device`

Input:

```json
{"device_id": "dev_…", "kind": "air_purifier", "name": "Bedroom purifier",
 "entities": [{"entity_id": "ent_…", "role": "active"}, {"entity_id": "ent_…", "role": "mode"}]}
```

`kind`, `name` and `entities` are optional. An `entities` item may include `"inverted": true` for a binary sensor role, as described in [Sensors](#sensors). Exposing an already exposed Device replaces its kind, name and assignments and keeps its AID and existing slots. Output is the stored accessory in the `list_accessories` item shape.

Reason codes, all `not_executed`: `device_not_found`, `kind_incompatible`, `roles_unresolved`, `entity_not_on_device`, `entity_type_mismatch`, `duplicate_role`, `capacity_reached`, `store_write_failed`. When the store write succeeds but the reload fails, the result is `failed` `reload_failed`: the exposure is saved and appears after the next successful reload.

### `unexpose_device`

Input `{"device_id": "dev_…"}`. Output `{"device_id": "dev_…", "status": "unexposed"}`. Unexposing a Device that is not exposed succeeds with `status` `not_exposed`. Reason codes match `expose_device` where they apply.

### `pairing_info`

Input `{}`. Output while unpaired:

```json
{"paired": false, "setup_code": "123-45-678", "setup_uri": "X-HM://0023ABCDE1HRTH"}
```

Output while paired: `{"paired": true, "controller_count": 2}`. The Function returns the setup code only while the bridge is unpaired. Copies already in Function history remain readable for the retention period, even after pairing. Treat access to Function history as access to the setup credential while that code remains valid.

### `reset_pairing`

Input `{"confirm": "reset_all_homekit_pairings"}`, a required constant. Removes every paired controller, generates a new bridge identity, setup code and setup ID, keeps exposures and identifiers, and reloads. Output is the unpaired `pairing_info` shape, with the same history-retention caution. The description warns that every iPhone loses the bridge and Home must be set up again. Reason codes: `not_executed` `store_write_failed`, `failed` `reload_failed`.

## Module interfaces and implementation shape

The package is flat, following `internal/adapters/zigbee2mqtt`. File prefixes group responsibilities, and no subpackage boundary exists without a dependency reason. Only files prefixed `accessory_` and `hap_` import `brutella/hap`, so copying the library into the repository changes only their imports.

### Types

```go
// internal/adapters/homekit/model.go
package homekit

type AccessoryKind string

const (
	KindLightbulb      AccessoryKind = "lightbulb"
	KindSwitch         AccessoryKind = "switch"
	KindOutlet         AccessoryKind = "outlet"
	KindLock           AccessoryKind = "lock"
	KindWindowCovering AccessoryKind = "window_covering"
	KindThermostat     AccessoryKind = "thermostat"
	KindAirPurifier    AccessoryKind = "air_purifier"
	KindSensor         AccessoryKind = "sensor"
)

type Role string // "on", "brightness", "color_temperature", "color", "lock", "position",
// "thermostat", "active", "mode", "speed", "child_lock", "pm25", "filter_life",
// "temperature", "humidity", "contact", "motion", "occupancy", "leak"

type RoleAssignment struct {
	EntityID string `json:"entity_id"`
	Role     Role   `json:"role"`
	Inverted bool   `json:"inverted,omitempty"` // binarysensor/v1 roles only
}

// Exposure is the persisted, Function-owned decision to expose one Device.
type Exposure struct {
	DeviceID  string           `json:"device_id"`
	Kind      AccessoryKind    `json:"kind"`
	Name      string           `json:"name,omitempty"` // empty uses the Device display name
	Entities  []RoleAssignment `json:"entities"`
	AID       uint64           `json:"aid"`
	Slots     map[string]uint16 `json:"slots"` // "role/entity_id" -> service slot (>= 1)
	ExposedAt time.Time        `json:"exposed_at"`
}
```

```go
// internal/adapters/homekit/store.go
type storeFile struct {
	Version   int                         `json:"version"` // 1
	Setup     SetupIdentity               `json:"setup"`
	NextAID   uint64                      `json:"next_aid"` // starts at 2, only increases
	Exposures map[string]Exposure         `json:"exposures"`
	Retired   map[string]RetiredAllocation `json:"retired"`
}

type SetupIdentity struct {
	Code string `json:"code"` // 8 digits, not a HAP-disallowed code
	ID   string `json:"id"`   // 4 characters [0-9A-Z]
}

type RetiredAllocation struct {
	AID   uint64            `json:"aid"`
	Slots map[string]uint16 `json:"slots"`
}
```

```go
// internal/adapters/homekit/core_client.go
type Device struct{ ID, Name, Kind string }

type Entity struct {
	ID, DeviceID, Name, Type string
	Support                  json.RawMessage
	Enabled                  bool
	Availability             string // "available" | "unavailable" | "unknown"
	State                    *EntityState
}

type EntityState struct {
	Value         json.RawMessage
	ObservationID string
	ObservedAt    time.Time
}

type Inventory struct {
	Devices  map[string]Device
	Entities map[string]Entity
	ReadAt   time.Time
}

type CommandError struct {
	HTTPStatus int
	Code       string // "outcome_timeout", "entity_unavailable", "adapter_unhealthy", "entity_disabled", ...
	CommandID  string // empty when Core did not return one
}
```

### Interfaces

```go
// internal/adapters/homekit/core_client.go
type coreClient interface {
	// ReadInventory pages GET /v1/devices and GET /v1/entities?units=canonical to completion.
	ReadInventory(ctx context.Context) (Inventory, error)
	// ExecuteCommand calls POST /v1/entities/{id}/commands and returns nil on satisfied or dispatched.
	ExecuteCommand(ctx context.Context, entityID, operation string, parameters any) error // *CommandError
}

// internal/adapters/homekit/facts.go
type ObservationFact struct {
	EntityID      string
	ObservationID string
	Value         json.RawMessage
	ObservedAt    time.Time
}

type factSource interface {
	// Run delivers decoded observation Facts in stream order until ctx ends or the consumer fails.
	Run(ctx context.Context, deliver func(ObservationFact)) error
}

// internal/adapters/homekit/plan.go
type Proposal struct {
	Kind       AccessoryKind
	Assigned   []RoleAssignment
	Unresolved []UnresolvedRole
	Unmapped   []string // Entity IDs
}

type UnresolvedRole struct {
	Role       Role
	Reason     string // "missing_required" | "ambiguous" | "class_required"
	Candidates []string
}

func Propose(device Device, entities []Entity, kind AccessoryKind) Proposal
func ValidateExposure(exposure Exposure, inventory Inventory) error // *PlanError with a Function reason code

// internal/adapters/homekit/accessory.go
type commandSink interface {
	// Submit admits a write into the accessory's batch. It returns a HAP status error only for local rejection.
	Submit(write Write) error
}

type Write struct {
	EntityID   string
	Operation  string
	Parameters any
	Revert     func() // restores characteristics from latest State after a failed Command
}

type builtAccessory struct {
	accessory *accessory.A
	apply     func(entityID string, state projectedState) // updates values and notifies controllers
	entities  []string
}

func buildAccessory(spec accessorySpec, sink commandSink) (builtAccessory, error)

// internal/adapters/homekit/runtime.go
type Deps struct {
	Session *adapter.Session
	Core    coreClient
	Facts   factSource
	Store   *Store
	Config  Config
	Logger  *slog.Logger
}

func New(deps Deps) (*Runtime, error)
func (runtime *Runtime) Run(ctx context.Context) error
func (runtime *Runtime) Functions() []adapter.Function
```

`Runtime` is the only type `internal/app/homekit` uses. Everything else is unexported, apart from the types the Function schemas and tests need.

## Project layout

```text
cmd/hearth-adapter-homekit/
├── main.go                         # new — signal-aware entry point
├── cli_config.go                   # new — flags, HEARTH_ADAPTER_HOMEKIT_* env, YAML resolution
└── cli_config_test.go              # new
internal/app/homekit/
├── config.go                       # new — Config, Validate
├── config_test.go                  # new
├── run.go                          # new — Connect, Store, Core client, Fact source, Runtime, Function server, joins
└── journey_e2e_test.go             # new — hearthd + simulator + adapter through Functions and HTTP
internal/adapters/homekit/
├── model.go                        # new — kinds, roles, Exposure
├── store.go                        # new — bridge.json load, atomic save, AID and slot allocation
├── setup_code.go                   # new — setup code and ID generation, X-HM:// URI encoding
├── core_client.go                  # new — paged inventory reads, Command calls, error mapping
├── facts.go                        # new — ephemeral ordered consumer, envelope and schema validation
├── projection.go                   # new — latest State per Entity, observed_at ordering, reachability
├── plan.go                         # new — role inference, kind compatibility, ValidateExposure
├── accessory.go                    # new — shared build, IID table, reachability-aware reads
├── accessory_lightbulb.go          # new
├── accessory_switch.go             # new — switch and outlet
├── accessory_lock.go               # new
├── accessory_cover.go              # new — window covering
├── accessory_thermostat.go         # new
├── accessory_air_purifier.go       # new
├── accessory_sensor.go             # new
├── convert.go                      # new — mCel/Celsius, speed and air-quality bands
├── command_runner.go               # new — batches, 25 ms window, bounded background Commands, revert
├── hap_server.go                   # new — brutella/hap lifecycle and reload
├── health.go                       # new — serialized SetHealth
├── functions.go                    # new — catalog and handlers
├── function_schemas.go             # new — input and output schemas
├── runtime.go                      # new — startup order, resync loop, rebuild debounce
└── *_test.go                       # new — per-file behavior tests
configs/homekit.example.yaml        # new
docs/adr/0028-allow-a-stateful-homekit-bridge-adapter.md  # new
docs/guides/homekit.md              # new — pairing, Docker, network checks, mappings, limits, backup
docs/architecture/architecture.md   # modify — stateful exception and HomeKit boundary entry
docs/reference/adapter-functions.md # modify — HomeKit Functions
docs/README.md                      # modify — guide index
README.md                           # modify — Adapter table
mise.toml                           # modify — ko-build list
.ko.yaml                            # modify — build entry
.github/workflows/release.yml       # modify — ko build list
go.mod, go.sum                      # modify — github.com/brutella/hap v0.0.35
```

`GLOSSARY.md` does not change. The existing Adapter definition, "a process translating between one configured external system and Hearth's wire protocol", covers a bridge whose external system is HomeKit.

## Deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D1 | ADR 0028 and architecture entry record the stateful exception, the Core-client role and the trust boundary | S | `docs/adr/0028-…`, `docs/architecture/architecture.md` | - | A16 |
| D2 | Binary, CLI, config validation and build wiring; process connects, claims and releases a Session | M | `cmd/hearth-adapter-homekit/`, `internal/app/homekit/config.go`, `run.go`, `configs/homekit.example.yaml`, `mise.toml`, `.ko.yaml`, `release.yml`, `go.mod` | - | A1 |
| D3 | Core client and Fact source with projection ordering and resync | M | `core_client.go`, `facts.go`, `projection.go` | D2 | A2, A3 |
| D4 | Store with atomic writes, AID and slot allocation, setup identity and URI | M | `store.go`, `setup_code.go`, `model.go` | D2 | A4, A5 |
| D5 | Planner: inference, compatibility and validation | M | `plan.go` | D3, D4 | A6 |
| D6 | Lightbulb, switch, outlet, lock, and window covering accessories, with the Command runner | L | `accessory.go`, `accessory_lightbulb.go`, `accessory_switch.go`, `accessory_lock.go`, `accessory_cover.go`, `command_runner.go`, `convert.go` | D5 | A7, A8, A9 |
| D7 | Thermostat, air purifier and sensor accessories | L | `accessory_thermostat.go`, `accessory_air_purifier.go`, `accessory_sensor.go`, `convert.go` | D6 | A10, A11 |
| D8 | HAP server lifecycle, reloads, reachability-aware reads, health and startup order | M | `hap_server.go`, `health.go`, `runtime.go`, `internal/app/homekit/run.go` | D6 | A12, A13 |
| D9 | Five Functions with schemas and reason codes | M | `functions.go`, `function_schemas.go` | D5, D8 | A14, A15 |
| D10 | Guide, Function reference, README and docs index | M | `docs/guides/homekit.md`, `docs/reference/adapter-functions.md`, `README.md`, `docs/README.md` | D9 | A16 |
| D11 | Real-device acceptance on the household iPhone and home hub, with evidence attached to the PR | M | none in the tree | D9 | A17 |
| D12 | Copy `brutella/hap` into `internal/third_party/hap` and fix defects, only if D6 to D11 find a library defect | M to L | `internal/third_party/hap/` | finding | A17 |

## Acceptance criteria

- [x] **A1:** With a valid config, the process claims the Session, appears in Adapter health, and on SIGTERM releases the Session and exits 0. An invalid field fails at startup with the field named.
- [x] **A2:** The Core client pages more than 200 Entities, requests `units=canonical`, and preserves HTTP statuses 504, 503, 409 and 502. It preserves Core's `entity_disabled` code and `command_id` on a structured 409 response and does not infer these fields from human-readable errors.
- [x] **A3:** A Fact with an earlier `observed_at` than the stored State is dropped. A later Fact updates the characteristic and notifies subscribed controllers. A consumer interruption triggers a full resync.
- [x] **A4:** `bridge.json` survives a crash between the temporary write and the rename with the previous content intact. AIDs never repeat across expose, unexpose and expose of other Devices. Re-exposing a Device restores its AID and slots.
- [x] **A5:** Generated setup codes never match the HAP-disallowed list. The `X-HM://` URI decodes back to the same code, category and setup ID.
- [x] **A6:** Inference assigns all roles for a simulated light, lock, cover, thermostat and temperature/humidity sensor. It reports `ambiguous` for a Levoit-shaped purifier and `class_required` for every binarysensor.
- [x] **A7:** A lightbulb batch of `On=true`, `Brightness=40` and `Hue=200` sends `power`, then `brightness`, then one `colorhs` set with the stored saturation. A batch with `On=false` sends only `power set false`.
- [x] **A8:** A write to an unreachable characteristic returns `-70402` without a Command. A Command that ends in 504 restores the characteristic to the latest State, and a later read returns that State.
- [x] **A9:** Lock and window covering mappings produce the specified HomeKit values, including `jammed`, `unknown`, `locking` and moving covers. No GarageDoorOpener service is published.
- [x] **A10:** Thermostat conversions round-trip 21.5 °C as 21500 mCel. Auto writes select `heat_cool` when supported. The valid target modes are limited to support.
- [x] **A11:** Purifier speed maps Levoit choices `1` to `4` to 25, 50, 75 and 100 and back. PM2.5 and filter bands match the tables. A `contact` assignment maps true to not detected, and the same assignment with `inverted: true` maps true to detected.
- [x] **A12:** Exposing a Device restarts the HAP server once for writes within 2 seconds, and the accessory database version increases.
- [x] **A13:** Health becomes `healthy` only after the HAP server, inventory and Fact consumer are up, and reports each unhealthy reason under its condition.
- [x] **A14:** Each Function validates input against its schema, returns the documented outputs and reason codes, and appears in Hearth's Function discovery and invocation history over HTTP and MCP.
- [x] **A15:** `pairing_info` returns `setup_code` and `setup_uri` only while unpaired. Neither credential appears in process logs. `reset_pairing` without the exact `confirm` constant fails schema validation.
- [x] **A16:** ADR 0028, the architecture entry, the guide and the Function reference describe the implemented behavior.
- [ ] **A17:** On the household iPhone and home hub: pairing, control and live update for a light, relay, lock, cover, thermostat, Levoit purifier and sensors; "No Response" when an Entity becomes unavailable; persistence across an adapter restart and a reload without losing rooms; remote control through the hub over cellular data; a second household member in the shared Home receiving updates; and stable notifications over at least 4 hours.

## Validation plan

| Acceptance | Check | Expected result | How to run | Prerequisites |
| --- | --- | --- | --- | --- |
| A1 | CLI and config tests; e2e start and stop against hearthd | Validation messages; Session claimed and released | `GO_PACKAGES='./cmd/hearth-adapter-homekit ./internal/app/homekit' mise run --skip-deps test`; e2e with `-- -tags=integration,e2e` | NATS from `mise run simulator-start` for e2e |
| A2 | Core client against a real hearthd with more than 200 simulated Entities | All pages read; error mapping per status | Integration test in `internal/app/homekit` with `-- -tags=integration` | Simulator stack |
| A3 | Projection unit tests; Fact source integration against real JetStream | Earlier Fact dropped; resync after consumer deletion | `GO_PACKAGES='./internal/adapters/homekit' mise run --skip-deps test`, plus `-- -tags=integration` | Simulator stack for integration |
| A4, A5 | Store and setup-code unit tests; subprocess killed at the pre-rename boundary | Previous content intact; no AID reuse; URI round-trip | `GO_PACKAGES='./internal/adapters/homekit' mise run --skip-deps test -- -tags=integration` | None |
| A6 | Table tests with Entity fixtures shaped like the simulator and Levoit Devices | Expected Proposal per Device | Same as above | None |
| A7 to A11 | Accessory and runner tests through the built `accessory.A` values and a fake `coreClient` | Specified Commands, order and characteristic values | Same as above | None |
| A12, A13 | Coordinator test with a real HAP server; startup and running fault tests through production Core | One published generation serves two exposure changes; Core records health and recovery | `GO_PACKAGES='./internal/adapters/homekit ./internal/app/hearthd' mise run --skip-deps test -- -tags=integration,e2e` | Loopback network and isolated test brokers |
| A14, A15 | Production Core HTTP and MCP discovery, admission, invocation, and history | Documented outputs and reason codes; successful calls retained for all five Functions | `GO_PACKAGES='./internal/adapters/homekit ./internal/app/hearthd' mise run --skip-deps test -- -tags=integration,e2e` | Loopback network and isolated test broker |
| A16 | Docs review against the implemented behavior | No contradiction | Manual review in the PR | None |
| A17 | Manual checklist on real hardware; screenshots and notes attached to the PR, not committed | Every listed behavior observed | Run the adapter with host networking; pair from the Home app; follow `docs/guides/homekit.md` | Household iPhone, second household member, Apple home hub, trusted LAN |

Final validation runs `mise run validate` with `GO_PACKAGES` unset.

CI exercises the HAP lifecycle and managed characteristic boundary. An independent software controller also exercised encrypted pairing, writes, events, and reset against the local simulator and the host-network Docker image. The software checks do not replace Apple Home and home-hub acceptance in A17.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| `brutella/hap` defects in pairing management or bridged notifications | High | Pairing failures or stale tiles | A17 covers shared homes and 4-hour notifications; D12 copies and fixes the library |
| mDNS fails across Docker, VLANs or IGMP snooping | Medium | Bridge not discoverable, or "No Response" from the hub | Host networking only; the guide documents separate mDNS and TCP checks |
| Identifier drift loses rooms and scenes | Low | Household reconfigures Home | Persisted AIDs and slots, never reused; A4 and A17 restart checks; back up `data_dir` |
| Losing `data_dir` | Low | Re-pair and reconfigure Home | Backup guidance; ADR records the exception |
| Remote unlock through the Agent or the hub | Medium | Physical security exposure | Locks appear only through explicit `expose_device`; ADR records the decision; A17 checks iPhone lock behavior |
| Prompt write success hides Command failure | Medium | Tile briefly shows a state the device never reached | Revert from latest State on failure; warning log with `command_id` |
| Levoit optimistic Observations and stale polls flip the purifier tile | High for Levoit | Confusing tile state | Documented as a Levoit limitation; no bridge-side holdoff |
| Facts lost during a consumer gap | Low | Stale tiles until resync | Re-create the consumer and run a full resync; `resync_interval` bounds staleness |
| Reload blips during exposure changes | High | "No Response" for a few seconds | Debounce; changes are infrequent and requested by people |
| iOS refuses `ColorTemperature` props outside its expected range | Medium | Color temperature control missing | Clamp props to the support range and verify in A17; record the observed range in the guide |

## Trade-offs made

| Chose | Over | Because |
| --- | --- | --- |
| Adapter plus Core client | A pure client or a Core module | Functions give the Agent control with retained evidence, health appears in Hearth, and credentials stay outside Core |
| A stateful data directory | Stateless operation | HomeKit requires stable pairing keys and identifiers |
| Exposure only through Functions | A YAML allowlist as well | One owner and one way to change it |
| Explicit roles for ambiguous Entities | Name matching | Names are not identity, and a wrong guess would control the wrong Entity |
| Prompt write success with revert | Waiting for the Command outcome | Lock and cover deadlines exceed what HomeKit will wait |
| Restarting the HAP server on changes | Live accessory changes | The library has no live add or remove API |
| An ephemeral Fact consumer plus resync | A named durable consumer | The bridge needs current State, not history; resync covers gaps without broker state |
| A flat package | Subpackages per concern | No dependency boundary justifies a split (`CODING_STANDARDS.md`, "Justify package splits with dependency boundaries") |

## Open questions

- [ ] Does iOS accept `ColorTemperature` props outside 140 to 500 mireds, and does it apply them? → Owner: implementer, resolved in A17.
- [ ] Does `brutella/hap` expose paired-controller state for `pairing_info`, or must the code read the store? → Owner: implementer during D8.
- [ ] Should `TemperatureDisplayUnits` follow the household unit system instead of configuration once a Core read exists for it? → Owner: Michael, after v1.
- [ ] Are the PM2.5 bands and the 10% filter threshold right for this household? → Owner: Michael, adjustable after A17.

## Success metrics

- The household controls every exposed Device from the Home app and Siri, locally and through the home hub, for two weeks without re-pairing.
- Home app tiles reflect Hearth State changes made outside HomeKit within 2 seconds on the LAN.
- The Agent adds and removes accessories through Functions without manual file edits.

## External references

- `brutella/hap` v0.0.35: <https://github.com/brutella/hap>, <https://pkg.go.dev/github.com/brutella/hap@v0.0.35>
- Unmerged pairing-list fix: <https://github.com/brutella/hap/pull/67>; bridged state issue: <https://github.com/brutella/hap/issues/58>
- Home Assistant HomeKit Bridge AID storage: <https://github.com/home-assistant/core/blob/dev/homeassistant/components/homekit/aidmanager.py>
- Home Assistant light write coalescing: <https://github.com/home-assistant/core/blob/dev/homeassistant/components/homekit/type_lights.py>
- Home Assistant thermostat mapping: <https://github.com/home-assistant/core/blob/dev/homeassistant/components/homekit/type_thermostats.py>
- HAP-python setup URI encoding and unavailable reads: <https://github.com/ikalchev/HAP-python/blob/dev/pyhap/accessory.py>
- HAP-NodeJS 149-accessory bridge limit: <https://github.com/homebridge/HAP-NodeJS/blob/master/src/lib/Accessory.ts>
- Home Assistant networking and Docker guidance: <https://www.home-assistant.io/integrations/homekit/#docker-network-isolation>
- Apple home hub requirements: <https://support.apple.com/en-us/102557>
