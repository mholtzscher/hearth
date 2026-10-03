# Hearth scheduled automations

Domain terms agreed during the scheduled automation design interview. Existing Hearth domain terms remain defined in [CONTEXT.md](CONTEXT.md).

## Language

**Scheduled Trigger**:
A Trigger that requests automatic admission of an Automation when a recurring household-local clock rule is due on an eligible weekday. The Cron Trigger is the supported Scheduled Trigger; it respects Automation enablement, Conditions, and the rule that an Automation may have only one active Run.
_Avoid_: Timer, delay, elapsed interval, time Condition

**Cron Trigger**:
A Scheduled Trigger expressing minute, hour, and weekday matches in one recurring clock rule, without calendar-date restrictions. It follows household-local clock boundaries rather than measuring elapsed time.
_Avoid_: Clock-Time Trigger, Time-Pattern Trigger, elapsed interval, periodic timer

**Scheduled occurrence**:
One distinct due instant of a Scheduled Trigger, whether or not it results in a Run; repeated autumn local times represent separate occurrences when their instants differ. An occurrence missed while Core is offline does not request admission when Core returns.
_Avoid_: Run, queued Run
