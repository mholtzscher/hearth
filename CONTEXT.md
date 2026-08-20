# Hearthd

Hearthd is a home automation system intended to operate independently of Home Assistant while remaining compatible with specialist protocol services.

## Language

**Functional replacement**:
A Hearthd installation that provides all required household behavior with Home Assistant eliminated from operation. It may continue to depend on specialist protocol services.
_Avoid_: Full-stack replacement, Home Assistant parity

**Adapter**:
A process that translates between one configured external system and Hearthd's wire protocol.
_Avoid_: Integration, plugin

**Adapter instance**:
One configured occurrence of an adapter, identified by a stable subject-safe slug within a household.
_Avoid_: Adapter type, process ID

**Binding**:
The durable association between an adapter's external object and its canonical Hearthd Device and Entities. An adapter-scoped stable binding key preserves the association when an external identifier changes; ambiguous identity conflicts require explicit reconciliation.
_Avoid_: Discovery result, entity name

**Migration adapter**:
A disposable adapter that keeps a household operational while devices move from Home Assistant to native ownership in Hearthd. It must not introduce Home Assistant concepts or dependencies into the core and is removed after migration.
_Avoid_: Compatibility layer, foundational integration

**Household**:
The single home automation environment administered by one Hearthd installation.
_Avoid_: Site, tenant

**Device**:
A physical or virtual thing represented in Hearthd that groups related entities.
_Avoid_: Accessory, node

**Entity**:
One independently addressable state or control point belonging to a device. State reads and commands target entities.
_Avoid_: Device capability, endpoint

**Observation**:
A fresh value report obtained by an adapter from an external system or device. It records when the adapter acquired the value, may preserve when the upstream source says the value last changed, and records when Hearthd received it; it does not by itself prove what caused the outcome.
_Avoid_: Physical confirmation, proof of causation

**State**:
The current value Hearthd has accepted for an entity. It is the observation with the latest adapter acquisition time, using receive order to break ties; a newer same-value observation advances its evidence and timestamps, and State is not a requested or desired value.
_Avoid_: Desired state, target

**Command**:
A request to change one controllable entity before a deadline. Its outcome is satisfied only after a fresh post-dispatch observation matches the requested value; dispatch or acceptance alone is not satisfaction, an unavailable owner causes failure rather than deferred delivery, and only one command may be in flight per entity.
_Avoid_: Action, service call, queued job

**Canonical ID**:
An immutable Hearthd-assigned identity for a device or entity that remains stable when names, external identifiers, or owning adapters change.
_Avoid_: Name, external ID
