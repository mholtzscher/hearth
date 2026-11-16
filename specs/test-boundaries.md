# Concrete test boundaries and execution

Status: approved in Plannotator review `pn-820c91`; implemented and locally
verified. Published-head CI is reported in PR #196.

This replaces the withdrawn generic testing rules with an executable suite
design. It builds on the fuzz/mutation removal and CI-only race default already
delivered in PR #196. `CODING_STANDARDS.md` stays unchanged.

## Agreed execution policy

- Plain local `mise run test` runs unit tests without race detection.
- Local `mise run validate` includes every test level, without race detection.
- CI includes every level with race detection.
- Keep existing task names. Use Go build tags to request integration or E2E tests
  during focused iteration.

## Discrete rules

### 1. Unit tests exercise one owner without infrastructure

The untagged suite may use domain objects, dependency doubles, controlled
goroutines/time, schema codecs, fixture files, and `httptest.ResponseRecorder`.
It may not open SQLite, start/connect to a broker, listen on a network socket,
or start a production application.

Real in-process routers/decoders with a service double are unit tests and
belong in the default suite. `httptest.NewServer` opens a socket and belongs to
integration. Temporary files, generated conformance examples, and re-executing
the test binary to check a fatal assertion do not make a test application E2E.
Generator/compiler subprocess checks belong to integration.

A `Run` call that rejects invalid configuration or a canceled context before
opening infrastructure can remain a unit test. Classify the executed path, not
the function name or imported types.

### 2. Integration tests prove a real boundary or selected assembly

Tag tests that exercise real SQLite, NATS/JetStream, MQTT, WebSocket, listening
HTTP/MCP, executable startup/signals, or hand-assembled module/application
components with infrastructure. A fake Core peer over a real broker is
integration. Production startup failure and shutdown-resource tests are also
integration unless they exercise a complete public journey.

Keep SQL constraints, transaction rollback, migration preservation, broker
acknowledgment/redelivery, protocol framing, and resource joining at real
boundaries. Do not replace these with a fake to fit the default suite.

### 3. E2E means a public journey through production Core

An E2E test starts production Core assembly, drives HTTP/MCP or the public Adapter
SDK boundary, and observes the resulting public or durable outcome. Simulated
devices and fake upstream vendors are allowed and must be identified as such.

Production `Run` alone is insufficient. An Adapter running against a
hand-assembled Core peer remains integration. A Core startup error test is
integration. A real Core plus simulated Adapter driving a Command and verifying
State/history is E2E. No new browser or live-hardware requirement is introduced.

### 4. Put matrices with the owner of the decision

Domain classification and comparison matrices use unit fixtures.
HTTP input/status/schema matrices use the real decoder/router and a recording
service double, without a database or broker. SQL and protocol matrices use the
real boundary they protect.

E2E scenarios retain distinct production-wiring, public-flow, and recovery
guarantees. Demote a case only after identifying the lower-level assertion that
will detect its defect. Restart/no-replay, acknowledgment-floor recovery,
atomic evidence/outbox writes, and shutdown joins remain explicitly protected.

### 5. Make level selectable at file scope

Unit behavior files remain untagged. Integration behavior files use
`//go:build integration` and `_integration_test.go`; E2E behavior files use
`//go:build e2e` and `_e2e_test.go`.

Split mixed files before tagging. Move test bodies and helpers without changing
assertions in the classification pass. Keep generated test filenames unchanged;
the generated conformance tests currently run without infrastructure.

Enforce matching tags and suffixes through the existing `lint` task. The checker
must distinguish behavior files from fixture-only files, rather than requiring
every tagged helper to have a behavior-test suffix. The enforcement contract is
specified below.

### 6. Classify fixture support with its consumers

Fixture-only files keep recognizable helper/fixture names. Plain data builders
and other infrastructure-free helpers can remain untagged, even when only
integration or E2E tests currently use them.

Tag a fixture file when it imports test infrastructure, such as an embedded
broker or container helper, references declarations
excluded from the default build, or starts infrastructure during initialization.
A fixture needed by both integration and E2E uses
`//go:build integration || e2e`; one needed by only one level uses that level's
tag. Go compiles all selected helper files even if no test calls them, so
tagging only the behavior files does not remove their helper dependencies.

Split immutable data builders from helpers that start brokers, databases, or
applications. A tagged behavior file must not leave its heavy imports or
infrastructure initialization in an untagged helper. Keep tests with their
behavioral owner rather than creating a global test tree.

### 7. Use the existing commands for all levels

```sh
# Default local unit feedback.
GO_PACKAGES='./internal/modules/automations' mise run test

# Add the selected real-boundary tests to ordinary tests.
GO_PACKAGES='./internal/modules/automations' mise run test -- -tags=integration

# Include every level in the selected package.
GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration,e2e

# Full local completion gate, all levels, non-race.
mise run validate
```

Tag selection is additive. Enabling integration also runs untagged tests.
`GO_PACKAGES` still selects packages, and omitted selection still means `./...`.
CI continues to enable race detection through `CI=true`. A local `-- -race`
override remains available but is not required.

### 8. Verify selection and retained coverage, not labels alone

The default suite must actually run without Docker or network listeners.
Every former test must appear in the combined selection unless a separately
reviewed relocation/consolidation names its retained protection. Packages with
no default tests must not be mistaken for evidence that their integration
behavior was checked.

