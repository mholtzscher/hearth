# Feature: Zigbee2MQTT bulb attributes (linkquality, startup temp, power-on behavior, effect)

**Status:** DRAFT — pending user approval. No implementation authorized until G1.
**Effort:** XL (D1–D9 non-live implementation; D10 live validation separately authorized). The critical path is XL even though independent adapter and core work can overlap after D3.

### Problem Statement

**Who:** Owners of the Third Reality `3RCB01057Z` office-table-lamp (and future Z2M bulbs with the same exposes) who inspect/control bulbs through Hearth.
**What:** Four Zigbee2MQTT exposes have no Hearth entity: `linkquality` (device-root numeric, access 1), `color_temp_startup` (nested `light`-feature numeric, access 7), `power_on_behavior` (device-root enum, access 7), `effect` (device-root enum, access exactly 2, set-only).
**Why it matters:** Without them the lamp's signal health is invisible, startup temperature and power-restore behavior cannot be set, and effects cannot be triggered — all inspect/control operations, no automation.
**Evidence:** Read-only retained Wanda MQTT `bridge/devices` inventory for `office-table-lamp` (software `1.00.74`): user state `brightness 20`, `color_temp 298`, `linkquality 18`, `power_on_behavior previous`, `state ON`. Expose placement/access/bounds/`values`/`presets` (including the `previous` ↔ wire-65535 metadata mapping) are real fetched metadata. The on-wire startup state value and effect dispatch outcome are **not** captured (§Gates D10). `color_temp_startup` and `effect` were absent from live state. `start_bind` is out of scope.

### Proposed Solution

Add four entity types behind one startup `set` API, reusing existing lifecycles. Hybrid mapping: `linkquality` → generic read-only number sensor `hearth.numericsensor/v1` (fractional-capable; the linkquality entity carries 0–255 bounds and unit `lqi`, with integer-only adapter decoding); diagnostic is its purpose, not a distinct capability — no diagnostic metadata framework. `power_on_behavior` → generic observable `hearth.enumsetting/v1` (dynamic choices); `color_temp_startup` → generic observable `hearth.numericsetting/v1` (bounded number or dynamic named choice; wire 65535 never public); `effect` → generic stateless `hearth.enumaction/v1` (`trigger`, terminal `dispatched`, explicitly unverified). No all-exposes discovery engine: the adapter maps only these four named exposes with exact-match eligibility; malformed siblings are omitted without suppressing valid ones (existing `UnmarshalJSON` sibling isolation in `discovery_wire.go`).

Two small catalog-driven extensions carry the semantics with no literal type-ID branching anywhere in runtime code: manifest `stateless` flag (observation policy) and per-operation `outcome` (`observed` vs `dispatched`). `outcome` is REQUIRED on every operation in every manifest (existing seven gain `"outcome": "observed"`; no implicit default). The NATS acceptance envelope and subjects are unchanged; only hand-written HTTP/TS render the new terminal `dispatched` label.

### Scope & Deliverables

Dependency-correct order. D1 changes the manifest schema, parser model, and existing manifests together, so strict manifest decoding and generation remain green. D2 changes the catalog interface and all generated callers together. Critical path: D1 → D2 → D3 → D4 → D6 → D7. D5 parallels D4 after D3. D8 follows D5–D7; D9 follows D4–D8. D10 is separately authorized and blocks only real-hardware acceptance, not D1.

| Deliverable | Effort | Depends On | Content |
|-------------|--------|------------|---------|
| D1 manifest contract + parser model | M | — | `entitytype-manifest.schema.json` and `main.go` manifest/model structs accept: `op` += `in`, `in_if_present`, `eq_optional`, `gte_if_present`, `lte_if_present`; operations gain REQUIRED `outcome`; optional `stateless` (default false); optional `support_validation` (roots restricted to `support`); `satisfied_when` `minItems` removed so dispatched declares `[]`. Existing manifests add explicit `"outcome": "observed"`. Parser tests prove strict decoding/generation stays green; behavior compilation/rendering waits for D2. |
| D2 generator + catalog policy interface | L | D1 | `internal/cmd/entitytypegen/`: parse/validate the new manifest fields, compile membership and optional-leaf rules, emit dispatched/no-predicate operations and `ValidateSupport`, and extend examples/conformance so dispatched operations declare no outcome examples or satisfaction callback while observed operations retain positive/negative outcome coverage; stateless SDK facades omit observation constructors. Bind `type: number` state/support scalars to Go `float64` (`ruleOperand` already emits `float64(...)` comparisons, `schemaComparable` already covers `number`) and revise `TestTypeEmitterRejectsLossyNumberBindings` to the new binding. Change `catalog.go` and all generated callers together: `OutcomeKind`, `stateless`, `IsStateless`, and `DefineOperation(outcome)`. Regenerate existing entity types before validation. |
| D3 four entity types | M | D2 | `numericsensorv1`, `enumsettingv1`, `numericsettingv1`, `enumactionv1` schemas/manifests/examples + `mise run generate`; catalog registration |
| D4 persistence + lifecycle | L | D3 | Migration CHECK + `commands.sql`/sqlc + `validCommandCompletion`/`sameCompletion` + `CommandResult` constructor (§Types/§Interfaces); stateless rejection in `classifyObservation`; dispatched terminal path reusing `runCommand` lifecycle |
| D5 adapter settings (3 exposes) | M | D3 | `discovery_wire.go` values/presets parsing; linkquality/startup/power discovery, decoders, translators; `planner_light.go` optional-sibling composition |
| D6 action runtime (effect) | M | D4, D5 | Effect plan + adapter dispatched path (no `/get`, no matcher, no observation; FIFO release on accept) |
| D7 API + UI | M | D5, D6 | HTTP dispatched rendering/omission (§Interfaces); web dispatched-vs-satisfied views |
| D8 tests + fixtures | M | D5–D7 | Unit/fixture tests (§Test Strategy); sanitized Wanda-shape inventory fixture; `proof_flow_captures` linkquality assertions; stale `testdata/bridge-devices-3rcb01057z.json` kept only if regression-useful |
| D9 docs + ADR | S | D4–D8 | `README.md`, `specs/generated-entity-type-behavior.md`, `specs/zigbee2mqtt-adapter.md`, unnumbered draft ADR under `docs/adr/` |
| D10 live validation (separately authorized) | S | D1–D9 | Physical startup-65535 + effect-dispatch capture/report (§Gates G2) |

### Non-Goals

