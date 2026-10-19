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

## API contracts

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
[Ecowitt's pending work](internal/adapters/ecowitt/runtime_work.go). Two existing
Home Assistant migration candidates are the
[`relayEvents` queue](internal/adapters/homeassistant/client.go) and the snapshot
buffer in
[`reconcileAndStream`](internal/adapters/homeassistant/adapter.go). Ending a
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
