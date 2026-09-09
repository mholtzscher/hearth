# Cron-triggered automations

**Status:** Draft for technical review. Product behavior confirmed in the design interview; application implementation is not authorized by this document-writing task.
**Sequence:** Spec 2 of 2; depends on [automation execution and management](automation-execution.md), E1–E5.
**Effort:** L (roughly 1–2 days after execution foundations), with calendar correctness and recovery as the principal risks. Multiple identified cron Triggers are a contained expansion of matching and snapshot bookkeeping, not a new Trigger kind.

## Goal and baseline

Automatically invoke the already-defined execution path at household-local calendar times. The first concrete routine is a fixed local-time lighting routine, represented with cron rather than a separate weekday/time Trigger type. Do not add sensor events, a workflow engine, or another Command dispatcher.

`internal/app/hearthd/run.go` currently owns process assembly but has no scheduler. Existing health-supervision timers do not provide missed-occurrence or restart semantics. The schema-backed JSON DSL, immutable Runs, per-Automation admission, reserved Command identity, HTTP history, and recovery are owned by spec 1. Its `automation-definition.schema.json` defines the identified Triggers array's structural shape; this spec defines cron expression semantics and same-minute matching. A schema-valid string is not necessarily a valid cron expression.

## Product contract

- Each Automation has 1–32 identified five-field cron Triggers from the first release, with OR semantics; no Conditions or other Trigger kinds.
- Collect all matching Triggers for an Automation at one shared UTC evaluation minute before making one admission decision. Same-Automation, same-UTC-minute matches coalesce into one Occurrence and one Run, or one overlap skip, recording all matching IDs. There is no coalescing window, per-Trigger admission attempt, or per-Trigger skip; matches in different minutes are separate Occurrences.
- Household IANA timezone, loaded at startup; no per-Automation or expression-level timezone.
- Local wall-clock meaning follows timezone rules. Nonexistent minutes are skipped. A repeated local minute is eligible only at its first chronological occurrence, even if Hearth was down during that first occurrence.
- No offline catch-up, startup replay, or queue. On restart, begin with the next future UTC minute boundary, not the current partially elapsed minute.
- Within normal operation, admission may occur during its scheduled UTC minute; after that minute ends it is missed. This minute-wide tolerance accommodates scheduler jitter, not historical catch-up.
- If the same Automation already has a running Run, record one Occurrence containing all matching Trigger snapshots as skipped for `automation_run_active`; do not queue it.
- Disabled definitions produce no scheduled admissions. Manual invocation still works and does not alter the schedule.
- An Occurrence starts the same immutable, sequential, stop-on-failure Run as manual invocation. Active Runs retain their original definitions when edited or disabled.
- Backward wall-clock movement cannot replay an already-evaluated UTC minute. A household timezone change takes effect after restart for future minutes only.

## Cron grammar and ownership

Spec 1 owns `AutomationTriggerID string`, `AutomationTrigger { ID AutomationTriggerID; Kind string; Expression string }`, and `AutomationDefinition.Triggers []AutomationTrigger` with 1–32 entries. Every Trigger has kind `cron`. Author-supplied IDs must match the subject-safe slug pattern `/^[a-z0-9][a-z0-9_-]{0,62}$/`, be unique within the definition, and remain stable across reorder and edits. Spec 1 rejects duplicate IDs semantically; positional indexes are not Trigger identities. Every expression follows the grammar below, and Trigger OR semantics are distinct from cron's day-field OR rule.

`internal/modules/automations/cron_schedule.go` owns parsing, matching, and first-occurrence selection. Spec 1 delivers the canonical JSON Schema/codec plus cron syntax validation from this contract; spec 2 adds actual scheduling. Do not add a separate scheduler definition format or duplicate schema constraints in parser code. Use `github.com/robfig/cron/v3` pinned to v3.0.1 for parsing only, not its autonomous scheduler. This is an implementation choice: Hearth owns admission, DST policy, recovery, and diagnostics.