- No automation, no `start_bind`, no other exposes, no generic all-exposes discovery engine.
- No backward-compat shims; unreleased breaking changes move all in-repo callers together.
- No NATS schema/subject changes; no accepted-envelope change; no generated HTTP schema (none exists).
- No application-level retry; no exactly-once claim; no MQTT-ack-proves-device-action claim.
- No per-field approvals: single whole-draft approval (G1).

### Types

New code follows existing generated conventions (`entitytypes/<name>v1/`: `entitytype.json`, `state.schema.json`, `support.schema.json`, `set/trigger-parameters.schema.json`, `examples.json`). Schemas authoritative; generated Go never hand-edited. All objects closed (`additionalProperties: false`). Deadlines 10000 ms (colortemp/power precedent). Flat schema roots throughout (root-level `oneOf` unsupported by `define`/`propertyType`/`scalarKind`).

`hearth.numericsensor/v1` (generic read-only number sensor; linkquality is its first diagnostic use, not a distinct capability — no diagnostic flag in manifest, support, or catalog). State schema is unbounded `type: number`, bound to Go `float64` (new D2 binding; `ruleOperand` already emits `float64(...)` comparisons and `schemaComparable` already treats `number` as comparable, so `eq`/`gte`/`lte` need no DSL change). Binary64 precision: integers through 2^53 exact (linkquality 0–255 always exact); state and support values use normal float64 rounding. Generated validation and state equality operate on decoded binary64 values, not exact decimal wire values. Overflow fails codec decode; underflow may round to zero. Distinct wire values may compare equal after rounding, including bounds that differ beyond binary64 precision; inverted-bound rejection applies to decoded bounds. No schema-level envelope; support narrows per entity. `state_validation` is plain `gte`/`lte` of state against `support/state/minimum`/`maximum` (number–number, matching kinds); `support_validation` is `gte support/state/maximum support/state/minimum`. Linkquality support is `{"state": {"minimum": 0, "maximum": 255, "unit": "lqi"}}` with exact-integer 0/255 on the wire. Integer-only for linkquality is adapter-enforced (decode admits exact integers and rejects fractions as per-property issues, §Interfaces); core backstops range, not integrality — the DSL has no conditional validation, so no new op. `operations: {}` read-only, nil translator (temperature precedent).

```go
type State float64 // schema number; integers through 2^53 exact
type StateSupport struct {
    Minimum float64 `json:"minimum"`
    Maximum float64 `json:"maximum"`
    Unit    string  `json:"unit"` // minLength 1, maxLength 32
}
type Support struct {
    State      StateSupport     `json:"state"`
    Operations OperationSupport `json:"operations"` // empty object, no operations
}
```

`hearth.enumsetting/v1` (generic observable enum; power_on_behavior). String/choice bounds (1–128 / 1–64, unique) mirror `contracts/v1/common.schema.json` and registration-request bounds. Choices dynamic from expose `values`, never hard-coded. State and `set` validation via `in`; satisfaction via `eq`.

```go
type State string // minLength 1, maxLength 128
type StateSupport struct {
    Choices []string `json:"choices"` // minItems 1, uniqueItems, maxItems 64
}
type SetParameters struct {
    Value string `json:"value"` // minLength 1, maxLength 128
}
type SetSupport struct{}
```

`hearth.numericsetting/v1` (generic observable numeric setting with named alternatives). This is not color-temperature-specific: Zigbee2MQTT uses the same bounded-number-or-special-choice shape for `current_level_startup` (`minimum`/`previous`), `on_level` (`previous`), and on/off transition times (`disabled`). Those exposes remain out of scope; only `color_temp_startup` is allowlisted here. `mode` discriminates a numeric `value` from a named `choice`; `oneOf` requires exactly the corresponding field. State and `set` parameters share this shape. Number values and support bounds use `float64`, allowing future fractional settings; an adapter may enforce a narrower integer wire contract. Choices are dynamic per entity and may be empty for an ordinary numeric setting; unit is optional for dimensionless settings. Support for this bulb is `{"state":{"minimum":142,"maximum":454,"unit":"mired","choices":["previous"]},"operations":{"set":{}}}`. Numeric bounds are validated by `gte_if_present`/`lte_if_present`, named choices by `in_if_present`, and satisfaction by `eq` on required `mode` plus `eq_optional` on both optional payload fields. Adapter-local wire mapping only: when expose metadata advertises `previous`↔65535, `65535` ↔ `{"mode":"choice","choice":"previous"}`; otherwise an in-range integer N ↔ `{"mode":"value","value":N}`. An expose with no `previous` preset remains eligible with empty choices, while an advertised but invalid or duplicate `previous` mapping omits the feature (§Interfaces). In-range presets such as `warm` and `cool` are aliases for numbers, not distinct state; this adapter does not advertise those names as choices, while their target numbers remain available through the ordinary numeric range.

```json
{"type": "object", "additionalProperties": false,
 "required": ["mode"],
 "properties": {
   "mode": {"type": "string", "enum": ["value", "choice"]},
   "value": {"type": "number"},
   "choice": {"type": "string", "minLength": 1, "maxLength": 128}},
 "oneOf": [
   {"required": ["value"], "not": {"required": ["choice"]},
    "properties": {"mode": {"const": "value"}}},
   {"required": ["choice"], "not": {"required": ["value"]},
    "properties": {"mode": {"const": "choice"}}}]}
```

```go
type StateSupport struct {
    Minimum float64  `json:"minimum"`
    Maximum float64  `json:"maximum"`
    Unit    *string  `json:"unit,omitempty"` // when present: minLength 1, maxLength 32
    Choices []string `json:"choices"`        // items 1–128 chars; uniqueItems, maxItems 64; may be empty
}
type SetSupport struct{}
```

`hearth.enumaction/v1` (generic stateless enum action; effects). Not an enum setting: a setting holds a persistent choice completed by fresh matching observation; an action requests an effect with no reported state, completed as unverified dispatch. One merged enum-control type would need per-entity statefulness/outcome policies instead of type-level catalog policy; two small generic types avoid that variability and avoid pretending an effect has state. `Values` dynamic from expose `values`; parameter validation reuses `in`. State schema is the closed empty object (accepted by `requireClosedObject`, temperature/power precedent; codegen has no `null` support). Operation `trigger`, deadline 10000 ms, `"outcome": "dispatched"`, `satisfied_when: []`. The `trigger` operation carries no conformance outcome examples and emits no satisfaction predicate (`satisfied_when: []` → nil matcher); the generated stateless facade omits observation constructors. Statelessness explicit via manifest `"stateless": true` (default false); the adapter registers no decoder and claims no state properties.

