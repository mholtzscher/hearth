# Automation validation boundaries

Status: proposed implementation list. The rule is adopted in
[CODING_STANDARDS.md](../CODING_STANDARDS.md); the Go changes below are pending.

## Decision and scope

Apply the validation-boundary rule to Automations. Repositories remain safe
entry points for callers that bypass the Service. They accept ordinary domain
values and validate structural integrity themselves. The Service owns checks
against current Devices references. Transactional admission owns checks against
current definitions, receipts, active Runs, holds, and State evidence.

This deliberately retains some validation across independent entry points.
Removing all repeated checks would require changing those contracts and the
mutable domain types. This plan instead removes repeated work within a workflow
and validators without production callers.

Public HTTP, MCP, NATS, persisted JSON, and database schemas stay compatible.
Other modules are outside this implementation scope. No compiled-definition
cache or new package is needed.

## Current evidence

- `DecodeDefinition` performs schema validation and typed normalization.
  `ValidateDefinition` normalizes before Devices checks. SQLite independently
  normalizes writes, as its documented contract requires.
- `NormalizeDefinition` encodes to check size, discards the bytes, and SQLite
  immediately encodes again.
- `DecideConditions` validates the tree and checks snapshot coverage, then
  `EvaluateConditions` repeats both operations.
- Save-time condition reference validation walks a tree already validated by
  definition normalization.
- `ValidateRun` and `ValidateSkip` have only test callers. Their decision and
  provenance validation helpers do not enforce production behavior.
- `DefinitionCodec.ValidateCondition` has no callers. Its separately compiled
  condition schema exists only for that method.
- `ReceiveDeviceFact` matches and reads State before `AdmitDeviceFact` validates
  its input. NATS validates its decoded facts, but direct Service callers can
  bypass NATS.

## Ordered changes

### D1. Document the module's boundaries

Effort: S. Depends on: none. Acceptance: A1.

Update `internal/modules/automations/README.md` with the boundary table below.
Update `repository.go` comments to state that repository writes validate arbitrary
input structurally, while Devices reference validation belongs to the Service.
Add the preparation helper introduced by D2 to the existing file responsibilities.

| Entry point | Owns |
| --- | --- |
| Definition JSON decoding | Strict wire shape, bounds, typed structure, owned normalized values |
| Service create/replace | Typed structure and current Devices references |
| Repository create/replace | Typed structure, encoded size, revision concurrency |
| NATS fact decoding | Wire contract and mapped fact integrity |
| Service fact receipt | Fact integrity before matching or dependency reads |
| Repository fact admission | Fact integrity and transaction-local eligibility |
| Public condition helpers | Structural safety for freely constructed trees |
| Condition evaluation | Snapshot coverage and evaluation of supplied evidence |
| Step/Run completion | Terminal outcome input and legal persisted transition |
| Persistence decoding | Decode retained representation; preserve existing corruption checks |

Document pure matching helpers as operations over validated definitions and
facts. Export alone does not make every helper an independent input boundary.
Keep defensive parsing needed to use JSON, pointers, and durations safely.

### D2. Reuse normalized definition bytes inside persistence

Effort: S. Depends on: D1. Acceptance: A2.

In `definition_validation.go`, export the existing combined helper as
`NormalizeAndEncodeDefinition`. Keep `NormalizeDefinition` as a wrapper that
discards bytes. SQLite create and replace call the combined helper and use its
returned bytes directly instead of calling `EncodeDefinition` again.

```diff
-func normalizeAndEncodeAutomationDefinition(
+func NormalizeAndEncodeDefinition(
     definition Definition,
 ) (Definition, json.RawMessage, error)
```

The function returns an owned, structurally valid canonical definition and its
size-checked encoding. It does not consult Devices. Failure returns no usable
result and preserves existing error classification. No repository signature or
new domain type is required. Keep `EncodeDefinition` explicitly non-validating.

### D3. Remove repeated condition preparation within workflows

Effort: M. Depends on: D1. Acceptance: A3.

In `conditions_decision.go`, make `DecideConditions` call `EvaluateConditions`
directly. Remove its initial `RequiredConditionEntityIDs` call and coverage
check. Remove `missingSnapshotCoverage` if the caller search still confirms it
is private to this path. Preserve the complete sorted required Entity set in
`ConditionSnapshotRequiredError`.

In `definition_validation.go`, collect condition Entity IDs from the normalized
tree without revalidating it. Add an unexported collector in
`conditions_validation.go`, with its validated-tree precondition in the comment.
It returns sorted, deduplicated IDs. Only the post-normalization save path uses it.

```go
func requiredValidatedConditionEntityIDs(root Condition) []devices.EntityID
```

Keep `RequiredConditionEntityIDs` and `EvaluateConditions` safe for arbitrary
typed trees. Keep their signatures and tree bounds. Snapshot pre-reading and
transactional evaluation are separate operations: the latter must use the
transaction-loaded definition even when it differs from the pre-read version.

### D4. Reject malformed facts at Service entry

Effort: S. Depends on: D1. Acceptance: A4.

In `fact_processing.go`, call `ValidateDeviceFact` in `ReceiveDeviceFact` after
acquiring the admission reservation and before `admitAutomaticFact`. This keeps
the existing shutdown/unavailable precedence and deferred reservation release.
Return the validation error before repository reads, State reads, admission,
or worker startup.

Retain NATS mapping validation and SQLite `AdmitDeviceFact` validation. Each
accepts input independently; neither is a substitute for the Service boundary.

### D5. Remove unused validation APIs and their helper trees

Effort: M. Depends on: D1. Acceptance: A5.

