# Automation Conditions

Automations can require current Entity State before they run. A Trigger decides
*when* an Automation is considered; a Condition decides *whether* that
consideration may admit a Run.

Conditions are optional. A definition that omits `conditions` keeps its existing
behavior: any matching Trigger admits unconditionally. A Condition is evaluated
exactly once, when Hearth decides admission for one Device Fact or one manual
invocation, against a coherent batch of current State evidence read through the
devices module. State changes never start an Automation, and a later State change
never re-opens a decision that already committed.

This guide is the operator-facing contract for the feature specified in
[Automation Conditions](../specs/automation-conditions.md) and decided in
[ADR 0022](adr/0022-evaluate-conditions-from-state-snapshots.md). It
assumes a running `hearthd` and at least one registered Adapter; see the
[README](../README.md) for the simulator, Zigbee2MQTT, and Ecowitt recipes.

## Prerequisites and discovering IDs

Conditions reference canonical Entity IDs by registered identity, so the
Entities must already exist. The Zigbee2MQTT adapter registers an illuminance
Entity as a read-only `hearth.numericsensor/v1` with unit `lx`, and occupancy as
a read-only `hearth.binarysensor/v1` whose boolean State decodes only the
expose's declared `value_on`/`value_off` scalars:

```sh
curl http://127.0.0.1:8080/v1/adapters/zigbee2mqtt
curl http://127.0.0.1:8080/v1/entities
curl http://127.0.0.1:8080/v1/entities/ent_...
```

Copy each `id` you need. The examples below use the illustrative canonical IDs
`ent_01950000-0000-7000-8000-000000000001` (illuminance), `...0002` (occupancy),
`...0003` (the controllable light), and `...0010` (motion). **Substitute your own
registered IDs**; an unknown or stateless Entity is rejected at save time.

Save-time validation requires each referenced Entity to exist and be stateful. It
deliberately accepts an Entity that has never reported State, is currently
unavailable, is disabled, or has an owner that is unhealthy: those become
evaluation results, not definition errors.

## Definition contract

`conditions` is one optional root node, not an array. Every node has an
author-supplied `id` that is unique across the whole tree, uses the same
subject-safe slug grammar as Trigger and Step IDs (1–63 bytes,
`^[a-z0-9][a-z0-9_-]{0,62}$`), and is independent from the Trigger and Step ID
namespaces. Child order is retained in the recorded evidence.

| Kind | Required fields | Meaning |
| --- | --- | --- |
| `entity_state` | `entity_id`, `pointer`, `operator`, `operand`; optional `max_age_seconds` | Compare one selected current-State value with a static operand |
| `all` | nonempty `children` | Every child must be true |
| `any` | nonempty `children` | At least one child must be true |
| `not` | exactly one `child` | Negate true/false, preserve unknown |

`pointer` is an RFC 6901 JSON Pointer into the selected State value (`""`
selects the whole value). `operator` is one of `eq`, `ne`, `lt`, `lte`, `gt`,
`gte`. `operand` is exactly one static JSON value. Comparisons reuse the same
exact numerical and JSON equality semantics as Observation Trigger comparisons.
`max_age_seconds`, when present, is an integer from `1` to `2592000`.

`conditions` is optional and is preserved as omitted on an encoded round trip.
Explicit JSON `null` is invalid, as are unknown fields, contradictory family
fields, empty groups, duplicate IDs, more than 64 total nodes, more than 8 levels
of depth, and normalized definitions over 64 KiB. Each `entity_state` node is
compared independently; there is no shared intermediate value.

## A complete create request

This definition turns on a light when motion is reported, but only when the room
is dark and the adjacent room is not occupied. The `not` node deliberately makes
missing occupancy State block the action, because absence is not permission:

```sh
curl -X POST http://127.0.0.1:8080/v1/automations \
  -H 'content-type: application/json' \
  -d '{
    "name": "Office light on motion when dark and unoccupied",
    "enabled": true,
    "triggers": [
      {
        "id": "motion",
        "kind": "observation",
        "entity_id": "ent_01950000-0000-7000-8000-000000000010",
        "dispositions": ["applied", "unchanged"],
        "comparisons": [{"pointer": "", "operator": "eq", "operand": true}]
      }
    ],
    "conditions": {
      "id": "dark-and-free",
      "kind": "all",
      "children": [
        {
          "id": "room-dark",
          "kind": "entity_state",
          "entity_id": "ent_01950000-0000-7000-8000-000000000001",
          "pointer": "",
          "operator": "lt",
          "operand": 30,
          "max_age_seconds": 300
        },
        {
          "id": "other-room-unoccupied",
          "kind": "not",
          "child": {
            "id": "other-room-occupied",
            "kind": "entity_state",
            "entity_id": "ent_01950000-0000-7000-8000-000000000002",
            "pointer": "",
            "operator": "eq",
            "operand": true,
            "max_age_seconds": 120
          }
        }
      ]
    },
    "steps": [
      {
        "id": "turn_on",
        "entity_id": "ent_01950000-0000-7000-8000-000000000003",
        "operation": "set",
        "parameters": {"value": true}
      }
    ]
  }'
```

A `201` response echoes the created definition, its `revision`, and the `aut_`
identity used by every later request (elided fields are marked `...`):

```json
{
  "id": "aut_01950000-0000-7000-8000-000000000004",
  "revision": 1,
  "created_at": "2026-09-15T12:00:00Z",
  "updated_at": "2026-09-15T12:00:00Z",
  "definition": {
    "name": "Office light on motion when dark and unoccupied",
    "enabled": true,
    "triggers": [ ... ],
    "conditions": { "id": "dark-and-free", "kind": "all", "children": [ ... ] },
    "steps": [ ... ]
  }
}
```

### `any` admits despite an unknown sibling

A true `any` child admits even when a sibling is unknown, because the decision
only needs one satisfied requirement. This definition turns on a fan when either
the room is dark or the adjacent room is unoccupied:

```json
{
  "conditions": {
    "id": "any-permission",
    "kind": "any",
    "children": [
      {
        "id": "room-dark-any",
        "kind": "entity_state",
        "entity_id": "ent_01950000-0000-7000-8000-000000000001",
        "pointer": "",
        "operator": "lt",
        "operand": 30,
        "max_age_seconds": 300
      },
      {
        "id": "other-room-unoccupied-any",
        "kind": "not",
        "child": {
          "id": "other-room-occupied-any",
          "kind": "entity_state",
          "entity_id": "ent_01950000-0000-7000-8000-000000000002",
          "pointer": "",
          "operator": "eq",
          "operand": true,
          "max_age_seconds": 120
        }
      }
    ]
  }
}
```

With a dark reading and no occupancy State at all, `room-dark-any` is `true` and
`other-room-occupied-any` is `unknown`, so `any(true, unknown)` is `true` and the
Run is admitted. The unknown sibling's evidence is still recorded, so history
explains exactly which requirement was unknown. Add this object beside a valid
definition's other fields, not inside the `all` example above.

## Three-valued logic

Values are `true`, `false`, or `unknown`. Only a true root admits a Run.

| Children | `all` | `any` |
| --- | --- | --- |
| all true | true | true |
| all false | false | false |
| true and false | false | true |
| true and unknown | unknown | true |
| false and unknown | false | unknown |
| all unknown | unknown | unknown |

`not(true)` is `false`, `not(false)` is `true`, and `not(unknown)` remains
`unknown`. Hearth evaluates every node, including branches that cannot change the
root, so history explains every predicate rather than whichever branch happened
to short-circuit. That is why `not(other-room-occupied)` is `unknown` — and
therefore blocks the `all` example — when the adjacent occupancy Entity has no
State: absence is not permission to turn on the light.

## What "unknown" means

A leaf is unknown only for one of these reasons, in this precedence order:

| Reason | Meaning |
| --- | --- |
| `entity_missing` | The explicitly requested canonical Entity does not exist at the snapshot read |
| `state_missing` | The Entity exists but has no accepted State |
| `evidence_in_future` | An age bound is present and the Observation time is after the decision time |
| `evidence_expired` | An age bound is present and the evidence is older than the bound |
| `pointer_missing` | The valid pointer cannot select a value, including a noncanonical runtime array index |
| `type_mismatch` | `eq`/`ne` saw different top-level JSON kinds, or an ordering operator saw a non-number |

A comparison of two valid same-kind values is `true` or `false`. JSON `null` is a
real selected value, not missing evidence. `ne` on incompatible types is
`unknown`, not `true`. Composite nodes have no invented reason of their own;
their children explain them.