```go
type TriggerParameters struct {
    Name string `json:"name"` // minLength 1, maxLength 128
}
type TriggerSupport struct {
    Values []string `json:"values"` // minItems 1, uniqueItems, maxItems 64
}
```

DSL semantics (exact). Existing `eq`/`gte`/`lte` unchanged; no general expression language:

- `in`: left required scalar string ∈ right required string array. Right must have `minItems ≥ 1` + `uniqueItems`, checked at compile time from its schema; codegen emits a membership loop.
- `in_if_present`: LEFT must end in an optional string LEAF (intermediates required); RIGHT must be a required string array whose item schema matches the left string bounds, has `uniqueItems`, and may be empty. It passes when the left leaf is absent and otherwise requires membership. `numericsetting` uses it for optional `choice`; schema discrimination guarantees `choice` is present exactly in choice mode.
- `eq_optional`: true iff both sides absent, or both present and equal; false on one-sided absence or present-but-unequal. Absence participates (no skip-when-absent). `numericsetting` satisfaction uses required `mode` equality plus optional `value` and `choice` equality (either side may be the optional leaf; intermediate segments required).
- `gte_if_present`/`lte_if_present`: LEFT must end in an optional numeric LEAF (intermediates required); RIGHT fully required; vacuous pass when the leaf is absent. `null` never reaches rules (schemas admit no null; codec decode rejects it first). The `compileReferencePath` "traverses optional property" rejection in `behavior.go` stays default for all other ops/segments.
- `support_validation`: rule array with roots restricted to `{support}`. `loadModel` compiles it; codegen emits `ValidateSupport(support Support) error` into `zz_generated_behavior.go`; `EntityTypeDefinition.normalizeSupport` invokes it after codec decode, covering registration (`normalizeRegistration` → `NormalizeSupport`) and the command path (`ResolveCommand` re-decodes support). Example: `gte support/state/maximum support/state/minimum`, so `minimum > maximum` and duplicated choices fail at core registration from any adapter; an empty `numericsetting` choices array is valid.
- Numbers: no change to existing numeric comparisons. `eq`/`gte`/`lte` already accept matching-kind `number` operands (`compileEqualityRule`/`compileOrderedRule`); codegen already emits `float64(...)` comparisons (`ruleOperand`) and `==` equality (`schemaComparable`). `numericsensor` uses plain `gte`/`lte`; optional `numericsetting.value` uses the new present-guarded variants. Per-entity integer-only is NOT a DSL rule — the DSL has no conditional validation, so linkquality and this bulb's startup temperature enforce integrality in adapter decoding while core backstops range and choice membership (§Interfaces).

```diff
diff --git a/internal/modules/devices/model.go b/internal/modules/devices/model.go
@@
 type CommandRecord struct {
 	OutcomeObservationID *ObservationID
 	FailureCode          *CommandFailureCode
 }
+
+type OutcomeKind string
+const (
+	OutcomeObserved   OutcomeKind = "observed"
+	OutcomeDispatched OutcomeKind = "dispatched"
+)
 
 type CommandCompletion struct {
 	ID          CommandID
```

```diff
diff --git a/internal/modules/devices/model.go b/internal/modules/devices/model.go
@@
 type CommandResult struct {
-	CommandID     CommandID
-	ObservationID ObservationID
-	Value         Value
+	CommandID     CommandID
+	Outcome       OutcomeKind
+	ObservationID *ObservationID // non-nil iff Outcome is observed
+	Value         *Value         // non-nil iff Outcome is observed (Value is json.RawMessage)
 }
+// Invariant (enforced by constructor NewCommandResult at both completion
+// sites): observed requires both pointers non-nil; dispatched requires both
+// nil; any other outcome value rejected.
```

```diff
diff --git a/internal/modules/devices/catalog.go b/internal/modules/devices/catalog.go
@@
 type EntityTypeDefinition struct {
 	id               EntityTypeID
+	stateless        bool // from manifest "stateless" (default false)
 	normalizeSupport func(EntitySupport) (EntitySupport, error)
 	normalizeState   func(EntitySupport, Value) (Value, error)
 	equalState       func(EntitySupport, Value, Value) (bool, error)
 	operations       map[OperationName]erasedOperationDefinition
 }
+
 type erasedOperationDefinition struct {
 	resolve   func(EntitySupport, CommandParameters) (CommandParameters, error)
 	deadline  time.Duration
-	satisfies func(CommandParameters, Value) (bool, error)
+	outcome   OutcomeKind
+	satisfies func(CommandParameters, Value) (bool, error) // nil iff dispatched
 }
```

```diff
diff --git a/internal/platform/db/migrations/00001_initial.sql b/internal/platform/db/migrations/00001_initial.sql
@@
     status                 TEXT NOT NULL CHECK (
         status IN (
-            'requested', 'accepted', 'satisfied', 'rejected',
+            'requested', 'accepted', 'satisfied', 'dispatched', 'rejected',
             'adapter_unhealthy', 'entity_unavailable', 'outcome_timeout',
             'entity_disabled', 'internal_failure', 'interrupted'
         )
@@
     CHECK (
-        (status IN ('requested', 'accepted', 'satisfied') AND failure_code IS NULL)
-        OR (status = 'rejected' AND failure_code = 'upstream_rejected')
-        OR (status = 'adapter_unhealthy' AND failure_code = 'adapter_unhealthy')
-        OR (status = 'entity_unavailable' AND failure_code = 'entity_unavailable')
-        OR (status = 'outcome_timeout' AND failure_code = 'outcome_timeout')
-        OR (status = 'entity_disabled' AND failure_code = 'entity_disabled')
-        OR (status = 'internal_failure' AND failure_code = 'internal_error')
-        OR (status = 'interrupted' AND failure_code = 'core_restarted')
+        (status IN ('requested', 'accepted', 'satisfied', 'dispatched') AND failure_code IS NULL)
+        OR (
+            failure_code IS NOT NULL AND (
+                (status = 'rejected' AND failure_code = 'upstream_rejected')
+                OR (status = 'adapter_unhealthy' AND failure_code = 'adapter_unhealthy')
+                OR (status = 'entity_unavailable' AND failure_code = 'entity_unavailable')
+                OR (status = 'outcome_timeout' AND failure_code = 'outcome_timeout')
+                OR (status = 'entity_disabled' AND failure_code = 'entity_disabled')
+                OR (status = 'internal_failure' AND failure_code = 'internal_error')
+                OR (status = 'interrupted' AND failure_code = 'core_restarted')
+            )
+        )
     )
```

