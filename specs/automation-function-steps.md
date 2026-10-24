# Automation Function Steps

Status: Approved for implementation by explicit user request on 2026-10-23.
Effort: L–XL overall, with the evidence/lifecycle seam carrying most uncertainty.

## Problem

Household authors can invoke Adapter Functions through HTTP, MCP, and the built-in
agent, but cannot include them in Automations. For example, an Automation cannot
request Zigbee2MQTT `refresh_entity`, even though an operator can invoke it manually.
Functions are deliberately distinct from Entity Operations and Commands.

Allow a reached Automation Step to invoke one Adapter Function with fixed JSON
arguments. Reuse Adapter-owned routing, validation, deadlines, and durable
invocation evidence rather than introducing another execution engine.

## Agreed product decisions

- Fixed JSON arguments only. Function results are evidence, not workflow variables.
- HTTP and MCP support authoring and history. The built-in agent uses the existing
  MCP tools. Browser definitions and history support read-only display.
- Survive Adapter restarts when the declared Function contract is unchanged.
  Reject changed contracts before dispatch; do not silently adopt them.
- Continue only after authoritative, retained Function success. All other outcomes
  stop the Run. No retry, fallback, continuation-on-error, or restart replay.

## Current architecture and constraints

- `internal/modules/automations/definition.go:16` defines a sealed Step family.
  `definition_wire.go:82` rejects unknown kinds; `branch_execution.go:24` executes
  sequentially. Current kinds are command, notification, if, choose, and delay.
- Automations owns definitions, immutable Run snapshots, reached-Step evidence,
  and Run outcomes. Adapters owns Function catalogs and Invocation evidence.
  No cross-module SQL writes or shared transaction is allowed.
- `notification_execution.go:10` durably reserves an attempt link before calling
  Notifications. Function Steps need the same pre-effect link.
- `internal/modules/adapters/function_service.go:85` performs one bounded invocation.
  Its repository currently generates the invocation ID inside `Admit`
  (`sqlite/function_invocations.go:51`), too late for a durable Automation link.
- `function_invocation.go:29` pins an invocation to a Session catalog revision;
  the repository rechecks the revision, descriptor, and active runtime transactionally.
  Preserve these checks even when an Automation accepts a newer equivalent Session.
- Function discovery remains available offline. Callable means an active runtime,
  not necessarily healthy upstream evidence. Do not add Command health policy.
- `internal/app/hearthd/shutdown.go:194` currently drains Functions before Automations.
  That order must change for the new dependency.

## Proposed behavior

### Definition and discovery

Proposed Step document, usable anywhere a side-effect leaf is permitted:

```json
{
  "id": "refresh-weather",
  "kind": "function",
  "adapter_id": "zigbee2mqtt",
  "function_key": "refresh_entity",
  "contract_fingerprint": "sha256-v1:<64 lowercase hexadecimal digits>",
  "arguments": {"entity_id": "ent_<canonical UUIDv7>"}
}
```

The fingerprint is copied from Function discovery, not invented by the author.
Expose `contract_fingerprint` on HTTP/MCP Function list and detail projections.
Do not add per-Function MCP tools or change the existing public invocation input.

At create/replace, visit every arm. Validate the Adapter slug, Function key,
fingerprint syntax, bounded object arguments, and current discovered descriptor.
Compare the supplied fingerprint and validate/normalize arguments against its
input schema. Missing references, contract mismatches, or invalid arguments reject
the save with a safe Step-specific error. Retained offline catalogs may validate a
definition; an active runtime is not required to save. Operational read failures
remain infrastructure errors rather than being relabeled invalid input.

The client must explicitly rediscover and replace a fingerprint to accept a changed
contract, using the existing optimistic Automation revision check. Saving unrelated
edits must not automatically repin Function Steps.

Structural decoding and repository writes validate shape and bounds without
consulting live catalogs. Read/history decoding never requires an online Adapter.