Corrupt stored State, corrupt definitions, impossible evidence identities, and
storage failures are *errors*, never `unknown`. A corrupt State read rolls back
admission, writes no outcome, leaves the Fact pending for negative
acknowledgement, and logs `automation.condition_state_corrupt`. The pending Fact
blocks until repair or until it is stale; after 30 seconds the ordinary stale
classification records its usual Skip without reading State. Manual corruption
returns a safe HTTP 500.

## Freshness, availability, and disabled State

`max_age_seconds` bounds how old the *evidence* may be. Age is
`decision time - State.ObservedAt`, where `ObservedAt` is Core's broker-assigned
Observation time, not the Adapter acquisition time or an upstream change time. An
age equal to the bound is allowed; one nanosecond past it is expired. Omitting
the bound compares retained State regardless of age, and future evidence is
unknown only when a bound is applied.

A new accepted Observation refreshes both the Observation identity and the
evidence clock even when the value is unchanged: the State takes the new
Observation identity and `ObservedAt` while keeping an equivalent retained
value. The bound does not promise recent sampling, and it does not mean
"the value held for N seconds" — a new report may describe an older physical
measurement.

Availability, enablement, and freshness are separate concepts:

- An unavailable, unknown-availability, or disabled Entity may still supply
  retained State to a Condition. State is not erased when an Entity reports
  unavailable, and disablement does not reinterpret the last accepted State.
- Reporting availability does not change comparison results. Only an absent or
  age-bounded State does.
- Save-time validation accepts a stateful Entity that has never reported State,
  is unavailable, or is disabled.
- A Condition never weakens command-time rules: a Step's Command is still
  validated and satisfied under its own current enablement, availability, and
  freshness gates. A Condition reading retained State does not promise the
  Command will succeed.

## Automatic outcomes and history

For each current matching Automation, Hearth applies precedence before any State
is read: a duplicate `(fact_id, automation_id)` receipt is ignored; a Fact older
than 30 seconds records a `stale_fact` Skip; an already-running Run records an
`automation_busy` Skip. Only then are Conditions considered. A true root admits a
Run; a false root records a `conditions_false` Skip; an unknown root records a
`conditions_unknown` Skip. Skips create no Steps and reserve no Command
identities, and every automatic Condition Skip commits a matched-Fact receipt in
the same transaction, so redelivery never re-evaluates it against later State.

History summaries expose the decision mode, the root result when evaluated, and
the bypass flag. History detail exposes the full tree and every predicate:

```sh
curl http://127.0.0.1:8080/v1/automations/aut_01950000-0000-7000-8000-000000000004/history
curl http://127.0.0.1:8080/v1/automations/aut_01950000-0000-7000-8000-000000000004/history/ask_01950000-0000-7000-8000-000000000005
```

A `conditions_unknown` Skip's detail explains itself, including the missing State
and the retained value that did resolve (elided fields are marked `...`):

```json
{
  "kind": "skip",
  "skip": {
    "id": "ask_01950000-0000-7000-8000-000000000005",
    "source": "device_fact",
    "reason": "conditions_unknown",
    "fact": {"fact_id": "fct_...", "family": "observation", "entity_id": "ent_...0010", "variant": "applied"},
    "matched_triggers": [ {"id": "motion", "kind": "observation", "entity_id": "ent_...0010"} ],
    "condition_decision": {
      "mode": "evaluated",
      "bypass_requested": false,
      "snapshot": {"id": "dark-and-free", "kind": "all", "children": [ ... ]},
      "evaluation": {
        "evaluated_at": "2026-09-15T12:34:56Z",
        "result": "unknown",
        "nodes": [
          {"id": "dark-and-free", "result": "unknown"},
          {"id": "room-dark", "result": "true", "selected_value": 12,
           "observation_id": "obs_...", "observed_at": "2026-09-15T12:34:50Z"},
          {"id": "other-room-unoccupied", "result": "unknown"},
          {"id": "other-room-occupied", "result": "unknown", "unknown_reason": "state_missing"}
        ]
      }
    },
    "skipped_at": "2026-09-15T12:34:56Z"
  }
}
```

Nodes appear once each in definition pre-order. A leaf stores its selected value
when the pointer resolves, its Observation identity and observed time when State
exists, and any unknown reason. A selected JSON `null` is encoded as `null`;
missing evidence omits the value entirely. Composite nodes record only their ID
and result. History detail includes the Condition tree, so a Skip stays
explainable after the definition is replaced or deleted; all of this evidence is
pruned with the Row under the existing automation retention policy.