The explicit `failure_code IS NOT NULL` guard is required because SQLite
accepts CHECK expressions that evaluate to NULL. The existing completed-at
CHECK already requires every status other than `requested`/`accepted`,
including `dispatched`, to have `completed_at`. The existing
outcome-observation CHECK already requires every status other than
`satisfied`, including `dispatched`, to have a NULL observation ID.

Schema update is a pre-deployment edit of the initial migration (no compatibility migration; no production data to migrate). `dbqueries/commands.sql` `CompleteCommand` stays generic (`status/completed_at/failure_code`). Its repository call must encode an empty `CommandCompletion.FailureCode` as SQL NULL rather than the current valid empty string:

```diff
diff --git a/internal/modules/devices/sqlite_repository.go b/internal/modules/devices/sqlite_repository.go
@@
 	rows, err := queries.CompleteCommand(ctx, dbsqlc.CompleteCommandParams{
 		Status:      string(completion.Status),
 		CompletedAt: sql.NullString{String: formatTime(completion.CompletedAt), Valid: true},
-		FailureCode: sql.NullString{String: string(completion.FailureCode), Valid: true},
+		FailureCode: nullableCompletionFailure(completion.FailureCode),
 		ID:          string(completion.ID),
 	})
```

`nullableCompletionFailure` is a new value-form helper: it returns an
invalid `sql.NullString` for the empty dispatched code and a valid string
for non-empty failure codes. Keep the existing
`nullableCommandFailure(*CommandFailureCode)` unchanged for command-record
creation.

```go
func nullableCompletionFailure(value CommandFailureCode) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(value), Valid: true}
}
```

Generated `dbsqlc/` is regenerated, never hand-edited.
`sqlite_repository.go` also validates and compares the new completion:

```diff
diff --git a/internal/modules/devices/sqlite_repository.go b/internal/modules/devices/sqlite_repository.go
@@
 func validCommandCompletion(completion CommandCompletion) bool {
+	if completion.Status == CommandStatusDispatched {
+		return !completion.CompletedAt.IsZero() && completion.FailureCode == ""
+	}
 	expected := map[CommandStatus]CommandFailureCode{ //nolint:exhaustive // Only terminal failure statuses have failure codes.
 		CommandStatusRejected:          CommandFailureUpstreamRejected,
```

```diff
diff --git a/internal/modules/devices/sqlite_repository.go b/internal/modules/devices/sqlite_repository.go
@@
 func sameCompletion(command CommandRecord, completion CommandCompletion) bool {
-	return command.Status == completion.Status && command.FailureCode != nil &&
-		*command.FailureCode == completion.FailureCode && command.CompletedAt != nil &&
+	return command.Status == completion.Status && commandFailureMatches(command, completion) &&
+		command.CompletedAt != nil &&
 		command.CompletedAt.Equal(completion.CompletedAt)
 }
+// commandFailureMatches treats dispatched (and any outcome with empty
+// FailureCode) as matching only when the record has no failure code.
```

### Interfaces

Catalog surface: `DefineOperation` takes an `outcome OutcomeKind` parameter; nil matcher accepted iff outcome is dispatched; `Satisfies` errors on dispatched operations (defensive; the dispatched path never calls it). `IsStateless(typeID)` exposes the manifest flag for the repository seam. `DefineEntityType` gains a generated `validateSupport func(Support) error` argument; its `normalizeSupport` closure decodes and invokes that function. `TypeCatalog.ResolveCommand` calls `normalizeSupport(entity.Support)` before operation resolution, so registration and command execution enforce the same support rules. No caller branches on literal type-ID strings.

The generated support validator enters through the existing entity-type constructor:

```diff
diff --git a/internal/modules/devices/catalog.go b/internal/modules/devices/catalog.go
@@
 func DefineEntityType[State, Support any](
 	id EntityTypeID,
 	state *entitytypes.JSONCodec[State],
 	support *entitytypes.JSONCodec[Support],
+	validateSupport func(Support) error,
 	validateSupportedState func(Support, State) error,
@@
 	definition.normalizeSupport = func(raw EntitySupport) (EntitySupport, error) {
-		_, normalized, err := support.Decode(json.RawMessage(raw))
+		typed, normalized, err := support.Decode(json.RawMessage(raw))
 		if err != nil {
 			return nil, fmt.Errorf("invalid support for entity type %q: %w", id, err)
 		}
+		if err := validateSupport(typed); err != nil {
+			return nil, fmt.Errorf("unsupported support for entity type %q: %w", id, err)
+		}
 		return EntitySupport(normalized), nil
 	}
```

The constructor rejects nil `validateSupport`; codegen emits a no-op
validator for manifests with no `support_validation` rules. Existing
callers are regenerated in D2.

`ResolvedCommand` carries the operation policy into execution; it is not persisted as a second source of truth:

```diff
diff --git a/internal/modules/devices/catalog.go b/internal/modules/devices/catalog.go
@@
 type ResolvedCommand struct {
 	Parameters CommandParameters
 	Deadline   time.Duration
+	Outcome    OutcomeKind
 }
@@
-	return ResolvedCommand{Parameters: normalized, Deadline: operation.deadline}, nil
+	return ResolvedCommand{Parameters: normalized, Deadline: operation.deadline, Outcome: operation.outcome}, nil
```

`ExecuteCommand` passes `resolved.Outcome` to `runCommand`:

```diff
diff --git a/internal/modules/devices/command.go b/internal/modules/devices/command.go
@@
-		completed <- service.runCommand(lifecycleContext, command, waiter)
+		completed <- service.runCommand(lifecycleContext, command, resolved.Outcome, waiter)
@@
 func (service *Service) runCommand(
 	ctx context.Context,
 	command CommandRecord,
+	outcome OutcomeKind,
 	waiter <-chan CommandResult,
 ) commandOutcome {
@@
 	err = service.stores.Commands.MarkCommandAccepted(writeContext, command.ID, acceptedAt)
@@
+	if outcome == OutcomeDispatched {
+		// Persist CommandStatusDispatched with an empty failure code, then
+		// return the constructor-validated result with no observation/value.
+		return service.completeDispatchedCommand(ctx, command, waiter)
+	}
+	// OutcomeObserved alone continues into the existing waiter/timeout path.
```

`completeDispatchedCommand` is a private helper inside the existing command
lifecycle module, not a second lifecycle. It uses `persistenceContext`,
treats an idempotent same completion as success, and handles a competing
terminal completion with the existing `ErrCommandTerminal` behavior. Any
unknown outcome fails in `ResolveCommand` before command creation.

