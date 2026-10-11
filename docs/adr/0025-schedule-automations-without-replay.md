---
status: accepted
---

# Schedule automations without replay

Scheduled Automations request physical actions whose usefulness can expire before Core returns or recovers from a stall. Use a durable minute high-water mark to avoid reconsidering processed due instants, skip downtime occurrences, and evaluate only the current UTC minute after a live-Core stall. This deliberately favors missing an action over duplicating it or issuing a burst of stale actions.

Cron Triggers use the household timezone, skip nonexistent spring times without shifting them, and allow both distinct UTC instants of repeated autumn local times. Patterns continue through both passes of the repeated hour. Coincident matching Triggers share one admission decision; enablement, Conditions, and the ordinary busy rule still apply, without queues or retries.

The implementation contract is [Scheduled automation triggers](../../specs/scheduled-automation-triggers.md). [GLOSSARY.md](../../GLOSSARY.md) defines Scheduled Trigger, Cron Trigger, and scheduled occurrence.

## Consequences

- A backward system UTC clock adjustment pauses scheduling until time passes the durable high-water mark. The mark survives restart and history pruning. A household-local DST rollback does not move UTC backward or block its second pass.
- New, replaced, or enabled definitions are eligible only for due times strictly after the definition change.
- A live-Core stall misses previous minutes. A matching current minute may admit at any point before that minute ends; there is no separate lateness window.
- Restart does not catch up occurrences from downtime, even when Core returns within their minute.
- Calendar worker activation consumes its current UTC minute without admission, so boot time and the activation minute are not replayed.
- Restart between two autumn occurrences does not suppress the later future occurrence. A repeated local label is not a replay when its UTC instant differs.
- Only admitted Runs and admission Skips appear in ordinary history. Missed occurrences have no history entries, and there is no dedicated gap history.
- Public history carries the schedule source and matching Trigger IDs, without additional scheduled-time or timezone evidence.
- Binary/database downgrade after schedule usage is unsupported without an explicit operator migration. The database down migration refuses transactionally when schedule history exists; deleting history is not a normal rollback procedure. A database downgrade without schedule history does not make remaining Cron Trigger definitions readable by an older binary.

## Alternatives considered

Replaying missed occurrences or considering every recent due instant would preserve more schedule matches but could execute stale physical actions or create bursts of Runs and busy Skips. Following a backward-corrected system UTC clock would avoid a scheduling pause but require additional occurrence deduplication rules. First-fold-only execution would avoid two actions with the same local clock label, but needs custom ambiguity detection and would pause every-minute patterns through the repeated hour. The user chose native cron and Home Assistant DST behavior instead, accepting both autumn occurrences and retaining ordinary admission rules at each instant.

The initial design supported seconds and latest-only recovery within five seconds. The user narrowed it through an 80/20 review to minute precision and current-minute-only admission, then replaced separate clock-time and time-pattern payloads with one restricted five-field cron expression. Day-of-month and month must be literal `*`, preserving clock/weekday scope without introducing calendar-date or day-of-month/day-of-week OR rules. Core owns parsing and validation; a future UI may translate friendly controls into expressions. The subsequent DST review removed first-fold logic, historical timezone lookbacks, and local-label deduplication. Durable UTC progress and atomic admission remain required.
