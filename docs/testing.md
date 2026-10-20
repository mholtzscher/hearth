# Testing and validation

Use this guide when adding or changing tests, reviewing test placement, or
choosing validation scope. The approved migration contract and implementation
status are in [test boundaries](../specs/test-boundaries.md).

## Choose the boundary by the guarantee

Name the behavior, a plausible defect, and the source of the expected result.
Choose the smallest boundary that could expose that defect. Test levels describe
what executes. Keep tests with the behavior's owner, and make the declared level
selectable using the file conventions below.

| Boundary | What it protects | Fixture |
| --- | --- | --- |
| Unit | One owner's decisions, conversions, handlers, transitions, error classification | Real owner with narrow doubles; in-process router/decoder and `httptest.ResponseRecorder` allowed |
| Integration | Real persistence/protocol boundaries, executable lifecycle, selected assembly | Real SQLite, brokers, sockets, subprocesses, or hand-assembled systems |
| E2E | Public journeys through production Core assembly | Production Core driven through HTTP/MCP or the public Adapter SDK; simulated devices allowed |
| Frontend unit or component | Parsing, interaction, loading/error states, display contracts | Vitest/jsdom and small contract-shaped responses |

A real SQLite test can be cheaper and clearer than a fake that reimplements a
large repository. Use the real database for transaction guarantees and a real
broker for delivery semantics. Use a service double for transport validation
that does not depend on persistence. A jsdom test is not browser E2E evidence.

Unit tests may use temporary files, controlled goroutines/time, codecs, and
fixture data. They do not open real databases, brokers, listening sockets, or
applications. `httptest.NewServer` starts a real listener and is integration.
Generator/compiler execution is integration. A `Run` path that rejects invalid
configuration or an already-canceled context before infrastructure opens can
remain unit; production startup failure with infrastructure is integration.
An Adapter running against hand-assembled Core is integration, not E2E.

## File selection and fixture support

| File kind | Build constraint | Filename |
| --- | --- | --- |
| Unit behavior | None | Ordinary `_test.go` |
| Integration behavior | `//go:build integration` | `_integration_test.go` |
| E2E behavior | `//go:build e2e` | `_e2e_test.go` |
| Fixture-only support | As required by dependencies | Recognizable helper/fixture name; no behavior suffix required |

Split mixed files before tagging; preserve assertions during classification.
Generated test filenames stay unchanged. The checker in the existing lint task
enforces declared tags and suffixes, not arbitrary transitive infrastructure
behavior. Inspect fixture chains as well as filenames. `TestMain` is setup;
tests, benchmarks, and examples count as behavior entry points.

Plain data builders can remain untagged even if only tagged tests use them.
Tag fixtures importing test infrastructure, referencing tagged declarations,
or starting infrastructure at initialization. Use a single level's tag for
level-specific fixtures and `integration || e2e` for shared infrastructure
fixtures. Go compiles every selected helper file, including unused helpers.
Split plain builders from infrastructure setup so the default suite retains
only the dependencies it needs. Verify default, integration, E2E, and combined
selections compile independently.

Shared infrastructure helper packages (`dbtest`, `natstest`, and
`mosquittotest`) also use `integration || e2e`, keeping their test-only
dependencies out of the default package sweep.
The executable-only `cmdtest` helper package uses `integration`.

Keep exhaustive domain matrices with the owner of the decision. Each higher-level
scenario needs a distinct integration, assembly, recovery, or ordering guarantee.
There is no one-test-per-feature quota. Preserve independent entry-point safety
and error contracts when removing duplicated checks or tests.

For example, malformed notification bodies can exercise the real decoder with a
recording service double. They do not need an SDK Session and NATS. Core restart
tests that establish no redispatch and retained consumer acknowledgment positions
do need the durable system they assert against.

## Fixtures and concurrency

Construct only the data and dependencies needed by the assertion. Reuse immutable
preparation, such as the migrated SQLite template, but isolate each test's
mutable database, state, connections, and cleanup. Plain NATS suffices for Core
request/reply; file-backed JetStream belongs in stream and recovery tests.

Identify captured, sanitized, and synthetic fixtures and their relevant upstream
versions when compatibility depends on them. A synthetic peer verifies the
scripted contract, not real-device compatibility. Golden changes need a reviewed
contract or example as their source, rather than regeneration to silence a
failure.

