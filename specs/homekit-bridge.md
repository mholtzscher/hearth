# HomeKit bridge

Status: Implemented and validated locally; published delivery checks are tracked
on PR #180. The operator approved implementation explicitly. The disposable
experiment is evidence, not reusable production code. Overall effort: XL, with
medium confidence; protocol integration and Apple validation are the largest
uncertainties.

The operator subsequently relaxed the two vendor-patch requirements: use
unmodified HAP-NodeJS, allow omitted optional write-response values, and accept
upstream pairing-write failure behavior. Core authority and non-replay semantics
are unchanged. Implementation review found identifier-cache allocation and bridge-name
boundary defects; both were corrected with regression checks. See the
[production acceptance record](../docs/homekit-acceptance.md) for actual results
and limits. This document is not a release, merge, or all-checks-green claim.

## Implementation evidence

The unpatched revision passes 101 package checks and 20 real-process integration
checks. The results below describe the original patched build; the original
iPhone run is historical evidence, not acceptance of changed dependency behavior.

- D1–D5 are implemented under `bridges/homekit`, with 106 package checks and 23
  real-process integration checks passing. They include encrypted wire values,
  unsafe-baseline negative controls, distinct/overlapping Commands, actual
  pairing persistence failures, identity corruption, readiness events, listener
  loss, admission draining, and bounded dependency-cleanup escalation.
- D6 includes local CLI/container packaging, repository tasks, CI/release wiring,
  configuration, operator guidance, and completed operator-driven Apple Home
  checks. The nonroot host-network image smoke passed. Release publication and
  arm64 runtime verification have not been performed locally.
- Initial `mise run validate` runs passed HomeKit, web, lint, vet, generation,
  and fixture compilation, but existing MQTT tests failed on Docker's
  journald log retrieval hiding Ryuk readiness. This reproduces on unchanged Go
  baseline `a6335a3`. An existing Core readiness-log timeout also reproduces on
  that baseline; a separate current-tree package run passed. After the operator
  repaired Docker/Ryuk log retrieval, full local validation passed without
  disabling any checks. The existing Core test now exposes early startup errors
  and joins its fixture without changing its deadlines or assertions.
- The temporary production bridge and managed simulator stack are stopped;
  their private pairing/evidence remain outside Git. No firewall changes were
  made for production acceptance. Prior experiment-rule removal is unconfirmed.

## Problem and recommendation

Expose selected Hearth lights and plugs to Apple Home and Siri without retaining
Home Assistant. A technical household operator chooses the exports explicitly and
pairs one bridge. Hearth remains the authority for Device identity, Entity support,
State, availability, enablement, and Command outcomes.

Implement `hearth-bridge-homekit` as a standalone TypeScript/Node.js companion using
`@homebridge/hap-nodejs`. It consumes the existing HTTP API and NATS Device Facts.
It is not a Homebridge plugin, a Device-owning Adapter, or a new Core product
module. It claims no SDK Session, registers no Devices, publishes no Observations,
and does not appear in Core's Adapter inventory.

Approval establishes a narrow exception to the Go implementation stack in
[architecture](../docs/architecture.md): this outgoing bridge uses TypeScript,
while Core, the Go Adapter SDK, and existing Adapters remain unchanged. No new
general-purpose TypeScript SDK or language migration is part of this work.

## Evidence and its limits

The experiment in this conversation used HAP-NodeJS 2.2.3, an independent encrypted
HAP controller, real local Core/NATS, and simulated Devices. Thirteen automated
checks covered pairing, reads, distinct same-value Commands, notifications,
restart identity, rejected Commands, transport timeout, and dependency recovery.

The operator subsequently paired an iPhone running Apple Home, controlled a
simulated light on and off, and confirmed that a simulated physical change updated
the tile. After restarting the bridge with preserved storage, authenticated
control and return-path updates worked without re-pairing. Room assignment was
not explicitly confirmed. Apple-specific outage recovery, dimming, plug service
behavior, and the patched dependency have not been validated.

Each Apple Home toggle produced two same-value handler invocations and two
satisfied Core Commands. Their cause is unknown. This does not establish a
retry policy or exactly-once user intent. An ordinary async setter also reproduced
a stale requested-value notification after a newer Observation. A write-response
variant avoided the exercised schedule but did not prove general concurrency
correctness.

Temporary evidence is retained at
`/tmp/opencode/hearth-homekit-experiment-gGrAp3/`, including `README.md`, `MANUAL.md`,
`run-mTHskb/results.json`, and `manual/restart-*.json`. These paths are local
provenance, not runtime dependencies or durable CI fixtures. Never copy pairing
keys, setup codes, local addresses, or raw State traces into the repository.

Grounded contracts:

- [Entity/API shapes](../internal/modules/devices/api/types.go) and
  [Command outcomes](../internal/modules/devices/api/command.go).
- [Power](../entitytypes/powerv1/entitytype.json),
  [brightness](../entitytypes/brightnessv1/entitytype.json), and their checked-in
  schemas. Both `set` operations have ten-second Core deadlines.
- [Device Facts](device-facts.md) and
  [Observation Fact schema](../contracts/v1/observation-fact.schema.json).