Accept exactly five whitespace-separated fields in this order:

```text
minute    hour    day-of-month    month    day-of-week
0..59     0..23   1..31           1..12    0..6 (Sunday = 0)
```

Support `*`, comma-separated lists, inclusive ascending ranges, and positive `/step` increments within fields. Accept case-insensitive three-letter English month and weekday names supported by the parser. Reject seconds/six fields, year fields, `?`, `L`, `W`, `#`, negative/zero steps, empty comma-list elements, wrapping ranges, day-of-week 7, descriptors such as `@daily`, interval expressions such as `@every`, and `TZ=`/`CRON_TZ=` overrides. Trim surrounding whitespace and normalize separators to one space; do not silently change calendar semantics.

Traditional day matching: if both day fields are restricted, match day-of-month **OR** day-of-week; if either has wildcard semantics, both field tests must pass. Preserve the pinned parser's wildcard marker (`uint64(1) << 63`): `*` and `*/1` retain it, while `*/2` clears it. Numeric full ranges are restricted fields, not syntactic `*`. Thus `0 0 */2 * MON` matches odd day-of-month OR Monday, and `0 0 1-31 * MON` matches every day. Comma-list masks combine with bitwise OR, including wildcard markers. This is the explicitly selected robfig v3.0.1 day-field dialect, not a compatibility claim for every cron implementation. Other fields always combine with AND.

Syntactically valid but impossible schedules such as `0 0 31 2 *` may be stored; they never match. No upcoming-occurrence preview or bounded-lookahead promise is required initially. Include the impossible-date example in HTTP field documentation so validation does not imply eventual execution.

```go
// CronSchedule is a parsed five-field household calendar rule.
// Store compiled masks privately; do not expose the dependency's types.
type CronSchedule struct { /* private parsed schedule */ }

func ParseCronSchedule(expression string) (CronSchedule, error)

// MatchesCronMinute only matches the first occurrence of a repeated local minute.
// minute must be a whole UTC minute, and location is the household timezone.
func (CronSchedule) MatchesCronMinute(minute time.Time, location *time.Location) bool
```

`ParseCronSchedule` returns domain error `ErrInvalidAutomation` with a field-specific safe explanation (HTTP 400 for semantic cron errors). Structural Trigger errors are handled earlier by the canonical definition schema (HTTP 422). Configure the parser with exactly `Minute | Hour | Dom | Month | Dow` and prevalidate rejected extensions; parser configuration alone does not reject timezone prefixes or every unsupported token. Keep validation and runtime matching on the same compiled representation.

## Scheduling algorithm

One scheduler belongs to the single Hearth core process. No leader election, external scheduler, JetStream scheduling subject, or distributed job queue is introduced.

Use a process timer at one-second cadence, but reason exclusively in UTC minute boundaries. Inject time/wakeup behavior for deterministic tests; timer frequency is an implementation detail, not a claim of exact-second delivery.

1. On startup, load persisted scheduler progress, record any unevaluated interval through the startup minute as a diagnostic gap, and advance progress to `max(previous_high_water, floor_utc_minute(startup_now))`. A fresh database establishes its baseline without inventing historical missed occurrences.
2. On each tick, let `M = floor_utc_minute(now)`. If `M <= high_water`, do nothing. This suppresses repeated timer callbacks and backward wall-clock motion.
3. If intermediate whole minutes were not evaluated, record one bounded diagnostic interval; do not enumerate or execute their historical matches.
4. For minute `M`, in one SQLite transaction read the enabled, schedule-eligible definitions and evaluate every Trigger against this same minute and the startup-loaded timezone. For each Automation, first collect all matching Trigger snapshots in definition array order. An empty subset produces no Occurrence; otherwise perform the existing shared admission decision exactly once, atomically recording one Occurrence plus one admitted Run snapshot or one overlap skip. A started Run's `MatchedTriggerIDs` contains every collected ID in that order. Advance the high-water mark in this same transaction. Do not admit on the first match and then treat later matches as overlaps.
5. Sample fresh current time immediately before committing; if the evaluated minute is no longer current, roll back and retry from fresh time (never retain a stale ticker timestamp). Eligibility is assessed at this final check; already-admitted Runs and their effects may extend beyond the minute. Commit before launching any workers. Register committed Runs with the spec-1 lifecycle gate and execute them through the same worker. Admission and worker registration serialize with shutdown and the sticky executor-fault gate; a crash before worker launch leaves interrupted Runs, never replayable due work.
6. If the transaction fails, dispatch nothing and leave progress unchanged. Retry only while `M` is still the current minute; once time moves on, report a gap rather than replaying it. A persistence failure is visible in readiness/logging.

