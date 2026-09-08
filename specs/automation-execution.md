# Automation execution and management

**Status:** Draft for technical review. Product behavior confirmed in the design interview; application implementation is not authorized by this document-writing task.
**Sequence:** Spec 1 of 2; [cron scheduling](automation-cron-scheduling.md) builds on this slice.
**Effort:** L–XL (several days), including the Command API seam, persistence, lifecycle, and integration tests. Multiple identified cron Triggers adds a contained M-sized model/schema/provenance change within this estimate; Conditions and sustained-condition Triggers remain separate future work.

## Problem and evidence

The first goal is a reusable platform for experimenting with household routines through HTTP, not a UI, configuration-file DSL, or general workflow engine. Establish inspectable manual execution before clock-driven execution.

Current foundations:

- `CONTEXT.md` defines Entity, Operation, Command, and their outcome semantics; its new Automation vocabulary records the interview agreement.
- `internal/modules/devices/command.go:Service.ExecuteCommand` already validates and persists Commands before dispatch, then waits for their outcomes. Caller cancellation stops waiting but does not cancel the Command.
- `internal/modules/devices/catalog.go:TypeCatalog.ResolveCommand` validates current support and normalizes parameters.
- `internal/modules/devices/sqlite_repository.go` owns transactional Command creation. Do not wrap it in another transaction: SQLite currently has one connection.
- `internal/app/hearthd/run.go` interrupts unfinished Commands on startup. Command records are not replayable work.
- There is no automation module, persisted Run, or canonical State-change event feed. This slice needs no new event feed or NATS contract.

## Agreed behavior

1. An Automation has a name, one or more identified cron Triggers with OR semantics, enablement, a revision, and a nonempty ordered sequence of Entity Operation Steps. Collect all matching Triggers for one Automation/UTC minute into one scheduled admission; record every matching Trigger ID in the Run. Initially there are no Conditions.
2. Creation defaults to disabled. Manual invocation is allowed while disabled; enablement gates only scheduling.
3. A manual request durably admits a Run and returns immediately. HTTP disconnects cannot cancel admitted execution.
4. A Run snapshots the definition and household timezone. Edits and disablement affect future admissions only.
5. Steps execute sequentially. Persist each result before starting the next Step. Stop on the first failure; remaining Steps are not attempted. Earlier effects are not rolled back.
6. Both existing successful outcomes count: `satisfied` for observed Operations and `dispatched` for dispatched Operations. Do not call a dispatched effect physically confirmed.
7. At most one active Run per Automation. Concurrent manual requests with different keys conflict; a scheduled overlap is recorded as skipped by spec 2. Different Automations and direct Commands may still target the same Entity concurrently.
8. No automatic retries, cancellation endpoint, delays, branching, groups, scenes, or cross-Automation arbitration.
9. Restart interrupts active Runs and never resumes or replays Steps. A successfully completed Command does not grant permission to resume its Run.
10. Delete conflicts while a Run is active. Otherwise delete the definition and preserve Run snapshots until retention removes them.
11. Terminal Run history defaults to 30 days, configurable; active Runs are never pruned. Idempotency lasts only while the Run is retained.

## Delivery boundaries and alternatives

Choose a new flat `automations` product module, using the existing devices service through a consumer-owned Command gateway. Keep persistence adapters and queries module-owned, as in `devices`.

The Automation definition is a schema-backed JSON DSL. Its checked-in JSON Schema is the canonical structural contract, following the entity framework's schema-plus-declarative-document approach. Unlike Entity-type manifests, individual household Automations are loaded and interpreted at runtime, not compiled into generated Go bindings.

| Chosen | Alternative | Reason / cost |
|---|---|---|
| Schema-backed JSON DSL | Go-first inferred request contract or custom text grammar | Explicit authoring contract and editor tooling; no new parser or per-Automation compilation |
| Manual execution first | Build scheduler and executor together | One independently testable end-to-end slice before time semantics |
| Immutable Run snapshot | Read live Steps during execution | Edits cannot change an in-flight sequence; more audit storage |
| Reserve Command identity before dispatch | Link only after `ExecuteCommand` returns | Crash-safe attribution without a cross-module transaction coordinator |
| Interrupt, never replay | Durable workflow resumption | Predictable recovery; partial effects require human inspection |
| Concrete SQLite repository | Generic repository/unit-of-work framework | No second persistence implementation; real SQLite tests protect admission races |

The reserved-ID choice is an implementation proposal, not a change to the agreed execution behavior. A reserved ID is an intent reference, not a Command record. Never claim atomic Run/Command creation or exactly-once physical effects.

