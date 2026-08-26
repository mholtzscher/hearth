# Multi-Entity Registration - Implementation Spec

- **Status:** Draft
- **Type:** Feature plan
- **Effort:** L
- **Created:** 2026-08-25
- **Revised:** 2026-08-25

## Problem

Hearth models a Device as a group of independently addressable Entities, and the built-in catalog already contains separate `hearth.power/v1` and `hearth.brightness/v1` types. Registration nevertheless accepts exactly one Entity per Binding. A Zigbee2MQTT adapter could therefore register a bulb's power or brightness, but could not attach both to the same canonical Device.

The restriction exists in the registration schemas, devices service, repository input, and the `UNIQUE (adapter_id, binding_key)` constraint on `adapter_entity_mappings`.

## Decision

Allow one registration request to describe 1-64 Entities belonging to one Device and Binding.

Registration remains additive and atomic:

- an existing Entity key preserves its canonical Entity ID;
- a new Entity key creates an Entity under the existing Device while the persisted Device aggregate remains at or below 64 Entities;
- an omitted Entity remains persisted and unchanged;
- an accepted response maps only the submitted descriptors, in request order;
- validation or reconciliation failure rejects the complete request without partial database writes.

The request and response already use plural arrays, so their field names and DTOs do not change. The v1 schemas are updated in place from exactly one item to 1-64. Existing single-Entity adapters remain valid.

The initial migration remains unshipped under the policy established by `unified-entity-support.md`. Rewrite it in place; only disposable local development databases need to be recreated.

## Behavioral Contract

### Validation

- `entities` contains at least 1 and at most 64 descriptors.
- Entity keys and Entity external IDs are each unique within a request.
- Duplicate keys or external IDs are `invalid_descriptor` when the request reaches the domain service.
- The service validates and normalizes every Entity descriptor before generating IDs or calling the repository.
- One invalid descriptor rejects the complete registration.
- A wire-valid additive registration that would exceed 64 persisted Entities for the Device is `invalid_descriptor` and rolls back atomically.
- Normalization operates on a copy and does not mutate adapter input.

The wire schemas reject requests containing 0 or 65 Entities before `Service.Register` runs. Such requests follow existing transport validation behavior rather than producing a registration rejection response. Direct domain calls still classify invalid cardinality as `RegistrationInvalidDescriptor`.

### Reconciliation

For an existing `(adapter_id, binding_key, entity_key)` mapping:

- canonical Device and Entity IDs are reused;
- Entity type remains immutable;
- submitted name and normalized support replace persisted values;
- the external Entity ID may change only when it was unclaimed at the start of the transaction or already belonged to that Entity.

For a new Entity key:

- a new canonical Entity ID is created under the Binding's Device;
- its external Entity ID must have been unclaimed at the start of the transaction.

External Entity IDs cannot transfer between Entity keys in one registration, even if the previous owner is also submitted with a new external ID. Swaps and transfers are `identity_conflict`. A transfer requires one registration that releases the old ID, followed by another that claims it. This keeps reconciliation independent of descriptor order.

External-ID ownership is evaluated against the transaction's pre-write state. The repository must produce the same identity outcome regardless of descriptor order; how it stages reads and writes is internal to the repository.

Omitted Entities receive no writes. A new key with an existing external ID is an identity conflict, not a key rename.

### Response

An accepted `binding.entities` contains exactly one mapping for each submitted descriptor and preserves request order. It is a registration result, not a complete persisted inventory. Reordering descriptors changes response order but not canonical IDs.

Existing permanent rejection codes retain their meanings for wire-valid requests:

- `invalid_descriptor`: request or persisted-aggregate cardinality, duplicate, field, catalog, or support validation failure;
- `immutable_type_change`: an existing Entity key is submitted with a different type;
- `identity_conflict`: a Binding or external ID belongs to another canonical object.

Rejected responses expose no canonical IDs.

### Atomicity and Retry

Domain validation and registration-parameter construction complete before repository access and produce no writes on failure. Device reconciliation and all submitted Entity reconciliation then occur in one serializable SQLite transaction; any repository failure rolls back that transaction.

Transport validation failures happen before registration. Failure to deliver an accepted NATS response can happen after the database commit and does not roll it back. Adapter retry must return the same canonical IDs through ordinary idempotent re-registration.