Observation classification (transactional, in `SQLiteRepository.ProjectObservation` → `classifyObservation` in `sqlite_observations.go`, never in `Service.ProjectObservation` which only validates shape and delegates). Precedence preserves all existing higher-priority checks, then stateless before codec normalize:

1. duplicate ID → `duplicate` (unchanged); 2. stale/unknown runtime → `stale_runtime`; 3. unknown entity → `unknown_entity`; 4. wrong adapter → `wrong_adapter`; 5. disabled without linked command → `entity_disabled`; **6. stateless type → `invalid_value` rejection, metadata persisted without value (existing rejected-observation shape);** 7. codec normalize + `EqualState` (unchanged).

```diff
diff --git a/internal/modules/devices/sqlite_observations.go b/internal/modules/devices/sqlite_observations.go
@@
 func (repository *SQLiteRepository) classifyObservation(
 	view EntityWithState,
 	value Value,
 	rejection *ObservationRejection,
 ) (Value, ObservationDisposition, *ObservationRejection, error) {
 	if rejection != nil {
 		return nil, DispositionRejected, rejection, nil
 	}
+	if stateless, err := repository.catalog.IsStateless(view.Entity.TypeID); err != nil {
+		return nil, "", nil, err
+	} else if stateless {
+		invalid := RejectionInvalidValue
+		return nil, DispositionRejected, &invalid, nil
+	}
 	if _, err := repository.catalog.NormalizeSupport(view.Entity.TypeID, view.Entity.Support); err != nil {
```

Command lifecycle (`command.go`, `Service.runCommand`): reuse requested → accepted → terminal; no duplicated lifecycle functions. Dispatched completes only after BOTH correlated adapter accepted response AND durable commit of the terminal record with `CommandStatusDispatched`, notifying the waiter with the dispatched constructor result. Pre-dispatch parameter errors (wrong `in`/`in_if_present` membership or out-of-range numeric-setting value) reject in `ResolveCommand` before dispatch. `contracts/v1/command-response.schema.json` unchanged (`accepted`/`rejected` report adapter acceptance, not terminal outcomes).

HTTP/TS (actual field names). `CommandResultBody` (`api/types.go`) uses `command_id`, `status`, `observation_id`, `value`; `ExecuteCommand` (`api/command.go`) currently hard-codes `"satisfied"`. Translation: `Outcome == observed` → `"satisfied"` with both fields present; `Outcome == dispatched` → `"dispatched"` with both omitted (not null). `CommandRecordBody` gains the `"dispatched"` status vocabulary; `GET /v1/entities/{id}` for a dispatched entity returns `state: null` (existing never-observed behavior).

```diff
diff --git a/internal/modules/devices/api/types.go b/internal/modules/devices/api/types.go
@@
 type CommandResultBody struct {
-	CommandID     string `json:"command_id"`
-	Status        string `json:"status"`
-	ObservationID string `json:"observation_id"`
-	Value         any    `json:"value"`
+	CommandID     string `json:"command_id"`
+	Status        string `json:"status"` // "satisfied" | "dispatched"
+	ObservationID *string `json:"observation_id,omitempty"` // present iff satisfied
+	Value         *any    `json:"value,omitempty"`          // present iff satisfied
 }
```

```diff
diff --git a/internal/modules/devices/api/command.go b/internal/modules/devices/api/command.go
@@
-	return &ExecuteCommandOutput{Body: CommandResultBody{
-		CommandID: string(result.CommandID), Status: "satisfied",
-		ObservationID: string(result.ObservationID), Value: value,
-	}}, nil
+	status := "satisfied"
+	var observationID *string
+	var bodyValue *any
+	if result.Outcome == devices.OutcomeDispatched {
+		status = "dispatched"
+	} else {
+		id := string(*result.ObservationID)
+		observationID = &id
+		bodyValue = &value
+	}
+	return &ExecuteCommandOutput{Body: CommandResultBody{
+		CommandID: string(result.CommandID), Status: status,
+		ObservationID: observationID, Value: bodyValue,
+	}}, nil
```

```ts
// web/src/api/types.ts — field names match Go tags.
export type CommandStatus =
  | "requested"
  | "accepted"
  | "satisfied"
  | "dispatched"
  | "rejected"
  | "adapter_unhealthy"
  | "entity_unavailable"
  | "outcome_timeout"
  | "entity_disabled"
  | "internal_failure"
  | "interrupted";

export interface CommandRecord {
  id: string;
  entity_id: string;
  operation: string;
  parameters: Record<string, unknown>;
  status: CommandStatus;
  requested_at: string;
  deadline_at: string;
  accepted_at?: string;
  completed_at?: string;
  outcome_observation_id?: string;
  failure_code?: string;
}

export type CommandResult =
  | { command_id: string; status: "satisfied"; observation_id: string; value: unknown }
  | { command_id: string; status: "dispatched" };
```

Adapter contract (`internal/adapters/zigbee2mqtt/`). No catalog dependency in the adapter: explicit local plan values, trusted from construction and validated at the seam. Only generated facades and the core catalog use type policy separately.

```diff
diff --git a/internal/adapters/zigbee2mqtt/entity_plan.go b/internal/adapters/zigbee2mqtt/entity_plan.go
@@
 type entityPlan struct {
 	Descriptor       adapter.EntityDescriptor
+	StatePolicy      entityStatePolicy // entityStateful (default) | entityStateless
 	StateProperties  []string
 	GetProperties    []string
 	DecodeState      stateDecoder
 	TranslateCommand commandTranslator
 }
+
+type entityStatePolicy uint8
+const (
+	entityStateful entityStatePolicy = iota
+	entityStateless
+)
```

```diff
diff --git a/internal/adapters/zigbee2mqtt/entity_plan.go b/internal/adapters/zigbee2mqtt/entity_plan.go
@@
 type plannedCommand struct {
 	SetValues     map[string]json.RawMessage
 	GetProperties []string
 	Deadline      time.Time
 	Matches       func(stateReport) bool
+	Outcome       plannedOutcome // plannedObserved (default) | plannedDispatched
 }
+
+type plannedOutcome uint8
+const (
+	plannedObserved plannedOutcome = iota
+	plannedDispatched
+)
```