- HAP-NodeJS 2.2.3 [Characteristic](https://github.com/homebridge/HAP-NodeJS/blob/v2.2.3/src/lib/Characteristic.ts),
  [Accessory](https://github.com/homebridge/HAP-NodeJS/blob/v2.2.3/src/lib/Accessory.ts),
  and [identifier cache](https://github.com/homebridge/HAP-NodeJS/blob/v2.2.3/src/lib/model/IdentifierCache.ts).

## Scope and exclusions

The first release supports one bridge per process and 1–149 explicitly configured
accessories. A light exports power and optionally brightness. A plug exports
power as a HomeKit Outlet. The same process supports both profiles.

Supported deployments are a local Linux Node.js CLI and a Linux host-network
container, initially linux/amd64 and linux/arm64. Use the repository's Node 26 and
pnpm 11 toolchain. Dependencies are independently locked in `bridges/homekit`;
the dashboard remains a separate package.

Excluded: color, adaptive lighting, sensors, buttons, cameras, thermostats, locks,
Matter, Thread, HomeKit-controller functionality, automatic export discovery,
hot configuration reload, cloud relay, automatic firewall changes, Core API or
database changes, Core Bridge health resources, a management UI, command retries,
time-window deduplication, durable command queues, and historical fact replay.
Mac/Windows and Docker Desktop networking are not supported first-release targets.
This is not an Apple-certified implementation or a commercial-certification claim.

## Operator contract

Illustrative production configuration:

```yaml
bridge_id: homekit-main
name: Hearth
data_dir: .data/homekit-main
pairing_code_file: /run/secrets/hearth-homekit-code
hap_port: 51826
advertise_interfaces: [enp7s0]
core_url: http://127.0.0.1:8080
nats_url: nats://127.0.0.1:4222
reconcile_interval: 5s

accessories:
  - key: living-room-lamp
    name: Living room lamp
    kind: light
    power_entity: ent_<canonical UUIDv7>
    brightness_entity: ent_<canonical UUIDv7>
  - key: desk-plug
    name: Desk plug
    kind: outlet
    power_entity: ent_<canonical UUIDv7>
```

The placeholder Entity IDs above are explanatory, not valid configuration.
Discover real IDs with the existing Entity/Device HTTP APIs. Require power and
brightness for one light to belong to the same canonical Device. Do not require
that Device's `kind` be `light`: the selected HomeKit profile is an operator
decision, while compatible Entity types/support determine eligibility.

Every key is a unique lower-case subject-safe slug, 1–64 ASCII characters, using
letters, digits, and single internal hyphens. Entity IDs must be canonical `ent_`
UUIDv7 values and may occur only once across all configured characteristic slots.
Require `hearth.power/v1` with `set` support for power and
`hearth.brightness/v1` with `set` support for brightness. Outlet mappings reject
brightness. Validate descriptors against their authoritative checked-in schemas,
not a handwritten permissive substitute.

Names are explicit bridge-local display names, 1–64 UTF-16 code units, nonblank,
and free of control characters. The root bridge name additionally has a maximum
of 58 UTF-8 bytes, reserving five bytes for HAP's identifying suffix under its
63-byte mDNS limit. Reject longer root names during configuration validation,
before creating identity. The operator approved this constraint during
implementation after independent review reproduced the upstream limit.
These names do not override or track Core names. Names
and list order never determine HomeKit identity. Changes require restart.
Changing an existing key's `kind` is rejected; use a new key for a different
HomeKit service profile. Repointing a key to another compatible Entity is an
explicit replacement and retains that key's HomeKit identity.

Scalar settings follow CLI flag > `HEARTH_BRIDGE_HOMEKIT_` environment > optional
default `configs/homekit.yaml` > built-in defaults. An explicitly selected config
file must exist. Structured `accessories` and `advertise_interfaces` are YAML-only.
Reject duplicate YAML keys, multiple documents, malformed values, and unknown
keys. Strict unknown-key rejection is deliberate for this new package to avoid
silently omitting a misspelled export. It does not change other processes.
Relative data/secret paths resolve from the process working directory.

Defaults: name `Hearth`, HAP port 51826, reconciliation 5 seconds. Require explicit
`bridge_id`, `data_dir`, `pairing_code_file`, `core_url`, `nats_url`, and at least
one LAN advertisement interface. Ports are 1024–65535; reconciliation is 1–60
seconds, using the existing duration syntax restricted to a whole number of
milliseconds in this range. Resolve interface names on startup and reject
missing, down, loopback, or addressless configured interfaces.

Core URLs are explicit HTTP host-and-port origins with no credentials, path,
query, or fragment. NATS URLs use explicit plain `nats://host:port` without
credentials. Both are trusted-network endpoints. HTTPS, NATS credentials/TLS,
and remote authentication are excluded from this slice rather than silently
ignored. Secrets contain only the HomeKit code, not application configuration.

The secret file contains one permitted eight-digit HomeKit code in `XXX-XX-XXX`
form, optionally followed by one newline. The companion rejects codes with eight
identical digits and the sequences `12345678` and `87654321`; do not assume the
library validates this policy. No shared/default code and no secret in flags,
environment, YAML, logs, or ordinary startup output.

CLI behavior:

```sh
# Proposed commands, implemented by this feature.
hearth-bridge-homekit --config configs/homekit.yaml --initialize
hearth-bridge-homekit --config configs/homekit.yaml
hearth-bridge-homekit pairing-info --config configs/homekit.yaml
```

`--initialize` is an explicit CLI-only first-start authorization. It creates a
new identity only in a new/empty data directory, excluding its own lock file,
and continues serving. Ordinary
startup never initializes missing identity data. Reusing `--initialize` on an
existing identity fails without modifying it. `pairing-info` is an explicit
operator action that prints the code and setup URI to stdout, not logs. It
requires an initialized store, creates no listener, and never resets pairing.
QR rendering is optional documentation/tooling, not a production dependency.

The proposed glossary entry is:

> **HomeKit bridge**: The household's Apple Home entry point for selected Hearth
> Entities. It presents those controls to Apple Home without changing which
> Adapter owns their Devices or Entities.

## Identity and storage

`identity.ts` owns a versioned manifest, under `data_dir/bridge.json`:

```ts
type BridgeManifest = {
  version: 1;
  stage: "initializing" | "initialized";
  bridgeId: string;
  username: string; // generated persistent HomeKit MAC-format identity
  setupId: string; // generated persistent four-character HomeKit setup identity
  pairingCodeDigest: string; // SHA-256 change detector, not secret protection
  accessoryKinds: Record<string, "light" | "outlet">; // includes retired keys
};
```

Generate identity with cryptographic randomness. Accessory UUIDs derive from
`hearth/homekit/<bridge_id>/<accessory_key>`; the bridge UUID uses a distinct
bridge-only namespace. Use a fixed service subtype `primary` and characteristic
UUIDs from HAP-NodeJS. Never derive identity from names, list order, adapter IDs,
IP addresses, or current Entity values. Pass the saved username and setup ID to
HAP-NodeJS and disable unused-ID purging. Adding brightness preserves existing
On/IIDs; removing and restoring an optional characteristic reuses its identity.
Retain retired identifiers and kinds rather than compacting them automatically.
An explicit accessory removal can affect Apple scenes; numeric identity reuse
does not guarantee Apple restores deleted room or automation assignments.

`data_dir/hap/` holds the library's pairing keys and AID/IID cache. It contains no
Hearth State cache, Command journal, or replayable work. Require a private writable
local filesystem directory; create new directories with mode 0700 and owned files
with mode 0600. Disallow shared network filesystems for the first release.

Before any HAP store access, the CLI must hold a nonblocking exclusive OS `flock`
on `data_dir/bridge.lock` for the entire serving lifetime. Use util-linux `flock`
with argument arrays and a private runtime entry point, not a shell-evaluated
configuration string. The public CLI supervises the locked runtime and forwards
termination signals. Competing startup fails before reading/mutating HAP identity.
The kernel releases the lock on process exit; do not implement stale-PID eviction.

Initialization validates configuration, secrets, and Core descriptors before
creating identity. Persist an `initializing` manifest before permitting the
library to generate its files. After HAP initialization has established the
required AccessoryInfo and IdentifierCache, validate them and atomically commit
the `initialized` manifest. An interrupted initialization fails closed on later
startup and requires operator inspection/restoration or explicitly abandoning
the new data directory; it never silently replaces keys.

For an initialized store, missing/malformed manifest, AccessoryInfo, or
IdentifierCache, bridge-ID mismatch, unwritable storage, or a changed configured
pairing code is fatal before advertising. Retain a SHA-256 digest of the
initial code in the private manifest solely to detect changes without logging
the code; its low-entropy input means the digest is not protection against
disclosure. Validate files before handing them to
the library, which otherwise treats some missing/corrupt data as a new store.
Watch the parent directory for removal/replacement of the HAP directory and
recheck required files at reconciliation. Detected unexpected loss is fatal
rather than permission to regenerate. This is not instantaneous detection of
every transient external filesystem change.

Manifest writes use temporary-file write, fsync, rename, and parent-directory
fsync. Library file writes remain the library's atomic-rename behavior, not a
cross-file transaction or guaranteed power-loss durability. Back up the complete
stopped bridge data directory. Restore it as a unit. A missing mount must fail
ordinary startup rather than produce a new accessory. No reset/delete command is
included; operator documentation must explain removal from Apple Home before
intentionally abandoning an identity. Prototype pairing data is not migrated.

## HomeKit mapping and dimming

| Profile | HomeKit service | Characteristics |
|---|---|---|
| `light` | Lightbulb | On; optional Brightness |
| `outlet` | Outlet | On |

Do not fabricate OutletInUse, electrical usage, StatusFault, battery, or Identify
effects. An Identify request acknowledges identification but performs no physical
Command. Pairing/category information describes the root as a Bridge.

On maps directly to a power boolean. Brightness reads preserve the integer
percentage accepted by Core, including non-step-aligned States. Advertise
brightness minimum 0, maximum from support, and HomeKit minimum step 1. Do not
advertise Core's command step as HomeKit's State precision, rescale percentages,
or clip a valid current value to command granularity.

For an integer requested brightness `b` within 0..support.maximum and a Core
command step `s`, translate to the nearest valid step, rounding ties upward:

```ts
const largest = Math.floor(maximum / step) * step;
const commandValue = Math.min(largest, Math.floor(b / step + 0.5) * step);
```

Zero is valid. A maximum below one full command step leaves zero as the only
commandable value; do not invent a positive value. Reject nonintegers or values
outside advertised bounds. The library may normalize client input before the
handler; validate the value delivered to the companion and test raw-wire behavior
separately. Readback and write-response values come from actual State, not the
quantized request. For maximum 100 and step 5, a request for 83 becomes 85 while
an observed 83 stays 83. For maximum 83 and step 5, a request for 83 becomes 80.

Power and brightness are independent. A brightness write submits only a
brightness Command, including at zero. On submits only power. The bridge never
adds a power Command, restores a remembered brightness, or promises an atomic
power/brightness transaction. Vendor effects still arrive as ordinary State.

Topology and descriptor-derived bounds freeze for a process lifetime. During
reconciliation, missing Entities, changed types/device grouping, or changed
normalized support make the affected accessory incompatible until restart with
valid mappings. Keep its HomeKit identity and topology; do not remove/recreate it
or silently drop Brightness. Entity enablement and availability remain dynamic,
not descriptor changes. Names remain the configured bridge-local names.

## Types and ownership

These are proposed internal package types, not new Core wire contracts. JSON is
`unknown` until validated at `config.ts`, `hearth.ts`, or `facts.ts` boundaries.

```ts
type AccessoryConfig =
  | { key: string; name: string; kind: "light"; powerEntity: string;
      brightnessEntity?: string }
  | { key: string; name: string; kind: "outlet"; powerEntity: string };

type Config = {
  bridgeId: string; name: string; dataDir: string; pairingCodeFile: string;
  hapPort: number; advertiseInterfaces: readonly string[];
  coreURL: string; natsURL: string; reconcileIntervalMs: number;
  accessories: readonly AccessoryConfig[];
};

type EntityDescriptor =
  | { id: string; deviceId: string; kind: "power" }
  | { id: string; deviceId: string; kind: "brightness";
      maximum: number; commandStep: number };

type EntityRead = {
  descriptor: EntityDescriptor;
  enabled: boolean;
  availability: "unknown" | "available" | "unavailable";
  state: null | { observationId: string; value: boolean | number };
};

type Projection =
  | { kind: "usable"; revision: number; refresh: number;
      descriptor: EntityDescriptor; observationId: string; value: boolean | number }
  | { kind: "unusable"; revision: number; refresh: number;
      reason: "unknown_state" | "disabled" | "unavailable" | "read_failed"
        | "entity_missing" | "descriptor_changed" };

type SatisfiedCommand = {
  commandId: string; observationId: string; value: boolean | number;
};
type Operation = {
  deadline: number; // monotonic process time, never a wire timestamp
  signal: AbortSignal;
  state: "pending" | "completed";
};
```

`hearth.ts` validates the type/value correlation and brightness bounds before
constructing EntityRead. `projection.ts` owns local revisions, refresh sequencing,
and projections. `hap.ts` owns operation completion and HAP object access.
No persisted SQL models, raw HAP request objects, or NATS messages cross these
internal boundaries. Do not export a public SDK merely to hide these concrete
dependencies behind pass-through layers.

## Interfaces and data flow

```ts
// config.ts: parse precedence and validate all static configuration.
function loadConfig(argv: readonly string[], env: NodeJS.ProcessEnv): Config;

// hearth.ts: one bounded request per invocation, never an automatic POST retry.
class HearthClient {
  readEntity(entityId: string, signal: AbortSignal): Promise<EntityRead>;
  setEntity(entityId: string, value: boolean | number,
    signal: AbortSignal): Promise<SatisfiedCommand>;
}

// projection.ts: per-Entity state; callers do not coordinate HTTP completion order.
class EntityProjection {
  current(): Projection;
  hint(): void;
  refreshAfterNow(signal: AbortSignal): Promise<void>;
  onChange(listener: (projection: Projection) => void): () => void;
  close(): Promise<void>;
}

// facts.ts: subscription lifecycle and coalesced invalidation only.
function startFactHints(config: Config, hint: (entityId: string) => void,
  signal: AbortSignal): Promise<{ close(): Promise<void> }>;

// hap.ts: installs public callback handlers and owns one HAP Bridge.
function serveBridge(config: Config, manifest: BridgeManifest,
  client: HearthClient, signal: AbortSignal): Promise<void>;

// hap.ts: bounded external observation, not access to a private HAP listener.
function probeHAPListener(port: number, signal: AbortSignal): Promise<void>;
```

HTTP reads use `GET /v1/entities/{id}`. Writes use
`POST /v1/entities/{id}/commands` with
`{"operation":"set","parameters":{"value":...}}`. Only a valid HTTP success
with `status:"satisfied"`, canonical Command/Observation IDs, and correctly typed
outcome value counts as successful evidence. A `dispatched` result is incompatible
with these observed operations. Schema metadata such as `$schema` and unrelated
additive API fields may be ignored; validate every field the bridge consumes.
Limit each JSON response body to 1 MiB. Reject oversized/malformed responses with
a safe error; never follow redirects or include response bodies in diagnostics.

Core remains authoritative for command admission. Valid supported writes submit
once even when the last projection says unavailable, disabled, or has no State;
Core decides whether to attempt/reject them. This preserves recovery attempts
through an unavailable Entity and does not treat stale local availability as a
new ownership rule. A known incompatible descriptor rejects locally. Invalid
HomeKit values never create a Core Command.

### Projections and reconciliation

Use one active HTTP refresh per Entity and a coalesced dirty flag, not a queued
task per fact. Hints arriving during a refresh request one follow-up refresh.
Apply results in request-generation order; abandoned/timed-out requests and late
failures cannot mutate a newer projection. A successful result does not compare
Observation UUIDs or timestamps to invent a Core revision.

`refreshAfterNow` resolves only after a refresh started after its invocation
commits. If a refresh is already active, wait for the coalesced follow-up. Shared
work has its own two-second request timeout; a canceled waiter must not cancel
other waiters or extend its own operation deadline. A timed-out refresh is
invalidated before another starts, so a late body parse cannot replace the newer
value. On completion, callers sample `current()` rather than use a captured HTTP
result that might already have been superseded.

`onChange` synchronously notifies its listeners after committing a changed
projection and returns an unsubscribe function. `hap.ts` subscribes before HAP
publication, emits explicit value updates for usable projections, and marks
unusable characteristics with communication errors without inventing values.
It removes listeners after admitted handlers finish and before closing the
projections. Closing a projection joins its own refresh and prevents further
change callbacks. HAP's final synchronous callback boundary still rechecks
`current()`; an earlier change callback is not a completion capability.

A read failure commits `read_failed`; null State, disablement, and non-available
availability produce their distinct unusable projections. Runtime descriptor
incompatibility remains latched for that accessory until restart. Keep last-known
values internally if useful for HAP protocol representation, but never return
them as successful characteristic reads or emit default false/zero notifications
when the projection is unusable. HAP's accessory-database serialization may
require protocol defaults; those are not evidence of a usable Hearth State.

Subscribe using the current official Node NATS client packages, not deprecated
`nats@2.x`. Use ordinary Core NATS subscriptions narrowed to configured Entity
Observation subjects. Facts are non-authoritative hints: validate subject shape,
canonical Entity membership, and `applied|unchanged` variant; do not use their
payload values or allocate per-fact durable work. No JetStream consumer, fact
acknowledgement floor, history replay, or stream provisioning is required.

Subscribe/flush before the initial projection refresh. On reconnect, renew
subscription readiness and invalidate all configured projections. Periodically
reconcile all configured Entities in batches with at most eight background HTTP
reads active; passes do not overlap. Foreground HAP reads/sets share the per-Entity
refresh mechanism rather than wait behind the whole background inventory sweep.

NATS disconnect degrades promptness, not command safety: HTTP reads/Commands and
periodic reconciliation remain usable. Facts do not announce availability,
enablement, or metadata changes, so reconciliation is mandatory even when NATS
is healthy. Intermediate physical transitions can be missed; promise eventual
current-State convergence, not a lossless occurrence stream or linearizable
cross-Entity snapshot. No State-age expiration is invented by the bridge.

### HAP completion, deadlines, and concurrency

Register public callback-based `on("get", ...)` and `on("set", ...)` handlers.
Do not also register `onGet`/`onSet`: the latter take precedence and introduce an
await between the user's returned value and library cache assignment. Add
WRITE_RESPONSE permission to On and Brightness.

One monotonic eight-second total budget starts at each handler entry and includes
all waits, POST, body parsing, and post-command refresh. Read refresh requests
retain their shorter two-second bound. Recheck the absolute deadline immediately
before completing the callback; a timer alone is insufficient. Node event-loop
blocking is outside a hard real-time guarantee. Core's ten-second deadlines do
not change.

A setter translates/validates its value, submits once, waits for satisfied Core
evidence, then calls `refreshAfterNow` within the same remaining budget. Command
result values establish that attempt's outcome but never overwrite current State.
At the final synchronous completion boundary, recheck operation liveness,
descriptor compatibility, and the current projection. For a usable projection,
call `updateValue(current.value)` and then `callback(null, current.value)` in the
same turn with no await between selection and callback. Callback getters use the
same current-projection completion rule. Publish only authoritative values.
WRITE_RESPONSE suppresses the library's automatic requested-value WRITE event;
explicit updates supply State notifications.

Each operation completes once. Timeout/shutdown marks it completed and aborts its
owned HTTP wait; late promises cannot call it again, mutate HAP cache, or publish
their captured result. Any later reconciliation is separately gated projection
work. Unavailable completion returns an error even if the Core Command previously
satisfied. This is an interface failure, not a retroactive Core failure.

No command queue, serialization, coalescing, supersession, or deduplication window
is added. Each valid received set is distinct, including repeated values. Commands
may overlap, including for one Entity. On and Brightness in one HomeKit request
are independent, non-atomic attempts; no ordering across their callbacks is
promised. After an ambiguous timeout, a later write is a new request and may
overlap an earlier admitted Command. This explicitly accepts Core's existing
concurrency semantics rather than falsely promising remote cancellation/order.

| Condition | HAP result |
|---|---|
| Invalid handler value, Core HTTP 400 | INVALID_VALUE_IN_REQUEST, `-70410` |
| Handler budget exhausted, Core HTTP 504 | OPERATION_TIMED_OUT, `-70408` |
| Missing/disabled/unavailable/incompatible Entity; Core 404/409/502/503; malformed response or transport failure | SERVICE_COMMUNICATION_FAILURE, `-70402` |
| Unexpected exception | SERVICE_COMMUNICATION_FAILURE, `-70402`, safe diagnostic |

A timeout does not prove non-execution. Disconnecting the HTTP caller does not
cancel an admitted Core Command. Never retry POSTs, reconstruct lost intent,
poll history to execute a fallback, or persist executable work. Subsequent
authoritative reconciliation may show an action after Apple Home saw an error.

Do not promise an immediate unsolicited Apple "No Response" transition:
HAP-NodeJS's cached error status is not a generic error-notification event. Actual
reads/writes must fail while unusable and recover without re-pairing. Normal
notifications are subscription-based and batched; preserve library ordering.
Tests normalize its reversed batches for chronological assertions while retaining
raw messages. Do not mutate event payloads or remove library listeners.

### Unmodified dependency policy

Pin HAP-NodeJS exactly to 2.2.3 with the normal frozen pnpm lockfile. Do not carry
vendor patches or private cache/listener hooks for stronger edge-service guarantees.

Write-response values are optional. The bridge supplies observed State through
the public callback API, but accepts the library omitting false/zero values from
successful responses. Reads and subscribed notifications remain authoritative;
do not echo a requested value to fill an omitted response field.

Pairing persistence follows upstream behavior. A filesystem failure may become
an HTTP error while leaving the process alive with changed in-memory pairing
data. Immediate process termination and rollback of those changes are not
guaranteed. The operator repairs storage and restarts before retrying pairing.
Normal pairing/restart persistence, startup identity validation, single-writer
locking, and detected identity-loss handling remain covered. Companion manifest
write failures have an application-owned boundary and still stop the process.

## Lifecycle and operational behavior

Startup order: validate local config/secrets; acquire the storage lock; validate
existing identity or verify eligibility for explicit initialization; fetch and
validate all configured descriptors; connect NATS and install subscriptions;
establish projections; construct the complete configured topology; initialize
new identity only when explicitly authorized; bind HAP and advertise selected LAN
interfaces. No empty/partial bridge is advertised to hide invalid mappings.
Missing initial State or unavailable Entities do not prevent valid topology from
starting. Disabled Entities with compatible descriptors are also valid startup
mappings. Initial connection/descriptor discovery, HAP binding, and advertisement
have one 30-second total startup budget; transient failure exits nonzero for the
supervisor to retry.
Initialized identity is preserved on every failure.

Install the public `listening` and `advertised` event handlers before calling
`publish()`. Ready means both events arrived and the local listener probe passed
within that startup budget. `publish()` resolving alone is not readiness:
advertisement failures can be caught/logged inside the library. An event deadline
or failed probe unpublishes/joins the attempted bridge and exits nonzero without
deleting initialized identity. `advertised` proves local advertiser completion,
not discovery by an iPhone or multicast delivery through a firewall.

The listener probe performs an unauthenticated loopback HTTP GET to
`/accessories` on the configured HAP port, with a one-second deadline and 4 KiB
response limit. The pinned library must return HTTP 470 and JSON HAP status
`-70401`; validate both. This establishes a recognizable reachable HAP listener,
not authenticated identity or external-LAN reachability. A probe never pairs,
writes a characteristic, or changes household State.

Once serving, Core/NATS outages do not destroy topology or force a new identity.
Core recovery is discovered through reads/reconciliation. An individual bad
Entity cannot crash unrelated accessories. Detected identity corruption and failed
companion manifest persistence stop the process; HAP pairing-write failures follow
the upstream policy above. Monitor listener health
with the same probe every five seconds, with no overlapping probes. Two
consecutive failures are fatal; one failure followed by success clears the
failure count. Stop this monitor on normal shutdown. Do not infer listener health
from nonexistent public close/error events or inspect internal TCP server fields.
This detects persistent local listener failure, not every transient close or a
runtime loss of multicast delivery. Supervisor restart retains pairing data.

SIGTERM/SIGINT close new operation admission, stop scheduling new reconciliation,
and allow admitted callbacks their original remaining budget. Join them before
unpublishing HAP, closing NATS, and releasing the storage lock. Finish shutdown
within a ten-second application deadline; on unexpected dependency failure,
complete remaining callbacks with errors and abort owned waits rather than
invent success. Parent/locked-child supervision must not orphan the runtime.
Container/process-manager termination grace is at least 15 seconds. Killing the
process does not revoke Commands already admitted by Core. Restart never replays.

No new health HTTP server is added. Provide safe structured stderr logs following
[logging conventions](../docs/logging.md), with `app=hearth-bridge-homekit`, pid,
component, fixed event/reason codes, accessory key/Entity ID, and known Command
IDs. Log dependency transitions and admission/timeout failures, not every poll.
No State, Command parameters, names, raw requests, subjects, URLs, unknown error
strings, secret paths, codes, setup URIs, or keys in logs, including dependency
debug logs. Pairing-info output is the explicit exception, never automatic.

Network documentation must distinguish advertisement interfaces from TCP binding:
the pinned library can listen on `0.0.0.0` even with restricted advertisement.
Do not claim interface confinement. Require an operator-managed trusted-LAN
firewall permitting UDP 5353 and the configured TCP port. No internet forwarding,
Core/NATS widening, automatic firewall edits, or multicast reflector setup.
Include a bounded mDNS browse check and a separate TCP reachability check.

The container uses Linux host networking, an unprivileged user, no privileged
mode/extra capabilities, readonly config and secret mounts, and one writable
private data volume. Include util-linux flock. Do not rely on ordinary Docker
port publishing to make multicast work. Existing host Core/NATS loopback binds
can remain unchanged with host networking. Host firewall exceptions remain an
operator action, not an application responsibility.

## Project layout and implementation contract

```text
bridges/                                      new — outgoing companion packages
└── homekit/                                  new — independently locked Node package
    ├── package.json                          new — CLI bin, build/test scripts, exact dependencies
    ├── pnpm-lock.yaml                        new — reproducible unmodified dependency graph
    ├── pnpm-workspace.yaml                   new — package-local native build policy
    ├── tsconfig.json                         new — strict TypeScript build
    ├── Dockerfile                            new — Node runtime, flock, nonroot image
    ├── Dockerfile.dockerignore               new — allow only source/build inputs in root context
    ├── src/
    │   ├── cli.ts                            new — public parsing, lock supervision, pairing-info
    │   ├── locked.ts                         new — locked entry point and runtime assembly contract
    │   ├── runtime.ts                        new — locked process assembly and lifecycle
    │   ├── config.ts                         new — config parsing/validation
    │   ├── identity.ts                       new — BridgeManifest and fail-closed persistence
    │   ├── logging.ts                        new — fixed-code structured diagnostics
    │   ├── hearth.ts                         new — EntityDescriptor/EntityRead, bounded HTTP
    │   ├── projection.ts                     new — Projection and coalesced authoritative refresh
    │   ├── facts.ts                          new — NATS hint subscription/recovery
    │   └── hap.ts                            new — HAP mapping, Operation, completion boundary
    └── test/
        ├── config-identity.test.ts           new — process/storage/config contracts
        ├── hearth.test.ts                    new — HTTP boundary and brightness translation
        ├── hap-contract.test.ts              new — independent encrypted wire behavior
        ├── recovery.test.ts                  new — real Core/NATS/simulator process recovery
        └── support/                          new — controlled latency and controller fixtures
configs/homekit.example.yaml                  new — non-secret explicit export example
docs/homekit.md                               new — setup, lifecycle, recovery, Apple smoke
deployments/README.md                         modify — companion image and host-network guidance
README.md                                    modify — outgoing bridge overview and commands
GLOSSARY.md                                  modify — HomeKit bridge term, no Adapter redefinition
docs/architecture.md                          modify — approved outgoing TS exception/ownership
mise.toml                                    modify — focused package tasks and validate inclusion
.github/workflows/mise.yml                    modify — HomeKit checks/image build CI job
.github/workflows/release.yml                 modify — publish separate HomeKit container
.gitignore                                   modify — local bridge config/data exclusions
```

`BridgeManifest` belongs to identity, Config/AccessoryConfig to config,
EntityDescriptor/EntityRead/SatisfiedCommand to hearth, Projection to projection,
and Operation to hap. Concrete package-local modules use manual dependency
assembly in runtime; HAP/vendor types do not become Core types. Read authoritative
power/brightness schemas from the repository's single source, copying them as
build assets into the Node artifact/image rather than hand-syncing validators.
Record their relative source paths in the build task and validate packaging.

Build to `dist/` with TypeScript and run with Node. Use the existing Vitest pattern
for package tests, including real loopback HAP connections. Local CLI support may
use the built package from a checkout; no npm registry publication is required.
Publish `ghcr.io/mholtzscher/hearth/hearth-bridge-homekit` with the repository's
release tag and `latest`, separately from ko-built Go images. Use pinned release
tags in operator examples. CI must use frozen pnpm installation and build the
container without pushing. Only the existing release workflow publishes images.

Proposed focused tasks, created by this feature:

```text
mise run homekit-install       frozen dependency install
mise run homekit-build         schema assets plus strict TypeScript build
mise run homekit-test          package and encrypted HAP contract tests
mise run homekit-integration   isolated real Core/NATS/simulator checks
mise run homekit-image         no-push multi-platform-capable image build
```

`mise run validate` must include the HomeKit build, tests, and integration gate.
The corresponding focused change to the existing validation entry point is:

```diff
 [tasks."validate"]
 description = "Run all validation"
-depends = ["validate-backend", "web-test", "web-build"]
+depends = ["validate-backend", "web-test", "web-build", "homekit-test", "homekit-integration"]
```

`homekit-test` and `homekit-integration` both depend on `homekit-build`, so the
integrated gate builds the artifact once before their checks.

The integration harness may reuse existing Go simulator/Core fixtures or their
existing executable boundaries, but must not import Core internals into the
production Node package or require live provider credentials. Define exact
fixture orchestration commands during D1 using repository tasks; do not assume
the operator's running simulator or API key is a CI dependency. Existing managed
operator smoke tests use `mise run simulator-start` / `simulator-stop`.

## Deliverables

Estimates are relative sizes, not scheduling promises. Decompose L deliverables
into reviewable implementation units without splitting the single contract.

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---|---|---|---|
| D1 | Locked unmodified dependency, schema assets, independent HAP controller and isolated integration harness | L | package/tooling, test/support, mise tasks | Approved spec | A1, A3, A4 |
| D2 | Restart-safe single-writer CLI, config, identity and explicit pairing-info | L | cli/config/identity, test/config-identity | D1 | A2, A3, A10 |
| D3 | On/off bridge with authoritative reads, distinct Commands and safe completion | L | runtime/hearth/projection/hap, contract tests | D1, D2 | A4–A7 |
| D4 | NATS hints, bounded reconciliation, recovery and shutdown | L | facts/projection/runtime, recovery tests | D3 | A6–A10 |
| D5 | Dimmable light mapping and plug service | M | config/hearth/hap, schema assets, contract tests | D3, D4 | A5, A11 |
| D6 | CLI/container delivery, CI/release, operator docs and Apple validation evidence | L | Dockerfile, workflows, docs/configs, validation tasks | D2–D5 | A1, A10–A13 |

## Acceptance and validation

All are required before declaring production implementation complete. Prior
prototype checks inform them but do not satisfy these production gates.

| ID | Exercise and expected result | How to check | Prerequisites |
|---|---|---|---|
| A1 | Frozen install uses unmodified HAP-NodeJS; strict build/schema packaging succeeds | `homekit-install`, `homekit-build`, `homekit-test`; inspect lockfile and package configuration | Node/pnpm |
| A2 | CLI/env/YAML precedence and every invalid mapping/secret/interface boundary fail before advertisement; valid light/outlet examples start | Real CLI fixture tests in `homekit-test`, including unknown keys and same-Device grouping | Linux/flock |
| A3 | Two processes cannot use one store; no implicit reset on missing/corrupt/unwritable/half-initialized storage; renamed/reordered config preserves pairing and existing AID/IIDs | Real filesystem/process tests, saved encrypted-controller pairing reused across restart; assert disk data and authenticated restart | Linux local filesystem |
| A4 | Independent encrypted writes succeed for true, false, zero, and positive brightness; response values may be omitted, but supplied values and subsequent reads reflect observed State | Integration tests inspect raw HTTP responses and authoritative reads, not a controller's echoed request object | Loopback sockets |
| A5 | Each handler invocation submits one correctly typed Core `set`; repeated values stay distinct; invalid values submit none; On/Brightness never synthesize each other | Controlled HTTP boundary plus real Core Command history IDs through `homekit-integration` | Isolated Core/NATS/simulator |
| A6 | Held successful Command response followed by opposite/newer physical State cannot regress subsequent reads or notifications to requested values; cover booleans, brightness zero, callback microtasks, and reversed overlapping completions | Independent subscriber/client, controlled latency, exact Observation IDs, raw and chronologically normalized event batches; ordinary setter is a failing negative control | Encrypted HAP + isolated Core |
| A7 | Total eight-second budget includes final refresh; rejection/malformed response/timeout/connection loss yield defined errors; no automatic POST retry, second callback, late success, or captured-value update | Controlled upstream delays and actual elapsed wire responses; retain Core record of ambiguous satisfied attempt; count POSTs through deadline plus reconciliation | Controlled HTTP/HAP |
| A8 | One active refresh per Entity and coalesced hints; old abandoned success/failure cannot replace new State/availability; exact post-command refresh barrier is honored | Delayed HTTP fixtures and subscriber observations, not private cache-field assertions | Package fixtures |
| A9 | Core outage causes failed HAP operations and recovers; NATS outage leaves HTTP/polling usable; reconnect restores fact-driven updates; enablement/availability-only changes reconcile without fact | Real recovery harness; disable fast polling for fact-path assertion, match exact accepted Observation IDs, test missing State without fake success values | Isolated Core/NATS/simulator |
| A10 | Shutdown closes admission and joins/invalidates work within budget; no orphan runtime; restart never replays; missing data mount fails closed; readiness requires both public events and a passing probe; two consecutive failed listener probes stop the runtime | Process tests, live PID/socket checks, delayed/missing advertised/listening events, real HTTP probe fixtures, and real stored identities before/after | Linux/flock |
| A11 | Dimming reads preserve observed 83 at step 5; request 83 at max 100 becomes 85; max 83 becomes 80; zero, bounds, maximum<step, and independent power behavior hold; Outlet has no fabricated usage | Raw-wire read/write tests, Core payload/history assertions, profile inventory checks | HAP fixtures and real simulator |
| A12 | Nonroot host-network container persists identity, serves HAP after restart, and ships no credentials; CI build passes and release uses the repository tag | `homekit-image`, container smoke with mounted private volume/config/code; workflow review, `mise run validate`, clean generated/build diff | Reachable Docker daemon; no image push in checks |
| A13 | Apple Home pairs a light, dimmable light and plug; on/off and brightness plus simulated physical changes work; bridge restart preserves pairing and explicitly checked room assignment; Core outage/recovery is honest and resumes without re-pairing | Operator procedure in `docs/homekit.md`, paired iPhone, exact Core Command/Observation IDs retained in sanitized acceptance report; include false/zero and rapid/overlapping controls | iPhone and reachable trusted LAN; home hub only for optional remote-access check |

The HAP watchdog currently totals approximately nine seconds. Tests must use the
eight-second application budget rather than trusting the library's ten-second
comments. Never mistake a library-generated default or controller echo for a
successful authoritative response. Assertions include accessory/characteristic
identity, supplied value/type, status, authoritative readback, and Core evidence.

Automatic contract tests need no iPhone, household deployment, provider keys,
internet connectivity to devices, or multicast routing. Apple smoke checks are
an explicit operator gate, not a default CI dependency. No physical hardware is
required to validate this first bridge release; the simulator can model all
included profiles. Real hardware compatibility remains adapter-dependent.

## Tradeoffs and risks

| Choice | Alternative | Reason and cost |
|---|---|---|
| Standalone HAP-NodeJS | Homebridge plugin / Go HAP | Retains a maintained protocol stack without the plugin host; accepts a Node service and a narrow stack exception |
| Explicit mappings and restart | Automatic inventory / hot reload | Predictable export identity and bounded scope; operator supplies canonical IDs |
| HTTP authority with NATS hints | Full fact projection / polling only | No stale replay authority or new revision protocol; NATS supplies prompt routing while periodic reads cover omitted change families |
| Overlapping independent Commands | Local FIFO with uncertain predecessor recovery | Matches existing Core semantics without promising cancellation/order; conflicting requests may affect each other's outcomes |
| Unmodified HAP-NodeJS | Vendor patches for stronger response/storage guarantees | Accepts optional response omissions and upstream pairing-write failure handling; avoids maintaining protocol-library modifications |
| Callback completion | Async onGet/onSet convenience | Avoids an extra cache-assignment scheduling gap; requires disciplined synchronous finalization |

Risks and mitigations:

- Apple behavior may differ for optional write responses or dimming. A13 is a release
  gate and cannot be replaced by the software controller's PASS result.
- Repeated client writes may create extra physical work or Command history. Keep
  evidence and test repeated values; do not infer intent or hide requests behind
  an arbitrary deduplication window. A proven duplicate handler bug must be fixed
  at its owner rather than normalized as client behavior.
- Facts and HTTP lack an exposed monotonic State revision. Coalesced sequential
  refreshes and callback sampling prevent known stale local completions but do
  not provide global linearizability or all-transition delivery.
- Missing storage can silently reset upstream library identity. Explicit
  initialization, flock, prevalidation, loss detection, and full-directory backup
  mitigate it; filesystem/power-loss recovery is not a distributed transaction.
- Host networking can expose HAP on more interfaces than advertisement suggests.
  Document wildcard binding and require narrowly scoped operator firewall rules.
- A Command can execute after the HomeKit client sees an error. Never retry or
  translate timeout into a claim of non-execution; reconcile actual State.

## Design rationale and approval

[Model the domain](file:///home/michael/.agents/skills/mstack/principles/model-the-domain.md)
keeps export identity separate from Device ownership and distinguishes unusable
projections from real false/zero State.
[Boundary discipline](file:///home/michael/.agents/skills/mstack/principles/boundary-discipline.md)
puts unknown JSON/schema validation at HTTP/config edges and requires one
synchronous HAP completion boundary.
[Experience first](file:///home/michael/.agents/skills/mstack/principles/experience-first.md)
led to snapping brightness requests while preserving actual readback, rather
than making normal slider movement fail or falsifying State precision.
[Idempotent operations](file:///home/michael/.agents/skills/mstack/principles/idempotent-operations.md)
led to persistent identity and explicit non-replay behavior, not speculative
deduplication of distinct writes.
[Test behavior](file:///home/michael/.agents/skills/mstack/principles/test-behavior.md)
requires independent encrypted-wire values and Core evidence rather than setter
call counts or controller request echoes.

No unresolved product-intent choices remain in this draft. The implementation
owner resolves mechanical dependency/package versions and exact integration
fixture orchestration within D1. The operator explicitly relaxed the two original
vendor-patch requirements in favor of upstream edge-service behavior. Changes to
ownership, profile scope, concurrency, or network trust return to approval.

Success means the configured household controls work in Apple Home with durable
Core Command evidence, authoritative return-path State, stable pairing/identity,
and verified failure/recovery behavior. It does not mean arbitrary HomeKit parity,
exactly-once user intent, or physical causation beyond Adapter evidence.

The operator approved this contract through the subsequent implementation
request. The acceptance table remains the completion gate; approval does not
establish that any production check passed.
