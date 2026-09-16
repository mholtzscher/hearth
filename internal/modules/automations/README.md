# Automations module ownership

Automations is one product module. Its packages separate domain behavior from
HTTP, NATS, and SQLite implementation details; Triggers, Runs, and Steps remain
parts of that module rather than independent packages or services.

## Package boundaries

| Package | Owns |
| --- | --- |
| `automations` | Definitions, Trigger matching, Condition definitions and three-valued evaluation, admission and execution use cases, retention policy, domain types, repository interfaces, and Run worker lifecycle |
| `automations/api` | HTTP operations, transport models, cursors, and error mapping |
| `automations/nats` | Device Fact wire decoding, consumer resources, and acknowledgement policy |
| `automations/sqlite` | Atomic persistence operations, query selection, row mapping, query sources, and generated query code |

The HTTP, NATS, and SQLite packages depend on `automations`, not the reverse.
Application assembly constructs the SQLite repository and injects it through the
existing `AutomationRepository` interface. `AutomationDefinitionRepository`
remains the narrower definition-management capability; there is no parallel
store aggregate or generic transaction framework.

The devices-facing `AutomationDevices` seam stays in the domain. SQLite never
executes device Commands, publishes NATS messages, or starts Run workers. State
evidence reaches the module only through that seam: automations never read
devices tables directly or maintain a second State projection.

## Conditions

An Automation definition may carry one optional, bounded Condition tree whose
nodes compare selected current Entity State or compose with `all`, `any`, and
`not`. Conditions are optional per definition and preserve omission. Evaluation
is pure and three-valued over an immutable State snapshot; only a true root
admits, and every node's evidence is recorded so history explains a decision
after State or the definition changes. A decision is validated once at write
time for envelope coherence, and the history table's CHECK constraints are the
remaining integrity guard, so reads decode retained decisions and trust them
rather than re-deriving the evidence. The transports, codecs, and persistence
shapes for definitions, evaluations, and decisions live with this module.

Conditions never initiate execution and are evaluated once per admission.
Devices remains the authority for State: the Service reads one coherent batch
through the devices seam outside the admission transaction, retries a bounded
number of times when the transaction reports incomplete coverage, and never
merges samples from different reads. Transaction-loaded current definitions are
the admission authority, so a definition edit between attempts is handled by the
next read rather than cached.

Automatic and manual admissions share the protocol. Duplicate, stale, and busy
precedence precedes any State read; false or unknown Conditions commit an
explainable Skip and start no workers. Manual invocation may explicitly bypass
Conditions, which is recorded in the admitted Run and bypasses nothing else. The
Service turns a committed manual Condition Skip into a typed blocked error only
after the transaction commits, so the required history is never rolled back.

See [the Conditions specification](../../../specs/automation-conditions.md) for
the implementation contract and [the operator guide](../../../docs/automation-conditions.md)
for the definition, manual, and history examples.

## Transaction boundaries

File boundaries organize related code; they do not divide existing transactions.

- Fact admission loads current definitions, matches Triggers, checks durable
  duplicate receipts, and commits every matching Run or Skip with its receipt,
  initial Steps, and Condition decision in one transaction. Stale-Fact
  classification precedes busy classification, and only an eligible match reads
  State. A missing-evidence pass writes nothing and returns the complete required
  Entity set so the Service can replace the whole snapshot before retrying.
- Manual admission checks the current definition and active Run before recording
  one immutable Run snapshot, its Condition decision, and its initial Steps.
  Disabled Automations still permit manual invocation.
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