Evidence values are visible here because State values are already visible through
the trusted API. Hearth never writes selected values, operands, whole trees, or
raw JSON into logs or HTTP error detail.

## Manual invocation

`POST /v1/automations/{automation_id}/runs` keeps its existing endpoint and
no-body behavior. Manual invocation has no Trigger and no Fact, continues to
ignore definition enablement, and still applies the existing gates and busy
checks before Conditions. A present body must be one strict JSON object:

```sh
# Omitted body, an empty object, or an explicit false all apply Conditions.
curl -X POST http://127.0.0.1:8080/v1/automations/aut_01950000-0000-7000-8000-000000000004/runs

curl -X POST http://127.0.0.1:8080/v1/automations/aut_01950000-0000-7000-8000-000000000004/runs \
  -H 'content-type: application/json' -d '{}'

curl -X POST http://127.0.0.1:8080/v1/automations/aut_01950000-0000-7000-8000-000000000004/runs \
  -H 'content-type: application/json' -d '{"bypass_conditions":false}'
```

If Conditions are false or unknown, the request commits a manual Skip and then
returns HTTP `409` with that Skip's history reference:

```json
{
  "type": "about:blank",
  "title": "Conflict",
  "status": 409,
  "detail": "automation conditions prevented manual admission",
  "code": "conditions_false",
  "history_id": "ask_01950000-0000-7000-8000-000000000006",
  "history_url": "/v1/automations/aut_01950000-0000-7000-8000-000000000004/history/ask_01950000-0000-7000-8000-000000000006"
}
```

Fetching `history_url` returns the committed Skip, whose `source` is `manual`, has
no Fact, and has an empty `matched_triggers` list. `code` is `conditions_false` or
`conditions_unknown`. A manual Skip never creates a Run, Step, or Command
identity, and every completed blocked request creates a distinct Skip. The
endpoint has no manual idempotency key, so reconcile an ambiguous manual POST
through history and never repeat it automatically.

### Explicit bypass

An explicit bypass records operator intent and reads no State for evaluation:

```sh
curl -i -X POST http://127.0.0.1:8080/v1/automations/aut_01950000-0000-7000-8000-000000000004/runs \
  -H 'content-type: application/json' -d '{"bypass_conditions":true}'
```

A successful admission returns the usual `202` with a Run-history `Location`. The
Run's `condition_decision` records `"mode": "bypassed"`, `"bypass_requested":
true`, and the definition snapshot, with no evaluation. Bypass affects **only**
Conditions: admission gates, the busy check, save-time definition integrity, and
execution-time Command validation all still apply, and the Run's Commands must
still satisfy normally. When a definition has no Conditions, a requested bypass is
recorded but the decision is classified `not_configured`, not a fabricated
evaluation.

Bodies with unknown members, a JSON `null`, a non-boolean `bypass_conditions`, a
non-object value, or trailing JSON are rejected before admission with no writes.
The body is limited to 1 KiB.

## Timing and limits

The batch State read is coherent across every requested Entity, but it precedes
the admission transaction. Conditions were checked once for the committed
admission; State may change between that read, admission, and any Command, and no
lock or physical-state guarantee extends into execution. History records what was
evaluated, not State at the Fact's time and not a promise that Conditions still
hold while Steps run.

To keep module ownership and avoid nested reads, Hearth reads the batch outside
the automation transaction and requires complete coverage before writing. If a
newly eligible requirement is not covered, admission writes nothing, requests one
bounded complete re-snapshot, and retries with a fresh decision time. An admission
performs at most two batch reads and three attempts inside a two-second budget.
Coverage that never stabilizes returns a safe HTTP `503` with code
`condition_snapshot_unavailable`, and no fabricated Skip. Ordinary storage
failures keep the existing safe `500` mapping.

## Related documentation

- [Automation Conditions specification](../specs/automation-conditions.md)
- [ADR 0022: Evaluate Conditions from coherent State snapshots](adr/0022-evaluate-conditions-from-state-snapshots.md)
- [Fact-driven automations specification](../specs/automations.md)
- [Canonical project language](../CONTEXT.md)
- [README](../README.md)