## Concrete model

Ownership: `internal/modules/automations/model.go`. IDs use canonical lowercase RFC 4122 UUIDv7, matching existing validation conventions: `aut_` for Automation and `arn_` for Run. Do not use `run_`, which already means Adapter runtime.

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

Define named constants for all status/source/kind literals above. Nullable fields reflect lifecycle states, not undocumented options: pending/not-attempted Steps have no reserved identities or start time; attempting a Step writes both reserved identities and start time; only ownership-verified Command evidence populates `CommandID`. `satisfied` and `dispatched` require their matching Command status and outcome. A pre-creation validation failure has no Command ID or outcome. An interrupted Step may still expose its linked Command's actual result without changing the Run's interrupted status.

Trigger IDs are author-supplied slugs matching `^[a-z0-9][a-z0-9_-]{0,62}$`, unique within a definition. Preserve IDs across reordering and edits to identify the same Trigger; changing an ID is removal/addition, not a history rewrite. Historical meaning is always interpreted against the immutable revision snapshot, so no global ID registry or cross-revision tombstones are needed. Duplicate IDs are a semantic 400 even if their expressions differ. Duplicate expressions with distinct IDs are allowed and coalesce normally.

`MatchedTriggerIDs` is an empty array for manual Runs, and a nonempty, duplicate-free subset of the snapshot's Trigger IDs for scheduled Runs, ordered as in the definition. Store all matches, not a selected winner. Scheduled time, household timezone, matched IDs, and the full definition snapshot supply the complete cron cause; manual invocation never fabricates a Trigger match.

Deep-copy definitions, matched-ID arrays, and parameter bytes across admission boundaries. Names are trimmed, nonempty, at most 200 Unicode code points; names need not be unique. Allow 1–32 Triggers, 1–100 Steps, and at most 64 KiB of encoded definition JSON. These are initial resource guards, not user-configurable features. Explicit `enabled` is supported; omission means false for both creation and full replacement. JSON Schema's `default` is an annotation, not mutation: the definition decoder explicitly applies this default. PUT replaces the whole definition rather than preserving omitted fields, so omitting enablement disables automatic triggering.

### Canonical definition schema and runtime DSL

Ownership: new `internal/modules/automations/automation-definition.schema.json`, embedded and compiled once by `automation_definition.go` using the existing `github.com/santhosh-tekuri/jsonschema/v6` dependency. Schema compilation failure fails service assembly/startup; never defer it until the first invocation. The schema's ID and `/v1` API identify this first contract version; do not add an unneeded per-document version field or migration machinery.

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

The schema owns required fields, structural bounds, discriminator values, and unknown-property rejection. Trigger ID uniqueness is semantic validation: JSON Schema `uniqueItems` compares whole objects and cannot enforce uniqueness of just `id`; do not mistake it for that constraint. The old singular `trigger` field is rejected, not supported as a compatibility form. `parameters` deliberately allows Operation-specific properties: the selected Entity type owns their schema and support-dependent constraints. Do not copy Entity parameter schemas or the Entity framework's `satisfied_when` rule DSL into the Automation definition. No Conditions, alternate Trigger variants, or user-defined expression operators are added in this slice.

Validation order:

1. Decode JSON without losing numeric precision and enforce the 64-KiB definition limit.
2. Validate against the canonical schema; return field-addressed errors and reject unknown properties before typed decoding can discard them.
3. Decode to `AutomationDefinition`, explicitly apply the disabled default, trim the name, and normalize each cron expression's whitespace while preserving Trigger IDs and order.
4. Enforce semantic rules: nonblank trimmed name, unique Trigger IDs, accepted cron grammar for every Trigger, canonical Entity IDs, current Entity existence/Operation support, and parameter validation through the existing device catalog.
5. Persist the normalized definition. At Run admission snapshot it; at Step execution revalidate current device support. Schema validity is not permission to bypass runtime checks.

Go structs are representations of validated documents, not a separately authoritative DSL grammar. No definition model generator is required. Use the existing schema library directly; do not extend the Entity-type code generator to generate one Go file per household Automation.

Concrete module seam:

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