Both local validation and CI must compile, lint, vet, and run tagged files.
Record cache state and distinguish reduced scope from a same-workload speedup.
Retain the existing property/example policy; add no fuzz or mutation tooling.

## Evidence and initial migration map

The metadata inventory covers all 467 Go test files at `58a22e4`: 23 currently
have an integration suffix, none an E2E suffix, 33 are generated, and 43 have no
test entry points. Those counts are not a semantic classification. Application
and command tests were examined in depth; module/Adapter discovery scanned 279
paths and traced representative fixtures in about 50 files. The final per-file
classification is a required migration deliverable, not a completed audit claim.

| Area | Concrete first change | Protection that stays real |
| --- | --- | --- |
| `sdk/adapter/availability_test.go` | Separate `TestHealthValidationAcceptsDocumentedAdapterNamespace` from real-NATS Session tests; move embedded-broker helpers to tagged support | SDK request/reply, retry identity, fencing, and publication acknowledgments |
| `internal/adapters/zwavejs/client_test.go` | Separate pure Value ID/argument codec cases from real WebSocket peer cases | Schema negotiation, frame rejection, correlation, deadlines, and reconnect |
| `internal/modules/automations/conditions_admission_test.go` | Move pure `TestDecideConditionsOutcome` to a default decision test file; tag SQLite-backed admission cases | Transactional admission and retained evidence |
| `internal/modules/automations/previous_comparisons_test.go` | Move `TestUnmatchedObservationDoesNotCreateReceiptOrHistory` to a tagged admission/receipt file | Real receipt/history semantics and re-admission after a definition change |
| `internal/modules/devices/nats/observation_test.go` | Separate pure wire-to-domain mapping from real broker handling | Commit-before-ack, redelivery, and projection |
| `internal/app/hearthd/notifications_integration_test.go` | Separate production-Core journey from hand-assembled fixtures; later put strict-input matrix in the API owner with an existing service double | Production registration/HTTP/MCP wiring and durable send evidence |
| `internal/app/hearthd/simulator_matrix_integration_test.go` | Keep classified as integration because Core is hand-assembled; later move duplicate domain matrices down only with a retained-assertion map | SDK/Core/HTTP connection, takeover/fencing, reconnect and acknowledgment guarantees |
| `internal/app/hearthd/held_state_slice_test.go` and other actual Core journeys | Rename/tag as E2E, with matching tagged support | Observation, scheduler, admission, Command and history connected through production Core |
| `internal/app/hearthd/{agent_lifecycle,history_pruning,schedule_scheduler,run_cancellation}_test.go` | Split default coordinator tests from real-resource/startup tests | Resource lifetime, cleanup order, startup failure and public availability |
| Adapter `internal/app/*/run_integration_test.go` files | Move pure `supervise` cases into default files; keep fake-Core/full-Adapter journeys as integration | Real Adapter startup, vendor protocol, registration, publication and shutdown |
| `internal/modules/notifications/sqlite/service_test.go` | In a later relocation pass, put gate/deadline policy in `notifications` using the existing Repository interface | Keep storage-failure, durable collision and no-replay proofs in SQLite |
| `internal/cmd/entitytypegen` | Keep conformance/renderer examples default; run compiler subprocess execution with integration tests | Executable generated SDK/catalog conformance and freshness |

Not every infrastructure test is oversized. In particular,
`devices/nats/command_sender_test.go` uses a real broker even for its
missing-adapter case; that case is not a pure classification test. The existing
Automation repository is broad, so replacing all runtime SQLite fixtures with a
large fake is not part of this design.

## Task changes

No production API, schema, persistence type, or dependency changes. Reuse the
existing test task for level selection; fixture execution needs no separate task.

In `mise.toml`, keep the existing race switch and add tag selection:

```diff
 race=false
+tags=
 if [ "${CI:-}" = "true" ]; then race=true; fi
-GORACE="${GORACE:-} atexit_sleep_ms=0" go test -race="$race" ${GO_PACKAGES:-./...}
+if [ "${CI:-}" = "true" ]; then tags=integration,e2e; fi
+GORACE="${GORACE:-} atexit_sleep_ms=0" go test -race="$race" -tags="$tags" ${GO_PACKAGES:-./...}
```

Make `validate-backend` request all levels from its existing `test` dependency:

```diff
 depends = [
   "lint",
-  "test",
+  { task = "test", args = ["-tags=integration,e2e"] },
-  "test-entitytype-fixtures",
   "vet",
 ]
```