A scheduler transaction reads current definitions; it must not reuse a stale compiled definition without checking its revision. A cache is unnecessary for this initial version. Do not hold a transaction while validating device support, invoking Commands, waiting on NATS, or running Steps. Trigger matching is pure and device validation occurs in execution.

Creation, schedule edits, and enablement changes become automatically eligible only at a strictly later UTC minute boundary than their write time. Persist `schedule_not_before` with the definition write, including updates that preserve the Trigger expressions or only reorder Triggers. This prevents a mid-minute API edit from causing retroactive execution depending on tick timing. A whole-definition update deliberately resets this lower bound; it does not cancel active Runs. Manual invocation ignores the bound.

Occurrence identity is `(automation_id, scheduled_at_utc)` and deliberately excludes Trigger IDs, expressions, revision, and timezone. Editing, restarting, or changing timezone cannot create a second admission for the same Automation at an already-processed instant. High-water progress is never pruned, so retained-history expiry cannot re-enable replay after a backward clock change.

### Daylight-saving selection

Matching numeric cron fields against local time is necessary but insufficient: both instances of a repeated minute have identical local fields. `MatchesCronMinute` must independently determine whether its UTC instant is the earliest real instant representing that local calendar minute. It cannot depend only on “last fired” memory; the process may start during the second instance.

Use a bounded backward comparison once per household/evaluated minute, not once per Automation. Compare `(year, month, day, hour, minute)` at the candidate with each of the preceding 2,880 UTC minute buckets; if an earlier bucket has identical local fields, reject the candidate. The bound covers the difference between offsets within ±24 hours in supported IANA tzdata, including date-line and half-hour rollbacks. Custom TZif data outside that invariant is not supported. This is at most 2,880 conversions per household minute and avoids a transition-resolution abstraction or reliance on `time.Date` selecting a particular fold instance.

Minute resolution is explicitly UTC buckets with local fields evaluated at their start. Contemporary minute-aligned IANA offsets are the intended household behavior; historically second-valued offsets do not gain a separate exact-second civil scheduler. UTC minute iteration naturally never visits nonexistent local minutes. Keep the fold check independent of cron field matching so it can be calculated once and reused across definitions.

Do not delegate this policy to `cron.Schedule.Next`: the library's scheduling behavior is not Hearth's first-fold-only contract. The acceptance examples below are independent oracles for the wrapper.

## Types and interfaces

Ownership: `internal/modules/automations/model.go`, `automation_scheduler.go`, `sqlite_repository.go`. Model additions are below; they extend spec 1, not existing application code yet.

