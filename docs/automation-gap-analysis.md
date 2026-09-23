# Home Assistant automation gap analysis

**Research date:** 2026-09-22

This document compares Hearth's implemented automation behavior with the 29
automations configured in the household's Home Assistant instance on the
research date. It identifies the automation capabilities needed for Hearth to
become a functional replacement for those automations. It is not a plan for
general Home Assistant feature parity.

Home Assistant configuration changes after the research date may make the
inventory stale. Re-read the live definitions before planning a migration.

## Hearth's implemented baseline

Hearth currently supports:

- Observation Triggers that compare one accepted Observation value with `eq`,
  `ne`, `lt`, `lte`, `gt`, or `gte`.
- Numeric ranges expressed as multiple comparisons against one Observation.
- Entity Event Triggers that match an exact Entity and event name.
- Held-State Triggers that admit once after a value predicate has remained
  satisfied for its configured duration, subject to the lifecycle and
  continuity limits below.
- Multiple alternative Triggers in one Automation.
- Optional current-State Conditions composed with `all`, `any`, and `not`.
- Static Entity Operation Steps executed in order.
- Definition validation, revision-controlled replacement, and manual Runs.
- Durable Device Fact consumption, duplicate suppression, and stale-Fact
  handling.
- Immutable Run snapshots, recorded Skips, Step outcomes, and retained history.
- Truthful restart behavior. Hearth interrupts unfinished Runs and does not
  replay their Commands.

The primary sources for this baseline are:

- [`specs/automations.md`](../specs/automations.md)
- [`specs/automation-conditions.md`](../specs/automation-conditions.md)
- [`internal/modules/automations/automation-definition.schema.json`](../internal/modules/automations/automation-definition.schema.json)
- [`CONTEXT.md`](../CONTEXT.md)

## Home Assistant inventory

The Home Assistant instance contained 29 automations. Twenty-eight were enabled,
one was disabled, 24 had standalone definitions, and five used blueprints.

The standalone definitions used these Trigger families:

| Trigger family | Automations using it |
| --- | ---: |
| State | 9 |
| Numeric State | 8 |
| Time | 5 |
| Device event | 4 |
| Conversation | 3 |
| Core startup | 2 |
| Time pattern | 2 |
| Sun | 1 |
| Zone | 1 |

The five blueprint-backed automations were:

- Battery Notes: battery not reported
- Battery Notes: battery replaced
- Battery Notes: battery threshold
- Random Light Colors
- Offline detection for Zigbee2MQTT devices with `last_seen`

No current Home Assistant automation can move to Hearth unchanged end to end.
Some Trigger matching can move today, but each complete workflow depends on one
or more missing capabilities or missing Entity Operations.

## Automation capabilities

### State transitions

State Transitions let an Automation react when an Entity moves from one matching
value to another, rather than whenever a new value matches.

They support exact changes such as `off` to `on` and numeric crossings such as
`<=25` to `>25`. Triggers may inspect previous and current values. Existing
single-value comparisons and numeric ranges remain supported.

The distinction matters because Hearth currently evaluates only the incoming
Observation. For a `gt 25` comparison, both `20` to `26` and `26` to `27` match.
Only the first is a threshold crossing. Restricting a Trigger to `applied` does
not solve this because any changed value has that disposition.

State transition support should include:

- Exact `from` and `to` matching.
- Previous-value comparisons.
- Upward and downward numeric crossings.
- Previous and current value evidence in automation history.
- A non-match when previous State is absent or incompatible.

State Transitions do not include a duration requirement. The temporal engine
should own behavior such as "above 25 for ten minutes."

### Temporal evaluation

The implemented Held-State Trigger provides a narrow form of temporal
evaluation: it tracks matching State from an eligible Observation and checks
current State at the deadline. It does not verify every Observation between
start and expiry, so a brief nonmatch hidden by Fact backlog can be missed.
Pending time is discarded on Core restart, and a silent Entity does not cause a
new hold to start after restart. Holds are also cleared by definition
replacement. This is not a general durable scheduler.

Remaining temporal capabilities include:

- Delay Steps.
- Wait timeouts.
- Daily and weekday Time Triggers.
- Interval and time-pattern Triggers.
- Durable Timer helpers.
- State-duration Conditions.
- Pending-work restart policy.