`DecodeAutomationDefinition` performs structural validation and typed/default decoding; semantic normalization/catalog checks remain in the service. `ValidateAutomationDefinition` applies the same schema to a typed definition for non-HTTP callers, not a second set of hand-maintained Go validation tags. Inject one compiled codec into the service and the API's schema registration. Keep schema-validator implementation types private. Module error `AutomationDefinitionValidationError` carries safe field paths/messages and is mapped to 422 by HTTP. These schema-backed routes also return 422 for malformed present JSON: with `SkipValidateBody`, Huma's RawMessage decoding reports it through its structural-error path. A missing required body is 400. Make this route-specific behavior explicit rather than adding a global Huma error override. Reuse the existing Entity codecs' number-preserving JSON decoding conventions rather than unmarshalling numbers through float64.

POST and PUT accept identical canonical definition bodies. PUT's required positive `expected_revision` is a query parameter, matching DELETE; IDs, revisions, timestamps, and Run status are not part of the DSL. Do not maintain a second handwritten definition schema in Huma tags or validators. Authoring tools can use the checked-in schema directly; HTTP tooling uses the existing OpenAPI document. No separate schema-serving endpoint is required.

Use Huma's existing explicit operation metadata: inputs have `Body json.RawMessage` (not `RawBody`), operations set `SkipValidateBody: true` and `RequestBody.Required: true`, and both JSON request bodies reference an `AutomationDefinition` OpenAPI component. Insert the canonical schema without keyword translation as `&huma.Schema{Extensions: document}`, where `document` is a number-preserving `map[string]any` decoded from `AutomationDefinitionSchema()`. Huma v2.39.1 serializes these entries inline, including standard JSON Schema keywords. Preserve `$schema`, `$id`, and future resource-local `$defs` intact. Do not unmarshal canonical schema JSON into `huma.Schema` fields: that supports only a subset and would silently lose keywords. The module's compiled JSON Schema validator is authoritative at runtime because Huma body validation is bypassed; query/path validation remains with Huma.

Concrete transport input shape in `api/automation_models.go`:

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

Thin handlers decode through the injected codec and pass the typed definition to the service, which also enforces the same schema for non-HTTP callers. Explicit decoding models with JSON tags map DSL field names into domain types; they do not introduce independent validation rules. Response Go models may remain explicit, but their definition portion must validate against the canonical schema in contract tests. Publish examples as data fixtures validated against this same source.

### Command identity seam: direct internal API change

Ownership: `internal/modules/devices/model.go`, `command.go`, `repository.go`, `sqlite_repository.go`, its API interface/callers, and tests. No external Command HTTP input gains caller-chosen Command or Correlation IDs.

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

Add `Service.ValidateCommand(ctx context.Context, input CommandInput) (CommandParameters, error)` in the same owner. It validates both supplied identities when nonempty, performs the same Entity ID/Operation/JSON/catalog validation, and returns an owned copy of normalized parameters, without generating IDs, writing records, checking temporary availability/enablement, or dispatching. Factor common validation; do not duplicate catalog rules. Execution still rereads current support, routing, enablement, and health at its existing stages.

Nonempty `input.ID` and `input.CorrelationID` replace their respective ID-generation calls after canonical validation; empty fields retain existing generation. Deadlines remain device-owned. The devices service must persist and use the supplied Correlation ID unchanged, including immediately terminal undispatched Commands. Existing direct HTTP callers leave both fields empty. Update every in-repository caller and fake directly; no legacy signature or compatibility wrapper.

Add `ErrCommandIDConflict = errors.New("command ID already exists")` beside the existing Command errors in `devices/repository.go`; `SQLiteRepository.CreateCommand` maps an ID uniqueness violation to this sentinel while preserving other errors. Duplicate IDs fail with zero dispatch, never return/replay the old attempt, and never enter terminal-outcome reconciliation. This is an internal error contract, not a new public input or an idempotent Command execution API.

### Automation service and dependencies

Ownership: `internal/modules/automations/service.go`, `automation_execution.go`.

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

`SQLiteRepository` owns transactional methods `CreateAutomation`, `UpdateAutomation`, `DeleteAutomation`, `AdmitManualRun`, `BeginAutomationStep`, `CompleteAutomationStep`, `CompleteAutomationRun`, `InterruptAutomationRuns`, and `PruneAutomationHistory`, plus the read methods above. Their arguments/results use these domain types, not sqlc rows; transactions stay inside repository methods. Clock and ID generators can be injected as unexported function fields in module tests; do not introduce a general clock framework for this slice.

Admission must serialize with updates, disablement, and deletion. In one transaction: look up an existing idempotency key first; otherwise load the latest definition, check the active-Run constraint, and write the Run, immutable Step snapshots, and key. Return only after commit. The service coordinates admission and worker registration under its lifecycle gate so shutdown cannot miss a committed Run. A crash between commit and worker launch leaves an interrupted Run on restart, not runnable backlog.

