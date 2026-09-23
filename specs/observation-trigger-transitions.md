# Observation Trigger transitions

**Status:** Implementation-ready; approved for task breakdown on 2026-09-22.
**Extends:** [Fact-driven automations](automations.md) and [Automation conditions](automation-conditions.md).
**Decision:** [ADR 0024](../docs/adr/0024-carry-previous-state-on-observation-facts.md).
**Effort:** XL; Devices, Automations, persistence, API, and documentation.

## 1. Problem statement

Observation Triggers compare only the accepted incoming value, so repeated reports can match the same threshold and cannot distinguish `off → on` from another report of `on`. Devices already reads the preceding State to classify each Observation, but discards that value before creating its Fact. Admission-time State can have advanced by the time Automations receives the Fact.

## 2. Proposed solution

Devices captures an owned copy of the normalized State value immediately preceding an Observation's acceptance as optional `data.previous_value` on its Fact. Absence means no previous State; present JSON `null` is a real predecessor. The additive version 1 field preserves evidence through delivery lag, redelivery, and restart while accepting queued older Facts.

Observation Triggers add `previous_comparisons` alongside existing `comparisons`. Both groups use the same comparison rules; each matches its corresponding value on the same Fact.

Matched Runs and Skips retain the predecessor in their Fact summaries as optional `previous_state_value`.

## 3. Behavior

### Predecessor identity

`previous_value` is the normalized value of the State row read by the same serializable Devices projection transaction immediately before it applies the accepted Observation. It is not a physical-change or Command-outcome claim.

The predecessor is present for `applied` and `unchanged` accepted Observations when State existed. The Entity type catalog's State equality classifies `unchanged`; Trigger comparisons still use their existing JSON comparison semantics. A first accepted Observation has no predecessor.

Rejected and duplicate Observations still create no Fact. Duplicate Fact delivery carries the originally committed predecessor and remains governed by the existing `(fact_id, automation_id)` receipt.

### Trigger matching

An Observation Trigger matches one Observation Fact when all of these hold:

1. `entity_id` equals the Fact Entity;
2. `dispositions` contains the Fact disposition;
3. every `previous_comparisons` entry matches `data.previous_value`; and
4. every existing `comparisons` entry matches `data.value`.

`previous_comparisons` and `comparisons` are independently optional arrays of zero to eight entries. Omitting a group is equivalent to an empty array. Either, both, or neither may be populated. There is no implicit inequality requirement: an `unchanged` Fact can match when its disposition is allowed and both groups match.

Previous comparisons reuse RFC 6901 pointers, `eq`, `ne`, `lt`, `lte`, `gt`, `gte`, normalized operands, exact numeric comparison, and JSON equality. With nonempty previous comparisons, a missing predecessor makes the Trigger a non-match. A missing pointer, invalid runtime array index, or incompatible runtime type also makes a comparison false, including for `ne`. These cases do not produce an admission error or an unknown result.

Crossings require no special operator:

```json
{
  "kind": "observation",
  "entity_id": "ent_temperature",
  "dispositions": ["applied"],
  "previous_comparisons": [
    {"value_pointer": "", "operator": "lte", "operand": 25}
  ],
  "comparisons": [
    {"value_pointer": "", "operator": "gt", "operand": 25}
  ]
}
```

Reversing the operators expresses a downward crossing. Multiple comparisons on either side express ranges.

### Definition validation

Each previous comparison receives the same structural validation as a current comparison:

- pointer length at most 256 UTF-8 bytes and valid RFC 6901 escaping;
- canonical array index syntax;
- closed operator set;
- one valid normalized JSON operand;
- ordering operators require numeric operands; and
- definitions remain within the existing normalized 64 KiB limit.

Previous pointers receive structural validation only at save time; current State is not necessarily the predecessor of a future Observation. Existing save-time current-comparison validation remains unchanged.

### Admission and races

Matching precedes admission, which retains receipt, stale Fact, busy Automation, then Conditions precedence. Disabled and unmatched Automations write no history or receipt. Every matched Run or Skip (`stale_fact`, `automation_busy`, `conditions_false`, `conditions_unknown`) retains the same available Fact summary values.

Definition replacement retains existing compare-and-swap and evaluation semantics. A Fact evaluated after replacement uses the current definition and the immutable previous/current values carried by that Fact. A receipt suppresses reevaluation after a prior matched outcome. An earlier unmatched evaluation still writes no receipt, so redelivery may match a replacement definition exactly as it can today.

Automatic admission remains one Automation-owned transaction across all matching Automations. Conditions use their coherent current-State snapshot, distinct from the Fact's projection-time predecessor. Matching reads no Device Fact value from Devices during admission.

### Restart and compatibility

New producers include `data.previous_value` whenever predecessor State existed. Queued version 1 Facts without it still match definitions with no previous comparisons, but cannot match nonempty `previous_comparisons`.

The durable outbox stores the predecessor in the Fact created in the projection transaction. Relay retry, JetStream redelivery, and Core restart reuse it. Existing Run interruption, non-replay, freshness, and history retention rules apply.

### Immutable evidence

For an Observation Fact summary:

- `observation_value` remains required and stores the incoming accepted value;
- `previous_state_value` is omitted when no predecessor existed;
- `previous_state_value: null` represents a real predecessor whose normalized value was JSON null; and
- both values are owned copies and survive Observation, Fact, State, definition, and ordinary Automation-history changes according to existing history retention.

Entity Event Fact summaries and manual outcomes have neither value. Evidence records values used for matching but does not add per-comparison result rows; matched Trigger snapshots already preserve the comparison definitions.

## 4. Types and storage

### Domain types

