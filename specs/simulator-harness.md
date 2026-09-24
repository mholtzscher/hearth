# Scripted Simulator Harness

**Status:** Accepted for implementation. The legacy `scenario:` simulator path stays
intact until its matrix coverage is re-expressed as scripted configs.

## Problem

The `hearth-simulator` process proves one hardcoded light (`power` Entity plus a
fixed set of failure scenarios). An agent validating a change — a new Entity type,
a new automation trigger, a Core-offline recovery path — cannot declare its own
Device, cannot choose Entity types, and cannot script the values the simulated
Device reports over time. Every new need means editing Go.

## Goal

An agent can, with no Go changes:

1. Declare Devices, Entities (any built-in Entity type), health, and availability
   in a YAML config file.
2. Launch the simulator and have each Entity emit a configured series of values
   on a configured interval, looping until stopped.
3. Interact with the simulated Devices through `hearthd` exactly as with real
   hardware (read State, send Commands, match Device Facts), and additionally
   poke the simulator directly through a loopback control channel (publish now,
   pause/resume a script) for deterministic tests.

## Non-goals

- Replacing the legacy `scenario:` path or its matrix integration coverage in
  this change. Legacy and scripted modes are mutually exclusive in one config.
- Cross-field Entity validation inside the simulator (for example brightness
  `multiple_of`). Config values are validated against the authoritative JSON
  Schemas; Core remains the enforcing validator and rejects the rest with its
  normal dispositions.
- A durable or authenticated control channel. The control server binds loopback
  only, is optional, and exists for test scripting.
- New Entity types. The harness covers exactly the built-in catalog the codegen
  already derives; unknown `type` values fail config load loudly.

## Configuration

```yaml
adapter_id: simulator
nats_url: nats://127.0.0.1:4222
control_addr: 127.0.0.1:8181   # optional; loopback only; absent disables control
devices:
  - binding_key: simulated-light
    name: Simulated light
    kind: light
    health: healthy              # | unhealthy:<reason-code>
    entities:
      - key: power
        name: Power
        type: hearth.power/v1
        support: {state: {}, operations: {set: {}}}
        initial: true
        outputs: {interval: 5s, values: [true, false]}
        commands: {set: {behavior: accept-and-publish, apply_parameters: true}}
      - key: temperature
        name: Temperature
        type: hearth.temperature/v1
        support: {state: {unit: mCel}, operations: {}}
        initial: 21500
        outputs: {interval: 10s, values: [21500, 21600, 21400]}
  - binding_key: simulated-button
    name: Simulated button
    kind: sensor
    entities:
      - key: events
        name: Events
        type: hearth.enumevent/v1
        support: {state: {}, operations: {}, events: {names: [single_press, double_press]}}
        outputs: {interval: 30s, values: [single_press, double_press]}
```

Rules:

- The existing top-level `adapter_id` + `devices` form remains valid. Alternatively,
  use `adapters:` with entries containing `adapter_id` and `devices`; see
  `configs/simulator.multi-adapter.example.yaml`. Do not combine the two forms.
  Adapter IDs must be unique, while Device binding keys can repeat across
  different Adapters.
- `binding_key`, `key`, and `adapter_id` follow the existing slug rules;
  `type` must be a known built-in Entity type, else load fails.
- `support`, `initial`, and every `outputs.values` entry are validated against
  that type's authoritative State/support schemas at load; the first failure
  names the Device, Entity, and value index.
- `outputs` without `values` (or with an empty list) means "publish `initial`
  once, then stay silent". With values, `values[0]` publishes at startup and
  multi-value lists advance one entry per interval, looping forever. A
  single-value list publishes only once.
- Commands not listed under `commands:` default to
  `{behavior: accept-and-publish, apply_parameters: true}`. Unknown operations
  for the Entity type are rejected by Core as today; the simulator never
  invents support.
- `behavior` is one of `accept-and-publish`, `accept-no-publish` (accepted but
  no outcome Observation, so observed-outcome Commands time out), or `reject` (with optional
  `reason`). `apply_parameters` applies the generic parameter-to-State rule
  below before publishing.

## Runtime semantics

- One `Register` per Device. Each configured Adapter owns its own SDK Session,
  runtime, and health report. Health defaults to healthy per Device and
  aggregates within its Adapter: the first unhealthy Device wins. A faulted
  Adapter does not change the health of another Adapter. Entity availability defaults to available;
  both are reported explicitly at startup.
- State Entities publish Observations with the normalized configured value.
  Event-source Entities publish Entity Events with the configured name, which
  must be listed in the Entity's support.
- Command handling is concurrency-safe and multiplexed across all controllable
  Entities. `accept-and-publish` accepts, optionally applies parameters, then
  publishes one Command-linked Observation of the current value through the
  accept evidence, so observed-outcome Commands can be satisfied.
- Generic parameter-to-State rule (documented, best-effort): if parameters are
  `{"value": X}` and current State is a scalar, State becomes `X`; if State is
  an object with a `value` member, that member becomes `X`. Otherwise State is
  unchanged and the current value is republished.
- A paused Entity's ticker stops advancing; Commands and control publishes
  still work. Pause exists so an agent can hold State still while asserting a
  Command outcome, then resume the script.

## Control channel

Optional stdlib HTTP server on `control_addr` (loopback only; non-loopback is a
config error):

- `GET /v1/sim/entities` lists every Entity with its canonical ID, type,
  paused flag, and current value/name. Multi-adapter configurations also include
  `adapter_id` in list, publish, pause, and resume responses. Select a scenario
  by `(adapter_id, binding_key, key)`; binding keys may repeat across Adapters.
- `POST /v1/sim/entities/{entity_id}/publish` with `{"value": <json>}` publishes
  one Observation now (State Entities), or `{"name": "<event>"}` publishes one
  Entity Event now (event sources). A successful response retains the flat
  Entity snapshot and adds exactly one `observation_id` or `event_id`: the
  canonical SDK publication ID carried on the wire and recorded by Core.
  Success means broker acknowledgment, not Core acceptance; poll Core for that
  exact ID and its disposition. Error responses expose no success ID; a
  transport error may still be ambiguous and must not trigger blind retries.
- `POST /v1/sim/entities/{entity_id}/pause` and `/resume` control the script
  ticker.

The primary validation path remains `hearthd`'s HTTP API and Device Facts; the
control channel is a scripting aid, not a substitute.

## Conformance to architecture

- Adapters speak to Core only through the SDK Session; the harness keeps that
  seam and adds no Core imports.
- Schemas stay authoritative: the harness validates config JSON with the same
  embedded per-type schemas the generated facades compile, addressed through a
  small wiring table over generated `FS`/`SchemaFiles` (no duplicated schema
  text, no handwritten per-type behavior). Moving that table into `entitytypegen`
  output is tracked future work.
- One process owns one YAML file for non-secret configuration, unchanged.