At each Step, generate a Command ID with `devices.NewCommandID` and an independent fresh ownership marker with `devices.NewCorrelationID`. Atomically transition pending → running with both reserved values, and commit before passing both to `ExecuteCommand`. Never obtain the ownership marker by reading an existing Command found under the reserved ID. This is deliberately two transactions: durable Step intent first, then existing durable Command creation. Neither reservation alone proves that a Command was created. Do not foreign-key `reserved_command_id` to a Command that does not yet exist.

Command ownership requires BOTH `record.ID == step.ReservedCommandID` and `record.CorrelationID == step.ReservedCorrelationID`. Use this same predicate for error reconciliation, Run-detail/history joins, and recovery inspection. An ID-only match must never populate the public `command_id`, status, or outcome fields. A Step with a persisted `command_id_conflict` failure never adopts a Command record, even if lookup finds a matching pair. The marker is already part of the devices Command record/wire contract; no new Command table or wire field is needed. Independent generation and non-reuse of the marker distinguish a Step from a pre-existing Command even if its Command ID collides, including a crash before the duplicate error can be recorded.

On successful outcome, commit Step status and then continue. On an execution error, handle confirmed duplicate/pre-creation failure before considering durable outcomes:

1. `ErrCommandIDConflict` fails the Step with `command_id_conflict`, marks later Steps not_attempted, and fails the Run. Do not reconcile the existing record or link it as evidence; no retry or later-Step dispatch is allowed.
2. Other confirmed validation/lookup failures before creation fail without adopting any record found under the reserved ID.
3. For ambiguous execution/persistence errors, inspect the reserved ID through `AutomationCommandRecords.GetCommand`. Only an ownership-matching terminal record is authoritative: map satisfied/dispatched to success and other terminal statuses to failure. `CommandExecutionError` alone does not establish durable terminality.
4. `ErrCommandNotFound` establishes that no Command exists and permits a failed Step without a Command ID. A mismatched ownership marker, nonterminal owned record, or other read error is uncertain execution, not success or an ordinary terminal Run; apply the sticky executor-fault policy below.

Recovery still interrupts active Runs without resuming Steps. A foreign ID match remains unlinked even when a crash prevented persistence of the duplicate/pre-creation failure; recovery must not turn that old Command into this Step's evidence.

On established failure, commit the failing Step, mark subsequent Steps not_attempted, and mark the Run failed. Known Command failure codes remain intact; pre-creation failures use `command_id_conflict`, `invalid_command`, `entity_not_found`, or sanitized `internal_error`. Unexpected errors never leak SQL, credentials, or adapter payloads. If execution is uncertain or result persistence fails, latch an executor fault: atomically close new Run and next-Step admission, report degraded readiness, retain affected active-Run claims, and require restart recovery. Never issue the next Step or silently mark an uncertain Run terminal. Other already-started Commands may finish and persist their known outcomes but cannot start another Step. Scheduler health recovery cannot clear this latch.

## SQLite contract

Modify `internal/platform/db/migrations/00001_initial.sql`; there are no deployments and no backfill/legacy path is required.

- `automations`: `id` PK, `revision` positive integer, `name`, `enabled` boolean, `triggers_json` (ordered canonical Trigger definitions), `steps_json`, UTC `created_at`/`updated_at`. Revision starts at 1 and increments on every successful PUT, including enablement-only changes.
- `automation_runs`: `id` PK, `automation_id` historical identity (no cascading definition FK), `revision`, `snapshot_json`, `source`, nullable `scheduled_at`, `matched_trigger_ids_json`, `status`, `started_at`, nullable `completed_at`, nullable `failure_code`, nullable `idempotency_key` (manual only). Snapshot includes all Trigger definitions, timezone, and enablement at admission. `matched_trigger_ids_json` is empty for manual Runs and holds all matching snapshot IDs in definition order for scheduled Runs; enforce source/list consistency.
- `automation_run_steps`: PK `(run_id, step_index)`, FK to Run with cascade on history pruning, `definition_json`, `status`, nullable unique `reserved_command_id`, nullable unique `reserved_correlation_id`, nullable `outcome`, nullable `failure_code`, nullable `started_at`/`completed_at`. Require both reserved fields to be absent or present together. Read actual Command evidence only through the ownership predicate on BOTH reserved values; Steps failed with `command_id_conflict` cannot link a record. An unrelated record sharing the reserved Command ID never contributes public Command ID/status/outcome, including after restart. Do not copy full Command observations or expose the internal correlation marker through the Run API.
- Partial unique index on `automation_runs(automation_id) WHERE status = 'running'` is the authoritative active-Run gate.
- Partial unique index on `(automation_id, idempotency_key) WHERE idempotency_key IS NOT NULL` is the manual retry gate.
- History indexes: `(started_at DESC, id DESC)` and `(automation_id, started_at DESC, id DESC)`; pruning index on terminal `completed_at`.
- CHECK constraints cover enums, positive revision, source/key/scheduled-time consistency, and terminal timestamp consistency. Validate JSON shape in the service and JSON validity in storage.