Trace (no hard-coded type IDs): `entity_effect.go` builds the plan (`StatePolicy: entityStateless`, empty `StateProperties`/`GetProperties`, nil `DecodeState`, translator returning `Outcome: plannedDispatched` with `{"effect": "<name>"}` and empty refresh) → `validateEntityPlans` exempts exactly `entityStateless` plans from the state-properties/decoder/refresh invariants → `translateCommand` (`command.go`) delegates and `validatePlannedCommand` exempts exactly `plannedDispatched` from refresh/matcher requirements → `commandAttempt` (`runtime.go`) carries `matches == nil`/empty refresh → `finishSet` (`runtime_commands.go`): after `/set` PUBACK (`mqttQoS = 1`) + `Responder.Accept() (CommandEvidence, error)`, a dispatched attempt publishes no `/get`, installs no matcher, publishes no observation, releases the per-IEEE FIFO slot on accept, and `finishHandler`s nil so the core durable-dispatched commit completes the waiter.

Discovery eligibility (exact matches; `PropertyUnique` gated; `access` via `expose_access.go` bits):

- `linkquality`: device-root numeric expose, publish bit required + set bit forbidden (`access & 1 != 0 && access & 2 == 0`); plans a `hearth.numericsensor/v1` entity (diagnostic use, no diagnostic metadata); no translator; `GetProperties` carries the property only when the expose grants get access, so gettable linkquality joins startup refresh while publish-only linkquality does not (temperature precedent). Decode admits exact integers 0–255 only — fractional or out-of-range payloads are per-property `stateDecodeIssue`s with siblings intact (adapter-enforced integer constraint; core backstops range via support bounds). Support `{"state": {"minimum": 0, "maximum": 255, "unit": "lqi"}}`. Unit `""` → `"lqi"` (explicit known mapping in code) or `"lqi"` passthrough; any other unit omits the entity. Device-kind-agnostic supplement; never gates/joins the power family.
- `color_temp_startup`: nested `light`-feature numeric, `access & 7 == 7`; plans a `hearth.numericsetting/v1` entity with mired bounds. Bounds come from exact-integer `value_min`/`value_max` (142/454) within 100–1000 with `minimum < maximum`. No `previous` preset yields `Choices: []`; exactly one `previous`↔65535 preset yields `Choices: ["previous"]`; an advertised `previous` with any other value or duplicate name omits the feature. Other numeric-valued presets are aliases and do not enter `Choices`. Decode `65535` to `{"mode":"choice","choice":"previous"}` only when that choice is supported; an in-range integer N decodes to `{"mode":"value","value":N}`; all other payloads become per-property `stateDecodeIssue`s. The translator rejects fractional or out-of-range numeric values before any MQTT publish. Set choice `previous` → `{"color_temp_startup":65535}` only when supported; integer value N → `{"color_temp_startup":N}`; both request a `publishGet` refresh (`reconcile.go`). Planned in `planner_light.go` as optional sibling of a power-eligible root (`validPowerFeature` gates the family; siblings validated independently). Key `startupcolortemp`, external ID `<ieee>/<root|epN>/startupcolortemp` (`scopedIdentity`/`entityLocation`).
- `power_on_behavior`: device-root enum, `access & 7 == 7`, non-empty unique `values` ≤64. `set {"value":...}` → `{"power_on_behavior": "..."}` + refresh; exact-equality matcher. Key `poweronbehavior`.
- `effect`: device-root enum, access exactly 2, device-unique property, non-empty unique `values` ≤64. Key `effect`, endpoint-scoped identity; `Values` from expose `values`.

```diff
diff --git a/internal/adapters/zigbee2mqtt/discovery_wire.go b/internal/adapters/zigbee2mqtt/discovery_wire.go
@@
 type upstreamExpose struct {
 	...
 	ValueStep   *float64         `json:"value_step"`
 	Features    []upstreamExpose `json:"features"`
+	Values      []string
+	Presets     []upstreamPreset
 	valueMinRaw json.RawMessage
 	valueMaxRaw json.RawMessage
+	valuesRaw   json.RawMessage
+	presetsRaw  json.RawMessage
 }
+
+type upstreamPreset struct {
+	Name  string
+	Value int64 // exact-integer decoded; non-integral entries rejected
+}
```

### Project Layout

```text
entitytypes/
├── entitytype-manifest.schema.json          # modify — op enum, outcome/stateless/support_validation
├── numericsensorv1/                           # new — generic read-only number sensor (float64); linkquality entity 0–255 integers via support + adapter decode
├── enumsettingv1/                           # new — observable setting with dynamic choices
├── numericsettingv1/                        # new — generic observable bounded number or named choice
└── enumactionv1/                            # new — generic stateless enum action
internal/cmd/entitytypegen/
├── main.go                                  # modify — loadModel validation (stateless/outcome/support_validation)
├── behavior.go                              # modify — in/in_if_present/eq_optional/gte_if_present/lte_if_present compilation
├── render_behavior.go                       # modify — membership/optional/dispatched/support emission
└── render_catalog.go                        # modify — stateless/outcome/ValidateSupport into catalog assembly
internal/modules/devices/
├── catalog.go                               # modify — OutcomeKind, stateless, DefineOperation(outcome), IsStateless
├── model.go                                 # modify — OutcomeKind, CommandResult outcome + constructor
├── observation.go                           # unchanged — shape validation only, delegates to store
├── command.go                               # modify — dispatched terminal path reusing runCommand lifecycle
├── registration.go                          # unchanged — calls NormalizeSupport (now support-validated)
├── sqlite_observations.go                   # modify — stateless invalid_value rejection in classifyObservation
├── sqlite_observations_test.go              # modify — stateless precedence/rejection tests
├── sqlite_repository.go                     # modify — validCommandCompletion/sameCompletion dispatched rules
├── zz_generated_entitytypes.go              # modify — regenerated (existing seven gain outcome observed)
└── api/
    ├── types.go                             # modify — CommandResultBody discriminated status/omission
    ├── command.go                           # modify — outcome translation (satisfied vs dispatched)
    ├── command_history.go                   # modify — dispatched status vocabulary in history reads
    └── get_entity.go                        # modify — stateless entities read state null (existing path)
internal/platform/db/migrations/
└── 00001_initial.sql                        # modify — pre-deployment CHECK edit adding dispatched
internal/modules/devices/dbqueries/
└── commands.sql                             # modify — dispatched completion via generic CompleteCommand (NULL failure_code)
internal/modules/devices/dbsqlc/
└── *.go                                     # modify — regenerated sqlc output (never hand-edited)
internal/adapters/zigbee2mqtt/
├── discovery_wire.go                        # modify — values/presets tolerant parsing
├── planner_light.go                         # modify — optional startup/power/effect siblings under power gate
├── device_planner.go                        # modify — identity/location for new keys
├── entity_plan.go                           # modify — StatePolicy/Outcome + validation exemptions
├── command.go                               # modify — translateCommand carries dispatched marker
├── entity_linkquality.go                    # new — read-only linkquality decode
├── entity_startupcolortemp.go               # new — startup decode/translate (65535 ↔ previous)
├── entity_poweronbehavior.go                # new — power-setting decode/translate/matcher
├── entity_effect.go                         # new — stateless effect plan (no decoder/matcher)
├── runtime_commands.go                      # modify — dispatched finishSet (no get/matcher, FIFO release on accept)
├── runtime_observations.go                  # unchanged — no effect observation path exists
├── testdata/                                # modify — sanitized Wanda-shape inventory fixture (synthetic-labeled)
└── *_test.go + proof_flow_captures_test.go  # modify — eligibility/mapping/lifecycle coverage
sdk/adapter/
└── <type>v1/                                # modify — regenerated facades for the four types
web/src/
├── api/types.ts                             # modify — CommandResult/CommandRecord discriminated unions
├── pages/EntityDetailPage.tsx               # modify — dispatched-vs-satisfied view, no fabricated state
└── pages/CommandsPage.tsx                   # modify — dispatched status vocabulary
docs/adr/
└── *.md                                     # new — unnumbered draft ADR for this feature
README.md                                     # modify — new entities/outcomes
specs/generated-entity-type-behavior.md       # modify — DSL/catalog/dispatched behavior
contracts/v1/                                 # unchanged — NATS acceptance envelope and subjects
```

