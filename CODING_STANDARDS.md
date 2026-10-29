# Coding standards

Use these criteria when reviewing changes. Repository checks enforce mechanical
rules; this document covers judgments about readability and responsibility.
Read the affected module's ownership documentation before recommending a change
to its organization.

Each rule records whether its source is observed code, a stated decision, or both.
Bad examples illustrate violations rather than reported codebase findings.
Linked implementations ground the observed rules. The structural review procedure
follows the rules.

## Validation boundaries

### Validate arbitrary input at each independent entry point

Validate external or freely constructed input at an explicit entry point and
reuse the validated result inside its workflow.

**Why:** A caller's validation does not protect an independently callable boundary.
**Applies to:** Entry points accepting external or arbitrary domain values,
including repositories even when services usually pass validated values.
**Source:** stated

Name each entry point's accepted input and validation responsibility. Names
should distinguish operations that validate and prepare arbitrary input from
operations that consume existing preparation.

Bad:

```text
A repository accepts arbitrary domain values but trusts validation by a service.
```

Good:

```text
The repository validates its arbitrary input. Its operation name distinguishes
that responsibility from an operation that consumes existing preparation.
```

### Recheck changing facts where the operation depends on them

Keep structural checks separate from checks against current external state and
perform transactional eligibility checks against the transaction's current data.

**Why:** Earlier validation cannot establish that a changing fact still holds.
**Applies to:** Operations whose eligibility depends on external or transactional
state.
**Source:** stated

Bad:

```text
An operation checks eligibility before the transaction and assumes it still holds
when the transaction changes the resource.
```

Good:

```text
An operation validates input structure separately, then checks eligibility against
the transaction's current data before making the dependent change.
```

### Share preparation results within their boundary

Reuse preparation results within a boundary and add a prepared type only when its
consumer and ownership contract justify it.

**Why:** Repeated preparation duplicates work, while an unjustified prepared type
adds an ownership contract that readers must understand.
**Applies to:** Preparation of normalized values, encoded bytes, collected
references, and other reusable results within a boundary.
**Source:** stated

Bad:

```text
Each step in one workflow normalizes the same input and recollects its references.
```

Good:

```text
The workflow prepares the values and references once and shares the results.
A prepared type is introduced only when its consumer and ownership justify it.
```

### Enforce production invariants through production entry points

Give every production validator a production enforcement point and test the
constructor, decoder, operation, or persistence constraint that owns the invariant.

**Why:** Testing an isolated validator does not prove production enforces its rule.
**Applies to:** Production validators and invariant tests; assertion-only helpers
belong in test files.
**Source:** stated

Bad:

```text
A test calls a validator directly, but no production operation calls it.
```

Good:

```text
The production decoder enforces the invariant, and its test submits invalid input
through that decoder. Assertion-only helpers stay in test files.
```

### Identify the remaining enforcement point before removing a check

Before removing a check, identify its input contract and remaining enforcement
point while preserving independent entry-point safety and error semantics.

**Why:** A seemingly duplicate check may protect a different caller or error contract.
**Applies to:** Changes that remove or consolidate validation checks.
**Source:** stated

Bad:

```text
A repository check is deleted because the service performs the same check,
without inspecting other repository callers or the errors they receive.
```

Good:

```text
The change identifies the accepted input, all independent entry points, and the
remaining enforcement point, then preserves safety and existing error semantics.
```

## Errors

Preserve public statuses, codes, durable outcomes, retry policy, and cancellation
behavior unless changes to them are explicitly agreed before implementation.