Function leaves count toward the existing combined limit of 32 Command,
Notification, and Function leaves. Keep 64 total nodes, depth 8, 1–32 children per
present sequence, and the current 66,080-byte raw/normalized/stored definition
limit. Arguments retain the Function profile's object/value/depth limits, further
bounded by the total definition limit. No limit increase is part of this feature.

### Stable declared-contract fingerprint

Adapters owns fingerprint calculation and publishes it as derived discovery data.
Define `ContractFingerprint` as `sha256-v1:` followed by 64 lowercase hex digits.
Hash UTF-8 bytes of `hearth.adapter-function-contract.v1\n` followed by the compact
JSON encoding of this ordered struct:

```go
struct {
    Key          FunctionKey     `json:"key"`
    InputSchema  json.RawMessage `json:"input_schema"`
    OutputSchema json.RawMessage `json:"output_schema"`
    TimeoutMS    int             `json:"timeout_ms"`
}
```

Use schema documents returned by `PrepareDescriptor`/`functionschema.Compile`.
They sort object members and preserve number literals. Whitespace and object-key
order are ignored. Array order and numeric spelling remain significant; this is
exact normalized-document matching, not semantic JSON Schema equivalence.
Schema annotations participate. The outer descriptor description, Adapter ID,
runtime ID, and catalog revision do not participate. Adapter identity is matched
separately, so unrelated Adapter instances are never interchangeable.

This fingerprint is not authentication, a behavior version, or an assurance that
vendor behavior is unchanged. Adapters remain responsible for honoring their
declared contract. No historical descriptor archive or SDK/wire change is needed.

### Reached execution

1. Apply existing Run execution gates. Generate a fresh reserved `afi_` invocation
   ID and durably append a running Function execution under the immutable snapshot.
   If that write fails, perform no Function call and use Run-scoped fault cleanup.
2. Call an internal Adapter service entry point with that ID, Adapter/key, expected
   fingerprint, and normalized arguments. Resolve the current catalog once.
3. Compare the fingerprint, prepare the normal revision-pinned invocation, and
   admit it transactionally through the existing runtime/descriptor checks.
   A catalog change between resolution and admission fails without dispatch;
   do not automatically rediscover or try another runtime in that attempt.
4. Make at most one application request, with the normal Function absolute deadline.
   The committed invocation belongs to the Function service, not the HTTP caller
   or Automation admission context. Keep dependencies alive until it finishes.
5. Read authoritative terminal evidence by the reserved ID. Verify ID, Adapter/key,
   contract fingerprint, normalized arguments, terminal evidence, and timestamps
   against the Run snapshot. Do not infer success from an RPC return alone.
6. Atomically commit Function Step completion and any failed parent Run. Advance
   only after retained `Succeeded` evidence and that Automation write both succeed.

Unselected/unreached Function Steps have no execution row or reserved ID. History
can derive “not attempted” by comparing the snapshot tree with reached evidence.
Definitions edited, disabled, or deleted during a Run do not change its snapshot.
Functions do not create Commands, synthetic Observations, or Device Facts.

| Situation | Function Step / Run result |
| --- | --- |
| Retained `Succeeded` | Step succeeded; execute next Step |
| Retained `Failed`, `NotExecuted`, or `Unknown` | Step failed; Run failed; preserve exact invocation outcome and reason |
| Known pre-admission missing Adapter/Function, changed contract/catalog, inactive runtime, invalid arguments, or closed internal admission | Step failed; Run failed; no fabricated invocation outcome |
| Invocation-ID conflict, persistence failure, missing/mismatched/nonterminal authoritative evidence, or unverifiable progress | Run-scoped `executor_fault` interruption; preserve reservation; never advance or retry |
| Shutdown before reservation | Interrupt Run under existing `core_stopping` policy; no new row/call |
| Shutdown after reservation | Allow this reserved call to settle; never start the following Step |
| Restart with unresolved work | Interrupt running Function evidence and Run as `core_restarted`; Adapter recovery independently marks unfinished invocations unknown; never resume |

