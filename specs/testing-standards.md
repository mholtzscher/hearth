# Testing standards and validation workflow

**Status:** Initial implementation completed. The user withdrew the added testing
section from `CODING_STANDARDS.md`; a concrete suite strategy is under review.
Verification snapshots and current CI are reported in PR #196.
**Date:** 2026-10-20
**Baseline:** `bd2d42b98d171c21cb7de80e22156a69202c3ae8` on `main`.

## Outcome and scope

Make agents' routine validation focused and inexpensive, and establish rules for
placing tests at the boundary that owns their behavior. Keep a comprehensive
pre-publication gate. The initial implementation establishes standards and
commands, removes fuzzing and mutation tooling, and preserves worthwhile
deterministic coverage from the removed fuzz targets.

Decisions from the discussion:

- Prioritize local agent feedback; use tiered validation.
- Choose test boundaries by risk and observable guarantees, rather than a fixed
  unit/integration ratio or a requirement to mock every dependency.
- Use focused non-race checks locally and full race-enabled validation in CI.
  Local race checks remain optional for concurrency diagnosis.
- Remove native fuzz tests and campaigns entirely.
- Remove mutation-testing tooling. Regression fixes should demonstrate failure
  on the buggy behavior when practical; that does not require a mutation tool.
- Permit justified property/model tests with bounded generation, replayable
  failures, shrinking where applicable, and an independent oracle. Existing
  deterministic examples remain the default; introduce no property library now.
- Establish no numerical runtime budget yet.

The first implementation does not migrate all existing fixtures or process
tests. The explicit fuzz-removal decision adds the necessary targeted test
conversions to the standards/workflow scope. Production behavior, protocol
contracts, package ownership, and test deadlines retain their current contracts.
Work and evidence from other experimental branches are excluded.

## Evidence and limits

### Local and CI observations

The baseline contains 472 Go test files in 81 test-bearing directories, 2,098
top-level `Test*` declarations, and 15 `Fuzz*` declarations. The 67 tracked
`testdata` files total 67,630 bytes: fixture *construction* and dependency scope
are more plausible concerns than large checked-in payload files.