Manual keys: 1–128 printable ASCII characters excluding whitespace. A repeated key returns its original Run even after edits, completion, disablement, or definition deletion, while retained. Resolve the key before definition existence and active-Run checks. There is no invocation body to change under the same key. After retention, a deleted definition returns 404; a still-existing definition accepts that key as a new invocation. Do not expose keys in Run API responses or routine logs.

Add an automations query/generation block to `sqlc.yaml`, with query sources in `internal/modules/automations/dbqueries` and generated output in `internal/modules/automations/dbsqlc`. Update `mise.toml` generation-check copying/diffing to cover both modules. Generated Go is implementation output, not handwritten spec work.

## HTTP contract

App assembly adds automations explicitly to `NewHTTPHandler`; update all callers. Module registration owns `/v1` routes, stable Operation IDs, and mapping to RFC 9457 errors. Keep the existing Echo/Huma stack, no new authentication model or UI.

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

Transport request shapes (owned by `automations/api/automation_models.go`) are explicit, separate from domain types:

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

The Entity ID and parameters above are illustrative; actual values must validate against the chosen Entity's type. PUT uses the same definition body and supplies required `expected_revision` in the query. Required definition fields are `name`, `triggers`, and `steps`; omission of `enabled` explicitly defaults to false on both POST and PUT, never preserves prior enablement. An Automation response adds `id`, `revision`, `created_at`, `updated_at`; spec 2 adds the read-only household timezone. Run detail maps the public domain fields to snake_case JSON, omits absent nullable fields and the internal `ReservedCorrelationID`, and includes both `reserved_command_id` and ownership-verified `command_id` with their documented distinction. Summaries contain ID, Automation ID/name/revision, source, matched Trigger IDs (empty for manual Runs), scheduled/start/completion timestamps, status, and failure code, not Step arrays or parameter payloads. Run detail also includes `matched_trigger_ids`, interpreted against its snapshot rather than the live definition.

Canonical definition-schema and structural Huma errors are 422, including malformed present JSON on these RawMessage routes; a missing required body is 400. Semantic invalid IDs, duplicate Trigger IDs, cron, keys, Operation-specific parameters, and cursors are 400. Missing, noninteger, or nonpositive revision query parameters are structural Huma errors (422); valid but stale revisions are conflicts (409). Structural parameter errors such as a non-object `parameters` value belong to the canonical schema and are 422. Missing resources are 404. Stale revisions and active-Run admission/delete conflicts are 409 with stable codes `automation_revision_conflict` and `automation_run_active`. Admission during shutdown/degraded persistence is 503 `automation_unavailable`. Unexpected failures are sanitized 500. A later Command failure is a failed Run resource, never a delayed HTTP 502/504.

Pagination follows existing limits (default 50, 1–200), `limit+1`, `items: []`, and optional `next_cursor`. Run ordering is `(started_at DESC, id DESC)`. Versioned canonical base64url JSON cursors bind resource and Automation filter, reject extra/trailing data and cross-filter reuse, and provide continuation rather than snapshot isolation. Historical filtering never requires the live definition to exist.

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

Household timezone is required, must load as an IANA location (UTC allowed, empty/`Local`/raw numeric offsets rejected), and is loaded once at startup. Embed `time/tzdata` in app assembly for portable binaries. Timezone edits require restart. Retention zero selects 30×24 hours, nonzero minimum 24 hours; prune terminal Runs completed strictly before the cutoff hourly in bounded batches (500), with no startup sweep. Spec 2 shares this retention for occurrence/gap history. Update example config, README, and every in-repository Config fixture; no fallback to the host timezone.

Startup order: migrate → interrupt active Commands → interrupt active Runs/Steps and mark pending Steps not_attempted → start device transports/Observation consumer → construct automation service → open HTTP admission. Run interruption uses `core_restarted`; a running Step's existing Command result remains inspectable but does not change recovery policy.

