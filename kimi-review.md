Review: internal/modules/automations

I read every non-test source file in the module (domain, api/, nats/, sqlite/), the JSON schema, the SQL queries, the supporting lifecycle/admission plumbing, and cross-checked against specs/automations.md and specs/automation-conditions.md. ~9,900 hand-written LOC (plus ~1,050 generated), ~12,800 LOC of tests.

Important context first: the two specs are extremely prescriptive (A8 "explanation integrity", §2.6 evidence rules, legacy-row normalization, OpenAPI publication, receipt dedup, stale/busy/condition skips, manual bypass). A large share of the module's weight is spec-mandated, not organic creep. The findings below are the
places where the implementation goes beyond the spec or is more complex than its own requirements.

Overengineering

1.  The strict JSON-schema codec is used as the row decoder on every read path — including the per-Fact admission hot path.
    automationRecord() (sqlite/definition_mapping.go:23) decodes every stored definition via DecodeAutomationDefinition, which runs full jsonschema validation. This runs:

- on every incoming Device Fact, for every automation row (planDeviceFact → planAutomationOutcome, sqlite/admission.go:133 inside the loop): N schema validations per Fact, N×Facts total;
- on every history list page row (validateRunSummaryDecision, sqlite/history_mapping.go:166) — a 50-entry page schema-validates 50 definition snapshots just to re-check the condition decision;
- on every definition Get/List.

This data was written by the same process, inside validated transactions. The module already demonstrates the cheaper policy it could use: DecodeMatchedTriggers does a plain typed decode + normalize with no schema. Distrusting your own writes at schema level on hot read paths is the single most expensive over-engineering
choice here (CPU scales with definitions × facts), and it's applied inconsistently (definitions get schema validation; matched triggers get cheap decode; fact summaries get hand-rolled validation).

2.  Redundant validation layering. A definition is validated up to 5 times on one create: API schema decode → NormalizeAutomationDefinition → ValidateAutomationDefinition (devices references) → repository's second NormalizeAutomationDefinition ("even when the caller bypasses the service", sqlite/definitions.go:16) →
    ValidateAutomationRun re-normalizes the whole snapshot again at Run insert (model.go:631). Some belt-and-braces is defensible at a persistence seam; five passes is ceremony.

3.  reflect-based slice-alias "cycle detection" is dead weight (conditions.go:326-335). Children []AutomationCondition holds values — a value slice can alias a backing array but cannot form a cycle, so recursion always terminates. And if a subtree were aliased under two parents, the duplicate-ID check (walk.ids) would reject it
    anyway. The only true cycle vector is Child *AutomationCondition, and childPtrs already covers it. The slices map[uintptr] guard protects against a non-problem and only changes which error message you get.

4.  deviceFactConsumerDeliveryOverride (nats/resources.go:144-181): a 21-case switch enumerating nearly every JetStream consumer knob (backoff, rate limits, priority groups, pinned TTL...) to name "the first unexpected override". The devices module's equivalent validation is one compound if over the fields that matter
    (devices/nats/entity_event_resources.go:120-135). The required-settings check directly below already covers the real invariants; the override enumeration is config-drift paranoia with marginal value and a standing maintenance burden (new JetStream fields silently escape it anyway).

5.  decodeHistoryCursor canonical-string round-trip (api/pagination.go:94): parses the timestamp, then rejects unless cursor.RecordedAt == parsed.UTC().Format(...) byte-for-byte. The base64 re-encode check in decodeCursor is in the same spirit. Canonical-cursor purity costs code and rejects harmless cursors for no security or
    correctness gain.

Overly complex implementations

6.  conditions_history.go (754 lines) — an entire validator that re-derives truth tables and re-executes leaf comparisons (big.Rat and all) to prove a stored evaluation isn't "fabricated". The spec's A8 demands the validators exist and tests reject fabrication; running them on every history read is an implementation choice.
    Worst sub-pattern: validateRunSummaryDecision/validateSkipSummaryDecision build partially-populated fake domain structs (AutomationRun{Source, Snapshot, ConditionDecision} with 10+ zero fields, history_mapping.go:192-199) purely to reuse the full validators. That reconstruct-to-reuse trick obscures what's actually being
    checked. If read-path enforcement stays, purpose-built summary checks would be far clearer.

7.  Admission reservation choreography (admission.go): StartManualRun has four reservation.Release() call sites (one defer + three explicit) interleaved with reservation.Go(...), where Go panics after Release (lifecycle/admission_group.go:96). Correct today, but correctness depends on call ordering across ~70 lines and three
    duplicated "release before logging so a blocked sink cannot hold Drain" comments. This wants a withAdmission(ctx, func() ...) helper so the scope is structural rather than commented.