```go
type AutomationOccurrenceStatus string // "started" | "skipped"

type AutomationOccurrence struct {
    AutomationID AutomationID
    Revision int64
    Name string // retained diagnostic name, even after definition deletion
    MatchedTriggers []AutomationTrigger // nonempty matched subset snapshots, in definition array order
    Timezone string
    ScheduledAt time.Time
    EvaluatedAt time.Time
    Status AutomationOccurrenceStatus
    RunID *AutomationRunID // present only when started
    SkipReason *string // "automation_run_active" only in this version
}

type AutomationScheduleGap struct {
    ID string // "asg_" + canonical UUIDv7
    FromExclusive time.Time // UTC minute high-water before the gap
    ThroughInclusive time.Time // last UTC minute intentionally not evaluated
    RecordedAt time.Time
    Reason string // "core_restart" | "clock_or_processing_gap"
}

type AutomationSchedulerState struct {
    HighWaterMinute time.Time
    Timezone string
}

type AutomationScheduleBatch struct {
    Runs []AutomationRunRecord
    Occurrences []AutomationOccurrence
    Gap *AutomationScheduleGap
}

type AutomationOccurrenceListParams struct {
    AutomationID *AutomationID
    BeforeScheduledAt *time.Time
    BeforeAutomationID *AutomationID
    Limit int
}

type AutomationScheduleGapListParams struct {
    BeforeRecordedAt *time.Time
    BeforeID *string
    Limit int
}

// EvaluateAutomationMinute atomically processes the current UTC minute only.
func (*SQLiteRepository) EvaluateAutomationMinute(ctx context.Context,
    now time.Time, location *time.Location) (AutomationScheduleBatch, error)
func (*SQLiteRepository) InitializeAutomationScheduler(ctx context.Context,
    now time.Time, location *time.Location) (AutomationSchedulerState, error)
func (*Service) StartAutomationScheduler(ctx context.Context) error
func (*Service) StopAutomationScheduler()
func (*Service) ListAutomationOccurrences(context.Context,
    AutomationOccurrenceListParams) (AutomationPage[AutomationOccurrence], error)
func (*Service) ListAutomationScheduleGaps(context.Context,
    AutomationScheduleGapListParams) (AutomationPage[AutomationScheduleGap], error)
```

`MatchedTriggers` retains each matching Trigger's ID, kind, and expression from the evaluated definition snapshot; it is not reconstructed from the current definition. Spec 1's Run field `MatchedTriggerIDs []AutomationTriggerID` is empty for manual Runs and nonempty for scheduled Runs, ordered by the matching definition snapshot's Triggers array. A skipped Occurrence retains the same complete matched subset without creating a Run. Reordering or editing a definition never changes historical snapshots or IDs.

Define named status/reason constants. The repository calls a module-owned pure evaluator while holding the transaction; it owns the atomic snapshot, not policy invented in SQL. Shared internal admission helpers receive the already-open transaction and never invoke repository methods that acquire another connection.

`StartAutomationScheduler` initializes progress synchronously and returns an error before readiness on failure, then starts the loop. Shutdown calls spec 1's `StopAutomationExecutionAdmission` first, atomically closing both Run and next-Step admission; `StopAutomationScheduler` then stops and joins the loop before draining already-started Commands. The scheduler service uses the same admission lifecycle gate as manual starts; per-Automation active uniqueness remains enforced by SQLite. Schedule conflicts are normal recorded skips, not process errors.

The scheduler does not record disabled minutes, every nonmatching minute, nonexistent local minutes, or second-fold minutes as Occurrences. Those are not eligible matches. Gaps explain intervals the scheduler did not evaluate; they do not claim the number or identity of missed Automations, whose definitions may have changed offline.

## Persistence and HTTP additions

Modify the initial migration and automations query sources/generation established in spec 1:

- Add `schedule_not_before` UTC minute to `automations`; writes use the first whole minute strictly after their write time. Spec 1 stores definitions in `triggers_json` rather than scalar cron expression/kind columns and Run matching IDs in `matched_trigger_ids_json`; scheduling uses those same representations.
- `automation_scheduler_state`: singleton key CHECK = 1, `high_water_minute`, `timezone`. Never prune the high-water mark, even if all definitions are deleted.
- `automation_occurrences`: composite PK `(automation_id, scheduled_at)` with `scheduled_at` in UTC, revision/name/timezone snapshot, nonempty `matched_triggers_json` array of complete matching Trigger snapshots in definition order, evaluation timestamp, status, nullable Run ID, nullable skip reason. No singular expression column, per-Trigger rows, or cascading FK to the live definition. Started rows must have a Run ID and no skip reason; skipped rows must have no Run ID and the overlap reason.
- `automation_schedule_gaps`: ID PK, exclusive/inclusive bounds, recorded time, reason. Bounds require `from_exclusive < through_inclusive`.
- Ordered indexes for all-Occurrence history `(scheduled_at DESC, automation_id DESC)` and Automation-filtered history; gap history `(recorded_at DESC, id DESC)`.
- Retention uses spec-1 policy. Skipped Occurrences prune by `evaluated_at`; gaps by `recorded_at`, strictly before cutoff. Keep a started Occurrence as long as its Run is retained or active; delete it atomically when pruning that Run. History deletion must not cascade to definitions or scheduler progress.

All timestamps stored in fixed-width UTC form, matching the existing SQLite ordering conventions. No NATS schema or Observation event changes.

Add thin module HTTP routes:

| Method / path | Behavior |
|---|---|
| `GET /v1/automation-occurrences` | Paginated started/skipped matches; optional `automation_id` filter |
| `GET /v1/automation-schedule-gaps` | Paginated unevaluated intervals and reasons |

Use explicit snake_case transport models corresponding to the domain fields above, including Occurrence `matched_triggers` with each Trigger's `id`, `kind`, and `expression`; Run history exposes spec 1's `matched_trigger_ids`. IDs and timestamp bounds form stable descending cursors, bound to resource/filter. Limits, errors, canonical cursor validation, deletion behavior, and lack of snapshot isolation match spec 1. Lists return `items: []` and optional `next_cursor`.

Automation definition responses additionally expose `household_timezone` for interpreting all cron expressions. It is read-only process configuration, not an editable per-Automation field. Run and Occurrence snapshots retain the timezone used at admission. There is no schedule preview endpoint in this version.

Structured diagnostics use literal event names `automation.scheduler_started`, `automation.schedule_gap`, `automation.occurrence_skipped`, and `automation.scheduler_failed`; include relevant IDs (all matching Trigger IDs for Occurrence diagnostics), UTC bounds, timezone, and safe reason codes, never full Step parameters. Scheduler health becomes false after scheduler persistence failure and healthy after a successful initialization/evaluation; duplicate-minute no-ops alone must not clear an unresolved error. This is only one readiness input: overall readiness also requires device readiness, open execution admission, and no sticky executor fault from spec 1. Scheduler success clears only the scheduler fault and can never reopen a latched executor or shutdown gate. Do not evaluate/admit new scheduled Runs while execution admission is closed.

## Ownership and deliverables

```text
internal/
├── modules/automations/
│   ├── cron_schedule.go, cron_schedule_test.go             # expand — matching and DST policy
│   ├── automation_scheduler.go, automation_scheduler_test.go # new — tick/lifecycle orchestration
│   ├── model.go, service.go                               # modify — occurrences/gaps and history
│   ├── sqlite_repository.go, *_test.go                    # modify — atomic minute admission/progress
│   ├── dbqueries/automations.sql, dbsqlc/                  # modify/generated — scheduler persistence
│   └── api/register.go, automation_models.go, pagination.go, *_test.go # modify — diagnostics API
├── app/hearthd/
│   ├── run.go                                             # modify — start scheduler after dependencies
│   ├── server.go                                          # modify — RuntimeReadiness integrates scheduler health
│   └── *_test.go                                          # modify/new — schedule/restart integration
└── platform/db/migrations/00001_initial.sql                 # modify — progress and Occurrence schema
README.md                                                  # modify — cron semantics, curl examples, recovery
```

