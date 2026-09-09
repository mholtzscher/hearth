# Automation execution and management

Status: Draft for technical review. Product behavior is agreed; this documentation task does not authorize implementation.
Sequence: Spec 1 of 2. Implement manual execution before [cron scheduling](automation-cron-scheduling.md).
Effort: L to XL, several days including persistence, lifecycle, and integration tests.

## Scope and existing implementation

Build HTTP management and inspectable manual execution in a flat `automations` module. Interpret schema-backed JSON definitions at runtime. The module owns its queries and concrete SQLite repository, and uses the existing devices service for Commands. Use the terminology in `CONTEXT.md`.

Definitions contain identified cron Triggers with OR semantics and ordered Entity Operation Steps. Spec 2 coalesces all matches for one Automation and UTC minute into one scheduled admission. This spec supplies the definition and Run contracts it needs. Conditions, other Trigger kinds, branching, delays, scenes/groups, reusable routines, CLI/UI, and suspended workflows remain out of scope.

Reuse these existing implementations:

- `devices/command.go:Service.ExecuteCommand` persists before dispatch and waits for the outcome. Caller cancellation stops waiting, not execution.
- `devices/catalog.go:TypeCatalog.ResolveCommand` validates support and normalizes parameters.
- `devices/sqlite_repository.go` owns Command-creation transactions. SQLite has one connection, so callers must not wrap these in another transaction.
- `internal/app/hearthd/run.go` interrupts unfinished Commands on startup without replay.

The `devices/` paths above are under `internal/modules/`. No new State event feed or NATS contract is required.

## Domain model

`internal/modules/automations/model.go` owns these types. Automation and Run IDs use canonical lowercase RFC 4122 UUIDv7 with prefixes `aut_` and `arn_`. The existing `run_` prefix belongs to Adapter runtimes.

```go
type AutomationID string
type AutomationRunID string
type AutomationTriggerID string // author-supplied slug, unique within one definition

type AutomationTrigger struct {
    ID AutomationTriggerID
    Kind string // exactly "cron" in this version
    Expression string
}

type AutomationStep struct {
    EntityID devices.EntityID
    OperationName devices.OperationName
    Parameters devices.CommandParameters // owned JSON object bytes
}

type AutomationDefinition struct {
    Name string
    Enabled bool
    Triggers []AutomationTrigger
    Steps []AutomationStep
}

type AutomationRecord struct {
    ID AutomationID
    Revision int64
    Definition AutomationDefinition
    CreatedAt time.Time
    UpdatedAt time.Time
}

type AutomationRunSource string // "manual" | "scheduled"
type AutomationRunStatus string // "running" | "succeeded" | "failed" | "interrupted"
type AutomationStepStatus string // "pending" | "running" | "satisfied" | "dispatched" | "failed" | "not_attempted" | "interrupted"

type AutomationRunSnapshot struct {
    AutomationID AutomationID
    Revision int64
    Definition AutomationDefinition
    Timezone string
}

type AutomationRunStep struct {
    Index int // zero based; immutable within this Run
    Definition AutomationStep
    Status AutomationStepStatus
    ReservedCommandID *devices.CommandID // allocated before attempted execution
    ReservedCorrelationID *devices.CorrelationID // internal ownership marker; not exposed by the Run API
    CommandID *devices.CommandID // only present for a Command owned by this Step
    CommandStatus *devices.CommandStatus
    Outcome *devices.OutcomeKind
    FailureCode *string
    StartedAt *time.Time
    CompletedAt *time.Time
}

type AutomationRunRecord struct {
    ID AutomationRunID
    Snapshot AutomationRunSnapshot
    Source AutomationRunSource
    ScheduledAt *time.Time // only scheduled Runs; UTC instant
    MatchedTriggerIDs []AutomationTriggerID // empty for manual, all matches for scheduled
    Status AutomationRunStatus
    StartedAt time.Time // admission time
    CompletedAt *time.Time
    FailureCode *string
    Steps []AutomationRunStep
}

type AutomationPage[T any] struct {
    Items []T
    HasMore bool
}
```

Define named constants for the status, source, and kind values. Pending and not_attempted Steps have no reserved identities or start time. Starting a Step records both identities and its start time. Only ownership-verified Commands populate `CommandID`, `CommandStatus`, and `Outcome`. Pre-creation validation failures have no Command ID or outcome. Interrupted Runs may expose their owned Commands' results without resuming execution.

