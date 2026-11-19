# Coding standards

Use these criteria to review readability and responsibility; repository checks
enforce mechanical rules. Read the affected module's ownership documentation
before recommending an organizational change. Each rule identifies its source as
observed code, a stated decision, or both. The structural review procedure follows
the rules.

## Validation boundaries

### Validate arbitrary input at each independent entry point

Validate external or freely constructed input at each independent entry point,
including repositories that normally receive validated values from services.
Reuse the validated result within the workflow. Name the accepted input and
validation responsibility, distinguishing operations that validate and prepare
arbitrary input from operations that consume existing preparation.

A caller's validation cannot protect another independently callable boundary.
**Source:** stated.

### Recheck changing facts where the operation depends on them

Separate structural checks from checks against current external state. Check
transactional eligibility against the transaction's current data before making
the dependent change; an earlier check cannot establish that a changing fact
still holds. **Source:** stated.

### Share preparation results within their boundary

Reuse normalized values, encoded bytes, collected references, and other
preparation results within their boundary. Add a prepared type only when its
consumer and ownership contract justify the extra concept. **Source:** stated.

### Enforce production invariants through production entry points

Give every production validator a production enforcement point. Test the
constructor, decoder, operation, or persistence constraint that owns the
invariant, rather than only calling an isolated validator. Assertion-only helpers
belong in test files. **Source:** stated.

### Identify the remaining enforcement point before removing a check

Before removing or consolidating a check, identify the accepted input, independent
entry points, and remaining enforcement point. Preserve each entry point's safety
and error semantics; an apparently duplicate check may protect another caller.
**Source:** stated.

## Errors