Under Hearth's current single-process, single-connection SQLite topology, concurrent registrations with identical normalized descriptors must converge on one Device ID and one Entity ID per key. Winner semantics for concurrent non-equivalent mutable descriptor updates are outside this feature.

## Types and Interfaces

The public domain, NATS, and SDK types already use `[]EntityDescriptor` and `[]EntityBinding`. Only the repository parameter becomes plural.

```diff
diff --git a/internal/modules/devices/repository.go b/internal/modules/devices/repository.go
@@
+type RegisterEntityParams struct {
+    EntityID EntityID
+    Entity   EntityDescriptor
+}
+
 type RegisterBindingParams struct {
     AdapterID  string
     BindingKey string
     DeviceID   DeviceID
-    EntityID   EntityID
     Device     DeviceDescriptor
-    Entity     EntityDescriptor
+    Entities   []RegisterEntityParams
     UpdatedAt  time.Time
 }
```

These signatures remain unchanged:

```go
func (service *Service) Register(
    context.Context,
    string,
    Registration,
) (Binding, error)

type RegistrationRepository interface {
    RegisterBinding(context.Context, RegisterBindingParams) (Binding, error)
}

func (session *Session) Register(
    context.Context,
    adapter.Registration,
) (adapter.Binding, error)
```

No HTTP, Observation, Command, typed-facade, or adapter interface changes are required. Those paths continue to address one canonical Entity at a time.

## Contract and Persistence Changes

```diff
diff --git a/contracts/v1/registration-request.schema.json b/contracts/v1/registration-request.schema.json
@@
           "minItems": 1,
-          "maxItems": 1,
+          "maxItems": 64,
```

```diff
diff --git a/contracts/v1/registration-response.schema.json b/contracts/v1/registration-response.schema.json
@@
               "minItems": 1,
-              "maxItems": 1,
+              "maxItems": 64,
```

JSON Schema does not enforce uniqueness by one object property, so duplicate key and external-ID checks remain domain behavior.

```diff
diff --git a/internal/platform/db/migrations/00001_initial.sql b/internal/platform/db/migrations/00001_initial.sql
@@
     PRIMARY KEY (adapter_id, binding_key, entity_key),
-    UNIQUE (adapter_id, binding_key),
     UNIQUE (adapter_id, external_entity_id),
```

The primary key continues to enforce one mapping per Entity key within a Binding. `entity_id UNIQUE` preserves one adapter mapping per canonical Entity, while `(adapter_id, external_entity_id) UNIQUE` preserves adapter-scoped external identity.

Responses contain only submitted Entities, so the feature does not require a complete-Binding inventory query.

## Implementation Shape

1. Change both registration schema bounds to 64.
2. Make `normalizeRegistration` validate cardinality, request-local uniqueness, fields, and support for every copied descriptor.
3. Generate one candidate Entity ID per normalized descriptor.
4. Change `RegisterBindingParams` to carry ordered Entity parameters.
5. In the existing repository transaction, reconcile identity without descriptor-order-dependent outcomes.
6. Reject immutable types and pre-existing external-ID ownership conflicts.
7. Reconcile the Device and submitted Entities, then return mappings in request order.
8. Commit only after every submitted Entity succeeds.
9. Remove the one-mapping-per-Binding database constraint.
10. Update the current architecture constraint and focused tests.

No public interface beyond the repository parameter shape changes.

## Project Layout

```text
contracts/v1/
|-- registration-request.schema.json      # modify - accept 1-64 descriptors
|-- registration-response.schema.json     # modify - return 1-64 mappings
`-- embed_test.go                          # modify - verify schema boundaries
internal/
|-- modules/devices/
|   |-- registration.go                   # modify - normalize all descriptors
|   |-- registration_test.go              # modify - cardinality and uniqueness tests
|   |-- repository.go                     # modify - plural repository input
|   |-- sqlite_repository.go              # modify - preflight and reconcile all Entities
|   |-- sqlite_repository_test.go         # modify - identity and transaction tests
|   `-- nats/registration_test.go          # modify - multi-Entity contract round trip
`-- platform/db/
    `-- migrations/00001_initial.sql       # modify - permit multiple mappings per Binding
docs/
`-- architecture.md                       # modify - state the new registration rules
specs/
`-- multi-entity-registration.md          # modify - own this feature contract
```