Use the [logging conventions](docs/logging.md#developer-conventions-and-safety)
for diagnostic fields and safety, and the [testing guide](docs/testing.md) for
test placement and validation. Error naming and mechanical error-handling checks
remain the responsibility of the configured linters.

### Keep actionable error classes with their module

Define a module-owned sentinel for a distinction a caller needs, and a typed error
only when a caller consumes structured information or a private diagnostic cause.

**Why:** A small error vocabulary exposes useful decisions without imposing a
universal taxonomy or speculative types on unrelated modules.
**Applies to:** Domain and service errors. Transport-framework types stay at their
protocol boundary; ordinary contextual errors remain appropriate for unexpected
failures that need no additional class.
**Source:** observed + stated

Examples include [CommandExecutionError](internal/modules/devices/command.go),
which carries a durable Command ID, and
[FunctionReferenceError](internal/modules/automations/function.go), which carries
the failing Step and a reason code.

Bad:

```text
A universal AppError contains HTTP status, log level, retry policy, and domain code.
Every operation adds a new error type even when no caller distinguishes it.
```

Good:

```text
The module exposes a not-found sentinel and a typed failure carrying a committed
record ID. Its transports choose their own presentations.
```

### Preserve useful error identity without wrapping noise

Preserve error identities and private causes needed by downstream decisions or
diagnostics, adding operation context only where it supplies useful information.

**Why:** Flattening an error loses classification, while wrapping every pass-through
call adds layers without explaining the failure.
**Applies to:** Internal Go error propagation and cleanup paths. Conversion into a
documented domain outcome may intentionally consume the original transport cause;
this rule does not require retaining every raw cause after that conversion.
**Source:** observed + stated

Bad:

```go
return fmt.Errorf("lookup failed: %s", err.Error())
```

Good:

```go
return fmt.Errorf("load destination: %w", err)
```

Return an existing error unchanged when the current layer adds no useful context.
If cleanup also fails, preserve the primary failure for diagnosis and make the
resulting classification deliberate rather than silently replacing it.

### Translate only known dependency failures

Translate a dependency failure into a domain class only when its identity and
meaning establish that class; preserve unexpected operational failures as internal
failures rather than blaming caller input.

**Why:** A database outage, corrupt stored row, or missing server dependency cannot
be repaired by changing an otherwise valid request.
**Applies to:** Repository, provider-adapter, and cross-module service boundaries.
Dependency-specific details such as SQL constraint codes belong to the adapter
that understands them, not to HTTP or MCP handlers.
**Source:** observed + stated

The [Notification reference path](internal/modules/automations/definition_reference_validation.go)
distinguishes a missing or invalid destination from an operational lookup failure.

Bad:

```text
Every failed Entity lookup is wrapped as invalid Automation input.
Every failure propagated by an agent graph is labeled a model-provider failure.
```

Good:

```text
Known missing or unsupported references become invalid-input errors. Other lookup
failures preserve their causes. Corrupt stored data is an internal failure.
```

### Map errors at the owning protocol boundary

Keep explicit protocol-local error mappings with deliberate precedence and a safe
fallback appropriate to that operation's contract.

**Why:** HTTP statuses, MCP tool failures, NATS dispositions, and streamed events
serve different consumers and must not acquire each other's policies.
**Applies to:** HTTP, MCP, NATS, and SSE translation. Reused translation belongs in
the protocol package's `errors.go`; operation-only mapping stays with its operation.
Keep transport status, wire serialization, and log-level policy out of domain
error types.
**Source:** observed + stated

The [HTTP Function mapping](internal/modules/adapters/httpapi/function_errors.go)
and [MCP Function mapping](internal/modules/adapters/mcpapi/errors.go) both give
persistence uncertainty precedence over joined client-error classes.

Bad:

```text
A shared classifier returns HTTP status, MCP code, log level, and retry behavior.
A broad invalid-input match hides a required persistence or typed-detail case.
```

Good:

```text
Each protocol maps the module's identities explicitly, preserves required
precedence, and handles unknown failures without exposing their text.
```

### Use Huma problems and derive their published schemas

Use Huma's `ErrorModel` and native validation conventions for HTTP problems, adding
documented machine codes and typed extensions only where consumers need them, and
derive problem schemas from the response types.

**Why:** Repeated base models and handwritten property lists create contract drift
without adding domain meaning.
**Applies to:** New and changed HTTP error contracts. Framework validation may omit
a machine code. Preserve existing documented codes and receipt/history fields;
review status or schema changes explicitly rather than normalizing them silently.
**Source:** stated

The [Adapter Function problem](internal/modules/adapters/httpapi/function_errors.go)
extends Huma's model with a code and invocation identity.

Bad:

```text
A module reimplements Huma's problem fields and maintains a separate schema list.
OpenAPI omits a reachable framework validation response.
```

Good:

```text
A transport type embeds huma.ErrorModel with an optional code and typed receipt
fields. Publication includes the operation's domain and framework failure statuses.
```

Do not replace the process-wide Huma error factory. Do not introduce a shared HTTP
helper until its consumers share the same behavioral contract.

### Distinguish operation errors from durable effect outcomes

Preserve the owning domain's distinction between a failed operation and a
truthfully recorded rejection, timeout, uncertainty, or idempotent result.

**Why:** Converting recorded outcomes into generic errors can lose evidence or
suggest unsafe retries of effects that may already have happened.
**Applies to:** Command, Function, Notification, Automation, and ingestion workflows.
Keep their documented admission, persistence, replay, and redelivery policies.
**Source:** observed + stated

The [Notification service](internal/modules/notifications/service.go) returns
provider evidence as an Attempt and uses Go errors for operation failures.

Bad:

```text
A committed rejected Notification attempt becomes HTTP 500. A common error helper
decides that every timeout should retry or every corrupt message should terminate.
```

Good:

```text
The caller receives the committed attempt and its documented outcome. The owning
consumer separately decides acknowledgement, retry, termination, or no reply.
```

### Give unexpected failures one safe diagnostic owner

Assign unexpected-failure diagnostics to the boundary that owns the recovery or
response decision, retaining a private cause until that owner can classify it.

**Why:** Discarding causes before diagnosis makes failures opaque; logging the same
failure at every layer creates noise without additional evidence.
**Applies to:** Runtime failure paths, including HTTP failures without an existing
service or worker diagnostic. Follow [docs/logging.md](docs/logging.md) for safe
fields, levels, operation contexts, and prohibited data. Do not add deduplication
state or repository reads solely for logging.
**Source:** observed + stated

The [MCP failure boundary](internal/platform/mcpapi/error.go) retains internal causes
and records safe metadata without publishing or logging their messages.

Bad:

```text
A wrapper stores an internal cause that no diagnostic owner can inspect.
The repository, service, and HTTP handler each log the same raw error.
```

Good:

```text
The designated boundary records a fixed event and failure code with safe operation
metadata. The client receives fixed detail; unknown cause text stays private.
```

### Classify cancellation by the operation that owns it

Distinguish expected caller or shutdown cancellation from dependency faults and
operation deadlines before choosing a response or diagnostic severity.

**Why:** Normal cancellation is not an internal server fault, and one generic
timeout classification cannot explain which operation expired or what completed.
**Applies to:** Request, worker, streaming, and shutdown boundaries. Preserve each
operation's documented lifetime, including admitted effects that outlive callers.
A cancellation error alone does not establish that the owning request was canceled.
**Source:** observed + stated

Bad:

```text
Every context error becomes internal_error and an Error-level record. Alternatively,
every context.DeadlineExceeded is ignored or mapped to HTTP 504.
```

Good:

```text
The owner recognizes caller or shutdown cancellation without an internal-fault log.
Provider and persistence deadlines retain their own meaning and durable outcomes.
```

### Verify error contracts at the affected boundary

Test observable classification and presentation at the owning boundary, including
the wrapped, ambiguous, and unexpected cases the change can affect.

**Why:** An isolated mapping test or schema digest cannot prove that production
returns the documented status, structured details, or safe diagnostic.
**Applies to:** New and changed error behavior or publication. Follow
[docs/testing.md](docs/testing.md) for levels and fixtures; keep complete matrices
at their owner instead of repeating them across every integration layer.
**Source:** observed + stated

Bad:

```text
Only direct sentinels are tested. A changed OpenAPI digest is accepted without
checking a response, and a no-leak test inspects only the top-level detail string.
```

Good:

```text
Focused tests cover required precedence, wrapped failures, safe unknown fallback,
and cancellation policy. Transport tests check the complete emitted problem or
tool result, structured identities, and agreement with its published schema.
```

## API contracts

### Keep transport operations in operation-local files

Use one operation per file in module HTTP and MCP transport packages. Keep the
operation's registration metadata, input/output wrappers, handler, and helpers
used only by that operation together. Group genuinely reused definitions by
resource within the same protocol package.

**Why:** A reader should find an operation's public contract and translation to
the service in one predictable place, without searching package-wide input,
handler, and schema catalogs.
**Applies to:** New and reorganized handwritten module HTTP/MCP transport code,
including `httpapi` and module `mcpapi` packages. This is a file convention, not a
per-operation package or service boundary. Do not reorganize unrelated existing
code solely to apply it, or impose it on generated code or platform internals.
**Source:** stated

| File | Responsibility |
| --- | --- |
| `register.go` | One public `Register` for the complete module/protocol surface. Construct handlers and explicitly call private operation registrars. Private registration groups may organize those calls, but contain no operation metadata or handler bodies. |
| `handler.go` | Private handler structs and the consumer-owned service interfaces they need. Multiple service dependencies stay separate; registration does not merge their use cases or lifecycle policies. |
| `<operation>.go` | One operation's private `register<Operation>` function, route/tool metadata and contract selection, input/output wrappers, handler, and operation-only validation, mapping, or schema helpers. |
| `<resource>_models.go` | Protocol payload types reused by multiple operations, not operation-specific request/response wrappers. |
| `<resource>_mapping.go` | Domain-to-protocol mappings reused by multiple operations. |
| `<resource>_contracts.go` | Reused schema fragments, preparation getters, and validation contracts, when needed. Extraction does not change their preparation lifetime. |
| `errors.go` | Error translation reused within this protocol package. Keep operation-only error handling with the operation. |

Name operation files after the explicit HTTP operation ID or MCP tool name in
`snake_case`: HTTP `get-entity` and MCP `get_entity` each become `get_entity.go`
in their own packages. File organization does not change operation IDs, wire
contracts, or published schema names. HTTP and MCP need not expose the same
operations or have matching files/models.

Keep operation-only declarations local until another operation actually needs
them. Other reused helpers get names describing their concrete responsibility;
do not accumulate unrelated declarations in `inputs.go`, `models.go`,
`handlers.go`, or `contracts.go`. Do not add automatic registration, `init`
registries, per-operation subpackages, or empty scaffolding.

Operation-specific tests normally use the operation stem, such as
`get_entity_test.go` or `get_entity_integration_test.go`; choose the boundary and
build tags from [the testing guide](docs/testing.md). Keep cohesive catalog,
cross-operation, and lifecycle tests together under behavioral names rather
than forcing every test file to mirror a production file. Shared fixture-only
files remain recognizable as support code.

Bad:

```text
register.go owns every route/tool definition; inputs.go owns every request;
handlers.go owns every handler. Following one operation requires all three.
```

Good:

```text
devices/httpapi/
  register.go          # Register constructs handlers and calls registerGetEntity, ...
  handler.go           # handler and its Devices interface
  get_entity.go        # registration, GetEntityInput/Output, and getEntity handler
  list_entities.go     # its own registration, wrappers, and handler
  entity_models.go     # EntityBody shared by those operations
  entity_mapping.go    # shared entityBody mapping
  errors.go            # shared HTTP error translation
  get_entity_test.go   # operation-specific behavior
```

The sibling `devices/mcpapi` follows the same organization with its own tool
contracts, payloads, mappings, and errors; it does not import the HTTP package.

### Preserve JSON Merge Patch semantics for Device and Entity updates

Use JSON Merge Patch for Device and Entity PATCH requests, with request types,
runtime validation, and OpenAPI agreeing on omission and null semantics.

**Why:** Clients must be able to distinguish leaving a field unchanged from
explicitly clearing it.
**Applies to:** Device and Entity PATCH contracts. Request fields are optional;
omitted fields remain unchanged, and an empty object is a successful no-op for an
existing resource. Require `application/merge-patch+json` and reject unknown or
read-only properties. Accept explicit null only where the field contract defines
its meaning, such as clearing an override.
**Source:** stated

Bad:

```text
A Device PATCH rejects an empty object or resets omitted fields. OpenAPI marks
fields required even though runtime validation accepts their omission.
```

Good:

```text
A Device PATCH with application/merge-patch+json accepts {} as a no-op for an
existing Device. Omitted fields stay unchanged, and null clears an override only
when its field contract permits it. Request types and OpenAPI match runtime behavior.
```

## Naming and abstraction

### Define capability interfaces at the consuming boundary

Introduce an interface only when a current consumer needs the seam, define it in
the consuming package, and name it for the capability that consumer requires.

**Why:** A consumer should depend on the behavior it needs rather than an entire
implementation's method set or an abstraction with no current purpose.
**Applies to:** New and changed Go dependency interfaces. Include only the methods
the consumer needs. Keep a concrete dependency when an interface provides no
current benefit; this rule does not require a repository seam for every module.
**Source:** observed + stated

Examples include the Devices transport's
[`ObservationProjector`](internal/modules/devices/nats/observation.go), the
Notifications transport's
[`Registrar`](internal/modules/notifications/nats/registration.go), and
[`AutomationDevices`](internal/modules/automations/dependencies.go).

Bad:

```text
A registration transport accepts the whole persistence Repository interface just
to call Register, or a new interface mirrors a concrete service without a consumer
that needs the seam.
```

Good:

```go
type Registrar interface {
	Register(context.Context, notifications.Registration) (notifications.Destination, error)
}
```

### Establish a shared behavioral contract before extracting machinery

Extract shared machinery only when its consumers share the behavioral contract,
and keep module-specific retry and fault policy with the owning module or Adapter.
A shared component may own common policy only when an explicitly approved contract
standardizes that behavior across its consumers.

**Why:** Similar implementations do not establish interchangeable lifecycle or
failure semantics; premature sharing couples owners that need different policy.
**Applies to:** New and changed cross-owner abstractions. Sharing mechanics does
not transfer scheduling, readiness, deadlines, or domain policy to the shared
package unless the approved shared contract assigns those decisions to it.
Identical code alone is not a reason to extract owner-specific policy.
**Source:** observed + stated

The [platform guide](docs/platform.md) assigns cancellation and joining to
[`WorkerHandle`](internal/platform/lifecycle/worker_handle.go), while
[history pruning](internal/app/hearthd/history_pruning.go) owns its schedule and
failure policy. Reconnect jitter remains Adapter-owned in
[Ecowitt](internal/adapters/ecowitt/connection.go) and
[Zigbee2MQTT](internal/adapters/zigbee2mqtt/connection.go).
The [outbox relay contract](docs/outbox-relays.md) explicitly standardizes delivery
retry, polling, poison handling, and delivery faults across Device Facts and
Adapter health. Their storage, wire mapping, domain-error classification, and
consumer recovery remain module-owned.

Bad:

```text
A shared worker decides that any module operation failure closes all admission,
or identical reconnect helpers are extracted without comparing their owners'
retry and failure contracts.
```

Good:

```text
WorkerHandle shares cancellation and joining. The history-pruning scheduler owns
when to run and what a failed pass means. Adapter reconnect policy stays local
unless a shared behavioral contract justifies extraction.
The outbox relay owns the explicitly standardized delivery policy while its
module sources retain domain-specific responsibilities.
```

## Concurrency and lifecycle

### Give admitted work an explicit lifetime owner

Preserve each operation's documented cancellation and deadline contract, with an
explicit owner for admitted work that outlives its caller.

**Why:** Caller cancellation must neither abandon owned side effects nor leave
detached work running without a shutdown or completion owner.
**Applies to:** New and changed admission and asynchronous execution paths.
Distinguish caller cancellation, operation deadlines, shutdown cancellation, and
wait timeouts. Work that outlives a request needs an owned completion and joining
path, with its own deadline or explicit stop policy. Do not detach ordinary reads
or Agent turns merely because Commands use detached workers.
**Source:** observed + stated

[`ExecuteCommand`](internal/modules/devices/command.go) transfers its reservation
to a worker with the Command deadline. Notification
[`send`](internal/modules/notifications/service.go) owns send and persistence
deadlines. In contrast, [Agent turns](internal/modules/agent/service.go) inherit
caller cancellation and also register cancellation with the service's drain path.

Bad:

```text
A committed Command's worker inherits the HTTP request cancellation, or a worker
uses context.WithoutCancel without an operation deadline or service stop policy
and without a tracked completion path.
```

Good:

```text
The Command service tracks an admitted worker through its own deadline and joins
it during Drain even if the caller stops waiting. Agent turns retain their
different caller-cancelable contract and are canceled and joined during Drain.
```

### Join dependent work before closing its resources

Close admission and join dependent work before closing its resources, treating a
timed-out wait as an unfinished join rather than proof of completion.

**Why:** Canceling or ceasing to wait for a worker does not prevent it from using
NATS, SQLite, or other dependencies that shutdown might otherwise close.
**Applies to:** New and changed worker, callback, transport, and application
shutdown paths. A worker joins its own children before reporting completion.
Cancellation and joining remain distinct even when one method requests both.
**Source:** observed + stated

[`AdmissionGroup.Wait`](internal/platform/lifecycle/admission_group.go) does not
cancel admitted work.
[`WorkerHandle.Stop`](internal/platform/lifecycle/worker_handle.go) requests
cancellation, but its completion signal closes only after the worker returns.
[Core shutdown](internal/app/hearthd/shutdown.go) joins admitted work in dependency
order before withdrawing shared resources; see the
[platform guide](docs/platform.md#joining-and-cancellation-are-different).

Bad:

```text
A timed Drain returns a context error, so shutdown assumes the worker exited and
closes the database while that worker can still use it.
```

Good:

```text
Shutdown closes admission and requests the operation's documented stop behavior.
If a bounded join times out, dependencies stay alive until tracked work actually
finishes; only then does shutdown close them.
```

### Bound connection ingress and make overflow visible

Bound non-durable Adapter connection ingress and end the connection generation
visibly on overflow rather than silently evicting queued evidence.

**Why:** An upstream stream can outpace reconciliation or publication, causing
unbounded memory growth or silent loss of ordered evidence.
**Applies to:** New and changed in-memory ingress between upstream callbacks and
Adapter coordinators, including reconciliation buffers. The owner chooses the
bound and diagnoses overflow before reconnecting or resynchronizing. Semantically
redundant work may coalesce when that preserves the required evidence. SDK request
concurrency and durable queues follow their own contracts and are excluded.
Existing unbounded buffers are migration candidates, not scope for unrelated edits.
**Source:** observed + stated

The established pattern appears in
[Zigbee2MQTT's pending queue](internal/adapters/zigbee2mqtt/connection.go),
[Z-Wave JS's reconciliation buffer](internal/adapters/zwavejs/adapter.go), and
[Ecowitt's pending work](internal/adapters/ecowitt/runtime_work.go). Ending a
generation makes loss visible; it does not guarantee recovery of ephemeral input.

Bad:

```text
A callback relay appends indefinitely while its consumer is blocked, or evicts an
arbitrary queued report when full and continues as though no evidence was lost.
```

Good:

```text
The Adapter bounds its ordered ingress. On overflow it emits a safe diagnostic,
ends the generation, and follows its reconnect/resynchronization policy instead
of silently dropping a chosen report.
```

## Structural review

Apply this section when a change creates, renames, moves, or splits files or
packages, or adds responsibilities to an existing file.

### Name files for their actual responsibility

Choose a filename that communicates the file's responsibility without relying on
the implementation conversation.

**Why:** Readers use filenames to find the code that owns a behavior.
**Applies to:** Handwritten files in structural changes covered by this section;
domain-model vocabulary is appropriate when it describes the actual responsibility.
**Source:** stated

Bad:

```text
A file is named for the implementation task even though its declarations own a
different, lasting domain responsibility.
```

Good:

```text
A one-sentence description of the file's contents matches its filename, using
domain vocabulary when that vocabulary describes the responsibility.
```

### Use consistent Step file roles across Automation kinds

Organize each Automation Step kind by definition/preparation, execution state,
runtime, and serialization using the same filename roles across kinds.

**Why:** Readers should not need to search `*_evidence.go`, a definition file, and
`*_execution_state.go` to find the same lifecycle responsibility for different
Step kinds.
**Applies to:** New or reorganized handwritten Step files in
`internal/modules/automations`. Shared Step definitions may remain in
`definition.go` and shared definition codecs. If/Choose keep their shared branch
decision, result, evaluation, runtime, and codec files because selection evidence
differs from the lifecycle of Command, Delay, Notification, and Function Steps.
Keep cohesive behavioral tests together rather than mirroring production files.
Do not create empty files or new packages for symmetry.
**Source:** observed + stated

| File role | Owns |
| --- | --- |
| `<kind>.go` | Kind-specific definition preparation and snapshot lookups, when needed. |
| `<kind>_execution_state.go` | Closed lifecycle and terminal evidence, state validation, evidence lookups, and derived properties. |
| `<kind>_execution.go` | Runtime dispatch, reconciliation or waiting, and persistence gates. |
| `<kind>_execution_codec.go` | Kind-specific wire DTOs and execution serialization. |

Command preparation follows this convention too. Keep its normalization and
parameter validation in `command.go`, not in definition-wide validation.
Shared type declarations do not make kind-specific preparation a shared concern.

Bad:

```text
command_execution_state.go   # Command lifecycle
delay.go                     # Delay definition mixed with lifecycle
notification_evidence.go     # Notification lifecycle
function_evidence.go         # Function lifecycle
```

Good:

```text
command_execution_state.go
delay_execution_state.go
notification_execution_state.go
function_execution_state.go
```

### Keep declarations with the owner their callers and rules identify

Keep a file's declarations cohesive and move symbols only when their callers or
rules establish a clearer owner.

**Why:** Misplaced declarations hide responsibilities from readers and callers.
**Applies to:** Handwritten files in structural changes covered by this section;
each proposed move needs a concrete ownership or navigation reason.
**Source:** stated

Bad:

```text
A general decoder is hidden among comparisons, or service configuration is hidden
among persistence contracts.
```

Good:

```text
The decoder and service configuration have discoverable owners matching their
callers and rules. A proposed move names that owner and explains the improvement.
```

### Keep workflow preparation discoverable with its entry point

Keep source-specific preparation discoverable with its entry point and give each
shared helper an identifiable responsibility.

**Why:** Readers need to trace preparation, decision, and execution without
guessing which helper owns a source-specific step.
**Applies to:** Service workflows in structural changes covered by this section.
**Source:** stated

Bad:

```text
Source-specific preparation is hidden in a general helper file with no clear
relationship to the workflow's entry point.
```

Good:

```text
The entry point makes its source-specific preparation discoverable. Shared helpers
have named responsibilities, and preparation, decision, and execution can be traced.
```

### Name tests for behavior and distinguish fixture support

Name tests for the behavior they exercise, make fixture-only files recognizable
as support code, and preserve cohesive cross-cutting tests.

**Why:** Test organization should reveal behavior rather than force a one-to-one
mapping to production files.
**Applies to:** Tests in structural changes covered by this section, including
transport-parity and shutdown-ordering tests that span production files.
**Source:** stated

Bad:

```text
A cohesive shutdown-ordering test is split solely to mirror production filenames,
and a fixture-only file is named as though it contains behavior tests.
```

Good:

```text
The shutdown-ordering test stays cohesive and names the behavior it exercises.
Fixture-only files are recognizable as test support.
```

### Justify package splits with dependency boundaries

Explain the dependency boundary a package change enforces and check visibility,
dependency direction, and transaction or lifecycle ownership.

**Why:** A package split without an ownership boundary adds navigation without
clarifying responsibilities.
**Applies to:** Package changes covered by this section; shared filename prefixes
and file length alone do not justify a split.
**Source:** stated

Bad:

```text
Files become a new package solely because they share a prefix or are long.
```

Good:

```text
The package change names its dependency boundary and explains visibility,
dependency direction, and transaction or lifecycle ownership across that boundary.
```

### Compare lifecycle and transport machinery with existing counterparts

Compare new or changed lifecycle and transport machinery with the nearest platform
primitive and sibling-module implementation, starting with the
[platform guide](docs/platform.md).

**Why:** A per-file inventory alone cannot establish that shared machinery has
the right owner or semantics.
**Applies to:** New or changed lifecycle and transport machinery in structural
changes covered by this section. Record the concrete counterpart and why the
change reuses it or needs different semantics. Check admission, cancellation,
joining, and domain-policy ownership across the boundary.
**Source:** stated

Bad:

```text
A review inventories new lifecycle files but never compares their admission,
cancellation, joining, or domain-policy ownership with existing implementations.
```

Good:

```text
The review names the platform and sibling-module counterparts, explains reuse or
different semantics, and checks admission, cancellation, joining, and domain-policy
ownership across the boundary.
```

### Keep ownership documentation and navigation aligned with the change

Check affected ownership documentation and navigation links against the new
layout and behavior.

**Why:** Stale ownership descriptions and links direct readers to the wrong code.
**Applies to:** Documentation and navigation affected by structural changes covered
by this section.
**Source:** stated

Bad:

```text
A file moves to a new owner while the ownership guide still points to its old path.
```

Good:

```text
The ownership guide and navigation links match the new path and responsibility.
```

## Structural review procedure

Use this procedure for changes covered by the structural review rules above.

### Establish the review scope

1. Identify the comparison base and inspect the diff, staged changes, and new
   untracked files that belong to the change. Record that scope in the review.
2. Inspect the affected directory's file inventory and the complete contents of
   changed files. Read neighboring callers, helpers, types, and tests needed to
   understand their ownership.
3. Identify generated files and their sources. Review their source ownership
   rather than recommending hand edits to generated output.

### Assess each changed handwritten file

Describe what the file does in one sentence, based on its contents, and compare
that description with the filename. Apply each applicable structural review rule
above, including related workflow and test placement.

### Report and completion

Return a compact inventory of changed handwritten files with their responsibility
and a verdict: keep, rename, move declarations, or split. Group files only when
each filename is listed and the same explanation applies. Account for generated
artifacts separately through their sources.

For each recommended change, cite the file and symbols, explain the navigation
or ownership problem, and name the proposed destination or responsibility.
Distinguish required corrections from optional naming preferences.

The structural review is complete when every in-scope file is accounted for,
related workflow and test placement has been checked, and unresolved questions
are stated. Passing checks or a declaration-preservation comparison supports
correctness review; neither establishes that the resulting organization is clear.