Trigger IDs follow the schema's slug pattern and must be unique within a definition. Preserve IDs across edits and reordering. Changing an ID removes one Trigger and adds another; history retains the old snapshot. Distinct IDs may use identical expressions. Scheduled Runs record a nonempty, duplicate-free subset of snapshot Trigger IDs containing every match in snapshot order. Manual Runs record an empty array.

Deep-copy definitions, matched-ID arrays, and parameter bytes at admission. Names need not be unique. Definition size is limited to 64 KiB of encoded JSON; the schema below sets the remaining structural limits. These limits are fixed for this version. Both POST and PUT default omitted `enabled` to false. PUT replaces the whole definition, so omission disables automatic triggering rather than preserving prior enablement.

## Definition schema and validation

Embed `internal/modules/automations/automation-definition.schema.json` in `automation_definition.go`. Compile it once with the existing `github.com/santhosh-tekuri/jsonschema/v6` dependency and fail startup if compilation fails. The schema ID and `/v1` API identify the contract version.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:hearth:schema:automation-definition:v1",
  "title": "Hearth Automation definition v1",
  "type": "object",
  "additionalProperties": false,
  "required": ["name", "triggers", "steps"],
  "properties": {
    "name": {"type": "string", "minLength": 1, "maxLength": 200},
    "enabled": {"type": "boolean", "default": false},
    "triggers": {
      "type": "array",
      "minItems": 1,
      "maxItems": 32,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "kind", "expression"],
        "properties": {
          "id": {
            "type": "string",
            "pattern": "^[a-z0-9][a-z0-9_-]{0,62}$"
          },
          "kind": {"const": "cron"},
          "expression": {
            "type": "string",
            "minLength": 1,
            "description": "Five-field cron in the household timezone; calendar semantics are validated separately."
          }
        }
      }
    },
    "steps": {
      "type": "array",
      "minItems": 1,
      "maxItems": 100,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["entity_id", "operation_name", "parameters"],
        "properties": {
          "entity_id": {"type": "string", "minLength": 1},
          "operation_name": {
            "type": "string",
            "pattern": "^[a-z0-9][a-z0-9_-]{0,62}$"
          },
          "parameters": {"type": "object"}
        }
      }
    }
  }
}
```

This schema is the structural source of truth. The service checks Trigger ID uniqueness because `uniqueItems` compares whole objects. The Entity catalog owns Operation-specific `parameters` schemas and support constraints.

Validation proceeds in this order:

1. Decode JSON with the number-preserving conventions in `entitytypes/codec.go` and enforce the size limit.
2. Validate the schema before typed decoding can discard unknown properties. Return safe errors with JSON Pointer paths.
3. Decode the typed definition, apply the disabled default, trim the name, and normalize cron whitespace. JSON Schema's `default` annotation does not apply defaults itself.
4. Reject blank trimmed names and duplicate Trigger IDs. Validate every cron expression, canonical Entity ID, target existence, supported Operation, and its parameters through the catalog. Disabled or unavailable targets may be saved.
5. Persist the normalized definition. Execution rechecks current device support and control eligibility.

`automation_definition.go` owns the handwritten codec and typed decoding models. Go types represent the schema; they do not define separate validation rules.

```go
// AutomationDefinitionCodec owns the embedded definition schema and explicit defaults.
type AutomationDefinitionCodec struct { /* private compiled schema and source */ }

type AutomationDefinitionIssue struct {
    Path string // JSON Pointer into the definition
    Message string // safe explanation, no input payload echo
}

type AutomationDefinitionValidationError struct {
    Issues []AutomationDefinitionIssue
}

func (*AutomationDefinitionValidationError) Error() string