These capabilities share scheduling and durable deadline storage, but expiry has
different effects. A held predicate may admit a new Run. A delay resumes an
existing Run. A Time Trigger starts an admission decision. A Timer helper emits
a completion event.

Sunrise, sunset, reusable schedules, calendar restrictions, timezone handling,
and daylight-saving-time behavior should use the same scheduler with separate
occurrence calculators.

### Run control flow

A bounded Step tree should support:

- Trigger-ID Conditions.
- `if`, `then`, and `else` branches.
- `choose` branches with a default.
- Parallel Step groups.
- Repetition over an explicit bounded input.
- Explicit Run termination.
- Invocation of a reusable Step sequence.

Trigger-ID Conditions and branching should arrive together. At least ten of the
standalone Home Assistant automations choose different actions based on which
Trigger fired.

Parallel groups and repetition require more execution and history work than
branching. They can follow the initial branch model.

### Waiting and continuation

Waits and delays require Hearth to retain executable Run progress without
holding an execution worker. This capability includes:

- Waiting for a State or Device Fact.
- Wait timeouts.
- Delays.
- Durable suspended Runs.
- Run cancellation.
- An explicit restart policy for suspended Runs.

This changes Hearth's current non-resumable Run model and should be designed as
one feature rather than as independent delay and wait special cases.

### Run admission policies

Hearth currently permits one active Run per Automation and records later matches
as `automation_busy` Skips. The household automations also need:

- Restart, which cancels an active Run and starts the new one.
- A bounded queue.
- Bounded parallel Runs.
- An optional per-Entity or per-Device concurrency key.
- Queue overflow policy.
- Run cancellation.

Restart depends on cancellation. Queued execution needs durable pending
invocations or Runs. Parallel execution requires replacing the one-active-Run
storage invariant. These policies should share one admission model.

### Runtime data binding

One typed value-resolution system should provide:

- Triggering Entity identity.
- Incoming Observation value.
- Previous State value.
- Current State value.
- Entity Event payload when Hearth adds typed event payloads.
- A prior Step's response.
- Variables.
- Dynamic Entity targets.
- Dynamic Step parameters.
- String interpolation.
- Small numeric transforms such as add, subtract, and clamp.
- Random selection.

Hearth should not add separate template languages for notifications, Entity
targets, and command parameters. JSON Pointer references and a small set of typed
transforms cover most household use cases without importing Jinja semantics.

### Provider Steps

Several household actions do not map cleanly to an Entity Command with an
observed State outcome. Hearth needs typed provider Steps for:

- Notifications.
- Persistent notifications.
- AI and image analysis.
- Other request-and-response providers.
- Capturing a provider response for a later Step.

Entity Operation Steps should remain the typed device-control mechanism. A
provider Step needs its own acceptance and outcome contract.

### Virtual Entities

A common first-party virtual Entity mechanism should provide:

- Boolean values.
- Buttons or event sources.
- Counters.
- Date and time values.
- Select values.
- Durable timers.
- Reusable schedules.

Booleans, counters, and selects mostly use ordinary State and Operations. Timers
and schedules also depend on temporal evaluation.

### Scenes and reusable definitions

Related capabilities should arrive in this order:

1. Reusable static Step sequences.
2. Static scenes that describe desired Entity States.
3. Runtime State snapshot and restore.
4. Parameterized Automation templates.
5. Reusable Condition groups.

Runtime snapshots are harder than static scenes because a Run creates and later
consumes the snapshot data. Blueprint compatibility should not precede the
runtime capabilities that those blueprints require.

### Dynamic Entity sets

Battery and offline-device checks need operations over a changing set of
Entities:

- Query Entities by type, label, area, or metadata.
- Filter results by current State.
- Iterate over results.
- Aggregate selected values.
- Use the result as an action target or formatted provider input.

This should follow fixed-Entity automation support. Dynamic sets introduce
questions about definition validation, result bounds, partial failure, and
changes to collection membership during a Run.

### Other Trigger sources

The household also uses Trigger sources that are outside Hearth's current Device
Fact families:

- Core startup.
- Conversation phrases.
- Zone entry and exit.
- Sunrise and sunset.
- Home Assistant integration events.
- Z-Wave Central Scene events.

Physical controls should become Entity Events through their Adapter. A location
Adapter can own presence and zones. Conversation handling can invoke an
Automation manually. Core lifecycle events and generic provider events should
only be added for specific household behavior, not as an unrestricted event bus.