- From `run.go`, remove `ValidateRun`, `ValidateRunConditionDecision`, and their
  exclusively reachable provenance, matched-trigger, status, and attempt helpers.
  Keep `ValidateStepCompletion`, `ValidateRunCompletion`, and `NewRunSnapshot`.
- From `skip.go`, remove `ValidateSkip`, `ValidateSkipConditionDecision`, and
  their exclusively reachable provenance helpers.
- From `held_state.go`, remove `validateHeldStateEvidence` once its only callers
  above are gone. Keep runtime duration conversion and held-state matching checks.
- From `definition_codec.go`, remove `DefinitionCodec.ValidateCondition`, the
  `condition` schema field, and separate subtree compilation. Keep the condition
  schema inside the full definition schema and its strict decoding coverage.
- Keep `ValidateDeviceFactSummary`: SQLite history decoding calls it in production.

Before deleting the associated tests, map each meaningful invariant to an actual
constructor, admission operation, completion operation, decoder, or SQL constraint.
Add missing behavior coverage at that owner. If an invariant has no enforcement
point, establish it there rather than keeping an unused whole-record validator.
Do not assume sealed decision constructors validate their supplied evaluations:
they guarantee envelope shape, not every Run/Skip outcome relationship.

Rewrite `run_model_test.go` and `skip_model_test.go` around actual construction
behavior or remove cases that only exercise the deleted APIs. Retain shared
fixtures still used elsewhere. Prefer assertions on real admission/history
outcomes over copying the removed validators into large test-only validators.

### D6. Verify the boundary contracts

Effort: M. Depends on: D2-D5. Acceptance: A1-A6.

Use existing domain, API parity, NATS, and real-SQLite suites. Add targeted cases
only where the changes lack behavioral protection. Run
`mise run --skip-deps test` during implementation and `mise run validate` after
integration. The test task currently runs all Go packages; do not assume it
supports a package argument. Real-Mosquitto tests require reachable Docker.
Review formatting, generation, and module changes produced by validation.

## Acceptance criteria

| ID | Check and expected result |
| --- | --- |
| A1 | Review the README and repository comments against production call sites. Every retained validator has an identified input boundary or a caller within that boundary. |
| A2 | Direct SQLite create/replace rejects malformed or oversized typed definitions atomically. Valid definitions retain canonical bytes and ownership isolation. Code review confirms each write uses the combined helper's bytes. |
| A3 | Existing invalid-tree, tree-bound, true/false/unknown, and missing-coverage tests pass. Coverage errors retain the complete sorted Entity set. A concurrent definition edit requiring a new Entity still commits nothing. Review confirms one validation/coverage pass inside `DecideConditions`. |
| A4 | A malformed fact passed directly to the Service returns `ErrInvalidDeviceFact` with no repository or Devices calls and no workers. A closed admission gate still returns `ErrAdmissionUnavailable`. Direct repository and NATS malformed-input cases remain covered. |
| A5 | A repository-wide symbol search finds no removed APIs. Actual admission and history tests cover source/evidence relationships, matching Trigger identities, manual-only bypass, stale/busy precedence, and false/unknown Skip decisions. Completion tests protect terminal status/evidence rules. Strict definition decoding still rejects contradictory condition fields. |
| A6 | `mise run validate` passes; intended generated/formatting/module changes are reviewed. HTTP/MCP errors, retained history shapes, and restart/interruption behavior remain compatible. |

## File ownership

All paths below are relative to the repository. Existing files retain their
responsibilities; no production package split is proposed.

```text
CODING_STANDARDS.md                         # modified now: validation rule
specs/
  automation-validation-boundaries.md      # new now: implementation list
internal/modules/automations/
  README.md                                # modify D1: boundary map
  repository.go                            # modify D1: input contracts
  definition_validation.go                 # modify D2/D3: preparation and references
  definition_codec.go                      # modify D5: remove unused subtree API
  conditions_validation.go                 # modify D3: validated-tree collection
  conditions_decision.go                   # modify D3: use evaluation's checks
  fact_processing.go                       # modify D4: Service input boundary
  run.go                                   # modify D5: active construction/completion only
  skip.go                                  # modify D5: Skip model
  held_state.go                            # modify D5: remove unused evidence check
  run_model_test.go                        # modify D5: active invariant coverage
  skip_model_test.go                       # modify D5: active invariant coverage
  fact_processing_validation_test.go        # new D4: direct Service rejection
  definition_validation_test.go             # modify as needed D2/D3: preparation behavior
  sqlite/
    definitions.go                         # modify D2: reuse prepared bytes
    automation_repository_test.go          # modify as needed D5/D6: write/transition contracts
    conditions_admission_test.go           # modify as needed D5/D6: decision outcomes
    held_state_admission_test.go           # modify as needed D5/D6: hold provenance
```

## Risks and completion

- Removing validator-only tests can hide a missing production invariant. D5
  requires tracing each meaningful rule to an active enforcement point before
  deleting its test. Add narrow enforcement at the owning operation if needed.
- A non-validating collector is unsafe on arbitrary recursive values. Keep it
  private and restrict its caller to the freshly normalized save workflow.
- Service-entry validation can change error precedence. D4 places it after the
  admission gate and tests both branches.
- Mutable values cannot carry permanent validation guarantees. Repositories
  continue to validate independent inputs; no shared cache or unchecked public
  prepared-value wrapper is introduced.

Estimated total effort: L, roughly one to two days including invariant tracing
and validation. The two boundary/scope decisions are settled. Implementation is
complete when D1-D6 and A1-A6 are satisfied; this document does not claim the Go
changes have been implemented.