The canonical JSON Schema/codec/authoring fixtures, `go.mod`/`go.sum`, and sqlc/mise multi-module generation are owned by E2 in spec 1, not left as unassigned infrastructure work. This spec interprets the same runtime JSON documents; it does not generate Go code for individual Automations. No runtime dependency upgrade is needed in spec 2.

| ID | Outcome | Effort | Owner paths | Dependencies | Acceptance |
|---|---|---|---|---|---|
| C1 | Pure matching and first-fold selection with independent calendar fixtures | M | `automations/cron_schedule.go`, tests | E2 | B1–B3 |
| C2 | Atomic minute evaluation, same-minute match collection, progress, gaps, Occurrences, and scheduled Run snapshots | L | `automations/sqlite_repository.go`, model, SQL/generated output; migration | C1, E3 | B4–B7 |
| C3 | Scheduler lifecycle and diagnostic HTTP/readiness integration | M | `automations/automation_scheduler.go`, service/API; `hearthd/run.go`, `server.go`, tests | C2, E4 | B5, B8–B10 |
| C4 | End-to-end scheduling/restart regression and documentation | M | colocated tests, `hearthd/*_test.go`, README | C3, E5 | B1–B10 |

## Acceptance criteria and independent oracles

- **B1 — Grammar:** accept `0 19 * * 1-5`, lists/ranges/positive steps, and named month/weekdays. Reject every excluded extension above, including parser-supported timezone prefixes and `?`. Invalid definitions never dispatch or partially update storage. Impossible February 31 matches nothing.
- **B2 — Calendar semantics:** `0 19 * * 1-5` matches weekdays at household 19:00, not fixed UTC 19:00. `0 0 1 * 1` matches the first day OR Mondays; `0 0 * * 1` matches Mondays only. Cover wildcard-step behavior using fixed dates, leap-day matches, month ends, and non-UTC fixed-offset IANA zones.
- **B3 — DST:** in America/New_York, `30 2 * * *` has no match on 2026-03-08; `30 1 * * *` matches 2026-11-01T05:30:00Z and rejects 06:30:00Z. Starting at 06:20Z must not admit the second 01:30. In Australia/Lord_Howe, `45 1 * * *` matches 2026-04-04T14:45:00Z and rejects 15:15:00Z during the 30-minute fold. These hand-specified instants, not another call to the evaluator, are the oracle.
- **B4 — One evaluation:** repeated ticks and concurrent admission against real SQLite yield one Occurrence/Run per Automation/UTC minute. At 2026-06-01T19:00:00Z in UTC, distinct expressions `0 19 * * *` and `0 * * * *` in one Automation produce exactly one started Occurrence containing both Trigger snapshots and one Run containing both matching IDs in definition array order. A third nonmatching Trigger is excluded; at a minute when none match there is no Occurrence or Run. Matches in different minutes are never combined. Backward clock movement, including after restart or history pruning, does not replay processed minutes.
- **B5 — No catch-up:** restart at 19:10 does not execute 19:00, and restart during a matching minute does not execute that minute. A normal delayed tick within the current matching minute may admit it; crossing the minute boundary skips the old minute and records a gap. Jumping many hours records a bounded interval, not thousands of invented Runs.
- **B6 — Shared admission:** with a blocked manual Run, two different expressions matching the same minute commit exactly one skipped Occurrence containing both matching Trigger snapshots, no scheduled Run, and no queued work or per-Trigger skips. With no active Run, the matches create one source=scheduled snapshot through the same execution path. Manual Runs have empty `MatchedTriggerIDs`; scheduled Runs retain all matching IDs. Different Automations may execute independently.
- **B7 — Transactions and edits:** failed progress/Occurrence/Run commit causes zero dispatch and no partial records. A schedule edit/disable racing evaluation resolves in one transaction order; revision, matching Trigger snapshots/IDs, and Steps are never mixed. Reordering Triggers preserves author-supplied IDs while later matching snapshots follow the new array order; old history remains unchanged. Spec 1's semantic duplicate-ID rejection leaves no partial definition update. Mid-minute create/edit/enable cannot back-trigger that minute, including expression-preserving updates and reorder-only edits. Retrying after failure cannot duplicate a committed admission.
- **B8 — Recovery/lifecycle:** crash after scheduled admission and before worker launch interrupts that Run with zero replay; crash after first Command never resumes remaining Steps. Shutdown atomically closes Run and next-Step admission before joining the scheduler, then drains already-started Commands and keeps required device dependencies alive, including error cleanup paths.
- **B9 — History:** one skipped Occurrence explains overlap for all matching Triggers; started and skipped history retain complete matched Trigger snapshots after expression edits, reorder, or deletion. Gaps explain unevaluated intervals without claiming per-definition historical matches. Deleted-definition history remains readable. Started Occurrences survive while their Runs are retained/active and prune with them. Progress survives all history pruning. Cursor misuse and boundary retention tests follow spec 1.
- **B10 — Assembly/API:** invalid/missing/host-local timezone fails startup; embedded tzdata works without OS zoneinfo. Definition responses expose the effective household zone; historical snapshots do not change after restart with another zone. Runtime OpenAPI includes diagnostics schemas/routes, and scheduler persistence failures affect readiness. A later scheduler success must not clear an injected sticky executor fault or reopen closed admission.