8.  Five near-identical strict JSON decoders: decodeJSONValue (comparison.go), decodeStrictJSONObject (conditions_history.go:741), decodeSingleJSONValue (api/register.go), the cursor decoder (api/pagination.go:104), and StartAutomationRunBody.UnmarshalJSON (api/conditions.go:26). At least #2 and #3 are the same function; a
    shared helper would do.

9.  Terminal-status rules duplicated three ways: validateRunStatus (model.go), ValidateStepCompletion (execution.go), and the CompleteRun switch (sqlite/execution.go:74-93) — where the RunRunning case and the default case return the identical error. The layering is intentional (validate at both domain and persistence), but
    there's no shared predicate, so the rules can drift.

Feature creep

10. AdmissionOutcome is computed through three layers and then discarded. MatchedAutomations/StartedRuns/RecordedSkips/DuplicateOutcomes are tallied in the SQLite transaction, returned via AdmissionResult, returned by Service.ReceiveDeviceFact... and the only production caller does _, admissionErr :=
    receiver.ReceiveDeviceFact(...) (nats/consumer.go:151). It's test-only observability wearing a production API.

11. Legacy skip normalization (normalizeLegacySkip, skipSource, the NULL-decision branch): the spec demands it, but the same spec says dev databases are recreated and no upgrade compatibility is promised. ~100 lines of permanent runtime complexity to keep hand-written fixtures readable is a spec-level decision worth
    revisiting, not implementation creep per se.

12. Minor unused flexibility: operation()'s variadic tags ...string resolves to "Automations" at all eight call sites; conditionComponentName has a default branch generating component names for $defs that never occur; publishConditionSchemas publishes all $defs (including internal conditionID/children helpers) as named OpenAPI
    components.

Readability

13. File naming/organization. Three "admission" files with different meanings (admission.go = service admission/logging, conditions_admission.go = coverage-retry loop + ManualRunInput, sqlite/admission.go = transactional planning) and four "conditions" files. conditions_history.go is really condition decision codec + validator
    — history is incidental; conditions_admission.go is really the admission retry policy and belongs in admission.go. As named, you can't find things by guesswork.

14. Comment style. The prose is accurate and often genuinely helpful ("inverts true and false while preserving unknown"), but it's over-systematized: "it performs no reads or writes of its own" appears ~5 times verbatim; "never logs definition JSON / Fact values / ..." boilerplate recurs on every logging function; several doc
    comments describe other functions' callers rather than the function under them. The signal-to-noise ratio would improve by cutting the repeated disclaimers to one authoritative location.

15. model.go at 885 lines mixes identity types, trigger/step/fact models, history models, pagination, and four big validators. Splitting validators (validate_*.go) from the model types would match the module's otherwise good file-per-concern discipline.

What's genuinely good

Clean hexagonal seams (AutomationRepository, AutomationDevices), pure domain functions for matching/evaluation, one clear transaction owner, stable error classes mapped once at the API edge, honest no-replay/no-fabrication execution semantics, and simple readable SQL. The structure is right; the weight is in verification
machinery.

Suggested priorities (no changes made)

1.  Stop schema-validating your own writes on read paths (#1, #2): decode rows with a typed unmarshal + cheap invariants; keep the schema codec at the API/write boundary only. Biggest win for both complexity and hot-path cost.
2.  Decide the read-validation policy once (#1, #6): either trust committed rows or validate at one layer — not schema-on-read and fabricated-evidence validators and fake-struct reconstruction.
3.  Delete the reflect slice-alias guard (#3), AdmissionOutcome or wire it to metrics (#10), and the override enumeration (#4).
4.  Consolidate the strict decoders (#8), the terminal-status rules (#9), and the admission reservation scope (#7); rename/regroup the admission/conditions files (#13).

todo → Write review findings
● completed

Review complete. The final response was already delivered above. Nothing more to do — the user asked for a review report without changes, and I've delivered it. The todo list is complete.

Review complete — no changes made. The headline finding: the module's architecture is sound, but it spends a large fraction of its ~10k LOC on verification machinery (schema-validating its own committed rows on every read and every incoming Fact, fabricated-evidence validators running on history reads, redundant alias/cycle
guards) rather than on its actual job. Items #1–#2 are where I'd start; they're simultaneously the biggest overengineering and the only ones with real runtime cost.