Pre-admission reason codes are `adapter_not_found`, `function_not_found`,
`function_contract_changed`, `catalog_changed`, `adapter_unavailable`,
`invalid_function_arguments`, and `function_admission_closed`.
Map only documented definitive pre-admission errors to these codes. In particular,
a failed/uncertain persistence operation does not establish “not executed.”
After possible external effects, retained `unknown` is a failed Run, not permission
to retry. A later operator invocation is a separate attempt and can repeat effects.

## Types and interfaces

### Adapters-owned seam (D1)

Proposed additions to `internal/modules/adapters/function_types.go`:

```diff
 type FunctionKey string
+type ContractFingerprint string
@@
 type Function struct {
     AdapterID       string
     CatalogRevision CatalogID
     Descriptor      Descriptor
     Callable        bool
+    ContractFingerprint ContractFingerprint
 }
```

New types and methods, owned by `function_contract.go` and `function_service.go`:

```go
type ContractInput struct {
    AdapterID           string
    FunctionKey         FunctionKey
    ContractFingerprint ContractFingerprint
    Arguments           json.RawMessage
}

type ReservedInvokeInput struct {
    InvocationID InvocationID
    ContractInput
}

func FingerprintDescriptor(Descriptor) (ContractFingerprint, error)
func (s *FunctionService) ValidateContractInput(context.Context, ContractInput) (json.RawMessage, error)
func (s *FunctionService) InvokeReserved(context.Context, ReservedInvokeInput) (Invocation, error)
func (s *FunctionService) ClosePublicAdmission()
```

`ValidateContractInput` is the save-time boundary. It checks the retained
descriptor/fingerprint and returns owned normalized
arguments without checking runtime callability or executing. `InvokeReserved`
requires a valid caller-reserved ID and repeats current contract validation at
execution. Keep validation names/errors explicit; add `ErrFunctionContractChanged`
and `ErrInvocationIDConflict`. Fingerprint failures from corrupt stored descriptors
are persistence errors, not valid mismatches.

Extend immutable `PreparedInvocation` to carry the selected invocation ID.
The public `Invoke` service generates its own ID; internal `InvokeReserved` uses
the supplied ID. `FunctionRepository.Admit` consumes that prepared identity and
no longer generates it. An existing ID rejects with `ErrInvocationIDConflict`,
even for identical input. It never dispatches, joins, or replays an earlier attempt.
No caller-supplied ID or idempotency facility is exposed over HTTP/MCP/NATS.

The changed preparation signature makes identity validation explicit:

```diff
-func PrepareInvocation(function Function, input InvokeInput, start time.Time) (PreparedInvocation, error)
+func PrepareInvocation(function Function, input InvokeInput, id InvocationID, start time.Time) (PreparedInvocation, error)
```

Add `PreparedInvocation.ID() InvocationID`; preparation rejects an invalid ID.
Migrate internal callers and their fixtures together.

Reuse the existing public `GetFunction` and `GetInvocation` methods. Preserve
public `InvokeInput` and its required Session catalog revision. Keep the public
entry point named `Invoke`; it checks the new public gate as well as all-admission.
`InvokeReserved` bypasses only the public gate. `StopAdmission`/`Drain` still close
all admission and join work. Synchronize the public gate and acquisition as in
Notifications; do not build a new general lifecycle framework.

### Automations-owned definition and consumer seam (D2)

Proposed changes to `definition.go` and `dependencies.go`:

```diff
 type StepBody interface{ isStepBody() }
+
+type FunctionStep struct {
+    AdapterID           string
+    FunctionKey         adapters.FunctionKey
+    ContractFingerprint adapters.ContractFingerprint
+    Arguments           json.RawMessage
+}
+func (FunctionStep) isStepBody() {}
@@
 type Dependencies struct {
+    Functions       AutomationFunctions
+    NewInvocationID func() (adapters.InvocationID, error)
```