Execution is process-owned, not request-owned. Worker calls must use a context independent of HTTP cancellation and shutdown cancellation while waiting for the current Operation's bounded lifecycle. Shutdown first atomically closes both new-Run admission and next-Step admission using the same lifecycle gate (`StopAutomationExecutionAdmission`), then stops and joins the scheduler. Beginning a Step (committing its reserved intent and registering it as in-flight) uses this gate: either it wins before closure and is drained as already started, or closure wins and it never invokes the devices service. Do not wait for scheduler shutdown before closing the Step gate. Keep NATS, health supervision, Observation consumption, and SQLite alive until current automation Commands return and Step outcomes are persisted; mark unfinished sequences interrupted with `core_stopping`. A Run that completed all Steps can still succeed. Join workers before tearing down dependencies. The existing HTTP five-second shutdown timeout is not a license to tear down active automation Command dependencies; drain may take the current Operation deadline plus its persistence allowance. A hard process kill falls back to startup interruption. Do not add replayable work or attempt to cancel already-dispatched Commands.

Because the Observation consumer currently receives the process context, assembly must give these drain-required dependencies a separately owned lifecycle context, canceled only after automation drain. Protect error-return cleanup paths as well as normal signal shutdown. Runtime readiness composes device readiness, recoverable scheduler health (spec 2), and the sticky executor-fault latch with logical AND. Closing normal shutdown admission also makes readiness false. No broader cancellation redesign for direct API Commands is required by this slice.

## Ownership and ordered deliverables

```text
CONTEXT.md                                      # modify — canonical Automation vocabulary
specs/
├── automation-execution.md                     # new — this contract
└── automation-cron-scheduling.md               # new — dependent scheduling contract
internal/
├── modules/
│   ├── automations/                            # new — owns definitions, admissions, Runs
│   │   ├── model.go, ids.go, errors.go           # new — domain model and invariants
│   │   ├── automation-definition.schema.json   # new — canonical JSON DSL structure
│   │   ├── automation_definition.go            # new — embedded schema, typed decoding and defaults
│   │   ├── testdata/automation-definitions/    # new — valid/invalid authoring fixtures
│   │   ├── service.go                          # new — management and manual admission
│   │   ├── automation_execution.go             # new — sequential worker and lifecycle
│   │   ├── cron_schedule.go                    # new in spec 1 — parser validation; expanded in spec 2
│   │   ├── sqlite_repository.go                # new — atomic writes and history reads
│   │   ├── dbqueries/automations.sql           # new — module SQL sources
│   │   ├── dbsqlc/                             # new/generated — sqlc output
│   │   ├── api/register.go                     # new — thin routes/handlers/errors
│   │   ├── api/automation_models.go            # new — public input/output mapping
│   │   ├── api/automation_schema.go            # new — lossless canonical schema publication in OpenAPI
│   │   ├── api/pagination.go                   # new — resource-bound cursors
│   │   └── *_test.go, api/*_test.go             # new — colocated risk-focused tests
│   └── devices/
│       ├── model.go, command.go                # modify — reserved identities and shared validation
│       ├── repository.go, sqlite_repository.go # modify — distinguish duplicate Command ID creation
│       └── api/, *_test.go                     # modify — all signature callers and regression tests
├── app/hearthd/
│   ├── config.go, run.go, server.go             # modify — assembly, readiness, config, drain and recovery
│   └── *_test.go                               # modify/new — HTTP and recovery integration
├── app/zigbee2mqtt/*_test.go                    # modify — direct ExecuteCommand callers
└── platform/db/migrations/00001_initial.sql     # modify — Automation/Run schema
sqlc.yaml, mise.toml                            # modify — both modules generated and checked
go.mod, go.sum                                 # modify — pinned cron parser dependency
configs/hearthd.example.yaml, README.md          # modify — config and curl workflow
```

