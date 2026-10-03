# Scheduled automation triggers

Status: Implemented, with restricted cron and native DST behavior. Effort: XL, cross-cutting domain, persistence, lifecycle, and transport work. Date: 2026-10-02.

Domain language: [GLOSSARY.md](../GLOSSARY.md) and [CONTEXT.md](../CONTEXT.md). Policy rationale: [ADR 0025](../docs/adr/0025-schedule-automations-without-replay.md). Existing admission and execution: [automations](automations.md), [Conditions](automation-conditions.md), and [Held-State Triggers](held-state-triggers.md).

## Problem and evidence

A household author needs Automations to start at household-local clock times and clock-field patterns without an Observation or Entity Event. Inspected Home Assistant configurations use daily times, quarter-hour boundaries, and an hour-divisor pattern. One restricted Cron Trigger represents each of these schedule rules; separate clock-time and time-pattern payloads are unnecessary. Solar schedules remain separate work.

Core already validates an IANA household timezone but does not inject the loaded location into automation runtime behavior. Automations already own transactional admission, immutable Run snapshots, Condition decisions, history, and one-active-Run concurrency. Held-State expiry has a separate app-owned worker and resets pending work on restart. Calendar scheduling must not inherit that reset model or turn it into a general deadline scheduler.

## Scope and non-goals

Add one `cron` Trigger kind to existing definitions, HTTP and MCP discovery and CRUD, history provenance, and existing web read-only rendering. Use a restricted five-field expression, minute precision, the household timezone, and a separate app-owned worker. All Trigger kinds can coexist in one definition and retain the existing limit of 32 unique Trigger IDs. Minute, hour, and weekday constraints belong inside the expression; day-of-month and month must be literal `*`.

No new HTTP routes, MCP tools, Device Facts, NATS resources, Core settings, execution queues, retries, per-Trigger timezones, web editor, schedule preview, gap history, or per-occurrence missed records. No unrestricted calendar cron, seconds/year fields, descriptors, elapsed intervals, one-off dates, solar events, time Conditions, delays, waits, or Trigger-dependent Step branching. All admitted Runs execute the existing ordered Steps; matching Trigger IDs do not select different actions. Manual invocation behavior is unchanged.

### 80/20 scope reduction

| Keep | Remove or defer | Work avoided |
| --- | --- | --- |
| Daily clock times and hour/minute patterns used in the inspected HA configurations | Clock seconds and seconds patterns | Seconds field, seconds grammar, and subminute schedule matching |
| One current-minute decision per Automation | Five-second lateness window and latest-match recovery | Candidate search, cutoff/evaluation coordination, fractional-age boundary cases |
| One cron expression for input and output | Separate clock-time/pattern payloads and integer input convenience | Duplicate Trigger families, custom field grammars, omission defaults, and JSON union normalization |
| Native DST behavior: skip missing times, allow repeated times | First-fold-only policy and historical timezone lookup | Fold detection, local-label deduplication, and suppression of repeated-hour patterns |
| Atomic UTC watermark, Conditions/busy, and lifecycle | Nothing from these safety contracts | These remain necessary to avoid replay, partial progress, and unsafe execution |

This is a scope reduction, not an assertion that 80% of effort disappears. Persistence migration and public/lifecycle integration still make the aggregate estimate XL. No custom DST ambiguity algorithm or timezone-transition subsystem remains.

## Definition contract

```json
{"id":"weekday_morning","kind":"cron","expression":"0 7 * * MON-FRI"}
```

```json
{"id":"quarter_hour","kind":"cron","expression":"*/15 * * * *"}
```

```json
{"id":"midnight_and_23","kind":"cron","expression":"0 0,23 * * *"}
```

These are Trigger objects within the existing definition, not standalone resources. The last example matches local midnight and 23:00 daily, not every 23 elapsed hours.

### Validation and normalization

The Trigger has exactly `id`, `kind:"cron"`, and required string `expression`. The expression has five fields in this order:

```text
minute  hour  day-of-month  month  day-of-week
0..59   0..23      *         *       0..6
```

- Day-of-month and month must be literal `*`, not semantically equivalent ranges or `*/1`. This excludes calendar-date scheduling and its day-field OR semantics.
- Minute, hour, and weekday support exact values, `*`, comma-separated lists, ascending inclusive ranges, and positive `/step` on a value, range, or wildcard. Steps start at the range's lower bound; `n/step` means `n` through the field maximum at that step. A bare `/15` is invalid; use `*/15`.
- Weekdays also accept case-insensitive three-letter `SUN` through `SAT`; numeric Sunday is 0. Reject 7, wrapping ranges, empty comma elements, signs, malformed ranges/steps, zero steps, and out-of-range endpoints. Leading-zero numeric tokens are accepted by the parser; there is no separate numeric-string normalization contract.
- All three variable fields combine with AND. Every field is explicit, so there are no omission defaults. `*/7` minutes means 0,7,...,56 and then 0 of the next hour, not a rolling seven-minute interval.
- Accept ordinary whitespace between fields, trim the expression, and collapse separators to one ASCII space on output/persistence. Preserve field token spelling and list order. Do not rewrite equivalent expressions into another syntax.
- Limit the input expression to 512 UTF-8 bytes before normalization. Reject empty/non-string/null expressions, wrong field counts, seconds/year fields, `@daily`/`@every` descriptors, `TZ=`/`CRON_TZ=` prefixes, and `?`, `L`, `W`, `#` extensions.
- Reject explicit null and unknown or other-family fields. Cron Triggers contain no `entity_id`, `local_time`, `weekdays`, `hours`, `minutes`, `seconds`, comparisons, disposition, or duration. Keep exactly one typed family payload for every Trigger, including previously supported kinds.