```go
type AutomationFunctions interface {
    ValidateContractInput(context.Context, adapters.ContractInput) (json.RawMessage, error)
    InvokeReserved(context.Context, adapters.ReservedInvokeInput) (adapters.Invocation, error)
    GetInvocation(context.Context, adapters.InvocationID) (adapters.Invocation, error)
}
```

Add `StepKindFunction = "function"`. Update all exhaustive switches, recursive
codecs, copies, collectors, leaf counters, definition validation, snapshot checks,
and public schema variants. Opaque Function arguments are not Entity references:
Core must not inspect `entity_id` or other vendor fields inside them. A missing
Functions dependency rejects saving/executing Function Steps, not unrelated Steps.

### Automations-owned execution evidence (D2/D3)

New sealed evidence types in `function_evidence.go`:

```go
type FunctionStart struct {
    RunID        RunID
    StepID       StepID
    Position     int // reached Function execution order, 0..31
    InvocationID adapters.InvocationID
    StartedAt    time.Time
}

//sumtype:decl
type FunctionExecutionState interface { functionExecutionState() }
type FunctionRunning struct{}
type FunctionSucceeded struct {
    CompletedAt     time.Time
    CatalogRevision adapters.CatalogID
}
type FunctionFailed struct {
    CompletedAt time.Time
    Evidence    FunctionFailure
}
type FunctionInterrupted struct {
    CompletedAt time.Time
    ReasonCode  string // core_stopping, core_restarted, executor_fault
}

//sumtype:decl
type FunctionFailure interface { functionFailure() }
type FunctionAdmissionRejected struct { ReasonCode string }
type FunctionInvocationFailed struct {
    CatalogRevision adapters.CatalogID
    Outcome         adapters.Evidence // only Failed, NotExecuted, or Unknown
}

type FunctionExecution struct {
    FunctionStart
    State FunctionExecutionState
}
type FunctionCompletion struct {
    RunID        RunID
    StepID       StepID
    InvocationID adapters.InvocationID
    State        FunctionExecutionState // terminal variants only
}
```

Implement sealed marker methods and exhaustive status mappings. Independent
repository entry points validate variant restrictions, nonzero UTC times, ownership,
snapshot Step kind, contiguous position, and reservation identity. Arguments and
results remain in Adapter-owned invocation records; do not copy raw result payloads
into Automation evidence. The Automation snapshot already retains fixed arguments.

Proposed additions to the existing `Repository` interface:

```diff
 type Repository interface {
     DefinitionRepository
+    RecordFunctionStart(context.Context, FunctionStart) error
+    CompleteFunction(context.Context, FunctionCompletion) error
```

Add `Functions []FunctionExecution` to `Run`. `RecordFunctionStart` requires a
running parent and matching snapshot leaf. `CompleteFunction` verifies the running
reservation and commits terminal Step evidence plus failed/interrupted parent
together. Success does not terminalize the parent. Extend existing completion,
executor-fault cleanup, startup interruption, and successful-Run checks so a
terminal Run cannot retain a running Function row.

## Persistence and retention (D3)

Add the next migration, currently `00018_automation_run_functions.sql`. Do not edit
historical migrations. Create Automation-owned `automation_run_functions` with:

- `run_id` foreign key to `automation_history`, cascading on history deletion;
  `step_id`; `position`; globally unique reserved `invocation_id`; `started_at`.
- `status` in running/succeeded/failed/interrupted; nullable `completed_at`,
  `catalog_revision`, `invocation_outcome`, and `reason_code`.
- Primary key `(run_id, step_id)`, unique `(run_id, position)`, and a partial unique
  index allowing only one running Function execution per Run.
- Checks: running has no terminal columns; succeeded has time/revision and outcome
  succeeded with no reason; failed has either an allowed pre-admission reason and
  no revision/outcome, or time/revision plus failed/not_executed/unknown and reason;
  interrupted has time and an allowed interruption reason, no invocation outcome
  or catalog revision. Start and completion times are diagnostic evidence, not
  lifecycle-order constraints; completion may precede start after clock correction.