The successful main-branch [CI run at `4cf63e7`](https://github.com/mholtzscher/hearth/actions/runs/39448412157)
spent 929.71 seconds in backend checks after a Go cache miss. Its race-test
command occupied approximately 713 seconds, including compilation; package
results reported 211.511 seconds for Core and 67.809 seconds for Automation API.
Lint occupied approximately 149 seconds. These are one run's observations,
not comparative benchmarks. Runner waiting time is excluded.

Local diagnostic environment: Linux/amd64, Go 1.27.1, 24 available CPUs, existing
build cache, Docker 29.8.2 available, initial load average approximately 1.1.
`mise run --skip-deps test -- -count=1 -json` took 108.114 seconds and **failed**:
80 test-bearing packages passed; Automations failed
`TestAutomaticAdmissionReadsOneSnapshotForMatchingConditionUnion` with a context
deadline. The selected test passed five isolated race-enabled repetitions.
This suggests a scope/load-sensitive failure but does not establish its cause
or repair it.

A separate `mise run --skip-deps test-profile` Core capture also **failed**,
waiting for `core.http_listening` in `TestRunCancelsCleanlyAfterReady`. Its CPU
profile attributes 22.37% of sampled CPU cumulatively to `newMCPServer`, including
19.03% to `automations/api.RegisterMCP`; race instrumentation is prominent in
flat samples. These percentages overlap and are not additive or wall-clock
savings. The source path is
[`internal/app/hearthd/http_handler.go`](../internal/app/hearthd/http_handler.go).
Profiling overhead and the failed run preclude using it as a successful timing
baseline.

Local raw evidence is under `/tmp/opencode/hearth-testing-audit-bd2d42b/`:
`tests.jsonl`, `tests-timing.txt`, `main-ci.log`, `daily-fuzz.log`, and
`core-profile/{tests.jsonl,cpu.pprof,trace.out,tests.test}`. These local artifacts
are not shipped project files. No full `validate` run or cold local build was
performed during this read-only audit.

### Technique assessment

Nine of the 15 fuzz targets have campaign tasks. Normal Go tests replay the
declared seeds for all targets. The latest inspected daily campaign executed
millions of inputs per target; all 30 daily runs from September 21 through
October 20 succeeded. No checked-in fuzz-failure corpus or documented
fuzz-discovered fix was found in the inspected main history since August 1.
This is limited evidence of return, not proof that fuzzing cannot find defects.

Some targets protect useful normalization, identity, or numeric invariants.
Others have weak or partly circular oracles: `FuzzDeviceState` does not assert
the sibling isolation its comment promises, and `FuzzTemperaturePlanBoundary`
derives its expected conversion using production `normalizeTemperature`.
Removing fuzzing trades away automatic input exploration. Deterministic cases
retain selected behavioral guarantees, not equivalent search breadth.

Fuzz campaigns and Gremlins are already outside local `validate`; deleting them
does not explain or promise a major local validation speedup. Workflow scoping
and later fixture-boundary improvements address that goal.

Commit `87442af` deliberately replaced Rapid tests with fixed examples and
operation sequences. The new policy permits justified generated properties
without automatically reversing that migration. Rapid is currently indirect
in `go.mod`; module tidying, rather than manual dependency removal, determines
whether it remains necessary transitively.

## Withdrawn generic standards

The user withdrew the following additions to `CODING_STANDARDS.md` after the
initial implementation. They are retained here as the proposal's history, not
as adopted review rules. The replacement will define discrete suite boundaries,
fixtures, organization, and local/CI execution in `docs/testing.md`.

Add these numbered judgment rules to `CODING_STANDARDS.md`, using its existing
rule/why/applies-to/source/example format. They apply to new and changed tests;
existing violations are migration candidates. The confirmed choices are stated
policy; source examples illustrate the rules, rather than making all existing
practice normative.

| ID | Rule to adopt | Concrete application |
| --- | --- | --- |
| R1 | Test a behavior at the smallest boundary capable of exposing its plausible defect. | A malformed notification body needs its real transport decoder and a recording service double; it does not need an SDK session or broker. |
| R2 | Derive important expected results from a contract, invariant, reviewed example, or independent model. | A temperature conversion expects literal milli-Celsius values; it does not call the production converter to calculate its answer. Pair rejection checks with valid controls. |
| R3 | Give each higher-level scenario a distinct integration, assembly, lifecycle, or recovery guarantee. | Keep a restart/no-replay test; put the complete classification matrix with its service owner. A universal one-test-per-feature quota would be too restrictive. |
| R4 | Construct only the fixture dependencies and data needed by the assertion, preserving per-test isolation. | Use a migrated SQLite template for repository tests, plain NATS for request/reply, and file-backed JetStream for persistence/recovery semantics. Reuse immutable setup; isolate mutable resources and state. |
| R5 | Synchronize on events or controlled time, and distinguish liveness deadlines from performance requirements. | Prefer channels/barriers or `testing/synctest`; use real sockets/time when that is the contract. Bound expensive concurrent fixtures instead of increasing deadlines or retrying failures until green. |
| R6 | Choose example or property tests for a named failure mode and require reproducible, non-vacuous assertions. | Use examples for finite cases; use a property when an independent law or state model covers combinations examples miss. Keep generation bounded and counterexamples replayable. |
| R7 | Preserve the owning guarantee before removing or moving a test. | Identify the surviving assertion and production entry point; retain real SQL atomicity, broker acknowledgment, and startup/recovery checks where fakes would conceal the defect. |
| R8 | Judge a suite by protected behavior and fault sensitivity, not test counts or coverage quotas. | Coverage is diagnostic. Verify a regression fails on the bug when practical. Maintain deterministic regressions rather than native fuzz targets or mutation campaigns. |

`docs/testing.md` will own the detailed boundary matrix, technique policy, and
command selection. `AGENTS.md` will link it when adding/changing tests or choosing
validation scope. `CODING_STANDARDS.md` will link it for testing reviews. This
keeps detailed operational guidance out of the always-loaded agent instructions.

The current testing policy supersedes technique/tooling prescriptions in older
feature specs. Their behavioral acceptance criteria and historical verification
records remain meaningful; historical mentions are not executable requirements
to restore removed fuzzing or Gremlins.

### Boundary matrix for `docs/testing.md`

| Boundary | Owns | Typical fixture |
| --- | --- | --- |
| Pure/domain or module service | Decisions, conversions, state transitions, error classification | Real owner plus small inputs; hand-written dependency doubles where they simplify the test |
| Repository/migration | SQL constraints, commit/rollback, ordering, upgrade/data preservation | Disposable real SQLite with production migrations and queries |
| HTTP/MCP transport | Decoding, status/error mapping, schema, serialization, method/tool registration | Real router/decoder and narrow service double; real storage only for an explicit cross-boundary guarantee |
| NATS/MQTT/WebSocket adapter | Actual wire behavior, acknowledgments, retained delivery, reconnect, framing | Real disposable boundary for protocol semantics; fake vendor peers for explicitly synthetic scenarios |
| Application/system integration | Production wiring, durable recovery, shutdown ordering, no replay | Only required Core/services, broker, and database; real process lifecycle when process behavior is asserted |
| End-to-end journey | A small set of important user-visible flows across production entry points | Assembled system or simulator; browser only when browser behavior is part of the claim |
| Frontend unit/component | Parsing, user interaction, loading/error states, display contracts | Vitest/jsdom and small contract-shaped responses; these are not browser E2E proofs |

These are boundaries, not mandatory directories. Property and example tests are
techniques usable at several boundaries; fuzzing and mutation are techniques,
not additional integration levels. Classify a test by what actually executes,
not its filename. Keep tests with their behavioral owner.

Use real SQLite for transaction guarantees and real brokers for delivery
semantics. An integration test can be cheap and justified. A large fake that
reimplements a database or a 30-method repository can cost more to understand
than a focused real component test. Captured/sanitized/synthetic fixture
provenance and relevant protocol versions must be clear where compatibility
depends on them. Synthetic peers do not establish real-hardware compatibility.

## Validation interface

### Reuse existing commands

The user approved this simplification after reviewing the initial implementation.
Add no task names. Reuse the existing `test` task with a non-race local default
and race instrumentation when `CI=true`. Keep `lint` and `vet` as separate
existing tasks. GitHub Actions supplies `CI=true`; an optional local race check
uses `mise run test -- -race`.

Select packages explicitly through `GO_PACKAGES`. The existing tasks retain
their default full selection when it is unset. Go test flags after `--` use
mise's existing argument forwarding, and normal build/result caches remain.

Examples:

```sh
GO_PACKAGES='./contracts/functionschema' mise run test
GO_PACKAGES='./entitytypes' mise run test -- -run '^TestJSONCodec' -count=1
GO_PACKAGES='./internal/platform/lifecycle' mise run --skip-deps test
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps lint
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps vet
```

Choose the owning package and relevant consumers/transports. Changes to a
shared codec, SDK, schema, or module contract require checking its consumers,
not only the edited directory. Prefer explicit package selection to an
unproven automatic changed-files selector.

The focused tasks run the selected packages' existing tests, including their
integration tests. They are not a new unit-only or guaranteed Docker-free lane.
Missing infrastructure remains a failure when a selected test requires it.
This phase adds no build tags, skip conventions, or maintained package lists.

### Iteration and completion

| Situation | Required action |
| --- | --- |
| Routine Go iteration | Focused `test`, with existing lint/vet tasks as needed; format through the existing mise task as appropriate |
| Goroutines, cancellation, ownership, shared mutable state | CI runs race checks; optionally diagnose locally with `test -- -race`; shuffle/repeat when schedule or order sensitivity warrants it |
| Migration/query/generator/schema changes | Run the owning real-boundary tests and existing generation/fixture checks; inspect regenerated output |
| Frontend changes | Focused Vitest via `mise run --skip-deps web-test -- <filter>` and frontend build/typecheck as appropriate; install dependencies when needed |
| Finished integrated change | Run `mise run validate`, review generated/format/module changes, and inspect the final diff before publication |
| New changes or failed checks | Rerun affected checks; re-establish full validation after integrating the final change |

Retain `test`, the race-enabled diagnostic `test-profile`, generated fixture
execution, and the comprehensive `validate` selection. Local `test` and
`validate` run without race instrumentation; CI retains it. CI continues backend
validation and frontend tests/container build. Full validation is a completion check rather
than a requirement after every local edit. Do not disable tests, increase
deadlines, or accept an unexplained flaky repeat to claim success.

## Fuzz removal and deterministic coverage

Remove the nine `fuzz-*` tasks, `fuzz`, `list-fuzz-tasks`, the PR workflow's fuzz
job, `.github/workflows/fuzz.yml`, and all 15 `Fuzz*` functions. Convert only
behaviorally useful unique cases; merge with existing example tests when they
already protect the same guarantee. Avoid replacing automatic search with a
large hand-written random loop.

Before deleting each target, record its retained test names or the existing
tests that subsume its worthwhile cases in this table. This is the bounded
coverage-preservation check for the implementation, not a requirement to retain
each redundant seed as a new test.

| Existing target(s) | Owning destination and required deterministic guarantee |
| --- | --- |
| `entitytypes/FuzzJSONCodecDecodeStability` | `entitytypes/codec_test.go`: accepted numeric spellings and int64 boundaries normalize stably; invalid types/overflow/trailing input reject; positive values remain correct |
| `functionschema/FuzzNormalizeObject` | `contracts/functionschema/json_test.go`: accepted object normalization preserves large numbers and is stable; existing explicit malformed/size/depth/duplicate cases retain rejection guarantees |
| `natswire/FuzzCodecObservationRoundTrip` | `internal/contracts/v1/natswire/codec_test.go`: typed envelope fields and raw numeric values survive; representative malformed envelopes reject for their intended rule |
| `natswire/FuzzCodecEntityEventRoundTrip` | `internal/contracts/v1/natswire/entity_event_codec_test.go`: valid event identity/name survives and forbidden fields/identity forms reject |
| `natswire/FuzzSubjectParsersCanonicalRoundTrip` | `internal/contracts/v1/natswire/subjects_test.go`: each supported route family parses/builds canonically; wrong case/version/UUID/wildcard/truncation/extra tokens reject |
| `natswire/FuzzParseDeviceFactSubjectCanonicalRoundTrip` | `internal/contracts/v1/natswire/device_fact_subjects_test.go`: valid fact families/variants and exact route values; invalid variants and noncanonical subjects reject |
| `zigbee2mqtt/FuzzDiscoverInventory` | `internal/adapters/zigbee2mqtt/discovery_test.go`: representative actuator/sensor/event/action plans keep required identities and capabilities; malformed siblings do not invalidate valid ones |
| `zigbee2mqtt/FuzzDeviceState` | `internal/adapters/zigbee2mqtt/state_test.go`: preserve exact projected values, unique identities, and a valid sibling alongside malformed input; do not preserve only nonempty-output assertions |
| `zigbee2mqtt/FuzzTemperaturePlanBoundary` | `internal/adapters/zigbee2mqtt/entity_temperature_test.go`: literal expected conversion/range cases including exponent syntax, limits, sub-milli precision, null/string, and overflow; reuse existing tables/sweep |
| `ecowitt/FuzzParseStationReport` | `internal/adapters/ecowitt/report_test.go`: size/field limits, accepted identity and secret non-retention, safe errors, and source-time bounds |
| `ecowitt/FuzzProjectReport` | `internal/adapters/ecowitt/observation_test.go`: independently expected finite values and sibling isolation, including malformed numeric data |
| `zwavejs/FuzzZWaveJSSnapshot` | `internal/adapters/zwavejs/discovery_test.go`: known eligible node projects expected descriptors; malformed/empty input and the entity bound have explicit outcomes |
| `zwavejs/FuzzZWaveJSEvent` | `internal/adapters/zwavejs/client_test.go`: accepted value-event identity is preserved; malformed Command Class/property forms reject at their decoder boundary |
| `automations/FuzzDecodeAutomationDefinitionConditions` | `internal/modules/automations/conditions_definition_test.go`: absent/null/present/nested conditions have contract-derived acceptance and canonical round-trip behavior |
| `automations/FuzzDecodeAutomationConditionDecision` | `internal/modules/automations/conditions_decision_codec_test.go`: supported decision variants and retained snapshots preserve meaningful data through round trip |

Remove now-unused fuzz-only helpers/constants, checking their other consumers.
Delete fuzz-only files after relocating useful coverage. In mixed files, retain
all unrelated behavior tests. Positive and negative fixtures must identify the
expected result explicitly; an unconditional return on decode error would hide
regressions in valid-case acceptance.

## Mutation tooling removal

Remove the Gremlins tool declaration and task from `mise.toml`, its tool block
from `mise.lock`, `.gremlins.yaml`, and the current README invocation. Do not
uninstall anything from the developer's machine or rewrite historical evidence.
Retain existing regression/negative-control tests that independently protect
real behavior, including generator-conformance checks; removing the campaign
tool is not a reason to delete these tests.

## Implementation layout and deliverables

No production domain, API, event, or persistence types change. The interfaces
changed are the developer commands above and removal of obsolete fuzz/mutation
commands. No new production seams or dependencies are required.

```text
.
├── CODING_STANDARDS.md             # modify — judgment rules R1–R8, policy link
├── AGENTS.md                      # modify — focused/full workflow and reading trigger
├── README.md                      # modify — developer entry points, remove obsolete invocation
├── docs/testing.md                # new — boundary/technique policy and validation selection
├── specs/testing-standards.md      # this plan — approval, conversion map, verification evidence
├── mise.toml                      # modify — remove fuzz and mutation tasks/tool; retain existing test interface
├── mise.lock                      # modify — remove Gremlins lock entries
├── .gremlins.yaml                 # delete — removed tool configuration
├── .github/workflows/
│   ├── mise.yml                   # modify — remove only fuzz job
│   └── fuzz.yml                   # delete — removed campaigns
├── entitytypes/                   # targeted test conversions from table
├── contracts/functionschema/      # targeted test conversion from table
└── internal/
    ├── contracts/v1/natswire/     # targeted test conversions from table
    ├── adapters/{ecowitt,zigbee2mqtt,zwavejs}/
    │                             # targeted test conversions from table
    └── modules/automations/       # targeted decoder test conversions from table
```

| ID | Outcome / owning paths | Dependencies | Size | Acceptance |
| --- | --- | --- | --- | --- |
| D1 | Publish R1–R8 and the boundary/technique policy in `CODING_STANDARDS.md`, `docs/testing.md`; wire `AGENTS.md`/README navigation | Approval | M | A1 |
| D2 | Document focused use of existing tasks in `AGENTS.md`, README, and `docs/testing.md`; retain full gate | D1 | S | A2, A3 |
| D3 | Remove fuzz machinery and map preserved behavior across all 15 targets and workflows | D1 | L | A4, A6 |
| D4 | Remove Gremlins tool/config/task/lock entries and current instructions | D1 | S | A5 |
| D5 | Review cleanup/diff; run focused and full checks; record actual results and timing limits here | D2–D4 | M | A1–A6 |

Sizes describe relative implementation/review scope, not elapsed-time promises.

## Acceptance and verification

- **A1 — Usable policy:** all six requested techniques (unit, integration, E2E,
  property, fuzz, mutation) have an explicit stance. Rules include examples and
  exceptions. Agent instructions point to the policy for test authoring/review
  and validation selection; old operational commands do not conflict with it.
- **A2 — Focused interface:** run `GO_PACKAGES='./contracts/functionschema'
  mise run test` and a filtered test command. Run scoped lint
  and vet through their existing tasks with `--skip-deps`. Confirm these skip
  generation/format/tidy/frontend tasks. Invalid package selection propagates
  failure. Verify no new task names, non-race local tests, race-enabled tests
  with `CI=true`, and the optional `-- -race` override. Inspect mise dry-run
  output and actual logs.
- **A3 — Full protection:** `mise run --dry-run validate` still schedules
  generation, format, tidy, lint, full Go tests, generated fixture execution,
  vet, frontend tests, and frontend build. Backend/web CI checks retain those
  existing responsibilities, including race instrumentation in CI. Missing Docker never silently skips required
  integration tests. Repository inspection currently shows no protected-branch
  required checks/rulesets; recheck if CI removal encounters a required fuzz job.
- **A4 — Fuzz removal with retained guarantees:** source search finds no `Fuzz*`
  entry points or `testing.F` usage; task/workflow inspection finds no active
  fuzz campaigns. Every inventory row names its retained ordinary tests or
  evidence of subsumption. Run focused affected-package tests and their lint/vet
  through mise; retained assertions test real decoder/projection behavior.
- **A5 — Tool removal:** configuration/lock parsing and mise task discovery
  show no Gremlins installation or mutation task. README has no active removed
  invocation. Existing meaningful negative-control/regression tests still run.
- **A6 — Integrated validation:** run `mise run validate` after final changes
  and review generated, formatting, and module diffs. Run `git diff --check` and
  inspect all changed files. Record failures as failures. The observed baseline
  timeouts require diagnosis if they block this gate; do not expand the scope
  into production changes or deadline increases without a new decision.

Focused conversion packages:

```sh
GO_PACKAGES='./entitytypes ./contracts/functionschema ./internal/contracts/v1/natswire ./internal/adapters/zigbee2mqtt ./internal/adapters/ecowitt ./internal/adapters/zwavejs ./internal/modules/automations' mise run --skip-deps test
```

All code/test validation is local with the project toolchain and reachable
Docker. CI execution additionally requires GitHub access. No live household
hardware or service credentials are needed for this scope.

Record warm/cold/cache state, scope, outcomes, and commands when timing the new
workflow. Report cached results separately from forced execution. A change in
selected workload or instrumentation is a workflow tradeoff, not a same-workload
speedup. Quantitative speedup claims require repeated comparable observations;
this phase has no numerical latency acceptance gate.

## Alternatives and risks

| Alternative | Benefit | Reason not selected |
| --- | --- | --- |
| Whole-suite migration now | Could reduce dominant fixture costs directly | User chose standards/workflow first; retain a concrete backlog |
| Unit-only default with new build tags | Cleaner infrastructure-free loop | Requires suite classification/migration beyond this phase |
| Keep selective fuzz and manual Gremlins | Preserves automated exploration/sensitivity tools | User explicitly chose removal after assessing their value |
| Automatically select all affected tests | Less manual scope selection | Requires a trustworthy dependency/asset-impact model; full gate remains the safety net |
| Force every service behind repository doubles | Very isolated service tests | Can duplicate SQL behavior and create large, brittle fake implementations |

Risks and mitigations:

1. **Losing useful edge cases during fuzz removal.** Inventory each target and
   preserve unique contract cases with explicit positive/negative expectations;
   acknowledge that deterministic coverage does not preserve exploration.
2. **Fast checks mistaken for completion.** Name and document their scope;
   require full local validation and the race/integration gate in CI.
3. **Existing heavy/parallel tests remain slow or flaky.** Record failures and
   measured owners, keep narrow iteration available, and prioritize the follow-up
   fixture work rather than weakening assertions or deadlines.
4. **Historical specs pull agents toward obsolete tooling.** Make the current
   policy's precedence explicit in the standards and agent reading path.

## Follow-up migration candidates

These are investigated candidates, not approved rewrites or promised speedups:

1. Move `TestNotificationStrictInputsAndStatusMapping` from the assembled fixture
   in `internal/app/hearthd/notifications_integration_test.go` to the existing
   `internal/modules/notifications/api` service seam. Keep genuine no-side-effect
   and production-wiring coverage at the boundary that can establish it.
2. Review simulator Observation/Command failure matrices against the existing
   Devices service/API/consumer matrices. Retain unique SDK/Core/HTTP seam and
   acknowledgment guarantees; demote only proven duplicates.
3. Narrow `TestRunCancellationForceClosesUnfinishedClientConnection` toward
   `serveHTTP` where its guarantee permits, retaining assembly/resource-release
   coverage if that is part of the contract.
4. Avoid constructing the complete MCP catalog in HTTP-only fixtures; retain
   production catalog construction and actual MCP/Agent integration tests.
5. Use plain NATS for request/reply tests that do not exercise JetStream, and
   review notification service-policy tests that currently use SQLite.

Preserve Core restart/no-redispatch, consumer acknowledgment-floor recovery,
transaction rollback, outbox retry identity, production assembly, and shutdown
joining guarantees. A lower-level assertion is a replacement only when it
actually detects the relevant defect.

## Approval

Approval adopts R1–R8 and authorizes D1–D5 as one scoped change. Broad fixture
migration stays follow-up work. The user approved this spec through Plannotator
session `pn-54bcf7` on 2026-10-20.

## Implementation record

- D1 initially added R1–R8 to `CODING_STANDARDS.md`. The user subsequently
  requested removal of that section while preserving the pre-existing coding
  standards. `docs/testing.md` remains the home for the concrete suite strategy;
  `AGENTS.md` and README point to it. That strategy is not complete yet.
- D2 initially added focused tasks, then the user approved removing both names
  to avoid command sprawl. The user then chose race instrumentation in CI only
  by default. The final interface reuses `test` and separate scoped `lint`/`vet`
  tasks. Full-selection behavior remains unchanged; `CI=true` enables race
  detection, and `-- -race` permits an optional local check.
- D4 complete. Gremlins configuration, declaration, lock entry, task, and current
  invocation are removed. TOML parsing confirms the configuration and lock
  remain valid and contain no Gremlins entry. No developer-installed tools were
  uninstalled.
- `mise run --dry-run validate` still schedules every retained completion check.
  This is task-graph evidence, not an executed full-gate result.
- D3 adapter conversions are integrated. Focused adapter lint/vet passed, and
  all three adapter packages passed three shuffled race-enabled repetitions.
  The retained coverage map is below. Codec conversions are also integrated;
  focused formatting, lint, vet, non-race tests, and race tests passed for their
  four packages. Integrated local verification passed as recorded below.

### Retained codec and condition coverage

| Removed target | Retained ordinary tests |
| --- | --- |
| `FuzzJSONCodecDecodeStability` | `TestJSONCodecNormalizesNestedIntegers`, `TestJSONCodecRejectsInvalidIntegerValues`, `TestJSONCodecDecodesEquivalentIntegerRepresentations`, `TestJSONCodecNormalizesAndValidates` |
| `FuzzNormalizeObject` | `TestNormalizeObjectPreservesNumberLiteralsAndIsStable`, `TestNormalizeObjectRejectsLossyOrUnboundedValues` |
| `FuzzCodecObservationRoundTrip` | `TestObservationCodecPreservesEnvelopeAndRawValues`, `TestObservationCodecRejectsMalformedEnvelopes` |
| `FuzzCodecEntityEventRoundTrip` | `TestEntityEventEnvelopeRoundTrip`, `TestEntityEventEnvelopeRejectsNonContractMessages` |
| `FuzzSubjectParsersCanonicalRoundTrip` | `TestSubjectConstructorsAndParsersMatchProtocol`, `TestStrictParsersRejectStablePreCutoverSubjects`, `TestSubjectsRejectUnsafeTokens` |
| `FuzzParseDeviceFactSubjectCanonicalRoundTrip` | `TestDeviceFactSubjectsCoverEveryFamilyAndVariant`, `TestParseDeviceFactSubjectRejectsNonCanonicalInput`, `TestDeviceFactSubjectBuildersRejectUnsafeInput` |
| `FuzzDecodeAutomationDefinitionConditions` | `TestDecodeAutomationDefinitionConditionsOmissionAndNull`, `TestDecodeAutomationDefinitionConditionsRoundTrip`, `TestDecodeAutomationDefinitionConditionsAcceptsBoundaries`, existing strict rejection tests |
| `FuzzDecodeAutomationConditionDecision` | `TestAutomationConditionDecisionRoundTrip`, `TestAutomationConditionDecisionSelectedNullIsEvidence`, `TestAutomationConditionDecisionRetainsSnapshotAfterDefinitionChange`, existing strict rejection and deterministic encoding tests |

### Retained adapter coverage

| Removed target | Retained ordinary tests |
| --- | --- |
| `FuzzDiscoverInventory` | `TestDiscoverCapturedThirdRealityLight`, `TestDiscoverMultiEndpointLight`, `TestDiscoverTemperatureSensorRegistersMilliCelsius`, `TestDiscoverCapturedButton3RSB22BZ`, `TestDiscoverCapturedSmartPlug`, `TestDiscoverInventoryDocumentBoundaries`, `TestDiscoverMalformedColorTempBoundsPreservesSiblings` |
| `FuzzDeviceState` | `TestDecodeCapturedFractionalState`, `TestDecodeDeviceStateIsolatesInvalidAndUnknownProperties`, `TestDecodeDeviceStateRejectsInvalidShape` |
| `FuzzTemperaturePlanBoundary` | `TestTemperaturePlanStateRangeBoundary`, `TestNormalizeTemperatureTable`, `TestNormalizeTemperatureWholeRange` |
| `FuzzParseStationReport` | `TestParseStationReportAcceptsSanitizedFixture`, `TestParseStationReportRejectsInvalidPayloads`, `TestParseStationReportEnforcesExactSizeAndFieldBounds`, `TestParseSourceTimeRules` |
| `FuzzProjectReport` | `TestProjectReportPublishesEveryValidMeasurementInCatalogOrder`, `TestProjectReportIsolatesMalformedSiblings`, `TestProjectReportPublishesOnlyPresentMeasurements` |
| `FuzzZWaveJSSnapshot` | `TestPlanSwitchTranscriptRegistration`, `TestPlanDimmerTranscriptRegistration`, `TestPlanEmptySnapshotProducesNoRegistrations`, `TestSnapshotStillRejectsInvalidJSONAndMalformedFields`, `TestPlanNetworkEnforcesEntityLimit` |
| `FuzzZWaveJSEvent` | `TestDecodeValueEventArgsPreservesIdentityAndRejectsMalformedForms`, `TestDecodeValueEventArgsRejectsKeyedValues`, `TestEventsAreRoutedWithNodeIdentity`, `TestMalformedFramesEndGeneration` |

### Integrated verification and review

- `mise run validate` passed with `GO_PACKAGES` unset. It ran generation,
  formatting, module tidying, lint with zero issues, vet, the full race-enabled
  Go selection, generated fixture execution, 69 frontend tests in eight files,
  and the frontend typecheck/build. The log is
  `/tmp/opencode/hearth-testing-audit-bd2d42b/validate.log`.
- That run reported 24.09 seconds with existing build and passing-result caches.
  Many unchanged packages, including Core, used cached results. This is
  correctness-gate evidence, not an uncached-suite comparison or proof that the
  earlier baseline timeouts are fixed. Frontend build emitted its existing
  large-chunk warning. Generation, formatting, and tidying left no extra changes.
- Focused command verification after the simplification passed locally, with
  `CI=true`, and with the optional `-- -race` override. `go version -m` on the
  resulting test binaries confirms no race build flag locally and `-race=true`
  for CI and the explicit override. No new task names remain. Earlier checks
  of the removed task guards are obsolete.
- After the CI-only race default change, `mise run validate` passed again with
  normal caches and no extra generated, formatting, or module changes. The log
  is `/tmp/opencode/hearth-testing-audit-bd2d42b/validate-ci-race-only.log`.
  This local run used non-race Go tests; CI retains race-enabled tests.
- Source search found no `Fuzz*` entry points or `testing.F` usage. Configuration
  checks found no active campaigns or Gremlins entries, and all 51 test names in
  the retained-coverage maps resolve to ordinary tests.
- Independent report-only reviews found no concrete defects in the
  standards/workflows, the seven adapter conversions, or the eight
  codec/condition conversions. Code, prose, comment, and structural cleanup
  retained the changed files with their existing owners. `git diff --check`
  passed. No production files or generated files changed.
- The initial D1–D4 and local D5 implementation passed its checks. D1 is now
  reopened for the concrete suite strategy. Current exact-head CI results are
  reported in [PR #196](https://github.com/mholtzscher/hearth/pull/196), rather
  than treating this verification snapshot as a live check status. No broad
  fixture migration or numeric speed target is claimed.
