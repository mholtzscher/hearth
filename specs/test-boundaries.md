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
