# Scheduled automation triggers

A `cron` Trigger admits an Automation at a recurrent household-local clock time.
It is part of an ordinary definition, not a separate resource or queued job.
Core's required `household_timezone` IANA setting supplies that timezone.
Changing it takes effect after Core restarts.
[ADR 0025](adr/0025-schedule-automations-without-replay.md) records the no-replay
and daylight-saving decisions.

## Definition

```json
{"id":"weekday-morning","kind":"cron","expression":"0 7 * * MON-FRI"}
```

Only `id`, `kind`, and `expression` are accepted. Cron Triggers have no Entity,
comparison, disposition, or duration fields, but can coexist with other Trigger
families. Expressions are required, non-null, and at most 512 UTF-8 bytes before
normalization. Output trims outer whitespace and joins fields with one ASCII
space, preserving field spelling and list order.

The five fields are `minute hour day-of-month month day-of-week`:

| Field | Accepted values |
| --- | --- |
| Minute | 0–59 |
| Hour | 0–23 |
| Day of month | Literal `*` only |
| Month | Literal `*` only |
| Day of week | 0–6 or case-insensitive `SUN` through `SAT`; Sunday is 0 |

Minute, hour, and weekday accept exact values, `*`, comma-separated lists,
ascending inclusive ranges, and positive `/step` on a value, range, or wildcard.
A step starts at its component's lower bound: `*/7` minutes means
0,7,…,56, then 0 in the next hour. `n/step` extends from `n` to the field maximum.
Leading-zero numbers are accepted.

Seconds/year fields, `@daily`, `@every`, timezone prefixes, `?`, `L`, `W`, `#`,
signs, zero steps, wrapping ranges, weekday 7, empty list elements, and bare
`/15` are rejected. Write `*/15` instead. Parser success alone is not contract
validation.

Minute, hour, and weekday combine with AND. `0 0,23 * * *` means local midnight
and 23:00, not every 23 elapsed hours. Field lists form a cross-product:
`0 7,9 * * MON,FRI` includes both times on both days. Use separate Triggers for
other combinations.

## Matching and admission

Each tick considers only the sampled current UTC minute, converted to household
local time. It never scans older minutes or queues future ones. Schedules use
clock boundaries, not elapsed durations.

Nonexistent spring times are missed without shifting. Both distinct UTC instants
of a repeated autumn local minute are eligible; enablement, busy, and Conditions
apply independently. A repeated local label is not a replay if its UTC instant
differs.

A durable global UTC-minute high-water mark records progress. Activation advances
it through the current minute without admission, deliberately missing that minute.
Ticks process only a sampled minute strictly after the mark and after the
definition's update time. Step edits and enablement changes therefore affect only
future minutes. Restart never catches up downtime. A backward clock pauses
scheduling until UTC passes the mark. Definition deletion and history pruning
never lower it.

One transaction loads current enabled definitions in Automation ID order, groups
matching Trigger IDs in definition order, and commits all outcomes with the mark.
Several matching Cron Triggers on one Automation produce one decision. Busy
precedes Conditions and records `automation_busy`; false or unknown Conditions
record their ordinary Skips. A Skip consumes the occurrence without retry, and
there is no `stale_fact` Skip or history for missed minutes. Workers start after
commit. Schedule, Fact, manual, and Held-State admissions remain separate; the
one-active-Run guard settles races.

## History and API

Outcomes use the `schedule` cause with matched Trigger IDs or snapshots in
definition order. There is no Fact or Held-State evidence, due timestamp, or
timezone field. Ordinary admission and evaluation timestamps provide timing
evidence. Matching Trigger details appear on Run or Skip detail, not collection
summaries. Definition writes return the normalized expression; the dashboard
displays it without an Entity link.

## Ownership and lifecycle

The Automations domain owns restricted parsing and household-local matching.
The Service prepares Condition snapshots outside SQLite transactions and retries
snapshot-coverage races within its admission deadline. The repository owns the
atomic tick and watermark.

The [app scheduler](../internal/app/hearthd/schedule_scheduler.go) activates after
recovery and before readiness, samples once per second, and stops before
Automation admission drains and Runs join. No-match ticks do not fail readiness.
An unexpected worker failure closes Automation admission, fails readiness, and
logs `core.automation_schedule_failed`; there is no silent restart or replay.
See the [module ownership guide](../internal/modules/automations/README.md).

## Upgrade and rollback

Cron definitions and `schedule` history need a compatible binary. The original
schedule migration's Down refuses when schedule history exists. Deleting history
is not a supported rollback; restore the matching pre-upgrade backup. Later
development resets are described in the [revision guide](automation-definition-revisions.md).