The [mise dependency reference](https://mise.jdx.dev/tasks/task-configuration.html#depends)
supports arguments on dependency tables. Verify actual argument forwarding
during implementation, rather than relying on a dry-run alone.

Existing lint and vet tasks will include both tags using `--build-tags` and
`-tags`, respectively, before package arguments. They analyze tagged code but
do not execute infrastructure. Fixture execution runs directly in the integration
suite; the redundant fixture task and environment guard are removed. The
diagnostic `test-profile` includes both tags so it retains its existing profiling
scope.

## Deterministic lint enforcement

Add a standard-library-only checker at `internal/cmd/testboundarylint` and invoke
it from the existing `lint` task before golangci-lint. It walks repository Go
test files without compiling them, parses Go declarations/build constraints,
and reports file/line diagnostics. It exits nonzero on violations. No new mise
task, linter dependency, or agent command is introduced.

| File kind | Required relationship |
| --- | --- |
| Unit behavior | Ordinary `_test.go` name, without a level tag |
| Integration behavior | `_integration_test.go` and the `integration` build constraint |
| E2E behavior | `_e2e_test.go` and the `e2e` build constraint |
| Fixture-only support | No behavior suffix required; may be untagged, `integration`, `e2e`, or `integration || e2e` |
| Generated tests | Preserve generated filenames; ordinary Go compile/test checks still apply |

The checker detects Go test, benchmark, and example entry points when deciding
whether a file contains behavior. `TestMain` is setup, not a behavior test.
It checks parsed constraints rather than searching source comments for strings.
For the current platform-neutral test files, level constraints use the forms
in the table. Additional platform-specific constraints need an explicit checker
extension rather than an unchecked escape hatch.

The check enforces declared level and naming consistency. It does not claim to
prove that a unit test never starts infrastructure through a transitive helper.
Fixture inspection, compilation of each selection, and the infrastructure-free
default run remain separate acceptance checks. The mechanical checker runs
through `lint`/`validate`; filename consistency alone is not boundary correctness.

Use a small internal interface for the checker, with tests feeding source
directly to avoid requiring real repository fixtures:

```go
type diagnostic struct {
    path    string
    line    int
    message string
}

func checkFile(path string, source []byte) []diagnostic
```

The checker tests must cover valid unit/integration/E2E files, both directions
of suffix/tag mismatch, a behavior file incorrectly tagged for both levels,
untagged data builders, single-level fixtures, shared fixtures, `TestMain`,
benchmark/example entry points, generated files, and malformed constraints.
An isolated invalid source fixture must make the actual lint invocation fail;
the clean migrated tree must pass. Keep the checker untagged so its own unit
tests run in the default suite.

## Deliverables and acceptance

```text
.
├── docs/testing.md                 # modify: discrete rules and execution selection
├── AGENTS.md                       # modify: default versus full-gate instructions
├── README.md                       # modify: developer examples
├── mise.toml                       # modify: existing task selection only
├── specs/test-boundaries.md        # this implementation contract and results
├── cmd/**                         # classify executable/config tests and support
├── contracts/**                   # classify schema/codec tests
├── entitytypes/**                 # retain generated conformance names
├── sdk/adapter/**                 # split unit construction from real-NATS tests
└── internal/
    ├── app/**                    # split unit, integration and Core journeys
    ├── adapters/**               # split protocol fixtures from pure conversion
    ├── modules/**                # classify policies, transports and real storage
    │   └── notifications/
    │       ├── service_admission_test.go  # new: gates, cancellation, joining
    │       ├── service_deadline_test.go   # new: deadline/nonsubmission policy
    │       ├── api/send_input_test.go     # new: real decoder with service double
    │       └── sqlite/*_test.go           # retain durable recovery/collision proofs
    ├── platform/**               # separate real boundaries from isolated workers
    ├── cmd/entitytypegen/**      # separate compiler execution from examples
    ├── cmd/testboundarylint/
    │   ├── main.go              # new: repository walk and lint diagnostics
    │   └── main_test.go         # new: tag/suffix and fixture classification cases
    ├── entitytypetest/**         # retain independent conformance checks
    └── {mcpapi,mcpecho}/**       # separate actual socket sessions from pure mapping
```

No production types or Go interfaces change. New recording doubles implement
the existing `notifications.Repository` and API `Notifications` interfaces
inside test files. File renames preserve package ownership and visibility.

| ID | Outcome and owning paths | Dependencies | Size | Deciding check |
| --- | --- | --- | --- | --- |
| D1 | Replace provisional `docs/testing.md` boundary guidance with rules 1–8; update `AGENTS.md`/README selection examples | Approval | S | No new generic section in `CODING_STANDARDS.md`; docs agree with actual task behavior |
| D2 | Complete a per-file classification for every Go test and fixture-support file; split mixed files and apply tags/suffixes by owner | D1 | L | Every existing entry point accounted for; default and combined selections compile; helper closure verified |
| D3 | Wire existing tasks to default local units, full local validate, and full race-enabled CI; add `internal/cmd/testboundarylint` to existing lint | D2 | M | Actual local/CI-mode executions, all-tag lint/vet, checker rejects invalid naming/tags and accepts fixtures, no added task names or missing levels |
| D4 | Move `TestNotificationStrictInputsAndStatusMapping` to `notifications/api`; split `TestServiceConcurrentSendsCancellationAndSeparateDrainGates` and move `TestDeadlinePreservesNonsubmissionAndIgnoresLateAcceptance` to module policy tests; retain assembled seam checks and SQL proofs | D2–D3 | L | Case-to-assertion map, focused tests, unchanged public behavior; no new production test seams |
| D5 | Verify full inventory, changed-package stability, full validation, independent review and CI; record results in this spec | D4 | M | No lost tests/guarantees, no generated/module drift, every full CI check passes |

Sizes express relative scope, not time promises. D2 is a classification-only
pass; D4 is a separate behavioral-test redesign. Do not combine broad assertion
rewrites with tagging, and do not remove tests just because they are expensive.
For ambiguous classifications, inspect the fixture chain before tagging.

Verification must cover:

1. Capture the old all-test inventory and compare names after moves. Document
   renamed or consolidated cases and retained assertions.
2. Exercise default `test`, `test -- -tags=integration`, all-tag `test`, local
   `validate`, and CI-mode race selection through mise. Check emitted test names
   and binary race metadata where relevant. Keep raw evidence outside the repo.
3. Inspect default selected test imports and helper initialization for broker,
   database, listener, and application startup. Run the default suite without
   Docker access; do not stop unrelated developer services to test this.
4. Keep real-Mosquitto checks failing, not skipping, when selected without their
   prerequisite. A full local gate still requires reachable Docker.
5. Run focused changed tests, shuffle/repeat where shared-state or scheduling
   risk changed, then `mise run validate`; inspect generation/format/tidy diffs.
6. Review naming, helper ownership, and actual boundary preservation. Required
    CI must pass for the published head; do not merge without a separate request.
7. Run checker unit cases and an isolated suffix/tag negative control through
   the actual lint task. Verify fixture-only and generated files are handled
   according to the table, and that no new task names appear.

Risks are accidentally excluding helper dependencies and mistaking a smaller
default suite for equivalent coverage. Compile each tag combination, keep an
exhaustive inventory, and retain the full local/CI gate. Classification improves
selection and discoverability; only measured runs can establish build-time gains.

## Approval boundary

Rules 1–8 and D1–D5 were explicitly approved in Plannotator review `pn-820c91`,
including the complete tagging migration and bounded owner-boundary relocations.
Unit tests remain untagged; no separate unit build tag is introduced.

## Implementation record

- Captured the pre-migration inventory of 467 files and 2,107 entry points at
  `/tmp/opencode/hearth-test-boundaries/before.json` before changing test files.
- D1 complete: adopted the approved rules in developer and agent guidance.
- D2 complete: every owner is classified, mixed files and fixture dependencies
  are separated, and generated names are preserved. Before D4, all 2,107
  original entry points remained in their original packages; all test bodies
  were whitespace-identical except qualification of `db.Open` in the extracted
  empty-path unit. Per-file inventories and declaration comparisons are retained
  in the evidence directories below.
- D3 complete: all-level validation, CI selection, lint/vet analysis, fixture
  execution, and profiling are wired. The checker runs first in existing lint,
  which stops on checker failure. An isolated copy of the actual task rejected
  a suffix/tag mismatch and passed after correction. No task names were added.
- First checkpoint: focused unit/integration checks passed for migrated owners;
  full local `mise run validate` passed in 43.42 seconds (mixed cache state,
  all levels, no race). This is not a speedup claim or final acceptance.
- The user authorized parallel migration writers, with isolated ownership and
  integrated review. All five writers completed; their commits were integrated.
- D4 complete: the notification HTTP/MCP matrix is now an API unit test; gate,
  cancellation, join, and deadline policy live in module units. Real SQLite
  retains six distinct durable attempts (including reopen verification),
  storage-failure recovery, collision/no-replay, and missing-destination
  non-admission proofs. The case-to-assertion map is retained below.
- Current combined inventory: 586 test/support files and 2,116 entry points:
  1,307 unit, 794 integration, and 15 E2E. The three D4 cases are the only
  behavioral-test redesigns; the post-audit corrections move unchanged bodies.
- Default units passed uncached with Docker inaccessible and every test binary's
  `bind`/`connect` forcibly denied. Across 1,021 process/thread traces there were
  zero bind, listen, or connect calls. Default selected test imports contain no
  infrastructure fixture packages, NATS server, or Testcontainers imports.
- Independent post-audit tag selections enumerate 1,307 entries by default, 2,101 with
  integration, 1,322 with E2E, and 2,116 combined. CI test-binary metadata confirms
  `-race=true` and `-tags=integration,e2e`; local all-tag metadata has no race flag.
- D5 local acceptance complete: the normal integrated `mise run validate`
  passed in 12.58 seconds with mixed caches, all levels, no race; generation,
  formatting and tidy introduced no unrelated drift. Integration-only and
  E2E-only whole-repository compilations also passed. Independent review found
  no actionable correctness issues and independently confirmed retention of
  every original test body outside the documented D4 redesigns.
- A separate full-gate run under process tracing failed three timing-sensitive
  checks: `TestInFlightDeadlineIsAmbiguousAndDoesNotRetry`,
  `TestCommandLateSetValueSuccessIsNeverAccepted`, and
  `TestSenderRealNATSEvidenceAndOneShotFailures/lost`. Their bodies are unchanged.
  Ten focused shuffled repetitions without tracing passed, followed by the
  normal full gate. This does not establish a flake repair; the failed
  diagnostic run and successful checks are both retained. No deadlines were
  increased and no production behavior changed.
- Actual validation child arguments confirm mise forwards
  `-tags=integration,e2e` to its existing test dependency. Required exact-head CI
  remains a publication gate; its authoritative result is reported in
  [PR #196](https://github.com/mholtzscher/hearth/pull/196), not inferred from the
  local pass. No merge is authorized or performed.

### Per-entry Classify audit

After the first publication, the user requested a Classify assessment for every
test. A complete fresh Code Mode rerun assessed all 2,116 entry points at
`dba5803026834253541a81c4c52686051e209074`, using only the raw file and exact
entry name. Expected labels were compared afterward, not provided as answers.
The configured backend was `typesafe-jev-latest` (`jev-1.13.0`).

The filter retained confidence below 0.8 plus every disagreement, unknown, or
error: 639 cases were reviewed; 1,477 high-confidence matches were counted but
not printed. Normal source inspection resolved missing fixture context without
adding generated context to classifier requests. There were no missing answers,
classifier errors, or unresolved cases after review. Confidence was a review
signal, not a calibrated correctness probability or replacement for lint/tests.

Review confirmed 637 existing labels and corrected two:

- `TestConditionDecisionPreservesSelectedJSONNull` moved from the SQLite
  migration integration file to untagged `condition_decision_test.go`, with its
  plain data builders in `condition_decision_helpers_test.go`. No database is
  opened; the test body and fixture behavior are unchanged.
- `TestCorePublishesDeviceFactsForPreStartupBacklogAndRestart` moved from the
  Device Facts E2E file to `device_facts_integration_test.go`. SQL seeding and
  raw NATS recovery, with readiness probes, do not constitute the required
  HTTP/MCP/public Adapter SDK-driven journey. Its assertions and helpers are
  unchanged; shared Core fixtures use `integration || e2e`. The real SDK/HTTP
  journey remains E2E.

Both moved test bodies were compared directly with the audited revision and
are identical. Actual Go test inventories still contain all 2,116 entries;
unit/integration/E2E/combined selections compile independently. The corrected
default suite also passed uncached with bind/connect denied and zero network
calls. The actual generator formatting path was additionally traced: even its
failing-second-output test executes `go env`, confirming its integration label.
Post-correction publication checks and exact-head CI are reported in PR #196.

### Verification evidence

Raw artifacts remain outside the repository:

- `/tmp/opencode/hearth-test-boundaries/`: baseline/final inventories, D2 body
  preservation, tag-selection summaries, checker controls, network-denied unit
  trace, local/CI binary metadata, and integrated validation logs.
- `/tmp/opencode/hearth-test-boundaries/classify-audit.md`: complete fresh
  per-entry audit coverage, filtered review totals, and the two corrections;
  `post-audit-*` files contain corrected selection/runtime evidence.
- `/tmp/opencode/hearth-test-boundaries/d4-notification-policy-evidence.md`:
  every moved notification row and its surviving assertion, including the
  distinction between decoder rejection and service/repository validation.
- `/tmp/opencode/automations-boundaries/`: exhaustive Automation classifications,
  declaration preservation, focused tag checks and unit network evidence.
- `/tmp/opencode/boundaries-modules/`: remaining module classifications,
  original-to-final entry map, declaration hashes and focused validation.
- `/tmp/opencode/boundaries-app/`: application/command classification and
  independent unit/integration/E2E execution evidence.
- `/tmp/opencode/boundaries-edges/`: Adapter/generator/MCP classifications,
  declaration preservation, generated fixture execution, and real-Mosquitto
  negative controls (selected checks fail rather than skip without access).

## E2E value audit follow-through

The user approved the source-audit recommendations on 2026-11-05. This is a
test-only change. Preserve production behavior and every existing journey's
distinct assembly, ordering, persistence, and recovery assertions.

### Task checkpoint

- Active playbook: mstack Feature, using the approved audit as the fix sketch.
- Workspace: `/home/michael/code/worktrees/hearth/test-hearth-e2e-value`.
- Branch: `test/hearth-e2e-value`; base: `8c570d03624d762eb37e9dc6e1d92b14abc4f7f4`.
- D1: strengthen dashboard content, expression HTTP/MCP evidence, simulator
  terminal outcomes, exact source-report identities, and restart no-replay checks.
- D2: move hand-assembled branch recovery and shutdown-resource tests to
  integration while preserving their assertions and all tag selections.
- D3: verify unavailable-State and held-time preconditions, notification payloads
  and catalog, and deterministic fixture readiness/cleanup diagnostics.
- Completion checklist: [x] D1; [x] D2; [x] D3; [x] independent review;
  [x] final focused checks and `mise run validate`; [x] one unmerged PR with green CI.
- Current step: all D1-D3 changes are integrated. Accepted worker commits are
  `2a9df92` (boundaries), `8b1ff0b` (public outcomes), and `5954916`/`84a78d7`
  (Function outcomes and bounded control requests). Review is complete. The
  revised dashboard turn/history are now asserted, and save consumes the actual
  revised artifact rather than the fixture. Final local checks cover that change;
  publication and exact-head CI are the remaining delivery gate.
- Verification evidence: the audited revision `a0f135f` passed all 23 E2Es
  uncached without race instrumentation in 20.781 seconds. That run does not
  establish flake rates or hardware/model compatibility. The updated `8c570d0`
  baseline also passed all 23 E2Es uncached in 15.008 seconds; Docker 29.9.2 is
  reachable for the full gate. Moved tests pass under integration-only selection;
  the default selection compiles and focused lint reports zero issues. An initial
  strengthened Conditions check incorrectly expected composite nodes in leaf
  evidence; it was removed in favor of the existing explicit root result and
  named-leaf checks. The corrected Conditions/Function/reusable checks pass.
- Evidence directory: `/tmp/opencode/hearth-e2e-value/` contains the before/after
  test inventories. All 23 original names survive: 21 remain E2E and two now
  belong to integration. The shutdown test body is unchanged apart from its tag.
  `journeys.log` records all 23 passing uncached across their corrected levels
  in 9.415 seconds. `validate.log` records the complete local gate passing in
  72.31 seconds, including generation, formatting, tidy, lint, all Go levels,
  vet, frontend tests, and frontend build. `journeys-race.log` records all 23
  passing with race detection in 39.018 seconds. The verbose non-race run has
  zero Session-release timeout warnings.
  After the final dashboard correction, `journeys-race-final.log` records all 23
  passing uncached with race detection in 43.262 seconds. `validate-final.log`
  records the complete final gate passing in 63.77 seconds; unrelated Go packages
  reused cached results. The helper extraction made an existing lint suppression
  unnecessary, so that suppression was removed before the final successful gate.
- Review recovery: the user cancelled looping reviewers. Both replacement
  `openai/gpt-6.1-terra#xhigh` reviews completed in one bounded pass. The valid
  revised-artifact finding was fixed. The proposed duplicate Fact payload checks
  were dismissed: the shared decoder already asserts payload Entity/variant
  agreement, and the journey now checks the route against the original report.
  All delegates have completed or been cancelled; none remains active.
- Delivery complete: PR #289 is open and unmerged at
  `2996894914122d634225599ccbe0327dc1917c8e`. Exact-head backend and web checks
  succeeded; main-only cache retention was skipped, not counted as a pass.
  `/tmp/opencode/hearth-e2e-value/published-checks.json` records stable head
  evidence. No required subset was reported; actual backend/web outcomes were
  verified instead of inferring success from an empty requirement set.

## E2E performance review checkpoint

- Active playbook: mstack Performance, measurement-only. User requested review,
  not implementation. Preserve all journeys, assertions, and production behavior.
- Workspace/branch: `/home/michael/code/worktrees/hearth/test-hearth-e2e-value`,
  `test/hearth-e2e-value`, revision `2996894914122d634225599ccbe0327dc1917c8e`.
- Completion checklist: [x] inventory 21 current E2E journeys; [x] repeated
  uncached timings; [x] separate profile and source attribution; [x] ranked
  recommendations with correctness constraints and measurement limitations.
- Metric: elapsed seconds for the 21 E2E journeys in `internal/app/hearthd`,
  CI-like `GOMAXPROCS=2`, `GOFLAGS=-p=1`, race enabled, existing mise test task.
  No speedup target or implementation attempt is authorized.
- Current step: review complete; recommendations only, no implementation. Evidence
  destination: `/tmp/opencode/hearth-e2e-value/performance/`.
- Delegation: bounded read-only fixture-cost review by Terra xhigh
  `ses_e532edbb8ffejwojTa5NE1eGZM` completed; no measured fixture win claimed.
  Parent confirmed wire-schema compilation and migrated images already use
  once-per-binary caches. Parent owns timing and wait analysis.
- Verification: five serialized, uncached race runs all passed exactly 21
  top-level E2Es, with zero skips, failures, or Session-release warnings.
  Package elapsed seconds: 39.636, 37.278, 37.801, 41.621, 32.586; median 37.801,
  range 32.586–41.621. Command wall times including mise/build overhead: 45.489,
  40.074, 40.646, 44.509, 35.376 seconds. Separate non-race diagnostic passed in
  12.688 package seconds (one run, not a comparative benchmark).
- Workload: Go 1.27.1 on Linux, 24 logical CPUs available but GOMAXPROCS=2,
  GOFLAGS=-p=1, default parallel=2; warm Go build cache, -count=1 test-result
  caching disabled, fresh per-process fixture preparation. This is E2E-only,
  not the full backend job. Host was not isolated; a brief vmstat capture showed
  approximately 8% aggregate I/O wait. No candidate was implemented or compared.
- Separate diagnostic profile passed in 38.027 package seconds, with 47.10 CPU
  sample seconds. Do not combine those samples with benchmark elapsed times.
  `newMCPServer` accounts for 5.23 cumulative CPU seconds, including 3.24 under
  `compileObjectContract`; SQL parsing accounts for 8.91 cumulative CPU seconds.
  These overlap caller totals and are not directly additive wall-time savings.
- Highest-priority test-fixture candidate: use test-owned non-pooling HTTP clients
  consistently in `waitForCoreHealthz`, `waitForCoreReady`, and
  `conditionsHistorySummaries`, draining and closing response bodies. They still
  use the global default client despite the existing non-pooling fixture's
  documented shutdown rationale. Aggregate HTTP shutdown blocking is 9.76s in
  the trace; the profile does not prove which Core instances had orphan sockets.
  Keep all readiness assertions and deliberate unfinished-connection tests.
- Highest-priority investigation: recovery journey median 8.31s, range
  3.25–17.97s. In the separate trace, its first and third stops block 4.82s and
  5.04s. Aggregate health relay/consumer drains also include multi-second waits.
  Correlate per-stop HTTP states, health relay operations, consumer closure, and
  NATS flushes before choosing a fix. NATS v1.54.0 checkDrained Flush waits total
  50.28 goroutine-seconds; that is not suite elapsed time or a proven root cause.
  Never bypass joins or reduce deadlines to manufacture a speedup.
- Production-only candidate, requiring separate approval: reuse immutable MCP
  contract compilation by actual contract content, preserving per-registration
  schema-hook behavior, defaults, diagnostics, validation, and per-server handlers.
  A reflect.Type-only cache is not justified: native schema hooks may be mutable.
  Wire schemas, Entity type codecs, and migrated images already have caches.
- Small test-only candidate: Device Fact `assertNone` spends 300ms on every
  successful call (300.48ms observed in trace). Replace only with a proven
  publisher/broker completion barrier and exact forbidden-publication evidence,
  not a shorter observation window or an immediate empty-channel check.
- Rejected shortcuts: deleting/reducing journeys, sharing mutable Core/SQLite/
  broker fixtures, skipping the empty-database startup, disabling race in CI,
  reducing contractual held-state time, and blanket t.Parallel changes.
- Principles applied: [Explain the number](/home/michael/.agents/skills/mstack/principles/explain-the-number.md)
  prevents treating aggregate CPU/wait samples or one non-race run as a speedup;
  [Test behavior](/home/michael/.agents/skills/mstack/principles/test-behavior.md)
  keeps durable recovery, real joins, and meaningful negative assertions intact.
- All measurement shells and bounded delegates completed. Raw evidence includes
  `race-{1..5}.jsonl`, matching `.time`/`.stderr`, `environment.txt`, `profile.jsonl`,
  `cpu.pprof`, `trace.out`, `sync.pprof`, `cpu-top.txt`, `cpu-cum.txt`,
  `sync-top.txt`, `wait-sites.txt`, and `startup-cpu-sites.txt` in the directory above.
- Next action: present ranked recommendations; await approval before experiments
  or implementation. No commits, pushes, or new PR for this review. Only this
  checkpoint changed; tests and production code remain untouched.

## E2E performance experiments checkpoint

- Active playbook: mstack Performance, bounded one-off attempts. User approved
  testing the four reviewed opportunities and validating every change separately.
- Approved scope: HTTP fixture isolation; diagnose actual recovery drain stalls;
  immutable MCP contract preparation reuse; replace the 300ms Fact absence wait
  only with equally strong completion evidence. Preserve all 21 journeys, real
  startup/restarts, race instrumentation, data isolation, deadlines, and assertions.
- Delivery workspace remains `test/hearth-e2e-value` at `2996894` with open PR #289.
  The prior review checkpoint edits stay here. Disposable candidate worktree:
  `/tmp/opencode/hearth-e2e-value/perf-experiment`, detached at `2996894`.
  No other worktree or session's edits will be changed. Only accepted candidates
  will be integrated into the delivery workspace; rejected owned diffs revert.
- Frozen comparison: `/tmp/opencode/hearth-e2e-value/performance/compare.sh` and
  `summarize.py`; five alternating baseline/candidate runs, identical exact 21-name
  selection, `-tags=e2e -parallel=2 -race -count=1 -json`, GOMAXPROCS=2, GOFLAGS=-p=1.
  Compile warmups are outside measured runs. Verify exact test names, zero skips,
  failures, and Session-release warnings on every run. Report package elapsed
  separately from command wall time and profiled CPU/aggregate wait time.
- Decision log: `/tmp/opencode/hearth-e2e-value/performance/decision.tsv`.
- Completion checklist: [x] scope/workspace/harness; [x] HTTP experiment;
  [x] recovery diagnosis; [x] MCP experiment; [x] Fact barrier experiment;
  [x] independent review and full validation of accepted changes; [x] report
  per-change keep/revert verdicts with raw evidence. No arbitrary target speedup.
- Current step: experiments complete. Keep HTTP isolation (`a3d89cb`) and output
  contract reuse (`436a1b7`); reject the Fact fence. The table in `decision.tsv`
  records every attempt. Publication and exact-head check evidence are recorded in
  `performance/final/published-pr.json` and `performance/final/published-checks.json`.
- Supporting read-only Terra review completed (`ses_e5322198fffellB4hCt0tvrqq2`):
  prefer output-only schema-content caching first; never share mutable compilers.
  Preserve hook evaluation per registration and test changed hooks/invalid startup.
- Initial measurement shell `sh_1acdf4407001FwX0hsRt3J4ysJ` completed. Active
  confirmation/profile shell: `sh_1ace7100e001H4iXk0Z4tpHeUu`, into
  `performance/01-http-confirm` and `performance/01-http-profile`.
  Await native completion; do not poll files or change either code snapshot.
- Fact-barrier design review (`ses_e532edbb8ffejwojTa5NE1eGZM`) found a concrete
  weakening: shutdown could cancel a delayed forbidden publication that the
  existing live-Core 300ms window would catch. Subscriber Flush+Barrier also
  requires a producer-side routing fence, not just PubAck/NumClients. Do not
  keep the proposed shutdown-first count check as an equivalent optimization.
  Candidate 4 must pass this negative control before timing, or be rejected.
- HTTP first comparison: focused race passed in 11.113s and focused lint had
  zero issues. Five A/B pairs all passed 21 journeys with no skips/failures/release
  warnings. Baseline median 36.763s (32.262–38.151), candidate 33.124s
  (32.222–34.774): provisional median reduction 3.639s / 9.9%, not yet accepted
  because spread overlaps and pair five was unchanged. Recovery candidate timings
  3.24/3.21/2.80/3.20/3.13s versus baseline 2.91/8.11/3.05/13.11/3.06s;
  Device Facts candidate 1.52–1.54s versus baseline 1.52–6.32s. Evidence:
  `performance/01-http/summary.json`, exact candidate `candidate-verified.patch`.
  Next: five confirmation A/B pairs and a separate candidate trace, still same
  unchanged snapshots. No accepted change or decision-log verdict yet.
- HTTP confirmation completed (`sh_1ace7100e001H4iXk0Z4tpHeUu`); no shell active.
  Across ten A/B pairs: baseline median 36.8985s (32.262–42.506), candidate
  33.072s (32.152–35.303), reduction 3.8265s / 10.37%; 9/10 positive pairs,
  exact two-sided sign p=0.021484375. All 210 journeys per side passed without
  skips/failures/release warnings. HTTP candidate accepted as `a3d89cb`, integrated
  diff byte-matched the measured candidate. Disposable worktree now clean at that
  accepted revision; it will compare the next change against HTTP isolation.
  Profile still has HTTP4.24s and health11.69s aggregate waiting; do not claim all
  recovery stalls fixed. Next: MCP output-content cache trial and owner regression
  tests, then five alternating same-workload pairs before keep/revert.
- MCP candidate is output-only and content-keyed. Schema hooks still run on every
  registration; successful immutable schemas are shared, compilers and handlers
  are not. Added registration-level changed-hook, per-server snapshot, cache-hit,
  invalid-output, and invalid-later-hook coverage. Initial owner race checks passed
  in 0.354s; lint rejected unchecked sync.Map assertions. Switched to a small typed
  mutex/map instead (no speculative cache policies). This is a pre-measurement
  correction, not a timed attempt or accepted improvement.
- Active shell `sh_1acf09137001Zyht84iJL07ex7`: rerun owner race+lint, then five
  A/B pairs in `performance/02-mcp-output`. Baseline/root is `a3d89cb`; candidate
  worktree adds only output_validation.go and registration_schema_snapshot_test.go.
  Await completion; do not poll files or mutate either measured snapshot.
- MCP owner race passed 0.365s and focused lint zero. Five A/B pairs passed all
  21 journeys with no skips/failures/release warnings. Baseline `a3d89cb` median
  32.693s (31.869–32.778), output-cache candidate 28.923s (28.780–29.390);
  all five paired reductions 2.946–3.913s, non-overlapping ranges. Clear timed
  win pending separate CPU/heap attribution and independent correctness review.
- Active profile shell `sh_1acf6e84e001ljri18kzJ3JTZu` writes
  `performance/02-mcp-output-profile/`; A/B shell `sh_1acf09137001Zyht84iJL07ex7`
  completed. Review `ses_e5322198fffellB4hCt0tvrqq2` is active, bounded read-only.
  Do not mutate candidate until profile/review finish. Next: accept/revert cache,
  execute the Fact barrier negative control in the disposable target, then perform
  final review and validation in the delivery worktree.
- MCP accepted as `436a1b7` after clean independent review; root files byte-match
  the measured candidate. Production change is only successful output-contract
  compilation reuse by full schema bytes, with unchanged hooks and handlers.
  Separate CPU profiles (not timings) attribute newMCPServer5.36→2.90 CPU seconds,
  compileObjectContract3.20→0.88; total CPU44.58→39.36s. Single sampled heaps were
  40.72→41.31MiB, with approximately 3.01MiB retained under output preparation;
  this is an observed lifetime tradeoff, not a repeated memory benchmark.
- Recovery diagnosis: all ten HTTP candidate recovery timings were 2.80–3.36s;
  with the cache they were 2.28–2.53s. Latest trace attributes first/second/third
  recovery stop waits to 38.75/17.79/39.87ms, rather than earlier multi-second
  stalls. No production shutdown/deadline change was justified or made. Aggregate
  waits elsewhere in the suite must not be attributed to this journey.
- Fact candidate rejected by executed negative control before performance timing.
  In disposable real-Core journey, schedule a forbidden extra publication 100ms
  later and cancel it with Core. Existing live-Core assertion fails for the
  expected unexpected-Fact reason; shutdown-first subscriber Flush+Barrier/count
  passes with logged `context canceled`, proving lost detection. Logs and exact
  rejected/probe diffs: `performance/03-fact-control/`. The deliberate failure's
  teardown warning is not accepted validation evidence. Probe/fence code reverted;
  original 300ms assertion remains. Disposable worktree should be clean at436a1b7.
- All experiment/profile shells and prior design reviewers completed. Next:
  final independent combined review, 23-original-journey race check and full
  `mise run validate`, then publish the accepted commits on existing PR #289.
- Final independent review `ses_e532edbb8ffejwojTa5NE1eGZM` completed clean on all
  five accepted files. Code cleanup found no extra wrappers, compiler sharing, or
  weakened assertions. Comment review retained the schema-content/hook ownership
  rationale. Prose cleanup corrected the PR's former test-only claim and records
  the process-lifetime output-cache tradeoff. No visible UI/CLI behavior changed,
  so screenshots do not apply.
- Final validation shell `sh_1acfc9bb2001uFyYixSh4QFSuU` completed: all 23 original
  journeys passed uncached with race instrumentation in 31.640s, with exact name
  preservation and zero skips, failures, or Session-release warnings. The full
  unset-GO_PACKAGES `mise run validate` gate passed in 65.44s: generation, format,
  tidy, lint0, all Go levels, vet, 278 frontend tests, and frontend build. The app
  and MCP packages ran fresh; unrelated Go packages reused cached results. No
  generated, formatting, or dependency changes resulted. Evidence is in
  `performance/final/`. All delegates and experiment shells are finished.
- Principles applied: [Explain the number](/home/michael/.agents/skills/mstack/principles/explain-the-number.md)
  required alternating same-workload runs and separate profiles before accepting
  improvements. [Test behavior](/home/michael/.agents/skills/mstack/principles/test-behavior.md)
  caused the faster Fact fence to be rejected after its negative control exposed
  lost detection. Each accepted change has its own measured baseline and commit;
  the two percentages are not additive or a direct cumulative benchmark.