| ID | Outcome | Effort | Owner paths | Dependencies | Acceptance |
|---|---|---|---|---|---|
| E1 | Reserved Command/Correlation input, duplicate-ID error, and no-side-effect validation; all callers updated | M | `devices/model.go`, `command.go`, `repository.go`, `sqlite_repository.go`, `api/`; `devices/`, `app/hearthd/`, `app/zigbee2mqtt/` caller tests | — | A1 |
| E2 | Schema-backed multi-Trigger JSON DSL and codec, Definition/Run model and provenance, cron syntax validation (including pinned parser dependency), SQLite atomic admission/snapshots/history | L | `automations/automation-definition.schema.json`, `automation_definition.go`, `testdata/automation-definitions/`, `model.go`, `ids.go`, `errors.go`, `cron_schedule.go`, `sqlite_repository.go`, `dbqueries/`, `dbsqlc/`; migration; sqlc/mise | E1 | A2–A4, A8, A11, A12 |
| E3 | Manual Run executor, result persistence, lifecycle and recovery | L | `automations/service.go`, `automation_execution.go`; `hearthd/run.go`, `config.go`, tests | E2 | A5–A7, A9 |
| E4 | HTTP management, canonical schema in OpenAPI, manual invocation, inspectable history and docs | L | `automations/api/`; `hearthd/server.go`, tests; configs/README | E3 | A3, A4, A8, A10–A12 |
| E5 | Cross-boundary regression and full validation | M | colocated module/API tests; `hearthd/recovery_integration_test.go`, `server_test.go` | E4 | A1–A12 |

## Acceptance and fault-focused verification

- **A1 — Identity before effect:** inside a fake Command sender, a separate read sees the running Step's reserved Command and Correlation IDs matching both values in the persisted Command. A duplicate supplied Command ID returns `ErrCommandIDConflict` with zero sends, not the old outcome. A failed Step-intent commit means zero sends; invalid target/parameters creates no Command. Direct callers retain existing generated identities, outcomes, and cancellation behavior.
- **A2 — Durable admission:** concurrent admission against real migrated SQLite admits exactly one Run per Automation; separate Automations remain independent. Transaction failure creates neither partial Run nor stray key/Steps. A definition edit race snapshots one whole revision, never mixed fields.
- **A3 — Safe manual retries:** same-key concurrent requests return one Run, including after completion, edit, disablement, and deletion. Different keys conflict only while active. After pruning, key reuse follows the documented new-invocation behavior.
- **A4 — Management invariants:** save validates every Step without dispatch; unknown targets/unsupported Operations fail; temporarily disabled/unavailable targets can be saved. Stale PUT/DELETE fails without change. Delete cannot race past active admission. Disabled manual invocation works.
- **A5 — Ordered outcomes:** delayed first Command prevents second dispatch. Both satisfied and dispatched outcomes advance. Every failure class stops later Steps, preserves earlier effects, and distinguishes failures with/without a Command record. No retries or rollback sends occur. Inject a Command outcome-persistence error: a nonterminal or unreadable durable record must retain the active claim and latch executor failure rather than becoming an ordinary failed Run; only an ownership-matching terminal record supplies the authoritative outcome. Prepopulate an otherwise identical successful direct Command, force its ID into a new Step while generating a fresh correlation marker, and assert failed Run, zero current/later-Step sends, unchanged old Command, and no public linkage to it. Repeat for both satisfied and dispatched old records. A known duplicate failure must not reconcile success even if a test supplies the old correlation marker too.
- **A6 — Snapshot isolation:** edit/disable during a blocked Step leaves the Run's name, revision, order, parameters, and timezone unchanged; the next Run sees the edit.
- **A7 — Recovery:** crash windows before Command creation, after Command creation, and after Command completion all retain attribution and interrupt the Run with zero restart dispatch. Already terminal Step evidence remains; pending Steps are not_attempted. Also crash after a Step reserves a colliding Command ID but before it records the creation failure: the independent correlation marker must prevent the old terminal Command from appearing as this Step's evidence on recovery or in history. Test through real SQLite plus embedded NATS, not only fake repository calls.
- **A8 — History:** deleted-definition history remains accessible; cursor misuse is rejected; paging remains valid under new inserts/pruning. Cutoff equality survives, older terminal history is removed, active Runs survive; pruning cannot remove deduplication evidence for retained Runs.
- **A9 — Lifecycle:** cancel the manual HTTP request after admission and observe continued execution. During shutdown admit no new Run and start no subsequent Step, even while scheduler joining is blocked; keep dependencies alive for the current outcome, persist interruption, and join workers. Persistence failure prevents all later Commands and degrades admission/readiness.
- **A10 — HTTP/OpenAPI:** real Echo/Huma requests establish routes, bodies, status/error codes, required idempotency and revision inputs, disabled default, asynchronous response, and sanitized errors. No endpoint accepts caller-assigned Command/Correlation IDs or exposes the internal reserved correlation marker.