Synchronize on channels, barriers, or controlled time. Use `testing/synctest`
where it represents the behavior; keep real sockets and time where they are the
contract. A liveness deadline bounds a wait, not a performance requirement.
Diagnose contention and bound expensive fixture concurrency rather than
increasing deadlines or retrying a failing test until green.

## Technique policy

| Technique | Policy |
| --- | --- |
| Example/table tests | Default for finite cases, domain boundaries, and regressions. Each case has an explicit expected outcome. |
| Property/model tests | Justified when an independent law or model covers meaningful combinations examples miss. Bound generation, support counterexample replay and shrinking where applicable, and avoid rejected-input oracles that make most executions vacuous. No default property library is required. |
| Integration tests | Use real dependencies for the semantics they own. Retain atomicity, acknowledgment, recovery, and joining guarantees that doubles cannot establish. |
| E2E tests | Keep important journeys and unique production-assembly guarantees. Put the complete decision matrices at their lower-level owner. |
| Fuzzing | Native fuzz targets and campaigns are not part of this project's test strategy. Preserve useful edge cases as ordinary deterministic tests. |
| Mutation testing | No mutation tool or campaign is required or maintained. For a regression, demonstrate failure on the buggy behavior when practical; existing meaningful negative-control tests remain valid. |
| Coverage | Diagnostic information about executed paths, not a quality quota or a reason to add duplicate tests. |
| Benchmarks | Use for an actual performance question, separate from correctness gates. Record workload, cache state, run counts and outcomes before claiming a speedup. |

Round-trip properties need explicit cases that establish correct values and
rejections. Two implementations can agree and still be wrong. Before moving or
removing a test, identify the surviving assertion that detects its defect.

This policy supersedes fuzz and mutation-tooling prescriptions in older feature
specs. Their behavioral requirements and historical verification records still
apply. Existing violations of the boundary rules are migration candidates, not
permission to rewrite unrelated tests.

## Local iteration

Select the owning Go packages and relevant consumers explicitly. Changes to a
shared schema, codec, SDK, or module contract require consumer checks, not just
the directory that changed.

```sh
GO_PACKAGES='./contracts/functionschema' mise run test
GO_PACKAGES='./entitytypes' mise run test -- -run '^TestJSONCodec' -count=1
GO_PACKAGES='./internal/platform/lifecycle' mise run --skip-deps test
GO_PACKAGES='./internal/modules/automations' mise run test -- -tags=integration
GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration,e2e
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps lint
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps vet
```

- Reuse `test`, `lint`, and `vet`; there are no separate fast-test or combined-check
  tasks. Set `GO_PACKAGES` explicitly to avoid a full sweep during iteration.
- `test` defaults to untagged unit tests locally. It runs without race
  instrumentation locally; `CI=true` selects all levels and enables race detection.
  Normal Go caching remains enabled, and standalone tests skip preparation.
- `test` forwards Go test flags after `--`. A local race check is optional through
  `mise run test -- -race`, particularly when diagnosing concurrency, cancellation,
  lifetimes, or shared mutable state. Shuffle and repeat tests when order or
  scheduling is a risk. CI performs the full race-enabled suite.

Build tags are additive: `-tags=integration` includes unit and integration tests;
`-tags=integration,e2e` includes every level. Selected real-Mosquitto tests fail
when Docker is unavailable. The untagged suite requires neither Docker nor
network listeners. Lint and vet analyze all levels even during focused iteration.

Use the existing mise tasks for formatting, generation, and module tidying.
Generator/schema/query changes also require the owning generated-fixture or
real-boundary checks. Review generated changes rather than editing generated
output by hand.

For frontend iteration, install dependencies when needed with `mise run
web-install`, then select tests through `mise run --skip-deps web-test --
<filter>`. Run `mise run --skip-deps web-build` for the typecheck/build boundary.

## Completion gate

Unset `GO_PACKAGES` and run `mise run validate` after integrating the final
changes. It retains generation, formatting, module tidying, lint, all test levels,
generated fixture execution, vet, frontend tests, and frontend build. Local Go
tests are non-race; CI runs them with race instrumentation.
Review the final diff, including generated, formatting, and module changes,
before publication. Revalidate after subsequent changes at the affected scope
and re-establish the full gate after final integration.

Report failed, skipped, cached, and unavailable checks honestly. An unexplained
failure followed by a passing retry does not establish a fix. Focused checks
provide iteration feedback; they do not replace the completion gate.

No fixed runtime budget is established yet. Report actual scope and cache state
when measuring validation. Removing work or changing instrumentation is a
workflow tradeoff, not a same-workload performance comparison.