Migration `00019_function_diagnostic_timestamps.sql` removes the chronology CHECK
introduced in migration 00018 by rebuilding the table without changing rows,
indexes, foreign keys, or other constraints. Its Down restores the old CHECK and
fails atomically if retained rows cannot satisfy it, rather than altering evidence.

The reserved invocation ID is an evidence link, not a cross-module foreign key.
Adapters and Automations retain separate transactions and pruning policies.
Function records remain subject to their existing 30-day terminal retention.
Automation evidence survives independently under Automation history retention and
remains readable when linked invocation detail has been pruned. Do not require a
live join to Adapters to render history, or extend Function retention implicitly.

Run start, Function reservation, Invocation admission, and completion may have
regressed wall times. Validate snapshot membership, identities, contiguous reached
positions, prior success, and legal transitions rather than timestamp inequalities.
Failure/interruption still commits the same completion time and reason on child
and parent atomically; earlier successful children need not have earlier wall times
than parent completion. Startup interruption succeeds with regressed time, preserves
observed timestamps, and never rewrites terminal evidence on repeated recovery.
Invocation ownership matching retains descriptor/argument/identity checks and
deadline validation, but does not compare admission time to reservation time.

Existing definitions, revisions, and Run snapshots need no rewriting; existing
history projects `functions: []`. Migration 00018 Down drops Function execution
evidence only. Older binaries cannot decode new Function definitions/snapshots; binary
rollback requires a matching pre-feature database backup, not merely migration
Down. Test fresh migration, upgrade with existing history/children, and cascade.

## Lifecycle and failure discipline (D4)

Assembly injects the concrete Function service into Automations and provides the
default ID constructor. No Adapters import of Automations is introduced.

At shutdown: close public Function admission, Agent admission, Automation admission,
and the existing public gates. Join Agent turns first. Join Automation Runs while
reserved Function and notification entry points remain available. Then close all
Function admission and join Functions; join Notifications and Commands before
withdrawing registration transports, NATS, dependency contexts, or SQLite.
No Step following a shutdown observation starts. A persisted reservation is
already admitted Run work and may finish even if public admission has closed.

Keep Function recovery before public admission and Automation startup interruption
before Run execution. Neither recovery path invokes a Function or repairs a Run
into success based on late evidence. Do not add a reconciliation worker.

Persistence faults stop only the affected Run using existing bounded, single-attempt
cleanup. Preserve durable terminal invocation evidence even when the Automation
completion write fails. Failed cleanup can leave only that Automation busy until
restart; it must not close unrelated admission or readiness.

## Public surfaces and browser (D5)

- Extend the existing v2 Automation definition and history schemas with `function`
  and a required `functions` array on Run detail, including empty arrays on old Runs.
  Existing Step documents and endpoints keep their meaning. A new union variant
  requires consumer updates; older strict clients are not promised to decode it.
- Each execution projects `step_id`, `position`, `invocation_id`, `status`,
  `started_at`, and terminal `completed_at`. Succeeded has catalog revision and
  invocation outcome succeeded. Failed invocation evidence has revision, exact
  invocation outcome, and reason. Pre-admission failures and interruptions omit
  revision/outcome. Never label a reserved identity proof of actual dispatch.
- Keep history summaries compact under current conventions. HTTP and MCP detail
  expose the same canonical evidence, while MCP retains its independent projections.
  Do not add a second nested result copy; callers use Function invocation detail
  for arguments/results while retained. Preserve numeric literals through the
  existing raw-JSON MCP/HTTP handling.
- New Function reference/contract create/replace failures use HTTP 422/Problem
  Details and MCP invalid-definition conventions, including Step paths. Preserve
  existing HTTP 400 structural-definition responses. This corrects the initial
  planning assumption that existing definition validation uniformly used 422.
  The public one-off
  Function invocation endpoint retains its existing 409 stale-revision behavior.
- Extend browser API types, exhaustive tree traversal, detail rendering, and history
  evidence. Show Adapter/key, fingerprint, bounded argument JSON, status/reason,
  and invocation identity; clearly distinguish no terminal invocation evidence.
  Browser rendering must not require Function discovery, poll, or invoke Functions.
  Authoring controls and a new Function history browser are excluded.