- **A11 — Canonical DSL:** validate hand-authored valid/invalid fixtures against the checked-in schema, codec, and real HTTP boundary. Reject unknown definition/Trigger/Step properties, the legacy singular `trigger` field, missing required fields, malformed Trigger ID slugs, invalid kinds, empty/oversized Trigger and Step arrays, and non-object parameters before typed decoding. Unknown properties inside parameters remain catalog-owned. Omitted enablement explicitly becomes disabled on both creation and replacement; PUT requires a positive revision query and rejects revision metadata inside the definition body. Schema-valid but unsupported Operation parameters and invalid cron fail semantic validation without writes or sends. Numeric values retain precision. Serialized OpenAPI's shared definition component is structurally identical to the embedded schema and both POST/PUT request bodies reference it. Verify response definition projections and documentation examples against the canonical schema. Exercise the real Huma RawMessage/SkipValidateBody path for missing required body (400), malformed present JSON (422), and well-formed schema-invalid JSON (422), rather than assuming Huma's default decoding error mapping is unchanged. An isolated schema-publication fixture with `$defs`/`$ref` must retain those keywords, preventing silent subset conversion.

- **A12 — Multiple Trigger identity and provenance:** save/retrieve a definition with multiple cron Triggers without changing IDs/order; reject duplicate IDs even when expressions differ. Permit duplicate expressions with distinct IDs. Schema and service tests cover 1 and 32 Triggers, and reject 0 and 33. Manual Runs always record an empty matched-ID array. Scheduled admission records every matching ID once in snapshot order; multiple matching Triggers never duplicate Step execution. Reordering/editing a live definition does not change historical Trigger snapshots or matched IDs. Spec 2's atomic coalescing tests establish one scheduled Run or one overlap skip per Automation/minute.

Use deterministic barriers/channels rather than sleeps for ordering and shutdown; use real SQLite for uniqueness, transaction, and join assertions. Each test's oracle is the behavior above, not the production evaluator. Preserve existing Command tests while updating signatures. A fake returning success alone does not establish physical outcomes, SQL atomicity, or recovery behavior.

Implementation verification: `mise run validate` (regenerates, formats, tidies, then checks); focused commands only through the repository mise tasks. Review the resulting diff including generated output. No real devices are needed for this contract; this document authorizes no physical actuation.

## Risks and follow-up

- Schema/OpenAPI/model drift: one structural source, lossless publication through the existing Huma extension map, and shared positive/negative contract fixtures; no independent handwritten request grammar or schema translator.
- Reserved intent may exist without an owned Command after validation/crash: test both-ID ownership matching and collision error handling in execution and history; never adopt an old terminal Command or manufacture a record to fill an audit gap.
- Partial effects are unavoidable: stopped/interrupted Runs retain per-Step evidence, and manual retry needs a new explicit invocation key rather than automatic recovery.
- Shutdown drain depends on Operation deadlines and persistence bounds: keep dependencies alive; hard-kill tests verify no recovery replay.
- Audit data contains historical parameters: bounded retention and no payloads/idempotency keys in routine logs reduce unnecessary exposure.

### Future priorities without speculative runtime features

The next priorities are context-aware behavior and sustained-condition Triggers. Preserve these distinctions while implementing this slice:

- **Cause versus permission:** matching Triggers explain why execution was considered; future Conditions decide whether it should proceed. OR-trigger semantics must not become an implicit AND-condition language.
- **Complete trigger context:** current cron provenance consists of all matching IDs, scheduled instant, timezone, and the immutable definition. Future State/event Triggers will need their own captured evidence and Condition evaluation inputs, not retrospective reads masquerading as historical facts. Do not add empty variables/template contexts to the current JSON DSL.
- **Eligibility versus suspended execution:** “absence has held continuously for five minutes” belongs to Trigger eligibility; new motion resets that eligibility before any Run is admitted. It does not require a Run to sleep between Commands. A later spec must choose restart/initial-State/staleness behavior for that eligibility timer; the current interrupted-Run policy does not silently decide it.
- **Admission versus Commands:** keep overlap decisions at the shared Run-admission seam and Command execution in the existing devices lifecycle. No distributed per-Step overlap checks, plugin registry, alternate execution policies, or unimplemented schema branches are needed now.

Context-aware Conditions and sustained-condition Triggers remain future specifications, not promised initial capabilities. Cancellable action sequences and restart-surviving suspended Runs were not selected for this planning branch and remain deferred. CLI/UI, scenes/groups, and reusable/parameterized routines also remain out of scope. Compare the current Home Assistant inventory against these boundaries before claiming household replacement coverage; representability with extensions is not first-release support.