Use pure fixed-instant examples for calendars, real migrated SQLite for admission/progress atomicity, deterministic fake clock/wakeup barriers for ticks and races, and embedded-NATS app tests for restart/no-dispatch evidence. Do not assert wall-clock sleep timing or infer physical truth from a fake sender. Preserve existing device-control behavior.

Verify implementation using `mise run validate`, with focused work only through the repository's mise tasks. Review schema/query/generated and formatting diffs. No live-device actuation is authorized by this spec.

## Risks, alternatives, and deferred work

| Risk / trade-off | Mitigation or deliberate choice |
|---|---|
| Library grammar is broader than product grammar | Explicit prevalidation plus contract tests, pinned parser, no library scheduler |
| Fold handling accidentally relies on memory or a one-hour offset | Bounded civil-minute comparison and New York/Lord Howe restart fixtures |
| Skip policy misses useful work during downtime | Visible gap history; deliberately no late household effects or replay |
| One transaction evaluates all definitions | Appropriate for initial household scope; avoid premature distributed scheduling; revisit if measured transaction latency approaches a minute |
| Abrupt clock correction suppresses future work until high-water is exceeded | Safety favors no replay; log gaps/progress and expose the behavior, rather than resetting deduplication silently |

Parser behavior was checked against the pinned upstream sources: [parser.go](https://github.com/robfig/cron/blob/v3.0.1/parser.go) and [spec.go](https://github.com/robfig/cron/blob/v3.0.1/spec.go). They are implementation evidence, not substitutes for Hearth's product tests.

### Bounded follow-up design notes — not initial scope

Future interests are Conditions/context-aware behavior and sustained-condition Triggers, such as requiring continuous absence. Conditions decide whether an Automation may proceed and remain separate from the Trigger cause that made it eligible. Sustained absence is Trigger eligibility duration, not a sleeping Run: absence must hold continuously for the required duration, and evidence that breaks it resets eligibility so a later absence must qualify anew. Initial, repeated, missing, and stale evidence policies need their own design before implementation.

Future State input must be captured through a separately specified canonical accepted-State seam, not raw Adapter Observations, with the evidence/context used for eligibility retained according to that future contract. These interests do not authorize Conditions or a new Trigger kind now, suspended/cancellable action sequences, or durable Run resumption. Do not introduce early plugin interfaces, general expression-engine scaffolding, runtime waits, or scheduler subscriptions in anticipation. The current expansion is only multiple identified cron Triggers sharing one atomic minute evaluation and existing admission path.

Product questions are resolved for the initial scope. This technical draft and spec 1 should be reviewed together before implementation.
