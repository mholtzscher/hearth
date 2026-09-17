I've read the full non-test surface of the module (domain, api, nats, sqlite), the schema/migration, the specs it implements, and how hearthd wires it. Findings below, ranked by impact. No changes made.

Overall

~9,900 lines of non-test Go (≈12,800 more of tests) for: CRUD on a definition, match a fact against triggers, evaluate a boolean tree, run steps sequentially, keep history. The domain is genuinely subtle (at-least-once facts, crash-safety, three-valued logic), and the core logic is correct-looking and well-separated. But a
large share of the code exists to defend against states the system itself cannot produce, or to work around a self-imposed boundary rule. The module reads like a spec was transcribed clause-by-clause into validators rather than designed for a minimal set of invariants.

Headline findings

### 1. The module does not trust its own database — full re-derivation on every read

conditions_history.go (754 lines) is almost entirely validation of data Hearth itself wrote seconds or days earlier:

- validateExactLeafComparison re-runs the comparison from the retained selected value and asserts it equals the recorded true/false.
- validateIncompatibleLeafSelection, rejectPreemptedFreshnessReason, validateBoundedEvidenceReason re-derive every unknown-reason precedence rule.
- validateGroupEvaluation/validateNotEvaluation recompute the truth table over recorded children.

This is then invoked on every read path: runFromRow → DecodeAutomationConditionDecision (schema-validate snapshot + full re-derivation) → ValidateAutomationRun (→ NormalizeAutomationDefinition again + ValidateRunConditionDecision → full re-derivation again). Same on write (persistRun → ValidateAutomationRun → re-derivation,
then EncodeAutomationConditionDecision → ValidateAutomationConditionDecision → re-derivation). One Run's evaluation is re-proven ≥4 times in its lifecycle.

Worst case: sqlite/history_mapping.go:validateRunSummaryDecision decodes the full 64 KiB definition snapshot through JSON Schema for every row of a history list page — a projection that by its own doc "never carries the full tree" — solely to run this validator, then discards it. A 200-item page = 200 schema validations for
nothing user-visible.

The SQL layer already has extensive CHECK constraints for these shapes. Belt-and-braces on read turns any latent encoder bug into a hard 500 on the history endpoint rather than a display glitch. Decode-and-trust (with the SQL CHECKs as the integrity guard) would delete the majority of this file and history_mapping.go.

### 2. The "coverage protocol" exists to avoid a JOIN in a single SQLite file

conditions_admission.go + the plan/resolve split in sqlite/admission.go: open transaction → load all definitions → match → count receipts → count running → collect required entity IDs → roll back with ConditionSnapshotRequiredError → read state via devices.GetEntityStateSnapshot → re-open transaction and redo everything →
possibly again → ErrConditionSnapshotUnstable → HTTP 503 condition_snapshot_unavailable. Plus classifyAdmissionError distinguishing caller-vs-admission deadlines, the admitWithCoverageRecovery generic, automationAdmissionAttempts/automationAdmissionSnapshotReads constants, and the plannedAutomation/conditional/required
machinery.

hearthd/run.go:90-111 shows devices and automations share one *sql.DB, and devices/sqlite/entity_state_snapshot.go is a single SELECT. Every conditional admission therefore does a guaranteed-wasted first transaction. The README frames "never read devices tables" as an architectural constraint, and it's defensible — but you
should know its price is: ~350 lines, a new error class, a new 503 code, a retry loop, and a two-phase planner, to avoid one read inside a transaction on a single-writer database. A middle ground (devices exposes a ReadStateSnapshot(tx)-style seam, or admission reads the snapshot before opening the transaction and accepts the
tiny stale window that already exists anyway) would collapse this.

### 3. Phantom "legacy" rows

normalizeLegacySkip, skipSource, decodeConditionDecisionColumn's NULL branch, DecodeAutomationConditionDecision's empty→not_configured, the AutomationConditionDecisionNotConfigured "legacy unconditioned history" comments, the three-way OR in the automation_history CHECK, the skip_source IS NULL reservation — and
sqlite/conditions_migration_test.go (390 lines) — all exist for rows with NULL skip_source/condition_decision_json.

