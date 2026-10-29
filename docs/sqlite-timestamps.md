# SQLite timestamp storage

Core writes timestamp TEXT columns as UTC with exactly nine fractional digits:
`2006-01-02T15:04:05.000000000Z`. This preserves nanoseconds and makes lexical
ordering and equality agree with represented instants. Conversion stays with the
owning module; this is not a shared mapper framework.

## Development cutover for #248

This is a **reset-based development cutover**, not a retained-data migration.
The project has no production database to upgrade. This decision supersedes
#248's original backfill, pre-upgrade cursor, and rollback acceptance requirements.

Before first running this version against an existing development installation:

1. Stop Core and its writers; for the local simulator, use `mise run simulator-stop`.
2. Locate the resolved `sqlite_path`. The `--sqlite-path` flag takes precedence
   over environment and YAML. The default simulator path is
   `.data/simulator-stack/storage/hearthd.db`.
3. Archive the stopped database together with any `-wal` and `-shm` sidecars if
   its evidence is useful. Then explicitly reset that database, or select a new,
   empty SQLite path. **A reset loses all Core SQLite data**, not just timestamps.
4. Start Core to create a fresh database through the existing Goose migrations.
   Re-register Adapters and recreate development configuration stored in SQLite.
   Discard pagination cursors from the old database.

There is no automatic deletion, normalization backfill, schema-version gate, or
historical migration rewrite. Opening an old database still parses legacy values
where its decoder previously accepted them, but does **not** fix mixed-width
ordering. Such reuse is unsupported for this cutover. Malformed values keep the
existing decoder errors; they are not clamped, fabricated, or silently discarded.
Do not run old and new binaries as mixed writers. To return to an older binary,
use its matching archived database or a fresh database, not the new database.
Broker state is not reset by this change; its existing delivery/recovery policy
still applies.

## Column and consumer inventory

Inventory covers application-owned scalar timestamps in the schema through
migration 24. `?` marks SQL-nullable columns. Views expose the underlying column
contract rather than introducing another encoding. "Parsed" means callers decode
to `time.Time`; it does not claim every column participates in time comparison.

### Devices normalization

All rows below now use `formatTime`, directly or through its nullable helpers.
For `formatNullableTime`, zero means NULL. For `nullableTime`, nil means NULL.
`formatSortableTime` delegates to the same encoding for existing lexical paths.
Decoders retain `time.RFC3339Nano` acceptance, including supported numeric offsets;
writers always emit UTC. Nullable semantics are unchanged.

| Table | Columns | Consumers and comparison contract |
| --- | --- | --- |
| `devices`, `entities` | `created_at`, `updated_at` | Registration, descriptor/name/enablement writes; parsed reads. Collections page by identity, not timestamp. |
| `adapter_bindings`, `adapter_entity_mappings` | `created_at`, `updated_at` | Registration/reconciliation metadata; mapping identity pagination. |
| `commands` | `requested_at` (already canonical); `deadline_at`, `accepted_at?`, `completed_at?` (normalize) | Command admission/acceptance/completion/recovery and linked Observation completion. Parsed diagnostic/deadline times; history uses lexical `(requested_at, id)` descending keysets and equality. `commands_entity_requested_idx` and `commands_requested_idx` support those reads. No Command retention cutoff. |
| `observations` | `adapter_received_at`, `source_updated_at?` (normalize); `observed_at` (already canonical) | Parsed evidence; retention uses lexical `observed_at < cutoff` via `observations_observed_at_idx`, protecting the current-State anchor. History indexes and cursors use `receive_order`, not wall time. |
| `entity_states` | `adapter_received_at`, `source_updated_at?`, `observed_at` | Normalize snapshot evidence; parsed snapshot/freshness reads. State advancement is receive-order based, not timestamp based. |
| `entity_availability_receipts` | `reported_at` | Normalize idempotent receipt evidence; lookup/equality guard uses request identity and fingerprint, not timestamp TEXT. |
| `entity_availability_current` | `source_observed_at`, `evidence_at`, `current_since` | Normalize report evidence; eligibility uses runtime/health revision. Parsed times, not lexical freshness arbitration. |
| `entity_availability_effective` | `current_since`, `evidence_at`, `source_observed_at?` | Normalize projected availability evidence; parsed reads. |
| `device_adapter_health_projection` | `applied_at` | Normalize projection application time; snapshot revision controls acceptance, not timestamp TEXT. |
| `health_transitions` (Entity rows) | `source_observed_at?`, `observed_at` | Normalize Entity history; history indexes/page cursors use `receive_order`. Adapters owns the sibling Adapter rows below. |
| `entity_events` | `emitted_at`, `received_at`, `recorded_at` | Already canonical; no change. Retention uses lexical `recorded_at < cutoff` and `(recorded_at, receive_order)` via `entity_events_retention_idx`. History uses receive-order cursors. |
| `device_facts_outbox` | `created_at`, `adapter_received_at?`, `source_updated_at?`, `observed_at?`, `reported_at?`, `received_at?`, `recorded_at?` | Normalize pending fact evidence. Queue order is `enqueue_order`; deletion is by fact identity. Relay parses times and re-encodes its unchanged wire contract, not raw storage bytes. |