- Update operator guidance, architecture, glossary, and the earlier Function spec's
  Automation exclusion to point to this extension rather than leaving contradictory
  current guidance.

## Proposed project layout

```text
internal/
├── modules/
│   ├── adapters/
│   │   ├── function_contract.go                 # new: fingerprint and contract validation (D1)
│   │   ├── function_types.go                    # modify: derived fingerprint/reserved input/errors (D1)
│   │   ├── function_invocation.go               # modify: prepared caller-owned identity (D1)
│   │   ├── function_service.go                  # modify: internal invocation/public admission gate (D1)
│   │   ├── httpapi/, mcpapi/                    # modify: Function discovery projections (D1)
│   │   └── sqlite/function_invocations.go       # modify: prepared identity, conflict rejection (D1)
│   └── automations/
│       ├── definition.go, definition_wire.go    # modify: Function Step variant (D2)
│       ├── definition_validation.go             # modify: all-arm references and limits (D2)
│       ├── branching.go, dependencies.go        # modify: kind/consumer seam/default ID (D2)
│       ├── function.go                          # new: structural normalization (D2)
│       ├── function_evidence.go                 # new: sealed execution evidence (D2/D3)
│       ├── function_execution.go                # new: reserve/invoke/verify/complete (D4)
│       ├── branch_execution.go, repository.go   # modify: execution dispatch and persistence seam (D3/D4)
│       ├── run.go, snapshot_steps.go            # modify: Run evidence/snapshot traversal (D2/D3)
│       ├── automation-*.schema.json             # modify: definition/history canonical schemas (D2/D5)
│       ├── httpapi/, mcpapi/                    # modify: models, mapping, published contracts (D5)
│       └── sqlite/
│           ├── functions.go                    # new: atomic Function evidence persistence (D3)
│           ├── dbqueries/functions.sql         # new: module-owned queries (D3)
│           ├── dbqueries/automations.sql       # modify: recovery/completion/cleanup (D3)
│           └── dbsqlc/                         # generated by mise (D3)
├── app/hearthd/
│   ├── run.go, shutdown.go                     # modify: injection and dependency drain ordering (D4)
│   └── automation_functions_*_test.go           # new: lifecycle/integration/public journeys (D4/D6)
└── platform/db/migrations/
    └── 00018_automation_run_functions.sql       # new: additive evidence table (D3)
web/src/
├── api/types.ts                                # modify: read-only Function definition/history types (D5)
└── pages/
    ├── automation-step-tree.ts                 # modify: bounded traversal/counts (D5)
    ├── AutomationStepTree.tsx                   # modify: Function leaf display (D5)
    └── AutomationDetailPage.tsx                 # modify: Function execution evidence (D5)
docs/automation-function-steps.md                # new: authoring/outcomes/upgrade guidance (D6)
docs/adapter-functions.md, docs/architecture.md   # modify: supported invocation paths/ownership (D6)
GLOSSARY.md, specs/adapter-functions.md          # modify: terminology and superseded exclusion (D6)
```

Update additional exhaustive collectors/mappers beside these named anchors, without
reorganizing unrelated files. Unit/integration tests live with each owning module.
Generated contracts are regenerated through the repository's existing mise tasks;
no new generator, SDK API, wire subject, stream, dependency, or configuration is needed.

## Deliverables and order

| ID | Outcome | Effort | Owners | Depends on | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D1 | Stable discovery fingerprint and reserved-ID Function service, preserving public invocation semantics | L | Adapters domain, SQLite, HTTP/MCP | — | A1, A2 |
| D2 | Function Step definitions, normalization, all-arm validation and snapshot variants | M | Automations domain/codecs/schema | D1 | A3 |
| D3 | Reached Function evidence, additive migration, retention/recovery/cleanup invariants | L | Automations domain/SQLite/shared migration | D2 | A4, A7 |
| D4 | Sequential invocation and evidence verification, assembly and shutdown ordering | L | Automations executor, hearthd assembly | D1–D3 | A5–A8 |
| D5 | HTTP/MCP definition/history and read-only browser compatibility | L | Both module transports, web | D1–D4 | A9 |
| D6 | Public journey checks, operator guidance, generated artifacts and full validation | M | hearthd tests, docs, contracts | D1–D5 | A10 |