| Schedule | Expression |
| --- | --- |
| Daily 07:00 | `0 7 * * *` |
| Weekdays 07:00 | `0 7 * * MON-FRI` |
| Every quarter-hour | `*/15 * * * *` |
| Every minute during hour 07 | `* 7 * * *` |
| Midnight and 23:00 | `0 0,23 * * *` |
| Weekend quarter-hours | `*/15 * * * SAT,SUN` |

Use `github.com/robfig/cron/v3` v3.0.1 for parsing only, configured with `cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)`. Do not use `ParseStandard`, which enables descriptors, or instantiate the library's job scheduler. Before calling the parser, enforce the restricted field count, literal calendar wildcards, byte bound, unsupported prefixes/tokens, and simple field-component syntax. Each comma component is a wildcard, numeric value, weekday name, or value/name range, optionally followed by `/` and a positive integer; wildcard range endpoints are invalid. The library accepts some inputs outside this contract, including `?` and empty comma elements, so parser success alone is insufficient. Reuse the parser's bounds, names, range, and step semantics instead of implementing another numeric grammar. Parser behavior was checked against the [v3.0.1 source](https://github.com/robfig/cron/blob/v3.0.1/parser.go).

The embedded definition schema retains its existing v1 identity and strict `oneOf` family model, adding only the `cron` branch. HTTP, MCP, direct typed Service calls, and repository writes enforce the same restricted language through Core validation. Schema discovery advertises `expression` as required, non-null, with the byte bound described, examples, and restriction text; simple structural constraints are in schema while range/step semantics are enforced by the parser. Normalize once per boundary, reuse the result, and recheck changing eligibility transactionally. Existing implemented definitions and history remain readable without rewriting their JSON. The unimplemented `clock_time`/`time_pattern` proposals need no compatibility aliases or data migration.

A future UI can translate friendly time/weekday controls into cron and preserve unsupported advanced expressions in an expression view. It must not be the validation authority; direct HTTP/MCP callers still receive Core validation. This spec adds no authoring UI or bidirectional translation API. Separate Triggers remain necessary for schedules that do not form a cross-product: Monday at 07:00 and Friday at 09:00 are two expressions, not `0 7,9 * * MON,FRI`, which would also run Monday at 09:00 and Friday at 07:00.

## Scheduling and admission semantics

### Time and DST

The only candidate on a processing attempt is `at.UTC().Truncate(time.Minute)`, converted into the injected household location for local field and weekday matching. Due instants are UTC minute starts and must also have local second zero. Never admit a future minute or scan older minutes. The worker is best-effort, not a real-time guarantee. Historical IANA offsets with nonzero local seconds are not shifted or rounded into matches.

Use native household-clock behavior: nonexistent spring times are missed, with no shift or recovery; both distinct UTC instants for a repeated local minute are eligible. For example, Chicago local 01:30 on 2026-11-01 occurs at 06:30Z and 07:30Z, and `30 1 * * *` matches both. An every-minute or quarter-hour expression continues through both passes of the repeated hour. Existing enablement, Conditions, and busy rules apply independently at each instant, so eligibility does not guarantee two Runs.

No first-fold predicate, `ZoneBounds` walk, 48-hour lookback, remembered local-time labels, or local-label deduplication is needed. Convert each current UTC minute to the household location and match its fields. This agrees with [robfig/cron v3.0.1 DST tests](https://github.com/robfig/cron/blob/v3.0.1/spec_test.go) and the source-verified Home Assistant Core 2026.9.4 behavior in [Chicago gap tests](https://github.com/home-assistant/core/blob/2026.9.4/tests/util/test_dt.py#L521-L550) and [fold tests](https://github.com/home-assistant/core/blob/2026.9.4/tests/util/test_dt.py#L436-L518). Native library behavior is a reference for the policy, not authority to bypass current-minute admission.

### Activation and monotonic progress

Persist one global UTC-minute high-water mark. At calendar worker activation, initialize or advance it to `max(existing, activation_time.UTC().Truncate(time.Minute))` without admitting any Run or Skip. Only due instants strictly after the mark can be considered. An activation-minute occurrence is deliberately missed. This initialization is required even for an empty or all-disabled definition set.

On each successful tick, atomically advance the mark through the current UTC minute, including no matches and disabled definitions. Jump directly over older minutes without reading their schedules or writing missed history. Never decrease or delete the mark through history pruning. If the system UTC clock moves backward, do no work until the current UTC minute passes the mark. An autumn household-local clock change does not move UTC backward, so its second pass remains eligible. Restart advances the mark through the new activation minute and never catches up downtime, including an occurrence in that same minute. Restart between autumn occurrences does not suppress the later, still-future UTC occurrence.

Each successful definition creation or replacement makes only instants strictly after its stored `updated_at` eligible. This includes Step-only edits and enablement changes. Deletion prevents any new admission. Previously admitted Runs retain their snapshots. No per-Trigger deadline or schedule row is needed.

### Current-minute selection

For each enabled Automation, consider only the current UTC minute start. It must be strictly after the transaction's watermark and the current definition's `updated_at`, and match at least one Cron Trigger's minute/hour/weekday rule. At that instant group all matching Scheduled Trigger IDs in definition order. There is one admission decision per matching Automation, not one per Trigger. Repeated household-local labels are different occurrences when their UTC instants differ.

A live worker waking at 09:00:59 may admit a still-unprocessed 09:00 occurrence. At 09:01:00, the 09:00 occurrence is missed regardless of age. After a ten-minute stall, an every-minute pattern may admit the current minute but never the intervening minutes. Startup at 09:00:20 consumes 09:00 without admission, so this live-stall behavior is not restart catch-up. Creating or replacing a 09:00 definition at 09:00:20 cannot make that minute eligible because its due instant precedes `updated_at`.

Do not merge Device Fact or Held-State admission into schedule grouping; their independent admission and busy guard settle races. Older minutes receive no history. No lateness constant, fractional-age comparison, latest-match search, or historical candidate list remains.

Within one transaction, load current enabled definitions in ascending Automation ID order, select matches, check the existing busy guard, evaluate current Conditions once, persist each Run or admission Skip, and advance the global mark. Busy takes precedence over Conditions and leaves them not evaluated, as today. Reuse `automation_busy`, `conditions_false`, and `conditions_unknown`; never use `stale_fact` for a schedule. A Skip consumes the occurrence without queuing or retrying it.

### Snapshot, failure, and crash behavior

The Service reads enabled definitions and collects Condition Entity IDs only for plausible Scheduled Trigger matches in the sampled current minute, excluding unrelated and nonmatching definitions. It obtains one coherent Devices snapshot, or uses the existing empty snapshot without a Devices read when no references are needed. The repository independently re-reads current definitions inside the transaction. If a currently eligible decision needs an Entity absent from the supplied snapshot, return existing `ErrConditionSnapshotRequired` and roll back the entire transaction. The Service recollects and retries within the existing two-second `AdmissionTimeout`. A definition race or minute rollover may require new references; an Entity covered by the snapshot but lacking State is ordinary unknown evidence, not a missing-snapshot error. Snapshot optimization never replaces transaction-local eligibility checks or the watermark write on no-match ticks.

Sample one fresh `At` immediately before each repository attempt; use `At.Truncate(time.Minute)` for the candidate and untruncated `At` for Conditions and history timestamps. If preparation crosses a minute boundary, process only the new minute, relying on the existing snapshot-coverage retry when necessary. There is no older worker-time cutoff or second clock sample in the admission input. As with existing admission, the evaluation sample defines the decision time; this is not a guarantee of commit or execution before the minute ends.

The snapshot is coherent but not atomic with device ingestion, consistent with existing automatic admission. Busy guards and definition eligibility are transaction-local. Retain the partial unique active-Run index as the final concurrency guard. Calendar, Fact, manual, and Held-State admission must serialize or follow the existing conflict handling so they cannot create two active Runs.

No Commands or Run workers start before commit. On success, register committed Run workers using the existing admission reservation and process-owned execution context. On transaction failure, write neither schedule outcomes nor watermark progress, and start no worker. On a crash after commit but before execution starts, existing restart recovery interrupts the persisted Run without redispatch; activation skips the occurrence rather than replaying it. A crash before commit also causes no replay after restart, because activation advances progress.

A calendar worker error closes automation admission, fails readiness, and surfaces through the existing app worker-failure path. Do not silently restart it or replay a batch. Normal shutdown cancellation is not an unexpected worker fault.

## Implementation contracts

### Domain types and helpers

```diff
diff --git a/internal/modules/automations/trigger.go b/internal/modules/automations/trigger.go
@@
  TriggerKindHeldState TriggerKind = "held_state"
+ TriggerKindCron TriggerKind = "cron"
@@
  HeldState *HeldStateTrigger
+ Cron *CronTrigger
```

New definitions in `internal/modules/automations/schedule.go`:

```go
type CronTrigger struct {
    Expression string // restricted five fields, whitespace normalized
}

// One sampled current minute, never public history evidence.
type ScheduleTick struct {
    At time.Time // UTC evaluation sample; candidate is At.Truncate(time.Minute)
    Location *time.Location // required household location
}
```

`schedule_matching.go` owns pure production matching used by repository admission:

```go
func MatchScheduledTriggers(
    definition Definition, minute time.Time, location *time.Location,
) ([]TriggerID, error)
```

No match returns an empty ID slice with no error. Invalid freely constructed definitions, invalid restricted expressions, zero/non-minute-aligned instants, or nil location return `ErrInvalidAutomation` wrapped with useful field context. A shared private parser helper returns the normalized expression and parsed `cron.SpecSchedule`; keep library types out of public domain and repository interfaces. Compile each expression once per definition preparation/attempt and match its exported minute/hour/weekday bitsets against the current household-local minute. Ignore the parser's default `Location`; the injected household location is authoritative. Day-of-month/month require no matching logic because their literal wildcards were validated. No fold preparation or rejection is needed. The repository separately enforces enablement, `updated_at`, and UTC watermark eligibility. Keep private helpers small; no cron job engine, latest-match helper, or compiled historical search is needed. The library's `Next` searches strictly after its input and is not used as an inclusive current-minute match predicate.

`Trigger.EntityID()` returns empty for `cron`. Update its comment accordingly. Validate payload exclusivity across all four kinds. Skip device reference validation for `cron`, while preserving Step and Condition reference validation. Immediate Fact matching returns false for `cron`. Deep-copy the Cron payload pointer in normalization and snapshots; its expression is an immutable string.

### History source

```diff
diff --git a/internal/modules/automations/run.go b/internal/modules/automations/run.go
@@
  RunSourceHeldState RunSource = "held_state"
+ RunSourceSchedule RunSource = "schedule"
```

Use existing `Run`, `Skip`, `AdmissionResult`, and `AdmissionSkip` shapes. For schedule rows, Fact and HeldState evidence are absent, Run matched IDs contain 1–32 scheduled IDs, and Skip matched Trigger snapshots contain the same group. Ordinary timestamps reflect admission/evaluation, not due time. Preserve existing summary shape; matched IDs remain on details, not new summary fields. No scheduled due timestamp or timezone is exposed. Existing immutable Run definition and Skip snapshots remain the evidence explaining the matched IDs.

### Service and repository boundaries

```diff
diff --git a/internal/modules/automations/dependencies.go b/internal/modules/automations/dependencies.go
@@
  Now func() time.Time
+ HouseholdLocation *time.Location
```

No implicit UTC fallback. Existing non-schedule workflows remain usable with nil location; schedule initialization/processing fails explicitly when location is missing. The app injects `Config.LoadHouseholdTimezone()` once after validation. No new startup timestamp dependency is required because activation persists the startup barrier.

```diff
diff --git a/internal/modules/automations/repository.go b/internal/modules/automations/repository.go
@@
  ListEnabledAutomations(context.Context) ([]Record, error)
+ // Initialization advances progress without admission, retaining a future mark.
+ InitializeScheduleWatermark(context.Context, time.Time) error
+ // Re-reads definitions and commits the entire tick plus progress atomically.
+ AdmitDueSchedules(
+     context.Context, devices.EntityStateSnapshot, ScheduleTick,
+ ) (AdmissionResult, error)
```

New Service methods in `schedule_processing.go`:

```go
func (service *Service) InitializeSchedules(ctx context.Context, at time.Time) error
func (service *Service) ProcessDueSchedules(ctx context.Context) (AdmissionOutcome, error)
```

Initialization validates configured location and activation time, then calls repository initialization under the existing admission timeout. Processing acquires the existing `AdmissionGroup` reservation, checks device Command admission, gathers only plausible-match Condition references, reads the coherent snapshot when needed, calls repository admission with a fresh `ScheduleTick`, and starts/logs only committed outcomes. `Dependencies.Now` owns the evaluation sample; the worker passes no ticker timestamp. Propagate `ErrAdmissionUnavailable`, context errors, configuration errors, and repository failures. Retry only existing snapshot-required races within the deadline, not arbitrary errors. `DuplicateOutcomes` remains zero for schedules; repeated processing in a consumed minute produces no new outcomes.

The repository validates independent inputs, requires a preinitialized mark, and never initializes and admits in the same call. Missing initialization is `ErrAdmissionUnavailable`, not a fabricated historical baseline. Keep watermark read/upsert internal to the concrete repository. Do not add public per-Trigger cursors, candidate-list methods, or a generic transaction abstraction. Reuse existing condition-decision and history-persistence helpers; make narrowly source-neutral helpers only where actually shared.

### SQLite schema and migration

New migration `internal/platform/db/migrations/00009_automation_schedules.sql` introduces:

```sql
CREATE TABLE automation_schedule_watermarks (
    id TEXT PRIMARY KEY CHECK (id = 'global'),
    highwater_at TEXT NOT NULL CHECK (length(highwater_at) = 30)
);
```

Use the existing fixed-width UTC timestamp codec `2006-01-02T15:04:05.000000000Z`; watermark timestamps always have zero seconds and nanoseconds. Runtime decoding validates timestamps and minute alignment. Initialization and tick updates use a monotonic upsert:

```sql
INSERT INTO automation_schedule_watermarks (id, highwater_at)
VALUES ('global', ?)
ON CONFLICT (id) DO UPDATE
SET highwater_at = MAX(highwater_at, excluded.highwater_at);
```

In admission, read the mark inside the same transaction as current definitions and all outcomes. If the current minute is at or below it, return an empty committed result without writes. Otherwise process every enabled definition against that one minute and update through it even when zero schedules match. No batching limit or historical loop is added.

Rebuild `automation_history` using the established FK-safe migration pattern to add `schedule` to Run/Skip sources. Preserve all existing columns, checks for older sources, historical rows, Run Step foreign keys, and page/active-Run/fact-outcome indexes. Schedule Run/Skip rows require no Fact or hold columns, 1–32 matched IDs/snapshots, and automatic Condition semantics without bypass. Schedule Skips allow only busy/false/unknown reasons. Protect absent evidence and provenance at both mapping and persistence boundaries. Verify `PRAGMA foreign_key_check` and restoration of FK enforcement.

Down migration must fail transactionally if schedule history exists rather than invent older provenance or silently delete history. With no schedule history, restore the previous history schema and remove the watermark table. Document that binary/database downgrade after schedule usage is not supported without an explicit operator migration; do not prescribe deletion as part of normal rollback.

Add watermark query sources to `sqlite/dbqueries/automations.sql`, regenerate `dbsqlc`, and keep generated types inside SQLite. Existing history pruning and definition deletion must not modify the global mark. No fact receipts are written for schedules.

### App lifecycle

New private contracts in `internal/app/hearthd/schedule_scheduler.go`:

```go
type scheduleProcessor interface {
    InitializeSchedules(context.Context, time.Time) error
    ProcessDueSchedules(context.Context) (automations.AdmissionOutcome, error)
    StopAdmission()
}

type scheduleTickSource func() (<-chan time.Time, func())

func startScheduleScheduling(
    ctx context.Context,
    logger *slog.Logger,
    processor scheduleProcessor,
    now func() time.Time,
    tickSource scheduleTickSource,
) (*lifecycle.WorkerHandle, error)
```

Synchronously initialize progress immediately before starting the worker using `now()`. Return initialization errors to app startup. Retain the existing worker pattern with a one-second ticker so it notices minute boundaries promptly, without adding an aligned-timer algorithm. Delegate evaluation-time sampling to the Service; never pass buffered ticker timestamps, overlap processing calls, or drain a synthetic backlog. The durable watermark makes repeated checks within a minute no-ops at admission. The worker delegates location and matching to Automations, not app code. Stop the ticker on worker exit. Follow existing held-worker cancellation and error propagation patterns.

Wire the worker into `run.go`, runtime readiness, HTTP worker-death selection, and `shutdown.go`. Start it only after repository, Devices, and Automation Service are ready to admit Commands, before normal runtime is declared ready. Stop calendar and held-State workers before closing automation admission and draining Run workers. Preserve existing order for device shutdown, inbound consumers, relay, transports, and database cleanup. Calendar-started Runs use the existing drain group. A stopped or failed worker makes readiness false; no-match ticks do not.

Add one `core.automation_schedule_failed` log event for unexpected worker failure with the existing error conventions; record it in `docs/logging.md`. Use ordinary automation Run/Skip logs for admission outcomes. No per-second no-match or missed-occurrence logs are required.

### HTTP, MCP, and web compatibility

Existing definition create/replace accepts `cron` and returns its whitespace-normalized expression. Existing history list/detail returns `source:"schedule"` without `fact` or `held_state`. Preserve existing operation IDs, errors, revisions, cursors, and body limits. Invalid definitions use existing invalid-Automation error mappings and leave definitions/history unchanged.

Focused HTTP DTO change:

```diff
diff --git a/internal/modules/automations/api/models.go b/internal/modules/automations/api/models.go
@@
- Kind string `json:"kind" enum:"observation,entity_event,held_state"`
+ Kind string `json:"kind" enum:"observation,entity_event,held_state,cron"`
@@
- EntityID string `json:"entity_id"`
+ EntityID string `json:"entity_id,omitempty"`
@@
  ForSeconds *int64 `json:"for_seconds,omitempty"`
+ Expression string `json:"expression,omitempty"`
```

Input and output schedule fields have the same shape: one expression string. Raw definition input remains validated through the canonical embedded schema and codec plus Core's restricted parser. Huma and MCP discovery expose only `id`, `kind`, and `expression` for this family, documenting literal calendar wildcards and unsupported extensions. Do not make Entity ID optional in existing family schema branches. Update all three history source enums and Trigger output mapping in `models.go`.

Update `mcpAutomationTriggerBody` and `mcpTriggerOutput` in `api/mcp_outputs.go` with `expression,omitempty` and `entity_id,omitempty`; the MCP output path is separate from HTTP mapping and must not drop the expression. MCP definition input discovery derives from the embedded schema. OpenAPI is runtime-served, not checked in. Test live schema and actual round trips, not source-string inventories.

Update affected web unions/types and existing detail rendering for `cron`, its expression, and the schedule source. Display the expression directly; natural-language rendering and authoring controls are not required. Do not display an Entity link for Scheduled Triggers. Preserve existing Device Fact/Held-State rendering. Correct only definition/history fields needed by this change; unrelated web type lag is not a cleanup task.

## Project layout and ownership

```text
hearth/
├── go.mod / go.sum                                  # modify, parser-only robfig/cron/v3 dependency
├── GLOSSARY.md                                      # new, agreed schedule vocabulary
├── CONTEXT.md                                       # modify, broaden Trigger/Skip source wording and link schedule terms
├── docs/
│   ├── adr/0025-schedule-automations-without-replay.md # new, accepted policy rationale
│   ├── automation-gap-analysis.md                   # modify, mark scheduled-trigger gap implemented when code lands
│   └── logging.md                                   # modify, calendar worker failure event
├── specs/
│   ├── scheduled-automation-triggers.md              # new, this implementation contract
│   └── automations.md                               # modify, link schedule contract and new source/kinds
├── internal/
│   ├── modules/automations/
│   │   ├── trigger.go                               # modify, kinds/payloads/validation/reference identity
│   │   ├── schedule.go                              # new, domain types and schedule policy inputs
│   │   ├── schedule_matching.go                     # new, restricted parsing and household-local current-minute matching
│   │   ├── schedule_processing.go                   # new, Service activation and transactional workflow
│   │   ├── dependencies.go                          # modify, household location injection
│   │   ├── repository.go                            # modify, initialization/admission seam
│   │   ├── run.go                                   # modify, schedule provenance
│   │   ├── admission_results.go                     # modify, generalize automatic-admission comments
│   │   ├── definition_codec.go                      # modify, one cron JSON family
│   │   ├── definition_validation.go                 # modify, normalization/copying and schedule reference rules
│   │   ├── trigger_matching.go                      # modify, exclude scheduled kinds from Fact matches
│   │   ├── automation-definition.schema.json         # modify, strict family schemas
│   │   ├── schedule_matching_test.go                # new, independent clock/DST/selection fixtures
│   │   ├── schedule_processing_test.go              # new, snapshot race/commit-only execution boundary
│   │   ├── definition_codec_test.go                 # modify, schedule validation/round trips
│   │   ├── definition_validation_test.go            # modify, typed schedule validation
│   │   ├── definition_management_test.go            # modify, schedule save/reference boundary
│   │   ├── sqlite/
│   │   │   ├── schedule.go                          # new, atomic outcomes and watermark persistence
│   │   │   ├── history_mapping.go                   # modify, schedule provenance invariants
│   │   │   ├── admission.go                         # modify only shared persistence helpers needing schedule support
│   │   │   ├── dbqueries/automations.sql             # modify, watermark reads/upserts
│   │   │   ├── dbsqlc/                              # regenerate, SQL-derived implementation
│   │   │   ├── schedule_test.go                     # new, real SQLite atomicity/progress/admission
│   │   │   └── schedule_migration_test.go           # new, history/FK preservation and downgrade constraints
│   │   └── api/
│   │       ├── models.go                            # modify, canonical DTOs/source enums/mapping
│   │       ├── mcp_outputs.go                       # modify, independent MCP canonical output mapping
│   │       ├── openapi_test.go                      # modify, live accepted schema assertions
│   │       └── mcp_parity_test.go                    # modify, actual schedule round-trip parity
│   ├── app/hearthd/
│   │   ├── schedule_scheduler.go                    # new, single calendar worker and synchronous activation
│   │   ├── schedule_scheduler_test.go               # new, deterministic worker lifecycle
│   │   ├── run.go                                   # modify, loaded timezone/dependencies/startup/readiness/worker failure
│   │   ├── shutdown.go                              # modify, calendar stop before admission drain
│   │   ├── runtime_readiness.go                     # modify only if existing variadic worker contract needs generalization
│   │   └── automations_api_test.go                  # modify, app-wired schedule/history coverage
│   └── platform/db/migrations/
│       └── 00009_automation_schedules.sql            # new, watermark and additive history migration
└── web/src/
    ├── api/types.ts                                 # modify, schedule Trigger/source unions
    └── pages/
        ├── AutomationDetailPage.tsx                 # modify, read-only schedule rendering
        └── AutomationDetailPage.test.tsx            # modify, schedule render regression
```

Keep product rules and the repository contract inside Automations; SQLite owns transaction mechanics; app owns configuration/lifecycle; API owns transport mapping. No new module or platform scheduler. Extend existing test fixtures/fakes for the two Repository methods without adding test-only production interfaces. Use existing definition test filenames when covering shared validation; do not create duplicates to match this tree mechanically.

## Deliverables

| ID | Outcome | Effort | Owning paths | Dependencies | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D1 | One canonical cron definition, restricted parser, and local matching with native DST behavior | M | `internal/modules/automations/{trigger,schedule,schedule_matching,definition_codec,definition_validation,trigger_matching}.go`, embedded schema and local definition/matching tests; `go.mod`, `go.sum` | none | A1–A3 |
| D2 | Atomic schedule history and durable monotonic progress with migration safety | L | `internal/modules/automations/sqlite/{schedule,admission,history_mapping}.go`, query sources, generated `dbsqlc`, migration 00009, SQLite tests; `run.go`, `repository.go`, `admission_results.go` | D1 | A3–A7, A9 |
| D3 | Service workflow, timezone injection, activation boundary, healthy single worker, shutdown/drain | L | `internal/modules/automations/{dependencies,schedule_processing}.go`, Service tests; `internal/app/hearthd/{schedule_scheduler,run,shutdown,runtime_readiness}.go` and lifecycle tests | D2 | A6, A8–A10 |
| D4 | HTTP/MCP schema and history compatibility plus existing web rendering | M | `internal/modules/automations/api/{models,mcp_outputs}.go`, OpenAPI/parity tests, app API tests; `web/src/api/types.ts`, detail page/test | D1–D3 | A1, A11–A12 |
| D5 | Domain/docs integration, generated consistency, full regression validation | M | `CONTEXT.md`, `GLOSSARY.md`, ADR 0025, `docs/{logging,automation-gap-analysis}.md`, `specs/{automations,scheduled-automation-triggers}.md`, all affected generated/test artifacts | D1–D4 | A13 |

The aggregate estimate remains XL. D1 drops from L to M because one Trigger family, a proven parser, and native DST matching replace custom pattern and fold algorithms. Persistence and lifecycle remain the highest-risk work; implement them before polishing web rendering. Add only the parser dependency, `github.com/robfig/cron/v3` v3.0.1. Its scheduler runtime is not used; this DST revision does not change the app's wake-up implementation.

## Acceptance and validation

The spec and accepted ADR are the test oracles. Do not compute expected due instants using production matching. Use literal reviewed UTC/local fixture pairs and explicit expected history outcomes. Own calendar logic in matching tests, persistence invariants in real SQLite tests, and lifecycle in deterministic worker tests. API and web tests protect their distinct transport/rendering risks rather than replaying every domain case.

| ID | Boundary and expected result | Primary check |
| --- | --- | --- |
| A1 | Save/read collapses expression whitespace and preserves tokens. Reject empty/null/non-string/over-512-byte expressions, nonliteral day-of-month/month wildcards, wrong field counts, prefixes/descriptors/extensions, invalid bounds, Sunday 7, zero steps, wrapping ranges, empty comma items, and other-family fields without writes. Existing kinds retain required Entity fields; old proposed clock_time/time_pattern kinds are not accepted. | Definition boundary tests including inputs accepted too loosely by the library; actual HTTP/MCP round trips |
| A2 | Daily/weekday fixed times, lists, ascending ranges, wildcard/range/value steps, case-insensitive weekday names, `*/7` minute reset, and midnight/23:00 match reviewed fixtures with AND semantics. Distinct weekday/hour pairings retain two Triggers rather than accidental cross-products. Cron never matches Device Facts. | `schedule_matching_test.go` and existing Fact matcher regression |
| A3 | `30 2 * * *` in America/Chicago 2026-03-08 never matches missing local 02:30 and is not shifted to 03:00/03:30. `30 1 * * *` on 2026-11-01 matches both 06:30:00Z and 07:30:00Z. Every-minute matching continues across 06:59Z to 07:00Z, which converts from 01:59 to repeated 01:00. `45 1 * * *` in Australia/Lord_Howe on local 2026-04-05 matches both 2026-04-04 14:45Z and 15:15Z, without a one-hour assumption. Both autumn occurrences can request admission, with ordinary busy/Condition rules; repeated checks of either UTC minute create no duplicates. | Literal timezone matching fixtures; one real SQLite repeated-occurrence test with the first Run terminal before the second instant. No live HA required |
| A4 | One enabled Automation with coincident Cron Triggers produces one Run containing all matching IDs in declaration order and source schedule; disabled/unmatched definitions produce no history. | Real SQLite admission/history tests |
| A5 | False/unknown Conditions and an active Run produce the existing respective Skips; busy precedes Conditions. No Fact/hold evidence, stale_fact Skip, queue, retry, or trigger-dependent actions. | Real SQLite admission test with coherent snapshot; existing execution checks |
| A6 | A live worker at 09:00:59 may admit unprocessed 09:00; at 09:01 it never searches 09:00. After a long stall, an every-minute pattern admits only the current minute. A definition written at 09:00:20 cannot admit 09:00. No future minute is admitted. | Real SQLite current-minute outcome tests with literal time samples |
| A7 | Two Automations due together both commit with progress, or both roll back on failure. Reprocessing a minute creates no duplicate history. Progress advances with zero matches, survives pruning, and cannot decrease on a backward clock. | Real SQLite transactions, rollback failure via real persistence boundary, reopen DB and prune |
| A8 | Activation misses its current minute and all downtime. Restart at 09:00:20 does not admit an unprocessed 09:00 occurrence; 09:01 is eligible normally. A preexisting future watermark remains intact and blocks scheduling until passed. Restart at 07:00Z between Chicago's two autumn 01:30 occurrences still permits 07:30Z; this is a future occurrence, not catch-up or a duplicate of 06:30Z. | Activation/persistence tests and Service/app wiring test |
| A9 | Definition replacement between pre-read and admission uses current revision, future-only updated_at and snapshot IDs; snapshot-required races and preparation crossing a minute boundary recollect as needed. Nonmatching definitions do not require a Devices snapshot, but a newly matching transaction-local definition still triggers coverage retry. Concurrent manual/Fact/held/schedule admission leaves at most one active Run. | Service preparation/race test plus real SQLite admission concurrency boundary |
| A10 | No execution before commit; failed transaction launches no Commands. Unexpected calendar failure closes admission and fails readiness; stop precedes drain and admitted Runs remain joined. Restart interrupts a committed-but-unstarted Run without redispatch. | Service commit-only execution test, existing restart recovery regression, deterministic app worker/shutdown tests |
| A11 | Runtime OpenAPI and MCP input discovery expose one strict cron family with required expression and restriction documentation. Actual create/get/replace and Run/Skip detail round trips preserve the normalized expression, grouped IDs, source, and absent Entity/Fact/hold placeholders. Invalid unrestricted expressions fail Core validation through both transports. Old source output remains unchanged. | API parity/OpenAPI tests and one app API integration |
| A12 | Existing detail page renders the cron expression, recognizes schedule history, and emits no empty Entity link; existing family rendering still passes. No authoring translator is required. | Extend `AutomationDetailPage.test.tsx`; web build |
| A13 | Empty-database and migration-8 upgrades preserve historical sources, Run Steps, FK checks and indexes. Invalid provenance is rejected. Down fails without mutation when schedule history exists. Generation is reproducible and all checks pass without unrelated changes. | SQLite migration tests, generation diff review, full validate |

### Commands and prerequisites

Use repository mise tasks, not underlying tools directly:

```sh
mise run --skip-deps generate
mise run --skip-deps format
GO_PACKAGES='./internal/modules/automations/... ./internal/app/hearthd ./internal/platform/db' mise run --skip-deps test
GO_PACKAGES='./internal/modules/automations/... ./internal/app/hearthd ./internal/platform/db' mise run --skip-deps lint
GO_PACKAGES='./internal/modules/automations/... ./internal/app/hearthd ./internal/platform/db' mise run --skip-deps vet
mise run web-test
mise run web-build
mise run validate
git diff --check
```

Focused tests cover A1–A11/A13; web tasks cover A12 and transport type compatibility. Full validation covers A13 and unrelated regression safety. SQLite and timezone checks are local, as are app tests with the repository's existing test infrastructure. Full validation requires installed mise tooling, frontend dependencies, and a reachable Docker daemon for existing real-Mosquitto tests. If unavailable, report the exact blocked check rather than claiming it passed. Live Home Assistant access is not required for implementation acceptance. Do not edit source or tests while validation runs. Review generated, formatting, and module changes before any separately authorized commit.

For sensitivity, use isolated mutation or negative controls for removing watermark monotonicity, replaying a previous minute, suppressing the second autumn occurrence, or launching workers before commit. Expected tests must fail for their protected defect. Do not introduce faults into the user's checkout or add exports solely to make tests possible. Controlled tick sources follow the existing worker pattern; database behavior is tested against SQLite, not mocked transactions.

## Trade-offs and risks

| Chosen approach | Alternative | Reason and cost |
| --- | --- | --- |
| Dedicated calendar worker | General scheduler shared with held-State expiry | Avoid refactoring distinct restart models; shared infrastructure may be revisited when other temporal features arrive |
| Global atomic watermark/tick | Per-occurrence queue or batched progress | Small durable state and straightforward no-replay behavior; large household definition sets can exceed admission timeout |
| One restricted five-field cron family | Separate clock-time/pattern payloads or unrestricted cron | One public shape and proven parser; calendar fields fixed to `*` avoid date rules, while direct callers use cron syntax |
| Current minute only | Five-second recovery or replay of missed instants | No historical search or age-bound coordination; live lateness can approach a minute, and previous minutes are deliberately missed |
| Core parsing with future UI translation | UI-owned validation or a new editor now | HTTP/MCP callers receive the same restrictions; friendly authoring and advanced-expression handling remain future UI work |
| Skip spring gaps, allow both autumn occurrences | Fold deduplication or shifting missing times | Matches native cron and HA behavior without a transition lookup algorithm; a fixed-time action can run twice during the repeated hour |
| Minimal existing history | Due-time/timezone evidence or gap records | Smaller API/storage change; operators cannot reconstruct exact missed-time coverage from history |

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Mistaking a repeated local label for a duplicate UTC occurrence | Suppressed fixed-time actions or an hour-long pattern pause | Keep progress in UTC; fixtures cover both autumn instants, continued patterns, half-hour rollback, and restart between occurrences |
| Library accepts syntax beyond the restricted contract | Unintended calendar/timezone behavior or malformed schedules | Pre-parser restriction checks, exact calendar wildcards, and negative boundary fixtures; never use the library job engine or Next for admission |
| Mark advances before all decisions commit | Permanently lost due Automations | One transaction for all enabled definitions and progress; rollback/multiple-Automation persistence tests |
| Definition changes introduce missing Condition references | Incorrect admission using stale snapshot | Transaction-local definitions, coverage error/retry within deadline, revision-race test |
| Many definitions overwhelm two-second admission | Worker failure closes admission | One candidate minute, direct local field matching, selective snapshot references, no temporal backlog scan; measure representative large definition set before landing, without weakening atomicity silently |
| Incorrect FK-safe history rebuild or downgrade | History/Run Step loss | Upgrade from migration 8 with populated older-source rows, empty DB migration test, FK checks and non-destructive downgrade refusal |
| Crash between admission commit and worker launch | Physical action never executes | Deliberately retain existing interruption/no-replay contract; verify restart recovery rather than redispatching |

## Approval and completion

The user approved minute precision and current-minute-only processing through the 80/20 review, requested one restricted cron family, and confirmed native DST behavior: skip spring gaps and allow both autumn occurrences for fixed times and patterns. Types, interfaces, examples, acceptance criteria, glossary, and ADR use that model. Day-of-month and month remain literal `*`; friendly UI translation remains out of scope. The app wake-up implementation is unchanged by this DST decision. Implementation may reorganize private helpers without changing behavior, ownership, or public shape. Feature completion requires D1–D5, pass/fail evidence for A1–A13, and intended generated changes reviewed. This document does not authorize commits or live automation changes.

D1–D4 are implemented. D5 documentation links the current kinds and sources in
[automations](automations.md), marks the clock-trigger gap implemented in the
[gap analysis](../docs/automation-gap-analysis.md), and documents
`core.automation_schedule_failed` in [logging](../docs/logging.md). Schedule
vocabulary remains in [GLOSSARY.md](../GLOSSARY.md) and [CONTEXT.md](../CONTEXT.md).
[ADR 0025](../docs/adr/0025-schedule-automations-without-replay.md) records the
downgrade policy. Final validation evidence and generated-diff review belong to
the implementation review, not this documentation-only update.