The implemented first-light spec and plan remain historical records of their original single-Entity scope. Production SDK types, the Home Assistant adapter, simulator, HTTP API, Entity-type manifests, and generators do not change.

## Deliverables

| Deliverable | Effort | Depends on |
|---|---:|---|
| D1. Contract and domain cardinality | M | - |
| D2. Atomic persistence reconciliation | M | D1 |
| D3. Focused tests, architecture documentation, and clean-checkout verification | M | D1, D2 |

Total effort is **L (1-2 days)**. Persistence reconciliation is the highest-risk work.

## Non-Goals

- Entity retirement or complete-inventory reconciliation
- Changes to Commands, Observations, State modeling, or Device-kind rules
- Adapter discovery or capability mapping
- Device ownership transfer or merge behavior
- Runtime Entity-type registration

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Additive registration leaves stale Entities | Keep omission non-destructive until Hearth has an explicit retirement lifecycle |
| A later Entity failure partially commits earlier changes | Keep all Device and Entity reconciliation in one transaction and test rollback from a later descriptor |
| External-ID moves become descriptor-order dependent | Evaluate ownership against pre-write state and reject same-request transfers and swaps |
| Rewriting the initial migration breaks local data | Limit recreation to disposable local development databases under the existing unshipped-migration policy |

## Trade-offs

| Chose | Over | Reason |
|---|---|---|
| Additive omission | Authoritative replacement | Omission cannot safely imply deletion without retirement semantics |
| Submitted-only response | Complete Binding inventory | Adapters need mappings for submitted descriptors, not stale omitted Entities |
| Reject same-request ID transfers | Atomic swaps and moves | Deterministic identity behavior is more valuable than an unused complex operation |
| 64-Entity Device aggregate limit | Unbounded additive registration | Bounded validation, persistence, and Device-detail work comfortably covers household devices |
| Rewrite migration `00001` | Add migration `00002` | The accepted project policy treats the initial migration as unshipped |

## Acceptance Criteria

- [ ] Wire schemas accept 1 and 64 Entities and reject 0 and 65.
- [ ] Existing single-Entity adapters and tests remain valid.
- [ ] One request registers power and brightness under one canonical Device.
- [ ] The accepted response contains exactly one mapping for each submitted descriptor, in request order.
- [ ] Re-registration and descriptor reordering preserve canonical IDs.
- [ ] Adding brightness to a power-only Binding preserves the Device and power IDs.
- [ ] Additive registration rejects a 65th persisted Entity without changing Device descriptors or mappings.
- [ ] Omitting power later leaves it unchanged and excludes it from the response.
- [ ] Duplicate request keys or external IDs are rejected before repository access.
- [ ] External-ID transfers or swaps between keys are rejected independent of request order.
- [ ] A failure on any submitted Entity rolls back all Device and Entity writes in that transaction.
- [ ] Concurrent identical registrations converge under the current SQLite topology.
- [ ] A lost accepted response followed by retry returns the committed canonical IDs.
- [ ] The rewritten initial migration preserves the remaining identity constraints.
- [ ] The clean-checkout gate passes.

## Success Metrics

- Existing single-Entity adapter behavior requires no production changes.
- A power Entity can gain a brightness sibling without changing either the Device ID or power Entity ID.
- Re-registration, omission, descriptor reordering, and response retry satisfy the acceptance criteria in deterministic tests.
- The repository's race-enabled clean-checkout gate passes without generated-code drift.

## Test Strategy

| Layer | Verification |
|---|---|
| Contract | Generate request and response fixtures at 0, 1, 2, 64, and 65 items |
| Service | Validate all descriptors, bounds, duplicates, support normalization, and input copying with the stub repository |
| Repository | Use migrated SQLite for initial multi-Entity registration, additive registration, aggregate-bound rejection, omission, reordering, external-ID transfer rejection, and rollback on a later-Entity failure |
| Concurrency | Repeat the existing eight-call test with identical two-Entity descriptors under the current database configuration |
| NATS | Round-trip a two-Entity registration and verify ordered response mappings |
| Regression | Run the existing race-enabled full suite and clean-checkout gate |

## Verification

```sh
go test ./contracts/v1 ./internal/modules/devices/...
devenv shell -- sqlc generate
devenv test
```

No Entity-type generator input changes.

## Open Questions

None. Retirement, complete inventory, non-equivalent concurrent updates, and Device-kind generalization are deferred explicitly.