These are scope estimates, not scheduling promises. D1 must prove identity and
admission behavior before dependent execution is built. Each unit ends with its
focused checks; final integration includes the full repository gate.

## Acceptance and validation

Follow `docs/testing.md`: complete decision matrices at the owner, real SQLite for
transactions, real NATS for routing/lifecycle, production Core for public journeys.
No actual household Function invocation is required for implementation validation.

| ID | Exercise | Expected result | Check boundary |
| --- | --- | --- | --- |
| A1 | Restart with changed catalog/runtime but same descriptor; reorder schema object keys; change outer description; then alter input/output schema or timeout | Same fingerprint for equivalent normalized documents/outer description; different fingerprint for any declared contract change; fixed known digest fixture | Adapters unit and discovery transport tests |
| A2 | Internal reserved ID, duplicate ID, public stale catalog revision, catalog replacement between resolve/admit | Exactly the reserved ID is retained and dispatched once; duplicate/conflict and stale admission never dispatch; public revision policy unchanged | Adapters unit plus real SQLite integration |
| A3 | Create/replace valid Function Steps in nested arms; offline retained catalog; wrong fingerprint, unknown reference, non-object/duplicate-key arguments, invalid schema values, oversize definition and mixed 33 leaves | Valid definitions normalize; all invalid cases reject at their independent production boundaries; unchanged existing variants remain accepted | Automations domain/transport units plus save integration |
| A4 | Reserve/complete under immutable Run snapshot; invalid Step kind/ID/position/state; failed start transaction; history cascade and old-data migration | Reservation precedes effects; invalid mutations reject; failure leaves no partial row; old definitions/history unchanged and old Runs show empty functions | Real Automation SQLite integration |
| A5 | Command → Function → Delay/Notification; nested selected/unselected arms; Function-only selected sequence; automatic and manual admission | Correct ordered payloads; only reached Function leaves invoked; busy rule and Conditions unchanged; retained success gates advancement | Executor owner tests and focused assembly integration |
| A6 | Retained failed/not_executed/unknown, missing/offline/changed Function, mismatched terminal record, terminal persistence failure, Automation completion write failure | No next Step; truthful failed versus interrupted Run; reservation and authoritative evidence retained; no retry, fallback, fabricated success, or global admission failure | Owner decision units plus SQLite fault integration |
| A7 | Restart between reservation/admission/dispatch/completion; prune Function detail before Automation history | Unresolved Runs/Steps interrupted without execution; Adapter unfinished invocations unknown; retained Automation evidence still readable without linked detail | Recovery/retention integration with production repositories |
| A8 | Close public admission before/after Step reservation and while Function is in flight | New public calls reject; reserved call can settle; no following Step; Automation joins before Function internal admission closes and dependencies remain alive | hearthd shutdown integration with barriers, not sleeps |
| A9 | HTTP/MCP discovery, create/get/replace, history detail; browser render of each evidence kind and old empty history | Fingerprint and canonical definition/evidence present; precise JSON preserved; browser remains read-only and does not crash on new variant; no authoring/invoke/poll controls | Transport units/integration plus frontend component tests |
| A10 | Discover through HTTP/MCP, author Automation, trigger/run it through production Core with synthetic SDK Function server, inspect both histories, restart Adapter with same then changed contract | Same-contract restart works on next Run; changed contract fails before dispatch; one linked Invocation per reached successful call; all repository validation passes | Core E2E, generated-artifact review, full gate |

Concrete commands for implementation, not executed for this planning-only change:

```sh
GO_PACKAGES='./internal/modules/adapters/...' mise run test
GO_PACKAGES='./internal/modules/adapters/...' mise run test -- -tags=integration
GO_PACKAGES='./internal/modules/automations/...' mise run test
GO_PACKAGES='./internal/modules/automations/...' mise run test -- -tags=integration
GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration,e2e
mise run --skip-deps web-test -- Automation
mise run --skip-deps web-build
mise run --skip-deps generate
```

Use focused mise lint/vet as needed. Unset `GO_PACKAGES` before final
`mise run validate`; review all generated/formatting/module changes. The full gate
requires a reachable Docker daemon for existing real-Mosquitto tests. Component
tests require installed web dependencies. Synthetic SDK/NATS tests establish the
Hearth contract, not real Zigbee2MQTT or Discord provider behavior.

## Alternatives and trade-offs

| Chosen | Alternative | Reason |
| --- | --- | --- |
| Native Function Step | Generic HTTP/MCP-call Step | Keeps ownership, typed history, bounds, and lifecycle policy in Core instead of workflow transport scripting |
| Stable declared-contract fingerprint | Pin Session revision / always use latest | Survives ordinary restarts without silently accepting schema/deadline changes |
| Fixed arguments | Expressions and result variables | Delivers invocation without turning this into a dataflow language |
| Separate reached Function evidence | Reuse Command attempts | Functions are not Entity Operations and have different authoritative outcomes |
| Reserved internal invocation identity | Link only after invocation returns | Preserves evidence of possible effects if a call or process ends before link persistence |
| Read-only browser | Full schema-driven authoring | Matches agreed first scope while keeping existing browser reads compatible |

Principles that changed the proposal:

- Model the domain:
  use a distinct Step and sealed execution states, not Command status overloads.
- Boundary discipline:
  keep catalog validation and invocation evidence in Adapters; Automation owns Run
  policy and consumes domain methods rather than transport or generated rows.
- Experience first:
  use a stable contract identity so unchanged Adapter restarts do not require
  editing every Automation. Keep browser compatibility despite API-first authoring.
- Test behavior:
  verify exact call payloads, retained outcomes, and blocked later effects through
  production boundaries rather than relying on call-count-only assertions.

## Risks and mitigation

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Separate module commits leave a reserved execution without a terminal link | Expected crash/fault window | Effects uncertain; Run cannot safely continue | Pre-effect reserved identity, authoritative verification, interruption/no replay, independent retained histories |
| New dependency makes shutdown close Functions too early | Likely if old order retained | Reserved call rejects or loses dependencies | Explicit public/internal gates and shutdown barrier integration test |
| Contract fingerprint interpreted as behavioral compatibility | Plausible | Changed vendor behavior executes under same schema | Document guarantee narrowly; no claim beyond declared normalized schemas/key/deadline |
| Strict clients/older binaries cannot decode new Step union | Expected for old consumers | Read failure or unsafe rollback | Update HTTP/MCP/browser together; unchanged existing documents; pre-feature database backup for binary rollback |
| Arguments copied into Automation snapshots contain sensitive information | Same model as existing Function arguments, with additional retention | Unexpected retained data | Document both retention scopes; use non-secret arguments and Adapter-held credentials; no raw arguments/results in diagnostics |

## Non-goals

No dynamic expressions, result branching, Function-result Triggers, new Device Fact
families, retry/idempotency API, job queue, restart replay, compensation, parallel
Steps, global concurrency cap, Notification Step removal, generic workflow engine,
SDK/NATS wire extension, schema-semantic equivalence engine, browser authoring,
secret-storage redesign, or provider-specific Core branching.

## Approval and stopping condition

The user approved this contract by explicitly requesting implementation on
2026-10-23. No unresolved blocking product question is intentionally deferred to
implementation. Seek renewed approval if source constraints materially change
these outcomes.

Implementation success requires A1–A10 plus the full validation gate, not merely
adding a new accepted Step kind. Ordinary delivery stops after one reviewed PR
with passing required checks, before merging.
