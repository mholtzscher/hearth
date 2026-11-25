# Hearth glossary

Hearth is a home automation system intended to operate independently of Home
Assistant while remaining compatible with specialist protocol services.

## Language

**View artifact**: An immutable, validated interface definition retained in a
committed agent tool result. It is not a saved dashboard.

**Dashboard draft**: The browser's working view definition. Manual edits and
generated revisions remain drafts until explicitly saved.

**Starter question**: A suggested prompt offered in an empty conversation.
Tapping one sends its prompt immediately. Core generates them from a household
digest on a schedule, and the browser falls back to fixed questions. A starter
question must not request a Command. Core drops any that starts with a command
verb. See [starter questions](docs/reference/agent-skills.md#starter-questions).

**Saved dashboard**: An independently persisted view definition with resource
references and a revision checked on replacement or deletion. It renders current
data without its source conversation or a model request.

**Read binding**: A declarative resource identity and bounded read requirement
in a view card. It cannot execute effects and is distinct from an Adapter Binding.
See [dashboards](docs/reference/dashboards.md).

**State field**: A scalar projection of Entity State at a JSON Pointer, derived
from its schema. The empty pointer selects the root.

**Quantity field**: A numeric State field with a canonical unit and optional
quantity kind. Schema annotations may fix these or reference Entity support.
See [units](docs/reference/units.md).

**Household unit system**: The household-wide `metric` or `us` display choice.
The backend projects ordinary numeric reads; stored State, Commands and
Automation operands remain canonical. See [read units](docs/reference/units.md).

**Functional replacement**: A Hearth installation providing required household
behavior without Home Assistant in operation. Specialist protocol services may
remain.

**Adapter**: A process translating between one configured external system and
Hearth's wire protocol. An **Adapter instance** is one configured occurrence
identified by a stable household-scoped slug.

**Adapter health**: The assessment of whether an Adapter instance has a live
process that can use its configured external system: `unknown` without current
evidence, `healthy` when both are usable, and `unhealthy` when either is not.

**Binding**: The durable association between an Adapter's external object and
its canonical Hearth Device and Entities. An Adapter-scoped stable key preserves
the association across external ID changes; ambiguous conflicts require explicit
reconciliation.

**Adapter Function**: An Adapter-defined capability outside the canonical Entity
Operation model. The Adapter defines its arguments, results and behavior; Core
owns discovery, validation, routing and durable Invocation evidence.

**Function catalog**: The immutable set of Functions registered by one Adapter
runtime, identified by a revision.

**Function Invocation**: A recorded attempt to execute an Adapter Function
against a selected runtime and catalog revision. It is evidence, not queued or
replayable work. See [Adapter Functions](docs/reference/adapter-functions.md).

**Migration adapter**: A disposable Adapter for moving a household from Home
Assistant to native ownership without introducing Home Assistant dependencies
into Core. Hearth's migration adapter has been removed.

**Household**: The single home automation environment administered by one
Hearth installation.

**Device**: A physical or virtual thing represented in Hearth that groups
related Entities.

**Entity**: An independently addressable State or control point belonging to a
Device. An event-source Entity instead has no State or Operations and reports
named Entity Events.

**Display name**: The human-readable name of a Device or Entity, supplied by
its Adapter unless a Hearth name override takes precedence. It is not identity.

**Name override**: A household-chosen display name that survives Adapter name
updates. Removing it restores the latest Adapter-supplied name without renaming
the external object or changing canonical identity.

**Entity availability**: The Devices-owned projection of whether an Entity can
be reached through its owner. It is `unknown` without eligible health and report
evidence, `available` with a matching available report from a healthy owner, and
`unavailable` when eligible evidence says so. It can lag authoritative Adapter
health and is distinct from enablement and State freshness.

**Entity enablement**: Core's reversible choice of whether an Entity participates
in normal control and State projection. New Entities default to enabled;
registration may set only their initial value. Management and the owning Adapter
may change it idempotently, with the last committed change taking effect.
Disablement retains identity, history and
last State, rejects new Commands and ordinary Observations, and rejects Events.
Only a linked Observation for an active pre-disable Command can still project
State and satisfy that Command. See [architecture](docs/architecture/architecture.md#health-availability-and-enablement).

**Entity support**: A type-specific statement of supported State and Operations.
An Operation is supported when present in support. Event-source support instead
lists the names it may report. Support changes do not change Entity identity or
the meaning of active Commands.

**Entity Event**: A named occurrence identified by its event ID. Redelivery of one
ID is the same event; a new ID is a new occurrence even when the name repeats.
Acceptance means Core's processing-time rules passed, not
that physical truth or causation is proven. It has no State, Command link,
arbitrary payload or claimed source time and does not change State or availability.
It is not the outbound `enumaction.trigger` Operation.

**Operation**: A named Entity-type command capability with a support shape,
parameters, deadline and observed or dispatched outcome policy. Invoking a
supported Operation creates a Command.

**Observation**: An Adapter's value report, identified once despite redelivery.
Accepted history contains a normalized value; rejected history contains a code
without the value. Neither the report nor acceptance proves physical truth or
causation. Levoit explicitly reports acknowledged control requests optimistically,
not as measurements.

**State**: The current value accepted for an Entity, determined by Core receive
order of first-seen valid Observations. A same-value report advances its evidence
and timestamps. State is not a desired-state queue. Levoit's explicitly
optimistic control State also enters history and Automations until later reports
replace it.

**Forecast**: A State snapshot predicting conditions for future periods. It
records what the source predicted, not the conditions themselves; a later
Observation replaces the snapshot.

**Command**: A deadline-bounded request for an Entity Operation. An observed
Operation is satisfied only by its own matching linked Observation; acceptance
alone is not satisfaction. A dispatched Operation completes on durable
acceptance without an Observation or State. Commands can overlap, and Core
records outcomes but never replays unfinished Commands after restart. An
unhealthy owner prevents dispatch; an unavailable Entity with a healthy owner is
still attempted.

**Device Fact**: A Core statement queued atomically with a first-seen accepted
Observation or Entity Event and published at least once to a bounded stream.
Consumers must tolerate duplicates and eviction. A Fact records Core evidence,
not physical truth; Command lifecycle has no Fact and remains authoritative in
durable Command history. See [outbox relays](docs/architecture/outbox-relays.md).

**Automation**: A named definition with zero or more alternative Triggers,
optional Conditions and an ordered Step sequence. An empty Trigger list permits
explicit invocation only. Disablement prevents automatic execution, not manual
or Step invocation.

**Trigger**: An identified reason for automatic Automation admission: a matching
Device Fact, a completed held-State requirement or a scheduled occurrence.
Manual invocation does not have a Trigger. The Entity Operation named `trigger`
is separate.

**Condition**: A composable requirement over Entity State or a Run's matched
Triggers. Its result is true, false or unknown; it does not initiate execution.
See [Conditions](docs/reference/automation-conditions.md).

**Admission Condition**: A State Condition that must be true before a Run starts,
unless an operator explicitly bypasses it for manual invocation.

**Branch Condition**: A Condition evaluated at a reached branching Step. An
unknown root result fails the Run instead of choosing a fallback.

**Branch decision**: Recorded evidence of a reached branch evaluation and its
selection, no-match outcome or failure. Selection does not prove that Commands
executed or succeeded.

**Entity Event Trigger**: A Trigger matching an accepted Entity Event Fact by
Entity and event name.

**Observation Trigger**: A Trigger matching an accepted Observation Fact by
Entity, disposition and configured value comparisons, optionally including
preceding State. An `unchanged` Observation can match.

**Held-State Trigger**: A Trigger requesting one admission decision when accepted
State satisfies a predicate for a duration. It tracks a **Hold**, not a queued
Run. Facts can lag, restart resets pending holds, and an admission decision
consumes a hold even when it produces a Skip. See
[Held-State Triggers](docs/reference/automation-conditions.md#held-state-triggers) for the hold and
re-arming rules.

**Scheduled Trigger**: A Trigger requesting automatic admission for an eligible
household-local clock occurrence. A **Cron Trigger** matches minute, hour and
weekday rather than measuring elapsed time.

**Scheduled occurrence**: One distinct due instant of a Scheduled Trigger,
whether or not it results in a Run. An occurrence missed while Core is offline
is not replayed. See [Automation schedules](docs/reference/automation-schedules.md).

**Step**: An instruction in an Automation's ordered sequence. A branching Step
selects a nested sequence, then execution continues after the branch unless the
Run fails or is interrupted.

**Command Step**: An instruction requesting one Entity Operation. It is distinct
from the Command attempt and its outcome.

**Delay Step**: An unconditional elapsed wait beginning when the Run reaches
it. The Run remains active; shutdown or restart interrupts it.

**Notification Step**: An instruction requesting one immediate notification at
a destination. Success means provider acceptance, not delivery or readership.

**SubAutomation Step**: An instruction admitting a separate Run of a statically
referenced Automation and waiting for durable child success. It passes no values,
produces no output and fails immediately if the target is busy.

**Function Step**: An instruction invoking an Adapter Function with fixed
arguments and an expected contract fingerprint. It advances only on retained
success; matching contracts can survive a Session change.

**Function execution**: A reached Function Step's reserved Invocation link and
retained evidence. Reservation alone does not establish dispatch or success.

**Notification execution**: A reached Notification Step's reserved Attempt link
and acceptance, failure or interruption evidence. It is not a delivery receipt
or permission to replay.

**Delay execution**: Evidence of a reached Delay Step's start, expected finish
and completion or interruption. It is not permission to resume after restart.
See [Step executions](docs/reference/automation-step-executions.md).

**Run**: One recorded Automation execution using a definition snapshot, started
automatically, manually or by a SubAutomation Step. Success means its selected
sequence and child Runs completed with required Command outcomes and provider
acceptances, not that physical effects or notification delivery were verified.

**Automation Skip**: A recorded admission decision that starts no Run, for
example because of Conditions, freshness or concurrency. A missed scheduled
occurrence is not a Skip, and a Skip does not queue execution.

**Canonical ID**: An immutable Hearth-assigned Device or Entity identity that
survives changes to names, external identifiers or owning Adapters.