## Required Entity Operations and providers

The automation engine alone cannot migrate the household workflows. Their Steps
also need support for:

1. Notifications and persistent notifications.
2. Static scenes and reusable command sequences.
3. Fan percentage and preset mode.
4. Climate HVAC and fan mode.
5. Media playback, track selection, stopping, and volume.
6. Select and enum settings.
7. Timer and virtual-helper control.
8. Runtime State snapshots and restoration.
9. Integration-specific maintenance actions such as Z-Wave ping.

## Household examples by missing capability

| Capability | Current Home Assistant examples |
| --- | --- |
| State transition or threshold crossing | Air Purifier Auto Shutoff, Laundry Notifications, Backyard Light Toggle |
| Held predicate with `for` (partially supported by Held-State Triggers; see limits above) | Apollo OTA Mode, Deep Freezer Notifications, Laundry Notifications, Potted Plant Moisture Alarm, Run HVAC Fan |
| Clock or sun occurrence | Evening Lighting, Daily Allergy Report, Daily Battery checks, Purge The Air |
| Trigger-based branching | Air Purifier Auto Shutoff, Deep Freezer Notifications, Evening Lighting, Laundry Notifications, Office Air CO2 Light, Office Control Dial, Shit Box Notifications |
| Delay or wait | Open/Close Doors, Backyard Light Toggle, Heading Out Button, Run HVAC Fan |
| Queued or parallel Runs | Office Control Dial, Battery Notes blueprints, Random Light Colors |
| Runtime parameter binding | Air Purifier Notifications, Potted Plant Moisture Alarm, Office Control Dial, Battery Notes, Random Light Colors |
| Virtual helpers or timers | Apollo OTA Mode, Laundry Notifications, Purge The Air, Run HVAC Fan |
| Scene activation or snapshot | Evening Lighting, Heading Out Button, Office Switch Double Tap, Run HVAC Fan |
| Dynamic Entity query | Offline Zigbee2MQTT Detection, Battery Notes daily checks |
| Conversation or presence | Office Switch Double Tap, Purge The Air, Run HVAC Fan, Leaving Work |

## Suggested implementation order

1. Add State Transitions with exact changes and numeric crossings.
2. Implemented: add Held-State Triggers with `for`; broader durable temporal
   evaluation remains future work.
3. Add Trigger-ID branching with `if` and `choose`.
4. Add a notification provider.
5. Add the fan, climate, media, select, and helper Operations used by the
   household.
6. Add Time, interval, and sun Triggers plus time Conditions.
7. Add delays, waits, timers, and suspended-Run persistence.
8. Add restart, queued, and parallel Run policies.
9. Add typed runtime data binding and small transforms.
10. Add reusable sequences, static scenes, and runtime snapshots.
11. Add presence, battery-health, offline-device, and AI provider support.
12. Add parameterized Automation templates after their runtime dependencies
    exist.

## State Transitions implementation handoff

Implement State Transition Triggers in Hearth.

Add optional previous-value matching to Observation Triggers so Automations can
react to transitions such as `off` to `on` and numeric crossings such as `<=25`
to `>25`, rather than every accepted Observation whose current value matches.

Requirements:

- Preserve existing current-value comparisons and range matching.
- Allow zero or more comparisons against the previous accepted State and the
  existing comparisons against the incoming Observation.
- Require all previous and current comparisons to match.
- Treat absent or incompatible previous State as a non-match.
- Retain previous and current values in immutable Run and Skip evidence.
- Validate JSON Pointers, operators, operands, and bounds with the existing
  comparison semantics.
- Preserve duplicate suppression, atomic admission, Condition evaluation,
  history, and restart behavior.
- Do not add held-State duration or `for`. The temporal automation feature owns
  that behavior.
- Test exact State changes, upward and downward numeric crossings, ranges,
  missing previous State, unchanged Observations, duplicate Facts, and
  definition replacement races.
- Update the domain model, strict JSON schema, persistence and history DTOs,
  HTTP and OpenAPI contracts, documentation, and tests.
- Follow `CONTEXT.md`, `specs/automations.md`, `specs/automation-conditions.md`,
  and `AGENTS.md`.
- Run `mise run validate` and review the resulting diff.