The resource writers in `internal/modules/devices/sqlite` cover registration,
metadata/device metadata, Commands, Observations/State, availability, Adapter
health projection, Entity Events, and Device Facts. Query consumers are in its
`dbqueries/` directory, with coherent batch State reads in
`entity_state_snapshot.go`.

### Adapter runtime and health normalization

`internal/modules/adapters/sqlite/time_mapping.go` owns `formatTime` and the
zero-means-NULL `formatNullableTime`. All listed variable-width scalar columns
become canonical; `parseTime` remains RFC3339Nano-compatible.

| Table | Columns | Consumers and comparison contract |
| --- | --- | --- |
| `adapter_instances` | `health_since`, `health_evidence_at`, `health_source_observed_at?` | Parsed current health, fencing and publication evidence. |
| `adapter_runtimes` | `claimed_at`, `last_heartbeat_at?`, `lease_expires_at`, `ended_at?` | Claim/heartbeat/takeover/release/expiry. Expiry **eligibility remains `julianday`-based**; result ordering is lexical `(lease_expires_at, runtime_id)`. The partial lease-expiry index is on `lease_expires_at`. Latest runtime selection is rowid-based. Encoding fixes result ordering, not the precision of SQLite's time conversion. |
| `health_transitions` (Adapter rows) | `source_observed_at?`, `observed_at` | Parsed history; receive-order indexes/cursors, not wall-clock ordering. |
| `adapter_health_outbox` | `enqueued_at` | Parsed publication envelope time; queue uses `enqueue_order`. |

Writers live in `runtime.go` and `health_updates.go`; queries live in
`dbqueries/runtime.sql` and `dbqueries/health_updates.sql`.

### Already-canonical owners

| Owner / tables | Columns | Consumers and comparison contract |
| --- | --- | --- |
| Adapters Functions: `adapter_function_catalogs`, `adapter_function_invocations` | Catalog `registered_at`; Invocation `started_at`, `deadline`, `completed_at?` | `functionTimestamp`; RFC3339Nano reads. Lexical `(started_at, id)` history keysets and `completed_at < cutoff` retention. Adapter/function history and completed-at retention indexes. |
| Notifications: `notification_attempts` | `started_at`, `deadline`, `completed_at?` | `timestamp`; RFC3339Nano reads. Lexical `(started_at, id)` history keysets and `completed_at < cutoff` retention; global/destination history indexes. Destinations have no timestamp columns. |
| Agent: `agent_conversations`, `agent_messages` | `created_at` in both tables | `encodeAgentTimestamp`; RFC3339Nano reads. Lexical newest-activity selection and whole-conversation retention comparisons; created-at and conversation/created-at indexes. Message history itself orders by integer identity. |
| Automations: `automations` | `created_at`, `updated_at` | `encodeAutomationTimestamp`; fixed-width decoder. Collections page by identity. |
| Automations: `automation_schedule_watermarks`, `automation_holds` | Watermark `highwater_at`; hold `started_at?`, `due_at?` | Same fixed-width codec. Lexical high-water MAX, due eligibility/comparison, and `(due_at, automation_id, trigger_id)` partial index. Receive-order guards remain independent. |
| Automations: `automation_history` | `recorded_at`, `fact_emitted_at?`, `run_started_at?`, `run_completed_at?`, `hold_started_at?`, `hold_due_at?` | Same fixed-width codec. Lexical `(recorded_at, id)` history keysets and `recorded_at < cutoff` retention, supported by page/retention indexes. Other times are parsed evidence. |
| Automations: `automation_step_executions` | `started_at`, `completed_at?` | Same fixed-width codec. Steps order by sequence. Instantaneous Step equality constraints remain unchanged; no new chronology constraints. |

Legacy tables replaced by earlier migrations are not active contracts. Goose's
own migration bookkeeping timestamp is tool-owned, not application evidence.

## Intentional representation exceptions

- JSON evidence, Adapter health `snapshot_json`, Function contract fingerprints,
  Entity availability request fingerprints, Agent message JSON, and user values
  are **not** scalar timestamp columns. Preserve their existing serialization.
  Health snapshot JSON deliberately uses RFC3339Nano, including its canonical
  byte/equality contract. Availability fingerprints marshal UTC `time.Time`
  independently of the SQLite helper.
- HTTP, MCP, NATS, and public cursor encodings are unchanged. Command and
  Automation cursors parse an instant before the repository encodes a canonical
  SQL bound; receive-order cursors remain integers. This is not permission to
  reuse cursors across the database reset.
- Do not derive lifecycle order from diagnostic wall times, clamp a backward
  clock correction, or restore the Function completion-after-start constraints
  removed by migration 19. This change affects representation only.

## Verification

The module tests exercise timestamp writers and decoders through unit and
real-SQLite tests. Runtime expiry tests cover whole and fractional seconds and
identity ties. Command tests preserve backward wall-clock corrections.
Observation/State/fact tests check precision, NULLs, and strict retention
boundaries. Existing Command tests
cover same-second pagination and identity ties. Fresh-database tests apply the
unchanged Goose migration set; upgrade/backfill tests are intentionally outside
this reset-based contract.

```sh
GO_PACKAGES='./internal/modules/devices/sqlite ./internal/modules/adapters/sqlite' mise run test -- -tags=integration
mise run validate
```