func NewAutomationDefinitionCodec() (*AutomationDefinitionCodec, error)
func (*AutomationDefinitionCodec) DecodeAutomationDefinition(
    raw json.RawMessage,
) (AutomationDefinition, error)
func (*AutomationDefinitionCodec) ValidateAutomationDefinition(
    definition AutomationDefinition,
) error
// AutomationDefinitionSchema returns an owned copy of the canonical schema bytes.
func (*AutomationDefinitionCodec) AutomationDefinitionSchema() json.RawMessage
```

Inject the compiled codec into the service and API registration. `DecodeAutomationDefinition` validates structure and decodes defaults. The service owns semantic checks and also calls `ValidateAutomationDefinition` for non-HTTP callers. Keep validator-library types private. JSON-tagged decoding models map fields to domain types. HTTP registration and error mapping are specified below.

## Command API changes

Modify `devices/model.go`, `command.go`, `repository.go`, `sqlite_repository.go`, and all API interfaces, callers, and tests. The changes are internal; HTTP callers cannot supply Command or Correlation IDs.

```diff
--- a/internal/modules/devices/model.go
+++ b/internal/modules/devices/model.go
@@
 type CommandParameters json.RawMessage
+
+// CommandInput permits internal callers to reserve command identity before execution.
+type CommandInput struct {
+    ID CommandID // empty generates an ID; nonempty must be a fresh valid Command ID
+    CorrelationID CorrelationID // empty generates an ID; Automation Steps reserve an independent fresh marker
+    EntityID EntityID
+    OperationName OperationName
+    Parameters CommandParameters
+}
```

```diff
--- a/internal/modules/devices/command.go
+++ b/internal/modules/devices/command.go
@@
 func (service *Service) ExecuteCommand(
     ctx context.Context,
-    entityID EntityID,
-    operationName OperationName,
-    parameters CommandParameters,
+    input CommandInput,
 ) (CommandResult, error) {
```

Add `Service.ValidateCommand(ctx context.Context, input CommandInput) (CommandParameters, error)` in `command.go`. Share its identity, Entity ID, Operation, JSON, and catalog validation with execution. It returns owned normalized parameters without generating IDs, writing records, checking temporary availability/enablement, or dispatching.

Execution rereads support, routing, enablement, and health at the existing stages. Supplied identities replace generation after canonical validation; empty fields retain generation. Persist the supplied Correlation ID unchanged, including for immediately terminal undispatched Commands. Devices still own deadlines. Direct HTTP callers leave both identities empty. Replace all callers and fakes without a compatibility wrapper.

Add `ErrCommandIDConflict = errors.New("command ID already exists")` to `devices/repository.go`. `SQLiteRepository.CreateCommand` maps only ID uniqueness violations to this error. Other errors retain their meaning. A duplicate creates no Command and dispatches nothing. The executor handles it before outcome reconciliation.

## Automation service

`internal/modules/automations/service.go` owns management and admission. `automation_execution.go` owns sequential execution and worker lifecycle.

```go
// AutomationCommands preserves the existing device command lifecycle.
type AutomationCommands interface {
    ValidateCommand(context.Context, devices.CommandInput) (devices.CommandParameters, error)
    ExecuteCommand(context.Context, devices.CommandInput) (devices.CommandResult, error)
}

// AutomationCommandRecords distinguishes durable failure from uncertain persistence.
// The existing devices.SQLiteRepository implements this read-only seam.
type AutomationCommandRecords interface {
    GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
}

type AutomationUpdate struct {
    ID AutomationID
    ExpectedRevision int64
    Definition AutomationDefinition
}

type AutomationListParams struct {
    AfterID *AutomationID
    Limit int
}

type AutomationRunListParams struct {
    AutomationID *AutomationID
    BeforeStartedAt *time.Time
    BeforeID *AutomationRunID
    Limit int
}

type AutomationManualRequest struct {
    AutomationID AutomationID
    IdempotencyKey string
}

type AutomationAdmission struct {
    Run AutomationRunRecord
    Reused bool
}

func NewService(repo *SQLiteRepository, commands AutomationCommands,
    commandRecords AutomationCommandRecords, definitions *AutomationDefinitionCodec,
    timezone *time.Location, logger *slog.Logger) *Service
func (*Service) CreateAutomation(context.Context, AutomationDefinition) (AutomationRecord, error)
func (*Service) UpdateAutomation(context.Context, AutomationUpdate) (AutomationRecord, error)
func (*Service) GetAutomation(context.Context, AutomationID) (AutomationRecord, error)
func (*Service) ListAutomations(context.Context, AutomationListParams) (AutomationPage[AutomationRecord], error)
func (*Service) DeleteAutomation(context.Context, AutomationID, int64) error
func (*Service) StartManualRun(context.Context, AutomationManualRequest) (AutomationAdmission, error)
func (*Service) GetAutomationRun(context.Context, AutomationRunID) (AutomationRunRecord, error)
func (*Service) ListAutomationRuns(context.Context, AutomationRunListParams) (AutomationPage[AutomationRunRecord], error)
func (*Service) StopAutomationExecutionAdmission() // atomically closes Run and next-Step admission
func (*Service) WaitAutomationRuns(context.Context) error
```

`SQLiteRepository` owns `CreateAutomation`, `UpdateAutomation`, `DeleteAutomation`, `AdmitManualRun`, `BeginAutomationStep`, `CompleteAutomationStep`, `CompleteAutomationRun`, `InterruptAutomationRuns`, `PruneAutomationHistory`, and the reads above. These methods own transactions and accept domain types rather than sqlc rows. Tests may inject clocks and ID generators through unexported function fields.

### Admission and idempotency

Allow one active Run per Automation. Different Automations and direct Commands may overlap on an Entity; there is no cross-Automation arbitration. Manual invocation is allowed while disabled. A different manual key conflicts while a Run is active; spec 2 records a scheduled overlap as skipped.

Serialize admission with updates, disablement, and deletion. In one transaction, look up the idempotency key before checking the live definition or active Run. Reuse its Run if found. Otherwise load the latest definition and timezone, claim admission, and write the Run, Step snapshots, and key. Edits affect future Runs only. Commit and register the worker under the lifecycle gate before returning the asynchronous HTTP response. A crash before worker launch leaves a Run to interrupt on restart, never work to replay.

Keys contain 1 to 128 printable ASCII characters excluding whitespace. A retained key returns the original Run after edits, completion, disablement, or deletion. After pruning, the same key starts a new invocation if the definition exists; otherwise return 404. Use a new key to request another execution while the old Run is retained. Deletion requires the expected revision, conflicts while a Run is active, and retains history.

### Step execution and ownership

Execute Steps in order. Generate a Command ID with `devices.NewCommandID` and an independent fresh Correlation ID with `devices.NewCorrelationID`. Atomically mark the Step running with both identities and its start time, then commit before calling `ExecuteCommand`. Never copy the marker from an existing Command. Step reservation and Command creation use separate transactions; they provide neither atomic Run/Command creation nor exactly-once physical effects.

Use one ownership predicate for reconciliation, history joins, and recovery. Both `record.ID == step.ReservedCommandID` and `record.CorrelationID == step.ReservedCorrelationID` must hold. A persisted `command_id_conflict` excludes linkage even when both match. ID-only matches never supply public Command evidence. Independent marker generation and non-reuse preserve this distinction after a crash before failure persistence. Correlation IDs already exist in Command storage and the wire contract.

A successful observed Operation produces status `satisfied` with outcome `observed`. A dispatched Operation produces status and outcome `dispatched`; it does not confirm a physical effect. Persist each Step result before advancing. Failures stop the sequence without retries or rollback. There is no cancellation endpoint.

Handle execution errors in this order:

1. `ErrCommandIDConflict` fails with `command_id_conflict` without reconciling or linking the old Command. Other confirmed pre-creation validation/lookup failures also exclude adoption.
2. For ambiguous errors, call `AutomationCommandRecords.GetCommand`. Only an owned terminal record supplies an authoritative outcome. Map the successful statuses above to success and other terminal statuses to failure. `CommandExecutionError` alone does not prove durable terminality.
3. `ErrCommandNotFound` permits failure without a Command ID. A foreign marker, nonterminal owned record, or other read error leaves execution uncertain.

On established failure, persist the failing Step, mark later Steps not_attempted, and fail the Run. Preserve known Command failure codes. Pre-creation codes are `command_id_conflict`, `invalid_command`, `entity_not_found`, and sanitized `internal_error`.

Uncertain execution or failed result persistence latches an executor fault until restart. Atomically close Run and next-Step admission, degrade readiness, and retain affected active claims without marking uncertain Runs terminal. Already-started Commands may finish and persist known outcomes. Scheduler recovery cannot clear this fault.

## SQLite contract

Modify `internal/platform/db/migrations/00001_initial.sql`; there are no deployments and no backfill/legacy path is required.

Use these tables and constraints:

- `automations` has primary key `id`, positive `revision`, `name`, boolean `enabled`, ordered `triggers_json`, `steps_json`, and UTC `created_at`/`updated_at`. Revision starts at 1 and increments on every successful PUT.
- `automation_runs` has primary key `id`, historical `automation_id`, `revision`, `snapshot_json`, `source`, nullable `scheduled_at`, `matched_trigger_ids_json`, `status`, `started_at`, nullable `completed_at`, nullable `failure_code`, and nullable manual-only `idempotency_key`. The snapshot retains the entire definition and timezone. Enforce source/matched-ID consistency from the domain model. Definition deletion must not cascade to Runs.
- `automation_run_steps` has primary key `(run_id, step_index)` and a Run foreign key that cascades on history pruning. Store `definition_json`, `status`, nullable unique `reserved_command_id` and `reserved_correlation_id`, nullable `outcome`, nullable `failure_code`, and nullable `started_at`/`completed_at`. Both reserved identities must be present or absent together. They are not foreign keys to Commands. Join Command evidence through the ownership predicate rather than copying Observations.
- Enforce active-Run uniqueness with `automation_runs(automation_id) WHERE status = 'running'` and manual-key uniqueness with `(automation_id, idempotency_key) WHERE idempotency_key IS NOT NULL`.
- Index history by `(started_at DESC, id DESC)` and `(automation_id, started_at DESC, id DESC)`. Index terminal `completed_at` for pruning.
- Add CHECK constraints for enums, positive revisions, source/key/scheduled-time relationships, and terminal timestamps. Storage validates JSON syntax; the service validates its shape.

Add module queries under `internal/modules/automations/dbqueries` and generated output under `dbsqlc`. Update `sqlc.yaml` and the `mise.toml` generation check to generate and compare both modules.

## HTTP contract

Add automations to `NewHTTPHandler` in app assembly and update its callers. Module registration owns the `/v1` routes, stable Operation IDs, and RFC 9457 errors. Retain the existing Echo/Huma stack and authentication policy.

POST and PUT use the same definition body. Their handlers decode it with the injected codec, then call the service. IDs, revision checks, timestamps, and Run status are transport metadata rather than DSL fields. Inputs in `api/automation_models.go` are:

```go
type CreateAutomationInput struct {
    Body json.RawMessage
}

type UpdateAutomationInput struct {
    AutomationID string `path:"automation_id"`
    ExpectedRevision int64 `query:"expected_revision" required:"true" minimum:"1"`
    Body json.RawMessage
}
```

Set `SkipValidateBody: true` and `RequestBody.Required: true`. Both JSON bodies reference the OpenAPI `AutomationDefinition` component. Populate it with `&huma.Schema{Extensions: document}`, using a number-preserving `map[string]any` decoded from `AutomationDefinitionSchema()`. Huma v2.39.1 serializes these entries inline. Preserve all keywords, including `$schema`, `$id`, and resource-local `$defs`/`$ref`. Unmarshalling into Huma's typed schema fields would lose unsupported keywords. Use `Body json.RawMessage`, not `RawBody`, to preserve the explicit request schema.

The module codec validates bodies; Huma still validates paths and queries. Authoring tools use the checked-in schema or existing OpenAPI. Validate response definition fields and example fixtures against that same schema.

| Method / path | Behavior |
|---|---|
| `POST /v1/automations` | Create; 201 with Location and Automation body |
| `GET /v1/automations` | List by ID ascending; `limit`, `cursor` |
| `GET /v1/automations/{automation_id}` | Definition and revision; 404 if deleted |
| `PUT /v1/automations/{automation_id}?expected_revision=N` | Full replacement with required positive revision query; 200 |
| `DELETE /v1/automations/{automation_id}?expected_revision=N` | Revision-checked delete; 204; active Run conflicts |
| `POST /v1/automations/{automation_id}/runs` | Required `Idempotency-Key` header, no body; 202 new, 200 reused; Run body and Location |
| `GET /v1/automation-runs` | List summaries, optional `automation_id`, `limit`, `cursor`; works for deleted definitions |
| `GET /v1/automation-runs/{run_id}` | Full immutable snapshot, Step evidence, failure reason; 404 after pruning |

Example definition, with a placeholder Entity ID:

```json
{
  "name": "Evening lights",
  "enabled": false,
  "triggers": [
    {"id": "weekdays", "kind": "cron", "expression": "0 19 * * 1-5"},
    {"id": "weekends", "kind": "cron", "expression": "0 18 * * 0,6"}
  ],
  "steps": [
    {"entity_id": "ent_<canonical-uuidv7>", "operation_name": "set", "parameters": {"value": true}}
  ]
}
```

Automation responses add `id`, `revision`, `created_at`, and `updated_at`; spec 2 adds the read-only household timezone. Run detail uses snake_case fields from the domain model, including its snapshot, `matched_trigger_ids`, `reserved_command_id`, and ownership-verified `command_id`. Omit absent nullable fields and internal `ReservedCorrelationID`.

Run summaries include Run ID, Automation ID/name/revision, source, matched Trigger IDs, scheduled/start/completion timestamps, status, and failure code. They omit Step arrays and parameters. Run responses never expose idempotency keys. Routine logs omit keys and Step parameters; errors must not leak SQL, credentials, or adapter payloads.

| Status | Condition |
|---|---|
| 400 | Missing required body or semantic invalid IDs, duplicate Trigger IDs, cron, keys, Operation parameters, or cursors |
| 422 | Schema/Huma structural errors, including malformed present JSON, non-object `parameters`, and missing, noninteger, or nonpositive revision queries |
| 404 | Missing resource, including a deleted definition or pruned Run |
| 409 | `automation_revision_conflict` for stale revisions; `automation_run_active` for admission/delete overlap |
| 503 | `automation_unavailable` during shutdown or degraded persistence |
| 500 | Sanitized unexpected errors |

Malformed present JSON follows Huma's RawMessage error path. Preserve these route-specific statuses without a global Huma override. Failures after admission appear in the Run resource, not delayed HTTP 502/504 responses.

Pagination defaults to 50, accepts 1 to 200, and fetches `limit+1`. Return `items: []` and optional `next_cursor`. Run order is `(started_at DESC, id DESC)`. Versioned canonical base64url JSON cursors bind resource and Automation filter. Reject extra/trailing data and cross-filter reuse. Cursors provide continuation, not snapshot isolation; historical filtering works after definition deletion.

## Configuration and lifecycle

`internal/app/hearthd/config.go` gains:

```diff
--- a/internal/app/hearthd/config.go
+++ b/internal/app/hearthd/config.go
@@
 type Config struct {
+    HouseholdTimezone string `yaml:"household_timezone"`
+    AutomationHistoryRetention time.Duration `yaml:"automation_history_retention"`
```

Require a household IANA timezone and load it once at startup. Accept UTC; reject empty values, `Local`, and raw numeric offsets. Embed `time/tzdata` rather than relying on the host's zoneinfo. Timezone edits require restart.

Retention zero selects 30 times 24 hours; nonzero retention must be at least 24 hours. Every hour, prune terminal Runs completed strictly before the cutoff in batches of 500. Keep active Runs and skip startup pruning. Spec 2 uses the same retention for Occurrences and gaps. Update the example config, README, and all Config fixtures.

Startup performs these steps in order:

1. Migrate the database and interrupt active Commands.
2. Interrupt active Runs/Steps with `core_restarted` and mark pending Steps not_attempted. Retain owned Command evidence, but never resume Steps, even when a Command succeeded.
3. Start device transports and Observation consumption.
4. Construct the automation service and open HTTP admission.

Workers use process-owned contexts independent of request and shutdown cancellation while awaiting the current Operation. Shutdown calls `StopAutomationExecutionAdmission` to close Run and next-Step admission atomically, then stops and joins the scheduler. Step-intent commit and in-flight registration use the same gate. A Step admitted before closure may execute and must drain; one blocked by closure must not call the devices service.

Keep NATS, health supervision, Observation consumption, and SQLite alive until current automation Commands return and their Step results persist. Interrupt unfinished sequences with `core_stopping`; fully completed Runs may succeed. Join workers before dependency teardown. Draining may take the Operation deadline plus its persistence allowance, beyond the existing five-second HTTP shutdown timeout. A hard kill uses startup recovery. Already-dispatched Commands are not canceled.

Give drain dependencies a separate lifecycle context and cancel it after worker drain, on both normal and error exits. Overall readiness requires device readiness, healthy scheduling from spec 2, open admission, and no latched executor fault. Scheduler recovery clears only its own fault. Direct API Command cancellation behavior remains unchanged. The precedence of fault retention and shutdown interruption needs clarification below.

## Files and deliverables

The tree assigns implementation files to deliverables. Tests remain with their owning modules. E5 covers their integration and extends `internal/app/hearthd/recovery_integration_test.go` and `internal/app/hearthd/server_test.go`.

```text
internal/
├── modules/
│   ├── automations/
│   │   ├── model.go, ids.go, errors.go        # E2, new. Domain types and invariants.
│   │   ├── automation-definition.schema.json # E2, new. Definition schema.
│   │   ├── automation_definition.go          # E2, new. Codec and defaults.
│   │   ├── testdata/automation-definitions/  # E2, new. Valid and invalid fixtures.
│   │   ├── service.go                       # E3, new. Management and admission.
│   │   ├── automation_execution.go          # E3, new. Executor and lifecycle.
│   │   ├── cron_schedule.go                 # E2, new. Parser validation; spec 2 adds scheduling.
│   │   ├── sqlite_repository.go             # E2, new. Transactions and history.
│   │   ├── dbqueries/automations.sql        # E2, new. Query source.
│   │   ├── dbsqlc/                          # E2, generated. Query code.
│   │   ├── api/register.go                  # E4, new. Routes, handlers, errors.
│   │   ├── api/automation_models.go         # E4, new. HTTP models.
│   │   ├── api/automation_schema.go         # E4, new. OpenAPI schema publication.
│   │   ├── api/pagination.go                # E4, new. Cursors.
│   │   └── *_test.go, api/*_test.go          # E2 to E5, new. Module and API tests.
│   └── devices/
│       ├── model.go, command.go             # E1, modify. Command input and validation.
│       ├── repository.go, sqlite_repository.go # E1, modify. Duplicate-ID error.
│       └── api/, *_test.go                  # E1, modify. Callers and regressions.
├── app/hearthd/
│   ├── config.go, run.go                    # E3, modify. Configuration and lifecycle.
│   ├── server.go                           # E4, modify. HTTP assembly and readiness.
│   └── *_test.go                           # E1, E3 to E5, modify/new. Caller, recovery, HTTP tests.
├── app/zigbee2mqtt/*_test.go                 # E1, modify. Command callers.
└── platform/db/migrations/00001_initial.sql  # E2, modify. Automation tables.
sqlc.yaml, mise.toml                         # E2, modify. Generation and checks.
go.mod, go.sum                              # E2, modify. Pinned cron dependency.
configs/hearthd.example.yaml, README.md       # E4, modify. Configuration and curl usage.
```

| ID | Outcome | Effort | Depends on | Acceptance |
|---|---|---|---|---|
| E1 | Add reserved identities, duplicate-ID errors, shared validation, and update callers. | M | None | A1 |
| E2 | Add the definition codec, domain model, cron validation, persistence, and generation. | L | E1 | A2 to A4, A8, A11, A12 |
| E3 | Execute manual Runs with persisted results, shutdown, and recovery. | L | E2 | A5 to A7, A9 |
| E4 | Expose management, invocation, history, and the canonical OpenAPI schema. Document usage. | L | E3 | A3, A4, A8, A10 to A12 |
| E5 | Verify module/API integration, app recovery, and runtime OpenAPI; run full validation. | M | E4 | A1 to A12 |

## Acceptance criteria

- **A1. Identity before dispatch.** Inside the fake sender, a separate read sees the persisted running Step's reserved identities matching both identities in the persisted Command. Duplicate IDs return `ErrCommandIDConflict` with zero sends. Failed intent commits also send nothing; invalid targets/parameters create no Command. Direct callers retain identity generation, outcomes, and cancellation behavior.
- **A2. Atomic admission.** Concurrent real-SQLite admissions allow one active Run per Automation while independent Automations proceed. Failed transactions leave no partial Run, key, or Steps. Edit races snapshot one whole revision.
- **A3. Manual retries.** Concurrent same-key requests return one Run, including after completion, edit, disablement, and deletion. Different keys conflict only while active. Test key reuse after pruning with both existing and deleted definitions.
- **A4. Management.** Validate all Steps without dispatch. Reject unknown targets and unsupported Operations; allow disabled/unavailable targets. Stale PUT/DELETE changes nothing. Deletion cannot race past admission. Disabled manual invocation works.
- **A5. Ordered outcomes and attribution.** Block the first Command and assert no second dispatch. Both success kinds advance; established failures stop later Steps without retries or rollback and preserve earlier results. Distinguish failures with and without Command records. An outcome-persistence error with nonterminal or unreadable evidence retains the active claim and latches failure. Only an owned terminal record is authoritative. Force collision with an otherwise identical old Command using a fresh correlation marker. Repeat for both satisfied and dispatched old Commands. Assert failed Run, no current/later sends, unchanged old Command, and no linkage. Known duplicate errors also reject reconciliation when the test reuses the old marker.
- **A6. Snapshot isolation.** Edit/disable during a blocked Step. The Run retains its name, revision, order, parameters, and timezone; the next Run sees the edit.
- **A7. Recovery.** Crash before creation, after creation, and after completion. Recovery interrupts Runs with zero dispatch, preserves owned terminal evidence, and marks pending Steps not_attempted. Crash after reserving a colliding ID but before recording failure. The independent marker must keep the old Command out of recovery and history evidence.
- **A8. History.** Retain deleted-definition history and reject cursor misuse. Paging works during inserts/pruning. Preserve cutoff equality, active Runs, and deduplication evidence for retained Runs; remove older terminal history.
- **A9. Lifecycle.** Cancel an admitted HTTP request and observe continued execution. Block scheduler shutdown and verify both admission gates are already closed. Drain current Commands, persist interruption, and join workers before dependency teardown. Persistence failure blocks later Commands and degrades readiness.
- **A10. HTTP/OpenAPI.** Real Echo/Huma requests verify the HTTP contract, required keys/revisions, disabled default, asynchronous responses, and sanitized errors. No endpoint accepts caller-supplied Command/Correlation IDs or exposes the reserved correlation marker.
- **A11. Schema contract.** Use hand-authored fixtures through the schema, codec, and HTTP handler. Before typed decoding, reject unknown properties, singular `trigger`, missing fields, invalid Trigger slugs/kinds, empty/oversized arrays, and non-object parameters. Operation-specific properties remain catalog-owned. Verify disabled defaults for POST/PUT, revision query validation, and rejection of revision metadata in the body. Invalid cron and unsupported parameters fail without writes/sends. Preserve numeric precision. Both request schemas must reference the unchanged embedded schema; response definitions and examples must validate against it. Test missing body as 400, malformed present JSON and schema-invalid JSON as 422. A `$defs`/`$ref` fixture must survive OpenAPI publication intact.
- **A12. Trigger identity and provenance.** Save/read preserves IDs and order. Reject duplicate IDs with different expressions; allow duplicate expressions with distinct IDs. Accept 1 and 32 Triggers; reject 0 and 33 through schema and service. Manual matches are empty. Scheduled matches contain every matching ID once in snapshot order without duplicate Step execution. Edits/reordering leave history unchanged. Spec 2 verifies one Run or overlap skip per Automation/minute.

Use deterministic barriers/channels for ordering and shutdown. Test uniqueness, transactions, and joins with real migrated SQLite; recovery tests also use embedded NATS. Assertions derive from this contract, not the evaluator under test. Preserve existing Command regressions when updating signatures. These checks establish software behavior, not physical outcomes; no device actuation is authorized.

Run `mise run validate` and review any generated, formatting, or module-metadata changes. Use repository mise tasks for focused checks.

## Open decisions

These ambiguities predate this editorial revision. The automation spec owner must resolve them without treating the shorter wording as a policy change.

1. E2, E4, and E5 claim A12, whose scheduled coalescing checks require spec 2. Spec 2 depends on E1 through E5. Clarify staged acceptance ownership without dropping those tests.
2. Executor faults retain uncertain active Runs until restart, while shutdown interrupts unfinished Runs. Specify which rule governs faulted Runs during graceful shutdown.
3. Confirmed pre-creation failures prohibit Command adoption, but the stored join exclusion names only `command_id_conflict`. Specify how other confirmed pre-creation failures durably exclude a later matching record.

## Follow-up scope

Future Conditions decide permission separately from Trigger causes. State/event Triggers need captured evidence and Condition inputs; later State reads cannot establish what an earlier Run observed. Keep admission policy separate from device Command execution.

Sustained absence is Trigger eligibility. New motion resets the duration before admission; the Run does not sleep. Its future spec must define restart, initial-State, and stale-evidence behavior independently of Run recovery. These notes do not add expression/plugin machinery or suspended execution to this version. Household migration coverage requires checking actual automations against supported behavior.