Preserve public statuses, codes, durable outcomes, retry policy, and cancellation
behavior unless changes are explicitly agreed before implementation. Use the
[logging conventions](docs/guides/logging.md#developer-conventions-and-safety)
for diagnostic fields and safety, and the [testing guide](docs/guides/testing.md)
for test placement and validation. Configured linters own error naming and
mechanical error-handling checks.

### Keep actionable error classes with their module

Define a module-owned sentinel for a distinction a caller needs. Use a typed error
only when a caller consumes structured information or a private diagnostic cause.
Keep transport-framework types at their protocol boundary; ordinary contextual
errors suit unexpected failures that need no additional class. Avoid a universal
taxonomy or speculative types.

For example, [CommandExecutionError](internal/modules/devices/command.go) carries
a durable Command ID, and
[FunctionReferenceError](internal/modules/automations/function.go) carries the
failing Step and reason code. **Source:** observed + stated.

### Preserve useful error identity without wrapping noise

Preserve identities and private causes needed by downstream decisions or
diagnostics. Add operation context only when useful, using wrapping such as
`fmt.Errorf("load destination: %w", err)` instead of flattening the error to
text. Return the existing error unchanged when the layer adds no useful context.
If cleanup also fails, preserve the primary failure for diagnosis and choose the
resulting classification deliberately.

This applies to internal Go propagation and cleanup. Converting a transport cause
into a documented domain outcome may intentionally consume the original cause;
not every raw cause must survive that conversion. **Source:** observed + stated.

### Translate only known dependency failures

Translate a dependency failure into a domain class only when its identity and
meaning establish that class. Preserve unexpected operational failures as internal
failures, rather than blaming valid input: a database outage, corrupt stored row,
or missing server dependency is not a client validation error.

At repository, provider-adapter, and cross-module service boundaries, keep
dependency-specific details such as SQL constraint codes in the adapter that
understands them, not in HTTP or MCP handlers. The
[Notification reference path](internal/modules/automations/definition_reference_validation.go)
distinguishes missing or invalid destinations from operational lookup failures.
**Source:** observed + stated.

### Map errors at the owning protocol boundary

Map errors explicitly within HTTP, MCP, NATS, and SSE boundaries, with deliberate
precedence and a safe fallback suited to each operation. Unknown failures must not
expose their text. Reused translation belongs in the protocol package's
`errors.go`; operation-only mapping stays with the operation. Keep transport
status, wire serialization, and log-level policy out of domain error types.

The [HTTP Function mapping](internal/modules/adapters/httpapi/function_errors.go)
and [MCP Function mapping](internal/modules/adapters/mcpapi/errors.go) give
persistence uncertainty precedence over joined client-error classes.
**Source:** observed + stated.

### Use Huma problems and derive their published schemas

For new or changed HTTP error contracts, use Huma's `ErrorModel` and native
validation conventions. Add documented machine codes and typed extensions only
where consumers need them, and derive problem schemas from response types.
Publication must include reachable domain and framework validation statuses.

Framework validation may omit a machine code. Preserve existing documented codes
and receipt/history fields; explicitly review status or schema changes instead of
normalizing them silently. Do not replace the process-wide Huma error factory or
introduce a shared HTTP helper before its consumers share a behavioral contract.
The [Adapter Function problem](internal/modules/adapters/httpapi/function_errors.go)
extends Huma's model with a code and invocation identity. **Source:** stated.

### Distinguish operation errors from durable effect outcomes

Preserve each domain's distinction between an operation failure and a truthfully
recorded rejection, timeout, uncertainty, or idempotent result. For Command,
Function, Notification, Automation, and ingestion workflows, retain the documented
admission, persistence, replay, and redelivery policies. The owning consumer
decides acknowledgement, retry, termination, or no reply.

A generic error can lose evidence or encourage an unsafe retry of an effect that
may have happened. The
[Notification service](internal/modules/notifications/service.go) returns provider
evidence as an Attempt and Go errors for operation failures.
**Source:** observed + stated.

### Give unexpected failures one safe diagnostic owner

Assign unexpected-failure diagnostics to the boundary that owns the recovery or
response decision. Retain a private cause until that owner can classify it. Avoid
logging the same failure at every layer, and do not add deduplication state or
repository reads solely for logging.

This includes HTTP failures without an existing service or worker diagnostic.
Follow the [logging guide](docs/guides/logging.md) for safe fields, levels,
operation contexts, and prohibited data. The
[MCP failure boundary](internal/platform/mcpapi/error.go) records safe metadata
without publishing or logging internal cause messages. **Source:** observed +
stated.

### Classify cancellation by the operation that owns it

Distinguish expected caller or shutdown cancellation from dependency faults and
operation deadlines before choosing a response or diagnostic severity. Normal
cancellation is not an internal server fault; a cancellation error alone does not
establish that the owning request was canceled.

Apply this to request, worker, streaming, and shutdown boundaries. Preserve each
operation's documented lifetime, including admitted effects that outlive callers.
Provider and persistence deadlines retain their own meaning and durable outcomes.
Do not ignore all deadlines or map them all to HTTP 504.
**Source:** observed + stated.

### Verify error contracts at the affected boundary

Test observable classification and presentation at the owning boundary for new
or changed error behavior or publication. Cover the wrapped, ambiguous, and
unexpected cases the change can affect, including required precedence, safe
unknown fallback, and cancellation policy.

Transport tests must check the complete emitted problem or tool result, structured
identities, and agreement with its published schema. A mapping test or schema
digest alone cannot prove the production response or safe diagnostic. Follow the
[testing guide](docs/guides/testing.md) for levels and fixtures, and keep complete
matrices at their owner rather than repeating them at every integration layer.
**Source:** observed + stated.

## SQLite persistence mappings

For new or changed handwritten SQLite conversions, apply the
[SQLite mapping standards](docs/reference/sqlite-mapping-standards.md), including
stored-row validation contracts, resource-specific mapping files, row-decoder
names, and timestamp compatibility. They also apply to conversions in a module's
root package. Existing differences are scoped migration candidates, not permission
for unrelated cleanup.

## API contracts

### Keep transport operations in operation-local files

Use one operation per file in new or reorganized handwritten module HTTP and MCP
transport packages, including `httpapi` and module `mcpapi`. Keep registration
metadata, input/output wrappers, the handler, and operation-only helpers together.
Group genuinely reused definitions by resource in the same protocol package.

This is a file convention, not a per-operation package or service boundary. Do
not reorganize unrelated existing code solely to apply it, or impose it on
generated code or platform internals. **Source:** stated.

| File | Responsibility |
| --- | --- |
| `register.go` | One public `Register` for the whole module/protocol surface; construct handlers and explicitly call private operation registrars. Private registration groups may organize calls but contain no operation metadata or handler bodies. |
| `handler.go` | Private handler structs and consumer-owned service interfaces. Keep multiple service dependencies separate; registration does not merge use cases or lifecycle policies. |
| `<operation>.go` | Private `register<Operation>`, route/tool metadata and contract selection, input/output wrappers, handler, and operation-only validation, mapping, or schema helpers. |
| `<resource>_models.go` | Protocol payload types reused by multiple operations, not operation-specific wrappers. |
| `<resource>_mapping.go` | Domain-to-protocol mappings reused by multiple operations. |
| `<resource>_contracts.go` | Reused schema fragments, preparation getters, and validation contracts when needed. Extraction does not change preparation lifetime. |
| `errors.go` | Error translation reused within the protocol package; operation-only handling stays with the operation. |

Name files after the explicit HTTP operation ID or MCP tool name in `snake_case`:
HTTP `get-entity` and MCP `get_entity` each become `get_entity.go` in their own
packages. Organization does not change operation IDs, wire contracts, or published
schema names. HTTP and MCP need not expose the same operations or have matching
files or models, and neither imports the other.

Keep operation-only declarations local until another operation needs them. Name
other reused helpers for their concrete responsibility; do not collect unrelated
declarations in `inputs.go`, `models.go`, `handlers.go`, or `contracts.go`. Do
not add automatic registration, `init` registries, per-operation subpackages, or
empty scaffolding.

Operation-specific tests normally use the operation stem, such as
`get_entity_test.go` or `get_entity_integration_test.go`. Choose boundaries and
build tags from the [testing guide](docs/guides/testing.md). Keep cohesive catalog,
cross-operation, and lifecycle tests together under behavioral names; identify
fixture-only files as support code rather than forcing every test to mirror a
production file.

### Preserve JSON Merge Patch semantics for Device and Entity updates

Device and Entity PATCH requests use JSON Merge Patch. Require
`application/merge-patch+json` and reject unknown or read-only properties.
Request fields are optional: omitted fields remain unchanged, and `{}` succeeds
as a no-op for an existing resource. Accept explicit null only when the field
contract defines its meaning, such as clearing an override.

Request types, runtime validation, and OpenAPI must agree on omission and null
semantics so clients can distinguish an unchanged field from a cleared one.
**Source:** stated.

## Naming and abstraction

### Define capability interfaces at the consuming boundary

Introduce a Go dependency interface only when a current consumer needs the seam.
Define it in the consuming package, name it for the required capability, and
include only the methods that consumer needs. Keep a concrete dependency when an
interface provides no current benefit; not every module needs a repository seam.

Examples include Devices'
[`ObservationProjector`](internal/modules/devices/nats/observation.go),
Notifications' [`Registrar`](internal/modules/notifications/nats/registration.go),
and [`AutomationDevices`](internal/modules/automations/dependencies.go).
**Source:** observed + stated.

### Establish a shared behavioral contract before extracting machinery

Extract cross-owner machinery only when its consumers share the behavioral
contract. Keep module-specific retry and fault policy with the owning module or
Adapter. A shared component may own common policy only when an explicitly approved
contract standardizes it across consumers.

Sharing mechanics does not transfer scheduling, readiness, deadlines, or domain
policy unless that contract assigns them to the shared package. Identical code
alone does not justify extracting owner-specific policy.

The [platform guide](docs/reference/platform.md) assigns cancellation and joining
to [`WorkerHandle`](internal/platform/lifecycle/worker_handle.go), while
[history pruning](internal/app/hearthd/history_pruning.go) owns its schedule and
failure policy. Reconnect jitter remains Adapter-owned in
[Ecowitt](internal/adapters/ecowitt/connection.go) and
[Zigbee2MQTT](internal/adapters/zigbee2mqtt/connection.go).
The [outbox relay contract](docs/architecture/outbox-relays.md) standardizes
delivery retry, polling, poison handling, and delivery faults across Device Facts
and Adapter health. Storage, wire mapping, domain-error classification, and
consumer recovery remain module-owned. **Source:** observed + stated.

## Concurrency and lifecycle

### Give admitted work an explicit lifetime owner

Preserve each operation's documented cancellation and deadline contract when
changing admission or asynchronous execution. Distinguish caller cancellation,
operation deadlines, shutdown cancellation, and wait timeouts.

Work that outlives its caller needs an explicit owner, a tracked completion and
joining path, and its own deadline or explicit stop policy. Do not detach ordinary
reads or Agent turns merely because Commands use detached workers.

[`ExecuteCommand`](internal/modules/devices/command.go) transfers its reservation
to a worker with the Command deadline. Notification
[`send`](internal/modules/notifications/service.go) owns send and persistence
deadlines. [Agent turns](internal/modules/agent/service.go), by contrast, inherit
caller cancellation and register cancellation with the service's drain path.
**Source:** observed + stated.

### Join dependent work before closing its resources

In worker, callback, transport, and application shutdown paths, close admission
and join dependent work before closing its resources. A worker joins its children
before reporting completion. Treat a timed-out wait as an unfinished join: keep
dependencies alive until tracked work finishes.

Cancellation and joining are distinct even when one method requests both.
[`AdmissionGroup.Wait`](internal/platform/lifecycle/admission_group.go) does not
cancel admitted work; [`WorkerHandle.Stop`](internal/platform/lifecycle/worker_handle.go)
requests cancellation but signals completion only after the worker returns.
[Core shutdown](internal/app/hearthd/shutdown.go) joins work in dependency order;
see the [platform guide](docs/reference/platform.md#joining-and-cancellation-are-different).
**Source:** observed + stated.

### Bound connection ingress and make overflow visible

Bound new or changed non-durable in-memory ingress between upstream callbacks and
Adapter coordinators, including reconciliation buffers. On overflow, safely
diagnose the condition and end the connection generation before reconnecting or
resynchronizing; do not silently evict queued evidence. The owner chooses the
bound. Semantically redundant work may coalesce when required evidence remains.

SDK request concurrency and durable queues follow their own contracts. Existing
unbounded buffers are migration candidates, not scope for unrelated edits.
Ending a generation makes loss visible but cannot guarantee recovery of ephemeral
input. Examples include
[Zigbee2MQTT's pending queue](internal/adapters/zigbee2mqtt/connection.go),
[Z-Wave JS's reconciliation buffer](internal/adapters/zwavejs/adapter.go), and
[Ecowitt's pending work](internal/adapters/ecowitt/runtime_work.go).
**Source:** observed + stated.

## Dashboard design system

Apply this section to new and changed code under `web/`. Earlier code is a
migration candidate, not a finding against an unrelated change.
[`design-system.test.ts`](web/src/design-system.test.ts) tracks class selectors,
raw form controls, hand-rolled card surfaces, direct `radix-ui` imports, text
sizes off the type scale, and line heights or tracking outside the type roles,
and fails when a count grows.

### Style elements with utilities, not class selectors

For new and changed files under `web/src` and `web/.storybook`, put styles on
elements as Tailwind utilities. Stylesheets hold only theme tokens, element
resets, keyframes, and custom variants. Class selectors separate styles from
components and, outside a cascade layer, override utilities including those on
shadcn controls. Existing selectors are migration candidates. **Source:** stated.

### Adjust another component's element through its slot classes

For new and changed components under `web/src`, put a class on the element it
styles. A parent passes classes for a child's part through the child's
`className` or `classNames` prop; the child merges them with `cn`. For example,
`<DataCard classNames={{ body: "gap-0.5" }} />` avoids coupling to child markup.

Descendant variants such as `[&_.token.string]:text-chart-5` are allowed for
markup the component does not author, including Prism tokens, Markdown output,
and Recharts. **Source:** stated.

### Name a component's parts with `data-slot`

Give each part of a new or changed shared component under `web/src` a `data-slot`
name. Tests find an element by role or label first, and by `data-slot` only when
it has neither. This keeps styling classes from becoming test hooks and favors
what a person can perceive. Shared
[components](web/src/components/button.tsx) already use `data-slot`.
**Source:** observed + stated.

### Express state as a data or ARIA attribute

For new and changed components under `web/src`, record visual state in a `data-*`
or `aria-*` attribute and style it with attribute variants. Children react through
`group-data-*` variants. This lets styles, tests, and assistive technology share
state instead of maintaining a separate state class.
[`ServerStatus`](web/src/features/workspace/server-status.tsx) publishes
`data-state`. **Source:** observed + stated.

### Take design values from theme tokens

For new and changed styles under `web/src`, use the `@theme` names in
[`index.css`](web/src/index.css) for color, type size, radius, and easing. Use
role names such as `foreground`, `muted-foreground`, `border`, or `card` where
they fit. Layout geometry, including widths, gaps, and grid tracks, may use
arbitrary values. When [`DESIGN.md`](DESIGN.md) names a value that `@theme`
lacks, add it to both. **Source:** stated.

### Use the shared control from `components`

For new and changed code under `web/src`, use the component in
`web/src/components` for buttons, text fields, selects, switches, segmented
controls, dialogs, popovers, and notices. Add a needed size or state to the shared
component rather than recreating its focus, hover, and disabled treatments.

A bare `<button>` is allowed for a press target without button appearance, such
as a whole-card tile press. The expanded canvas stays in place with
`useModalFocus` instead of using `Dialog`, because moving its editor into a
Dialog portal remounts the editor and loses invalid, unsaved input. Existing raw
controls are migration candidates. **Source:** stated.

Shared controls inherit the global `:focus-visible` outline and reduced-motion
rule from [`index.css`](web/src/index.css) instead of restating them. Hearth is
dark-only, so write colors unconditionally, with no light-mode variants.
**Source:** stated.

### Draw a card with `Card`

Render card-shaped surfaces in new or changed `web/src` code with
[`Card`](web/src/components/card.tsx). Use `size="compact"` on Development pages
and `asChild` to retain an `article` or `section`. A view data card uses
`DataCard`, which draws its surface with `Card`. The composer, sent messages, and
unsupported view content are not cards. Shared surfaces prevent radius, border,
and surface values from drifting from [`DESIGN.md`](DESIGN.md). **Source:** stated.

### Import Radix only in low-level component wrappers

Wrap each Radix primitive once in `web/src/components`, and import the wrapper
everywhere else in new or changed `web/src` code. If no wrapper exists, add one
first. This keeps styling and configuration consistent.
[`app.tsx`](web/src/app.tsx) uses the popover wrapper. **Source:** observed +
stated.

### Share a style at three uses with one intent

Move a control or style into `components` when three or more places use it for the
same purpose. Keep a part owned by one feature in that feature. Similar-looking
components with different purposes stay separate; premature extraction couples
unrelated behavior, while late extraction leaves copies to diverge.
**Source:** stated.

### Keep shared components independent of features

Put reusable controls and compositions in `web/src/components` and feature-owned
behavior in `web/src/features`. Shared components must not import from features.
Status and error presentations can remain shared even when they use common Hearth
vocabulary. Place stories and tests alongside their owner, and import shared
components directly from their modules rather than a barrel. Apply this to new
and reorganized dashboard components and feature code. **Source:** stated.

## Structural review

Apply this section when a change creates, renames, moves, or splits files or
packages, or adds responsibilities to an existing file.

### Name files for their actual responsibility

Name handwritten files for their actual responsibility without relying on the
implementation conversation. A one-sentence description of the contents should
match the name; domain-model vocabulary is appropriate when it describes that
responsibility. **Source:** stated.

### Use consistent Step file roles across Automation kinds

Organize new or reorganized handwritten Automation Step files in
`internal/modules/automations` by consistent definition/preparation, execution
state, runtime, and serialization roles. **Source:** observed + stated.

| File role | Owns |
| --- | --- |
| `<kind>.go` | Kind-specific definition preparation and snapshot lookups, when needed. |
| `<kind>_execution_state.go` | Closed lifecycle and terminal evidence, state validation, evidence lookups, and derived properties. |
| `<kind>_execution.go` | Runtime dispatch, reconciliation or waiting, and persistence gates. |
| `<kind>_execution_codec.go` | Kind-specific wire DTOs and execution serialization. |

Shared Step definitions may remain in `definition.go` and shared definition
codecs. If/Choose keep their shared branch decision, result, evaluation, runtime,
and codec files: selection evidence differs from the lifecycle of Command, Delay,
Notification, and Function Steps. Keep cohesive behavioral tests together rather
than mirroring production files. Do not create empty files or new packages for
symmetry.

Command preparation follows the same convention: keep normalization and
parameter validation in `command.go`, not definition-wide validation. Shared
type declarations do not make kind-specific preparation a shared concern.

### Keep declarations with the owner their callers and rules identify

Keep handwritten declarations cohesive. Move symbols only when their callers or
rules establish a clearer owner, and give each proposed move a concrete ownership
or navigation reason. **Source:** stated.

### Keep workflow preparation discoverable with its entry point

For service workflows, keep source-specific preparation discoverable with its
entry point and give each shared helper an identifiable responsibility. Readers
must be able to trace preparation, decision, and execution without guessing the
owner of a source-specific step. **Source:** stated.

### Name tests for behavior and distinguish fixture support

Name tests for the behavior they exercise and make fixture-only files recognizable
as support code. Preserve cohesive cross-cutting tests, including transport parity
and shutdown ordering, rather than forcing a one-to-one mapping to production
files. **Source:** stated.

### Justify package splits with dependency boundaries

For a package change, explain the dependency boundary it enforces and check
visibility, dependency direction, and transaction or lifecycle ownership.
Shared filename prefixes and file length alone do not justify a split.
**Source:** stated.

### Compare lifecycle and transport machinery with existing counterparts

Compare new or changed lifecycle and transport machinery with the nearest platform
primitive and sibling-module implementation, starting with the
[platform guide](docs/reference/platform.md). Record the concrete counterpart and
why the change reuses it or needs different semantics. Check admission,
cancellation, joining, and domain-policy ownership across the boundary.
**Source:** stated.

### Keep ownership documentation and navigation aligned with the change

Check affected ownership documentation and navigation links against the new
layout and behavior. Update stale paths and responsibility descriptions so
readers reach the correct owner. **Source:** stated.

## Structural review procedure

Use this procedure for changes covered by the structural review rules above.

### Establish the review scope

1. Identify the comparison base and inspect the diff, staged changes, and new
   untracked files belonging to the change. Record the scope in the review.
2. Inspect the affected directory's file inventory and complete contents of
   changed files. Read neighboring callers, helpers, types, and tests needed to
   understand ownership.
3. Identify generated files and their sources. Review source ownership rather
   than recommending hand edits to generated output.

### Assess each changed handwritten file

Describe each file's contents in one sentence and compare that description with
its filename. Apply every applicable structural review rule above, including
related workflow and test placement.

### Report and completion

Return a compact inventory of changed handwritten files with each responsibility
and a verdict: keep, rename, move declarations, or split. Group files only when
each filename is listed and the same explanation applies. Account for generated
artifacts separately through their sources.

For each recommendation, cite the file and symbols, explain the navigation or
ownership problem, and name the proposed destination or responsibility.
Distinguish required corrections from optional naming preferences.

The review is complete when every in-scope file is accounted for, related workflow
and test placement has been checked, and unresolved questions are stated. Passing
checks or a declaration-preservation comparison supports correctness review, but
neither establishes that the resulting organization is clear.