```diff
diff --git a/internal/modules/devices/device_facts.go b/internal/modules/devices/device_facts.go
@@
 type ObservationFact struct {
     Value         Value
+    PreviousValue Value // nil means no preceding State; []byte("null") is a real JSON null
 }
```

The Devices projection must deep-copy the previous State value before the State upsert can replace it. Ownership follows existing `Value` copy rules.

```diff
diff --git a/internal/modules/automations/model.go b/internal/modules/automations/model.go
@@
 type ObservationTrigger struct {
     EntityID            devices.EntityID
     Dispositions        []devices.ObservationDisposition
+    PreviousComparisons []ObservationComparison // 0–8, evaluated against PreviousValue
     Comparisons         []ObservationComparison // 0–8, evaluated against Value
 }
@@
 type ObservationFact struct {
     Value         devices.Value
+    PreviousValue devices.Value
 }
@@
 type DeviceFactSummary struct {
     ObservationValue   devices.Value
+    PreviousStateValue devices.Value
 }
```

Normalization, snapshots, API conversion, and repository conversion must deep-copy both slices. Typed Go input validation must reject more than eight comparisons in either group and preserve nil versus JSON null.

### Wire contract

`contracts/v1/observation-fact.schema.json` adds an optional unrestricted JSON property:

```diff
@@ data.properties
     "value": true,
+    "previous_value": true,
```

It is deliberately absent from `required`. Devices NATS DTOs use `json.RawMessage` or equivalent presence-preserving representation with `omitempty`; decoders must distinguish absent bytes from bytes containing `null`. Unknown fields remain rejected.

### Definition and HTTP DTOs

The strict definition schema and request/response DTOs add:

```go
PreviousComparisons []ObservationComparisonBody `json:"previous_comparisons,omitempty"`
```

Its item schema is identical to `comparisons`, including canonical `value_pointer` and the deprecated accepted `pointer` alias. Encoding always emits canonical `value_pointer`. OpenAPI advertises independent `maxItems: 8` limits.

Fact-summary responses add:

```go
PreviousStateValue json.RawMessage `json:"previous_state_value,omitempty"`
```

The field is present with raw bytes `null` for JSON null and omitted only when predecessor evidence is absent. Existing `observation_value` is unchanged.

### Persistence

Migration `00006_automation_history_previous_state_value.sql` adds:

```sql
ALTER TABLE automation_history
ADD COLUMN fact_previous_value_json TEXT
CHECK (fact_previous_value_json IS NULL OR json_valid(fact_previous_value_json));
```

The column is nullable for all existing rows, Entity Event outcomes, manual outcomes, first-State Observation outcomes, and legacy Facts. It may contain the text `null`. Update history insert/select SQL and regenerate SQLC. Do not rewrite historical rows or infer predecessor values.

The Devices outbox/pending-Fact persistence shape must also retain optional `previous_value` atomically with the accepted Observation. Use the existing outbox representation and migration conventions; do not query Observation history during relay.

## 5. Implementation and acceptance

Devices owns projection, outbox persistence, and NATS encoding in `internal/modules/devices/{sqlite,nats}` and `contracts/v1/observation-fact.schema.json`. Automations owns definition/schema, matching, NATS decoding, history persistence, and API/MCP mapping in `internal/modules/automations/{sqlite,nats,api}`. `internal/modules/devices/automation_validation.go` validates previous comparison structure. Update the linked domain and operator docs, regenerate SQLC, and inspect runtime OpenAPI; the repository does not check in OpenAPI artifacts. No new cross-module service interface, HTTP route, NATS subject or stream, consumer, receipt key, admission repository signature, or Condition snapshot interface is needed. Reuse `MatchObservationComparison` for both groups.

Verify the following with tests beside the owning behavior:

- [ ] Devices SQLite projection/outbox tests cover first State, `applied`, `unchanged`, JSON null, duplicates/rejections, and an immutable predecessor across relay retry and restart. Producer/consumer mapping and strict v1 schema fixtures cover absent versus null, exact JSON, and unknown-field rejection.
- [ ] Definition/schema tests cover independent eight-entry limits (a ninth fails), pointers, operators, operands, the `pointer` alias and canonical `value_pointer` encoding, normalized 64 KiB bounds, strict unknown fields, and mutation-after-normalization ownership. Existing definitions and queued Facts without the new fields keep their current behavior. Previous pointers receive structural-only save-time validation.
- [ ] Matcher tables cover `off → on`, `on → off`, both directions of `≤25`/`>25` crossing, ranges on either side, one-sided and empty comparison groups, `unchanged` disposition eligibility, and ordinary non-matches for absent evidence, missing pointers, invalid indices, or incompatible types including `ne`. Unmatched Facts create no history or receipt.
- [ ] Automation SQLite tests read retained Run and stale/busy/Condition Skip evidence, distinguish absence from JSON null, and confirm evidence survives source pruning and definition replacement. Test migration up/down and decoding of old rows, Entity Event outcomes, and manual outcomes. Preserve receipts, one-active-Run enforcement, all-Automation atomic admission, and history retention.
- [ ] Race tests admit a Fact after newer State exists and redeliver across a definition replacement. The Fact uses its committed predecessor; matched receipts prevent reevaluation while previously unmatched Facts may match the new definition.
- [ ] HTTP create/get/replace and history tests cover canonical field round trips and strict validation. Inspect runtime OpenAPI for both independent limits and optional evidence; MCP output tests preserve exact JSON numbers and omitted-versus-null predecessors.
- [ ] Run `mise run validate` and review generated, formatted, and module-metadata changes.

Deploy the v1 producer and strict consumer together: an older strict consumer rejects new Facts with `previous_value` during rollback. The field stays optional for new consumers reading older queued Facts. Existing State bounds and history retention constrain the additional Fact and history payload size.