There is exactly one migration (00001_initial.sql), edited in place, and it already contains both columns. No database created from this repo can contain such a row. Either this is dead code (likely, given the squash convention), or the migration strategy is broken for existing installs (in which case the legacy path is also
wrong, because an upgraded DB wouldn't have the columns at all). Either way it's inconsistent and should go.

### 4. Domain decisions live in the SQLite package

README.md says "pure domain decisions ... operate on domain values ... transaction orchestration stay[s] in SQLite." But the actual precedence policy — duplicate → stale → busy → conditions, the skip-reason derivation, manualConditionDecision, notEvaluatedDecision, resolveConditionalOutcome, bypass semantics — is all in
sqlite/admission.go. The domain package exports the building blocks (MatchAutomationTriggers, EvaluateAutomationConditions) and the persistence layer assembles the policy. Consequence: the most important business rules are only testable through a real database (sqlite/conditions_admission_test.go, 686 lines), and a Postgres or
in-memory repository would have to re-implement admission policy.

### 5. The same shape is described six or seven times

For a definition/condition: (a) automation-definition.schema.json, (b) domain structs, (c) automationDefinitionJSON/automationConditionJSON persistence structs, (d) AutomationDefinitionBody/AutomationConditionBody API structs — field-for-field identical to (c), (e) hand-written Go validation in
normalizeAutomationDefinition/conditionTreeWalk re-checking everything the schema already checked (name length, counts, slug patterns, disposition enum, uniqueness, max_age_seconds range), (f) SQL CHECK constraints, (g) hand-built OpenAPI in register.go (publishConditionSchemas with rewriteConditionRefs/conditionComponentName,
conditionDecisionSchema).

DecodeAutomationDefinition runs the schema, then unmarshals, then runs normalizeAutomationDefinition, which re-validates the same rules and then EncodeAutomationDefinitions the result to check size again. Pick one authority: either the schema is canonical and Go only does what schema can't (entity-ID grammar, cross-node ID
uniqueness, operand/operator compatibility), or Go is canonical and the schema is generated from it.

### 6. OpenAPI gold-plating

conditionDecisionSchema() builds a oneOf with Not/Required tricks purely to document a response DTO ("The DTO output mapping is unchanged; only the published schema is tightened"). publishConditionSchemas re-publishes the embedded $defs under invented component names via recursive $ref rewriting. api/conditions_openapi_test.go
(481 lines) tests the shape of the OpenAPI document. The web client doesn't consume any of the conditions fields (grep condition web/src → nothing). This is documentation-as-code for a consumer that doesn't exist yet.

### 7. Defending against impossible inputs

- conditionTreeWalk uses reflect.ValueOf(...).Pointer() and a child-pointer set to detect cycles and aliased slices in a tree that only ever arrives via JSON decoding, which cannot produce cycles. The comment about "unbounded recursion" is already handled by the depth limit.
- evaluateEntityStateLeaf re-checks snapshot coverage "even if a caller reaches it directly" — it's unexported.
- StartAutomationRunBody.UnmarshalJSON hand-parses {"bypass_conditions": bool} with a raw-member map, while the comment acknowledges Huma already validates the body against additionalProperties:false + boolean.
- ValidateAutomationRun enforces step-position contiguity, ID/snapshot agreement, per-status pointer presence — all of which the automation_run_steps CHECK constraint and NewAutomationRunSnapshot already guarantee.

### 8. Repeated micro-defensiveness

"Release before logging so a blocked sink cannot hold Drain" appears four times in admission.go with explicit reservation.Release() calls layered on a defer reservation.Release(). A blocking slog handler stalling shutdown is a theoretical concern that has now shaped the control flow of both admission entry points. If it's
real, fix it once in the logger; if not, the defer suffices.

Feature creep candidates

Reasonable ideas, but each carries real code and none has a consumer today:

- max_age_seconds with distinct evidence_in_future / evidence_expired reasons and a precedence rule over pointer_missing/type_mismatch — ~⅓ of the unknown-reason validation surface.
- Manual bypass_conditions → a bypassed mode, BypassRequested flag, "bypass with not_configured" special case, AutomationConditionsBlockedError with 409 + history_id/history_url, custom 409 response schema. Unused by the web UI.
- Manual Condition Skips persisted as history — a manual click that didn't run becomes a durable Skip with a special "manual skip" provenance family (RunSourceManual on Skips, MatchedTriggers: [] required non-nil, SQL CHECK branch, validateSkipProvenance). A plain 409 with the evaluation in the response body would be far
  simpler.
- Per-node evaluation evidence (SelectedValue with null-vs-absent semantics, ObservationID, ObservedAt per leaf, pre-order without short-circuit) — great for a debugging UI that doesn't exist; drives most of finding #1.
- math/big.Rat exact numeric comparison in comparison.go — correct but notable for home-sensor values; float64 with json.Number would be adequate and simpler.
- AdmissionOutcome counters (MatchedAutomations, StartedRuns, RecordedSkips, DuplicateOutcomes) returned from ReceiveDeviceFact — the only caller (nats/consumer.go) discards them (_, admissionErr := ...).

Readability

- Stutter: package automations exports AutomationRun, AutomationSkip, AutomationCondition, AutomationConditionDecisionMode, AutomationConditionUnknownEvidenceInFuture… Every external use reads automations.AutomationX. Dropping the prefix (automations.Run, automations.Condition) would shorten hundreds of lines.
- Comment density: doc comments frequently restate the spec at paragraph length (NewAutomationRunSnapshot, admitWithCoverageRecovery, AutomationPageLimit, DeviceFactSummary) and re-explain the same invariants ("never logs a value", "release before logging", "never replays or infers success") at every site. The signal-to-noise
  ratio makes it harder, not easier, to see what a function does.
- isKnown() triads on AutomationConditionResult, AutomationConditionUnknownReason, AutomationConditionDecisionMode, ComparisonOperator — enum membership checks that only matter because decoded strings aren't validated at the one decode boundary.
- Parallel error-wrapping helpers: invalidFact, invalidSummary, invalidTrigger, invalidRun, invalidSkip, invalidCondition, decisionInvalid, definitionIssue, reject(...) — nine ways to say ErrInvalidAutomation: ....
- Six-parameter functions are common (planAutomationOutcome, validateUnknownLeafEvidence, terminateDeviceFactMessage, NewAutomationRunSnapshot); plannedAutomation carries seven fields where a small sum type (run | skip(reason) | duplicate | pendingConditions(required)) would be clearer.
- sqlite/execution.go:CompleteRun inlines status validation while CompleteStep calls automations.ValidateStepCompletion — inconsistent placement of the same kind of rule.
- mapObservationDeviceFact / mapEntityEventDeviceFact in nats/device_fact_mapping.go are ~80% duplicated.

What's good

- execution.go is the right size for the problem: reserve identity → execute → verify ownership → record. stepOwnsCommand and preCreationFailure are clear and necessary.
- comparison.go's JSON Pointer + equality implementation is compact and correct.
- Acknowledgement policy in nats/consumer.go (Ack only after commit; Term for deterministic rejection; Nak with delay otherwise) is exactly right.
- Fact receipts + partial unique index for busy/dedup are a good use of the database as the final arbiter.
- Lifecycle handling (AdmissionGroup, WithoutCancel for workers, startup interruption) is sound.

If you were to act on this (priority order)

1.  Stop re-deriving evaluations on read; trust SQL CHECKs + the encoder. Delete most of conditions_history.go validators and history_mapping.go's summary-time snapshot decode. (Biggest win, lowest risk.)
2.  Remove the legacy-row paths and their test file; decide the migration story explicitly.
3.  Decide whether the no-cross-module-read rule is worth the coverage protocol. If yes, at least read the snapshot before the transaction to eliminate the guaranteed wasted first attempt.
4.  Move admission policy (planAutomationOutcome, manualConditionDecision, etc.) into the domain package as pure functions over already-loaded records; leave sqlite with load/write only.
5.  Make the JSON Schema the single structural authority and strip Go validation down to what schema can't express.
6.  Drop the hand-built OpenAPI condition/decision schemas and their test until a client needs them.
7.  Consider whether manual-skip-as-history and bypass are needed now, or can be a 409 body until the UI asks for them.
