# Testing and validation

Use this guide when adding or changing tests, reviewing test placement, or
choosing validation scope. The review rules live in
[Coding standards](../CODING_STANDARDS.md#testing).

## Choose the boundary by the guarantee

Name the behavior, a plausible defect, and the source of the expected result.
Choose the smallest boundary that could expose that defect. Test levels describe
what executes, not a filename or a required directory structure. Keep tests with
the behavior's owner.

| Boundary | What it protects | Fixture |
| --- | --- | --- |
| Unit or module service | Decisions, conversions, transitions, error classification | Real owner with small inputs and narrow dependency doubles where useful |
| Repository or migration | Constraints, commit/rollback, ordering, upgrade and data preservation | Disposable real SQLite, production migrations and queries |
| HTTP or MCP transport | Decoding, validation, status/error mapping, schema, serialization, route/tool registration | Real router and decoder with a narrow service double |
| NATS, MQTT, or WebSocket adapter | Wire behavior, acknowledgments, retained delivery, reconnect, framing | Real disposable boundary for protocol semantics; explicit synthetic vendor peers for scripted scenarios |
| Application integration | Production assembly, durable recovery, shutdown joining, no replay | Required Core services, broker and database; process lifecycle when that is the guarantee |
| End-to-end journey | Important user-visible flows across production entry points | Assembled system or simulator; browser when browser behavior matters |
| Frontend unit or component | Parsing, interaction, loading/error states, display contracts | Vitest/jsdom and small contract-shaped responses |

A real SQLite test can be cheaper and clearer than a fake that reimplements a
large repository. Use the real database for transaction guarantees and a real
broker for delivery semantics. Use a service double for transport validation
that does not depend on persistence. A jsdom test is not browser E2E evidence.

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
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps lint
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps vet
```

- Reuse `test`, `lint`, and `vet`; there are no separate fast-test or combined-check
  tasks. Set `GO_PACKAGES` explicitly to avoid a full sweep during iteration.
- `test` runs without race instrumentation locally and enables it when `CI=true`.
  Normal Go caching remains enabled, and standalone tests skip preparation.
- `test` forwards Go test flags after `--`. A local race check is optional through
  `mise run test -- -race`, particularly when diagnosing concurrency, cancellation,
  lifetimes, or shared mutable state. Shuffle and repeat tests when order or
  scheduling is a risk. CI performs the full race-enabled suite.

Focused commands run all existing tests in the selected packages, including
integration tests. They are not unit-only or guaranteed Docker-free commands.
Real-Mosquitto tests fail when Docker is unavailable.

Use the existing mise tasks for formatting, generation, and module tidying.
Generator/schema/query changes also require the owning generated-fixture or
real-boundary checks. Review generated changes rather than editing generated
output by hand.

For frontend iteration, install dependencies when needed with `mise run
web-install`, then select tests through `mise run --skip-deps web-test --
<filter>`. Run `mise run --skip-deps web-build` for the typecheck/build boundary.

## Completion gate

Unset `GO_PACKAGES` and run `mise run validate` after integrating the final
changes. It retains generation, formatting, module tidying, lint, full Go tests,
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
