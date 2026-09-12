# Hearth

Hearth is a home automation system intended to operate independently of Home Assistant while remaining compatible with specialist protocol services.

## Language

**Functional replacement**:
A Hearth installation that provides all required household behavior with Home Assistant eliminated from operation. It may continue to depend on specialist protocol services.
_Avoid_: Full-stack replacement, Home Assistant parity

**Adapter**:
A process that translates between one configured external system and Hearth's wire protocol.
_Avoid_: Integration, plugin

**Adapter instance**:
One configured occurrence of an adapter, identified by a stable subject-safe slug within a household.
_Avoid_: Adapter type, process ID

**Adapter health**:
The current assessment of whether an Adapter instance has a live Adapter process that can use its configured external system. It is `unknown` without current evidence, `healthy` only when both are usable, and `unhealthy` when either is unavailable.
_Avoid_: Adapter status, runtime health, upstream health

**Binding**:
The durable association between an adapter's external object and its canonical Hearth Device and Entities. An adapter-scoped stable binding key preserves the association when an external identifier changes; ambiguous identity conflicts require explicit reconciliation.
_Avoid_: Discovery result, entity name

**Migration adapter**:
A disposable adapter that keeps a household operational while devices move from Home Assistant to native ownership in Hearth. It must not introduce Home Assistant concepts or dependencies into the core and is removed after migration.
_Avoid_: Compatibility layer, foundational integration

**Household**:
The single home automation environment administered by one Hearth installation.
_Avoid_: Site, tenant

**Device**:
A physical or virtual thing represented in Hearth that groups related entities.
_Avoid_: Accessory, node

**Entity**:
One independently addressable state or control point belonging to a device. State reads and commands target entities. An event-source entity is the exception: it carries no State and no Operations and instead reports named Entity Events.
_Avoid_: Device capability, endpoint

**Entity availability**:
The current assessment of whether an Entity can be reached through its owning Adapter, distinct from Entity enablement and State freshness. It is `unknown` without a current explicit report, `available` only while the owner is healthy and reports it available, and `unavailable` when the owner is unhealthy or reports it unavailable.
_Avoid_: Entity health, Device health

**Entity enablement**:
Whether an Entity participates in normal control and State projection. An enabled Entity accepts valid Commands and Observations. Disabling immediately rejects new Commands, while Commands already requested or accepted retain their normal lifecycle; only Observations linked to those active Commands may still update State and satisfy them. A disabled event-source Entity rejects Entity Events with `entity_disabled` and has no Command-linked exception. A disabled Entity retains its canonical identity, Binding, history, and last accepted State, while other incoming Observations do not update State or satisfy Commands. The management API and owning Adapter may each explicitly enable or disable an Entity, with the last accepted change taking effect. Registration may choose a newly created Entity's initial enablement, which defaults to enabled; subsequent registration reconciles identity and descriptors without changing existing enablement. Disablement is reversible and distinct from temporary unavailability or removal from the household.
_Avoid_: Retirement, availability

**Entity support**:
An Entity's type-specific statement of its supported State space and Operations. An Operation is supported exactly when it is present in Entity support; support may change without changing the Entity's identity or the meaning of active Commands. An event-source Entity's support instead states its supported Entity Event names, and only a listed name is a supported name; an Entity Event whose name is not listed is rejected as `unsupported_event`.
_Avoid_: Constraints, capability list

**Entity Event**:
One named occurrence an Adapter durably reports for an Entity, identified by its event ID so redelivery of one ID is the same event while a new ID is a new occurrence even when the name repeats. Acceptance means the report passed Core's processing-time rules, not that it proves physical truth or causation. An Entity Event carries no State, Command link, arbitrary payload, or claimed source time, and recording one changes no State, Commands, bindings, enablement, health, or availability. Distinct from the outbound `enumaction.trigger` Operation, which requests a device action rather than reporting one.
_Avoid_: State change, Trigger, Observation

**Operation**:
A named command capability within an Entity type. The type defines its support shape, valid parameters, deadline, and outcome policy — observed with an outcome-matching rule, or dispatched without one; invoking a currently supported Operation creates a Command.
_Avoid_: Command, service call

**Observation**:
A fresh value report Hearth durably receives from an Adapter, identified once despite redelivery and carrying Hearth receive time, Adapter acquisition time, and optional upstream change time. Its retained record includes the normalized value when accepted or rejection metadata without the value when rejected; neither the report nor its acceptance proves physical truth or causation.
_Avoid_: Physical confirmation, proof of causation

**State**:
The current value Hearth has accepted for an entity. It is the latest first-seen valid Observation from the owning adapter by core-assigned receive order; a same-value Observation advances its evidence and timestamps, and State is not a requested or desired value.
_Avoid_: Desired state, target

**Command**:
A request to change one controllable entity before a deadline. For observed operations, its outcome is satisfied only after a fresh post-dispatch observation linked to that Command matches the requested value; dispatch or acceptance alone is not satisfaction, and an unhealthy owning Adapter causes failure rather than deferred delivery, while an unavailable Entity with a healthy owner is still attempted. A dispatched operation on a stateless entity instead terminates as `dispatched` once acceptance is durably recorded; it requests no observation and stores no state. Commands for one entity may overlap, each with an independent outcome; a satisfied outcome may be immediately superseded by another observation. The core durably records each attempt and outcome for history, but the record is not executable work: unfinished attempts become interrupted after restart and are never replayed.
_Avoid_: Action, service call, queued job

**Device Fact**:
One Core-verified statement published after the devices transaction establishing an accepted Observation or accepted Entity Event commits, so it has exactly two sources: accepted Observation evidence and accepted Entity Events. It reaches only live Core NATS subscribers and is never acknowledged, retried, replayed or stored by Hearth. A missing fact proves nothing about the underlying activity: the durable record and HTTP read API remain authoritative, and a fact reports what Core recorded, not physical truth. Command status transitions, including startup interruption, publish no fact: Command lifecycle remains authoritative in durable HTTP/SQLite history, and Observations are not its substitute.
_Avoid_: Entity Event, Observation, Command, Command lifecycle, event stream, event sourcing, change log

**Automation**:
A named definition containing one or more Triggers and an ordered sequence of Steps. Any Trigger can initiate execution; enablement governs automatic execution, not explicit manual invocation.
_Avoid_: Rule, workflow, scene

**Trigger**:
One identified, configured reason for automatically executing an Automation. Manual invocation starts a Run without a Trigger occurrence; an Entity Operation named `trigger` is a separate device-control concept.
_Avoid_: Command, invocation

**Step**:
One Entity Operation request in an Automation's ordered sequence. Attempting a Step creates a Command only if execution-time validation and durable creation succeed; the Step is the definition, not the Command attempt or its outcome.
_Avoid_: Action, Command

**Run**:
One recorded execution of an Automation using a snapshot of its definition, started automatically or manually. A successful Run means its Commands reached their Operations' required outcomes, not that all physical effects were confirmed.
_Avoid_: Command, occurrence

**Occurrence**:
One scheduled instant at which one or more of an Automation's Triggers match, recording all matching Triggers together. An Occurrence may start one Run or be skipped without executing Steps.
_Avoid_: Run, timer

**Canonical ID**:
An immutable Hearth-assigned identity for a device or entity that remains stable when names, external identifiers, or owning adapters change.
_Avoid_: Name, external ID
