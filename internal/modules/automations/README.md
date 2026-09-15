# Automations module ownership

Automations is one product module. Its packages separate domain behavior from
HTTP, NATS, and SQLite implementation details; Triggers, Runs, and Steps remain
parts of that module rather than independent packages or services.

## Package boundaries

| Package | Owns |
| --- | --- |
| `automations` | Definitions, trigger matching, admission and execution use cases, retention policy, domain types, repository interfaces, and Run worker lifecycle |
| `automations/api` | HTTP operations, transport models, cursors, and error mapping |
| `automations/nats` | Device Fact wire decoding, consumer resources, and acknowledgement policy |
| `automations/sqlite` | Atomic persistence operations, query selection, row mapping, query sources, and generated query code |

The HTTP, NATS, and SQLite packages depend on `automations`, not the reverse.
Application assembly constructs the SQLite repository and injects it through the
existing `AutomationRepository` interface. `AutomationDefinitionRepository`
remains the narrower definition-management capability; there is no parallel
store aggregate or generic transaction framework.

The devices-facing `AutomationDevices` seam stays in the domain. SQLite never
executes device Commands, publishes NATS messages, or starts Run workers.

## Transaction boundaries

File boundaries organize related code; they do not divide existing transactions.

- Fact admission loads current definitions, matches Triggers, checks durable
  duplicate receipts, and commits every matching Run or Skip with its receipt
  and initial Steps in one transaction. Stale-Fact classification precedes busy
  classification. Matching uses transaction-loaded definitions, not preliminary
  Service reads.
- Manual admission checks the current definition and active Run before recording
  one immutable Run snapshot and its initial Steps. Disabled Automations still
  permit manual invocation.
- Definition replacement and deletion enforce the expected revision atomically.
- Step and Run writes preserve terminal-outcome checks. Startup interruption
  records interruption; it does not replay execution or infer Command success.
- History pruning leaves running Runs and matched-Fact receipts intact.

Only after successful admission commits does the Service start Run workers.
Consumer acknowledgement follows durable admission, not worker completion.

Pure domain decisions and snapshot construction operate on domain values. SQL
encoding, generated row types, and transaction orchestration stay in SQLite.

## Tests and generation

Persistence tests live beside the SQLite implementation and exercise migrated
real databases through its public API. Domain and orchestration tests remain
with the behavior they protect; external-package Service tests may assemble the
public SQLite repository without adding a production import back to SQLite.

`sqlite/dbqueries` supplies `sqlite/dbsqlc` through root `sqlc.yaml`. Regenerate and
validate with `mise run validate`; do not hand-edit generated query code.
