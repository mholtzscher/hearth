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

**Adapter presence**:
Hearth's current evidence that an Adapter instance is running, established by a renewable lease. Presence is either online or offline and does not imply that the Adapter can communicate with its external system.
_Avoid_: Upstream status, Entity availability, NATS connection

**Upstream status**:
An Adapter instance's report of whether it can communicate with its configured external system. Upstream status is separate from Adapter presence so Hearth can distinguish an absent Adapter from a running Adapter whose external system is disconnected or rejects authentication.
_Avoid_: Adapter presence, Entity availability

**Bridge**:
A permanently supported Adapter that represents objects managed by an external system as Hearth Devices and Entities. The external system remains responsible for those objects' configuration and lifecycle, while Hearth assigns their canonical identities.
_Avoid_: Migration adapter, native adapter

**Binding**:
The durable association between an adapter's external object and its canonical Hearth Device and Entities. An adapter-scoped stable binding key preserves the association when an external identifier changes; ambiguous identity conflicts require explicit reconciliation.
_Avoid_: Discovery result, entity name

**Migration adapter**:
A disposable Adapter that keeps a household operational while devices move from an external system to native ownership in Hearth. It must not introduce external-system concepts or dependencies into the core and is removed after migration.
_Avoid_: Compatibility layer, foundational integration

**Household**:
The single home automation environment administered by one Hearth installation.
_Avoid_: Site, tenant

**Device**:
A physical or virtual thing represented in Hearth that groups related entities.
_Avoid_: Accessory, node

**Entity**:
One independently addressable state or control point belonging to a device. State reads and commands target entities.
_Avoid_: Device capability, endpoint

**Availability**:
An active Entity's current ability to provide fresh observations and, when controllable, accept supported Operations. An unavailable Entity retains its last State but rejects new Commands. Availability may reflect Adapter presence, Upstream status, or the external object's own status.
_Avoid_: State, Adapter presence, Upstream status

**Retired**:
The lifecycle of a Device or Entity that is no longer actively represented by its owning Adapter. Retirement reserves canonical identity and history, excludes the resource from active listings, permits direct retrieval, and rejects Commands.
_Avoid_: Unavailable, deleted, missing

**Entity support**:
An Entity's type-specific statement of its supported State space and Operations. An Operation is supported exactly when it is present in Entity support; support may change without changing the Entity's identity or the meaning of active Commands.
_Avoid_: Constraints, capability list

**Operation**:
A named command capability within an Entity type. The type defines its support shape, valid parameters, deadline, and outcome-matching rule; invoking a currently supported Operation creates a Command.
_Avoid_: Command, service call

**Observation**:
A fresh value report Hearth durably receives from an adapter. It records when Hearth received the report, when the adapter acquired the value, and optionally when the upstream source says the value last changed; it does not by itself prove physical truth or causation.
_Avoid_: Physical confirmation, proof of causation

**State**:
The current value Hearth has accepted for an entity. It is the latest first-seen valid Observation from the owning adapter by core-assigned receive order; a same-value Observation advances its evidence and timestamps, and State is not a requested or desired value.
_Avoid_: Desired state, target

**Command**:
A request to change one controllable entity before a deadline. Its outcome is satisfied only after a fresh post-dispatch observation linked to that Command matches the requested value; dispatch or acceptance alone is not satisfaction, and an unavailable owner causes failure rather than deferred delivery. Commands for one entity may overlap, each with an independent outcome; a satisfied outcome may be immediately superseded by another observation. The core durably records each attempt and outcome for history, but the record is not executable work: unfinished attempts become interrupted after restart and are never replayed.
_Avoid_: Action, service call, queued job

**Canonical ID**:
An immutable Hearth-assigned identity for a device or entity that remains stable when names, external identifiers, or owning adapters change.
_Avoid_: Name, external ID