### Acceptance Criteria

- [ ] A1 `mise run generate` + `mise run validate` clean; catalog holds all four types with §Types supports, 10 s deadlines, `in`/`in_if_present`/optional rules, dispatched `trigger`; `numericsensor` scalar state and `numericsetting.value` bind to `float64` with number–number range validation.
- [ ] A2 Sanitized Wanda-shape fixture discovers linkquality (0–255 `lqi`), startup numeric setting (142–454 mired, choice `previous`), power setting (4 choices), effect (8 values); a startup expose without presets still discovers with empty choices, while an invalid/duplicate advertised `previous` mapping omits only that entity; fractional linkquality/startup payloads are rejected by their adapter decoders with siblings intact; unknown/`start_bind` siblings are ignored; malformed sibling suppresses nothing valid.
- [ ] A3 With `previous` supported, `65535` decodes to `{"mode":"choice","choice":"previous"}`; without it, `65535` is a property issue. `250` decodes to `{"mode":"value","value":250}` and `455` against 142–454 support is a property issue, both with siblings intact.
- [ ] A4 `numericsetting` schema `oneOf` rejects `{"mode":"value"}` without `value`, `{"mode":"choice","choice":"previous","value":250}`, and either mode carrying the wrong payload field.
- [ ] A5 `in` rejects an unsupported enum value (e.g. `"eco"`); `in_if_present` rejects an unsupported numeric-setting choice; empty `numericsetting` choices are valid, while empty `enumsetting`/`enumaction` choices, duplicate choices, and numeric-setting choice items outside 1–128 characters are rejected at core registration via schema bounds.
- [ ] A6 Decoded binary64 `min>max` support is rejected at core registration, not only in adapter planning; fractional in-range state passes both generic numeric entity types, proving they are genuinely fractional. The linkquality decoder and both startup decode/translate directions independently enforce this bulb's integer wire contract; a fractional startup command produces no MQTT publish. Tests pin overflow rejection, underflow-to-zero, and equal decoded bounds for wire minimum `9007199254740993` / maximum `9007199254740992`.
- [ ] A7 Effect: no `/get` published, no matcher installed, no observation published (ordinary or linked); FIFO slot released on acceptance; terminal outcome `dispatched`, never `satisfied`.
- [ ] A8 Core rejects effect observations (even null/empty) with `invalid_value` and no stored value; `GET` entity state stays `null`; rejected observations appear as value-less diagnostic rows in the existing Entity State-history `all`/`rejected` filters, are absent from `state-updates`, and never create canonical State.
- [ ] A9 Power/brightness/colortemp still satisfy only via fresh post-dispatch linked observation within deadline.
- [ ] A10 Per-IEEE FIFO across all command kinds (effect joins it; no new queue discipline); cross-device concurrency intact; no auto-retry; QoS 1 duplicates possible, documented in code comments.
- [ ] A11 Reads and UI show `dispatched` vs `satisfied`; no fabricated effect state/history in any read path.
- [ ] A12 `eq_optional` truth table (absent/absent true; one-absent false; present-equal true; present-unequal false) and `in_if_present` absent/member/nonmember cases pass; JSON null is rejected at codec decode; numeric-setting values are checked against discovered 142–454 despite the generic schema having no color-specific envelope; wrong-membership commands fail pre-dispatch in `ResolveCommand`.
- [ ] A13 `dispatched` persists with `failure_code NULL` + `outcome_observation_id NULL`; every failure status with a NULL/mismatched failure code is rejected by the migration CHECK; terminal idempotence via `sameCompletion` holds for dispatched.
- [ ] A14 Live gate (§Gates G2): separately authorized physical capture confirms on-wire 65535 startup value and effect dispatch acceptance; sanitized artifact checked in.

### Test Strategy

| Layer | What | How |
|-------|------|-----|
| Unit (codegen) | `in`/`in_if_present` membership, `eq_optional` truth table, `gte/lte_if_present` vacuous-absent, `support_validation` min>max, number→`float64` binding + number range emission, dispatched/no-predicate + empty-object state | Generator unit tests in D1–D2 before entity types; `entitytypegen` golden tests; `TestTypeEmitterRejectsLossyNumberBindings` revised to the new binding |
| Unit (core) | Stateless rejection precedence; `CommandResult` constructor invariant; dispatched terminal commit; SQL NULL/failure-code constraints; `sameCompletion` idempotence | `sqlite_observations_test.go`, command lifecycle tests with fake catalog/store, migration constraint tests |
| Integration (adapter) | Eligibility matrix (access/units/presets/uniqueness), linkquality/startup integer-only decode, startup translator rejection of fractional values before MQTT publish, 65535 mapping, off-choices rejection, sibling isolation | Planner/decoder/translator tests over sanitized Wanda-shape + adversarial fixtures |
| Integration (flow) | Effect publishes no `/get`/matcher/observation; FIFO release on accept; ordinary kinds still linked-satisfy | `proof_flow_captures_test.go` + runtime harness (PUBACK → accept → commit) |
| Contract | HTTP discriminated unions omit evidence on dispatched; NATS envelope byte-identical | API mapping tests; existing NATS schema tests unchanged and passing |
| Live (gated) | Physical 65535 + effect acceptance | D10 sanitized capture/report only (G2 authorization required) |

### Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Startup on-wire report never captured (metadata real, state value not) | High | High | G2 live gate before real-hardware acceptance; fixtures labeled synthetic until then |
| MQTT QoS 1 ambiguity (PUBACK lost after broker applied publish; duplicates possible) | Medium | Medium | Document in code + spec; no auto-retry; stable error codes; history shows rejection, never false success; HTTP disconnect does not cancel committed lifecycle |
| Stateless exemption weakens `validateEntityPlans` invariants | Low | High | Gated strictly on explicit `entityStateless` plan value + catalog flag; ordinary plans unchanged; regression tests |
| Generator changes (`oneOf` + optional-leaf ops) regress existing seven types | Medium | High | D1–D2 first with golden tests; existing seven gain explicit `"outcome": "observed"`; full `validate` rerun |
| Number binding is lossy binary64 (revises the lossy-binding guardrail) | Medium | Medium | Integers through 2^53 exact (covers linkquality 0–255 and startup mireds); validation/equality use rounded binary64 values; overflow rejected, underflow may become zero; §Types states the precision contract; golden tests pin the new binding |
| Dispatched persistence CHECK drift (migration vs sqlc vs model) | Low | High | Single pre-deployment migration edit; regenerated sqlc; `validCommandCompletion`/`sameCompletion` unit tests (A13) |

### Trade-offs Made

| Chose | Over | Because |
|-------|------|---------|
| Generic `numericsensor` with purpose-documented linkquality use | `numericdiagnostic` capability + diagnostic metadata framework | Diagnostic is purpose, not a distinct capability; a flag/framework adds manifest/support/catalog surface for one entity. Retain generic sensor, no diagnostic metadata |
| `float64` state binding | Exact-decimal (`json.Number`/`big.Rat`) binding | `ruleOperand` already emits `float64(...)` and `schemaComparable` already covers `number`, so comparisons and `EqualState` work with zero DSL change; exact-decimal would need new comparison helpers without benefit for these bounded sensor/setting values. Retain `float64` |
| Adapter-enforced integer-only for linkquality/startup temperature | New conditional DSL op / `multiple_of` on numbers | DSL has no conditionals, so per-entity integrality cannot be expressed without breaking generic fractional use; float modulo brings rounding hazards. Adapter decode rejects fractions, core backstops range. Retain |
| Two generic enum types (`enumsetting` observed vs `enumaction` dispatched) | One merged enum-control type | State/completion contracts differ fundamentally (persistent choice vs unverified request); merge would need per-entity statefulness/outcome policies instead of type-level catalog policy |
| `enumaction` for effects | Light-specific `lighteffect` type | Reusable without enabling generic action discovery; adapter still maps only the named `effect` expose |
| `in` operator | Hard-coded choices / static schema enums | Choices vary per entity from discovered support; alternative loses the generic model. Retain |
| Generic `numericsetting` with dynamic named choices | Color-specific `startupcolortemp` | The same number-or-special-choice shape already recurs in Zigbee2MQTT `current_level_startup`, `on_level`, and transition-time settings; entity metadata/support retains meaning and units while discovery remains allowlisted |
| `in_if_present` operator | Hard-coded `previous` / required fake choice | Keeps generic named choices support-driven and allows ordinary numeric settings to carry an empty choice set without weakening validation |
| `eq_optional` leaf comparison | Wiring generated `EqualState` into satisfaction | Numeric-setting satisfaction must compare both discriminated optional payloads exactly; `EqualState` wiring replaces rather than eliminates a generator extension. Retain |
| `gte_if_present` + `lte_if_present` pair | One combined optional-range operator | Reuses existing ordering rules and covers optional reported-state validation too; combined form needs a new three-operand shape. Retain |
| `support_validation` mechanism | Handwritten per-type hooks / adapter-only checks | Core rejects invalid registration from any adapter through the existing `NormalizeSupport` path. Retain |
| Tagged numeric value or named choice | Raw number/string union / separate operations | A searchable discriminant gives generated Go a stable shape, one operation preserves setting semantics, and protocol sentinels stay adapter-local |
| Dispatched terminal `dispatched` | Fabricated state / reusing `satisfied` | Honest unverified semantics; no invented observations; distinct HTTP/TS vocabulary |
| Stateless rejection in repository classification | Rejection in `Service.ProjectObservation` | Transactional atomicity with persistence; single seam with explicit precedence (stale → unknown → wrong-adapter → disabled → stateless → normalize) |
| Stateless + outcome as catalog data, adapter-local plan enums | Adapter querying core catalog | Preserves the adapter/core boundary; adapter values trusted from construction and validated at `validateEntityPlans`/`validatePlannedCommand` |

### Open Questions

None blocking. All scope, mapping, DSL, lifecycle, and gate decisions are settled above; authorization proceeds via §Gates.

### Gates

- **G1 — Whole-draft approval → Owner: user.** Authorizes non-live implementation D1–D9 only. No physical actuation under G1.
- **G2 — Live validation, explicitly and separately authorized → Owner: user approves actuation; implementer executes.** Pre-merge / mark-complete gate: blocks real-hardware acceptance (A14) only, never D1 start. Requires physical mutation for startup/effect by nature (on-wire 65535 value, dispatch acceptance). Do NOT require live 65535 before coding. Artifact: sanitized capture/report (no secrets, no raw topics beyond shape); synthetic labels removed only by this artifact.
- **Merge gate:** G1 + D1–D9 acceptance (A1–A13) + G2 artifact (A14). `mise run validate` rerun with diff review before merge.

### Success Metrics

- [ ] Pass/fail: `mise run generate` + `mise run validate` clean with all four types registered (A1).
- [ ] Pass/fail: Wanda-shape fixture discovers exactly the four allowlisted exposes with §Interfaces eligibility (A2–A3).
- [ ] Pass/fail: off-choices, out-of-range, and shape violations rejected at the specified layer (A4–A6, A12).
- [ ] Pass/fail: effect path emits no `/get`/matcher/observation and terminates `dispatched` (A7–A8, A11).
- [ ] Pass/fail: no regression in observed-satisfaction, FIFO, retry, or NATS envelope behavior (A9–A10).
- [ ] Pass/fail: dispatched persistence invariant holds under edited migration (A13).
- [ ] Pass/fail: G2 sanitized live artifact confirms 65535 + dispatch acceptance (A14).
