# Testing and validation

Choose the smallest boundary that can expose a plausible defect, with an explicit
expected result. Keep tests with the behavior's owner. Migration history lives in
[test boundaries](../specs/test-boundaries.md).

## Test boundaries

| Level | Boundary | Fixtures |
| --- | --- | --- |
| Unit | One owner's decisions, conversions, handlers, or transitions | Real owner with narrow doubles; in-process routers/decoders and response recorders allowed |
| Integration | Persistence, protocols, executable lifecycle, or selected assembly | Real SQLite, brokers, sockets, subprocesses, or hand-assembled systems |
| E2E | Public journeys through production Core | HTTP/MCP or public Adapter SDK; simulated devices allowed |
| Frontend unit/component | Parsing, interaction, loading/error states, display contracts | Vitest/jsdom with contract-shaped responses; not browser E2E |

Units may use temporary files, controlled goroutines/time, codecs, and plain
fixtures, but must not start real databases, brokers, listeners, or applications.
`httptest.NewServer` and generator/compiler execution are integration. A `Run`
path that rejects invalid configuration or cancellation before infrastructure
opens can remain unit. Hand-assembled Core is integration, not E2E.

Keep complete decision matrices at their owner. Higher-level scenarios need a
distinct persistence, delivery, assembly, recovery, or ordering guarantee—not a
one-test-per-feature quota. Use real dependencies for semantics doubles cannot
prove; use service doubles for transport validation independent of persistence.
Before moving or removing tests, identify the surviving assertions, including
independent entry-point safety and error contracts.

## Files and helpers

| File kind | Build constraint | Filename |
| --- | --- | --- |
| Unit | None | Ordinary `_test.go` |
| Integration | `//go:build integration` | `_integration_test.go` |
| E2E | `//go:build e2e` | `_e2e_test.go` |
| Fixture-only | As dependencies require | Recognizable helper/fixture name; no behavior suffix required |

- Split mixed files before tagging; preserve assertions and generated filenames.
  Tests, benchmarks, and examples are behavior entry points; `TestMain` is setup.
- Keep plain builders untagged. Tag helpers that import infrastructure, reference
  tagged declarations, or start infrastructure during initialization. Shared
  infrastructure uses `integration || e2e`; level-specific helpers use that level.
  Go compiles unused helpers too, so separate plain builders from setup.
- `dbtest`, `natstest`, and `mosquittotest` use `integration || e2e`; executable-only
  `cmdtest` uses `integration`.
- Lint checks declared tags/suffixes, not transitive behavior. Inspect fixture
  chains and verify default, integration, E2E, and combined selections compile.

## Fixtures and techniques

- Build only what assertions need. Reuse immutable preparation, but isolate each
  test's mutable state, database, connections, and cleanup. Plain NATS suffices
  for request/reply; use file-backed JetStream for stream/recovery guarantees.
- Identify captured, sanitized, or synthetic fixtures and relevant upstream
  versions. Synthetic peers prove scripted contracts, not device compatibility.
  Golden updates need a reviewed contract/example, not regeneration to pass.
- Synchronize with channels, barriers, or controlled time (`testing/synctest`
  where suitable). Keep real time/sockets when contractual. Bound fixture
  concurrency; diagnose failures instead of increasing deadlines or retrying
  until green. Liveness deadlines are not performance targets.
- Default to example/table tests with explicit outcomes. Property/model tests
  need an independent law/model, bounded generation, replay, shrinking where
  applicable, and non-vacuous assertions; no property library is required.
  Round trips also need explicit correct-value and rejection cases.
- Native fuzz targets/campaigns and mutation tooling are not maintained. Keep
  useful deterministic edge cases and negative controls; demonstrate regressions
  fail on buggy behavior when practical. This supersedes older tooling
  prescriptions, not their behavioral requirements or historical evidence.
- Coverage is diagnostic, not a quota. Benchmarks answer actual performance
  questions separately from correctness gates; report workload, instrumentation,
  cache state, repetitions, and outcomes. There is no fixed runtime budget, and
  reduced scope is not a same-workload speedup.

## Local iteration

Use existing mise tasks, selecting owning packages and affected consumers.
Shared schema, codec, SDK, and module-contract changes require consumer checks.

```sh
GO_PACKAGES='./contracts/functionschema' mise run test
GO_PACKAGES='./entitytypes' mise run test -- -run '^TestJSONCodec' -count=1
GO_PACKAGES='./internal/modules/automations' mise run test -- -tags=integration
GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration,e2e
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps lint
GO_PACKAGES='./contracts/functionschema' mise run --skip-deps vet
```

- Local `test` defaults to untagged units, without race instrumentation or
  preparation; normal Go caching applies. Flags follow `--`. Use `-- -race`
  optionally and shuffle/repeat when scheduling or order matters.
- Tags are additive: `integration` includes units; `e2e` includes units;
  `integration,e2e` includes all levels. Units need neither Docker nor listeners.
  Selected real-Mosquitto tests require Docker and fail without it.
- Lint/vet analyze all levels, including focused runs. `CI=true` selects every
  test level with race detection.
- Use mise tasks for formatting, generation, and module tidying. Generated-code
  changes need owning fixture/real-boundary checks; review generated diffs rather
  than editing outputs by hand.
- Frontend: `mise run web-install` when needed, then
  `mise run --skip-deps web-test -- <filter>` and
  `mise run --skip-deps web-build` for typecheck/build.

## Completion gate

Unset `GO_PACKAGES` and run `mise run validate` after final integration. It covers
generation, formatting, module tidying, lint, all test levels, generated fixture
execution, vet, frontend tests, and frontend build. Local Go tests are non-race;
CI enables race detection.

Review the final diff, including generated/formatting/module changes. Revalidate
later edits at the affected scope and rerun the full gate after final integration.
Report failed, skipped, cached, and unavailable checks honestly; a passing retry
does not explain a failure. Focused checks do not replace the completion gate.
