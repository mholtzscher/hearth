# Automation file-purpose map

## Coverage and boundary check

Paths below are relative to `internal/modules/automations/`. Each existing tracked
file and each new module file in this cleanup has one row, including test support,
generated code, and the retained Rapid failure artifact. Files outside this module
(application assembly, migrations, browser code, and specifications) are not part
of this inventory. This map itself lives in `docs/`.

The inventory contains **332 files and 332 purpose rows**, with no missing, extra,
or duplicate rows. Directory counts are root 156, HTTP 48, MCP 42, private cursor
2, NATS 13, SQLite 64, SQL sources 2, generated SQL Go 4, and historical Rapid
corpus 1.

This is an ownership check, not a proposal for new packages. Domain and service
behavior stay in `automations`; HTTP, MCP, NATS, and SQLite remain adapters. The
private cursor package is shared only by this module's transports. File splits
do not split transactions or change public contracts.

The cleanup merges Run model/state/labels, separates Command and Run runtime,
separates structural and live-reference validation, extracts Step transitions,
and gives branch execution serialization its own codec. Kind-specific wire DTOs
stay with their codecs. Cohesive tests are not split to mirror production files.

Command, Delay, Notification, and Function lifecycle models use
`<kind>_execution_state.go`, alongside `<kind>_execution.go` for runtime and
`<kind>_execution_codec.go` for serialization. Kind-specific definition preparation
stays in `<kind>.go` where needed. Shared definitions and If/Choose decisions keep
their existing owners. See the [Step file-role standard](../CODING_STANDARDS.md#use-consistent-step-file-roles-across-automation-kinds).

## Domain and service production files

| File | Purpose |
| --- | --- |
| `README.md` | Module ownership, validation boundaries, lifecycle, and transaction guide. |
| `admission_cause.go` | Closed manual, Device Fact, held-state, and schedule provenance values. |
| `admission_cause_validation.go` | Validate retained provenance against matched Trigger evidence. |
| `admission_logging.go` | Stable admission, Run-start, Skip, and corrupt-State diagnostics. |
| `admission_results.go` | Automatic and manual admission results and Skip summaries. |
| `automation-definition.schema.json` | Canonical v2 public definition JSON schema. |
| `automation-history.schema.json` | Canonical v2 public history and execution JSON schema. |
| `branch_decision.go` | Branch decision model, labels, and intrinsic evidence-shape checks. |
| `branch_decision_codec.go` | Strict retained If/Choose decision DTOs and JSON conversion. |
| `branch_decision_validation.go` | Validate branch evidence against prepared snapshots and Trigger matches. |
| `branch_evaluation.go` | Evaluate prepared If/Choose roots and preserve evaluated prefixes. |
| `branch_execution.go` | Traverse selected sequences, persist decisions, and stop branch execution. |
| `branch_execution_codec.go` | Encode/decode branch execution payloads and check envelope consistency. |
| `branch_result.go` | Closed If/Choose selected, fallback, unknown, and error result families. |
| `branching.go` | Recursive Step types, bounded tree preparation, and stable Command-leaf traversal. |
| `command_execution.go` | Reserve, dispatch, reconcile, and complete Commands; record pre-reservation drain. |
| `command_execution_codec.go` | Command wire DTO and strict lifecycle/reservation serialization. |
| `command_execution_state.go` | Closed Command states, reservations, success evidence, and validation. |
| `comparison.go` | JSON-pointer lookup, comparison validation, and exact JSON-value comparisons. |
| `condition_evidence.go` | Known/unknown State and Trigger-match evidence; derive leaf results. |
| `condition_evidence_validation.go` | Validate retained Condition evaluation and leaf evidence shape. |
| `condition_summary.go` | Closed materialized Condition explanation summaries and derived labels. |
| `conditions.go` | Condition trees, three-valued results, and closed admission decisions. |
| `conditions_codec.go` | Strict Condition-tree wire DTOs and recursive JSON conversion. |
| `conditions_decision.go` | Turn a Condition evaluation into an admission decision and Skip reason. |
| `conditions_decision_codec.go` | Full admission decision/evaluation serialization and validation. |
| `conditions_decision_evidence_codec.go` | Compact decision evidence encoding and reconstruction with retained Conditions. |
| `conditions_evaluation.go` | Pure, full-tree three-valued Condition evaluation over supplied State. |
| `conditions_snapshot.go` | Read coherent Condition State through Devices; build the empty snapshot. |
| `conditions_validation.go` | Normalize bounded Condition trees and collect required Entity identities. |
| `definition.go` | Definition, Step, record, and listing models with closed Step bodies. |
| `definition_codec.go` | Definition schema access, bounded strict decoding, encoding, and matched Triggers. |
| `definition_labels.go` | Derive Step, Trigger, and Condition kinds from concrete bodies. |
| `definition_management.go` | Service create/read/list/replace/delete use cases and definition diagnostics. |
| `definition_reference_validation.go` | Save-time live Devices, destination, and Function checks across all arms. |
| `definition_validation.go` | Pure normalization, owned copies, bounds, canonical bytes, and structural errors. |
| `definition_wire.go` | Concrete definition Step/Trigger DTOs and strict discriminated JSON conversion. |
| `delay.go` | Delay definition preparation and snapshot duration lookup. |
| `delay_execution.go` | Native elapsed waiting with durable start/completion and interruption gates. |
| `delay_execution_codec.go` | Delay wire DTO and lifecycle serialization. |
| `delay_execution_state.go` | Closed Delay lifecycle states, validation, and derived completion. |
| `dependencies.go` | Devices/Notifications/Functions seams and service dependency defaults. |
| `errors.go` | Stable domain errors and typed snapshot-coverage/Condition-blocked errors. |
| `fact.go` | Observation/Entity Event Facts, integrity validation, and owned cloning. |
| `fact_labels.go` | Derive Fact identity, family, time, and admission source labels. |
| `fact_processing.go` | Automatic Fact receipt, snapshot preparation, atomic admission, and worker dispatch. |
| `function.go` | Function Step preparation, contract reference checks, and snapshot lookup. |
| `function_execution.go` | Reserve and invoke once; reconcile authoritative Invocation evidence. |
| `function_execution_codec.go` | Function wire DTOs and outcome-preserving lifecycle serialization. |
| `function_execution_state.go` | Closed Function lifecycle/failure evidence, validation, and derived completion. |
| `held_state.go` | Hold candidates/evidence, duration bounds, and held-State matching. |
| `held_state_processing.go` | Process due holds with bounded admission and Condition snapshot preparation. |
| `history.go` | Service history reads and bounded retention sweeps. |
| `history_model.go` | Run-or-Skip detail, summary families, and history listing parameters. |
| `history_schema.go` | Embed and expose the canonical public history schema. |
| `ids.go` | Generate and parse Automation-owned typed identities. |
| `json_codec.go` | Shared strict JSON decoding and canonical value helpers. |
| `manual_runs.go` | Manual admission, optional Condition bypass, and worker launch. |
| `notification.go` | Normalize static notification Steps using Notifications-owned message rules. |
| `notification_execution.go` | Reserve notification links, Send once, and reconcile authoritative Attempts. |
| `notification_execution_codec.go` | Notification wire DTO and lifecycle/outcome serialization. |
| `notification_execution_state.go` | Closed notification lifecycle/failure evidence, validation, and snapshot evidence lookup. |
| `pagination.go` | Shared generic pages and bounded page-size policy. |
| `repository.go` | Definition and runtime persistence capability interfaces. |
| `run.go` | Run types, closed states/outcomes, completion validation, snapshots, and derived labels. |
| `run_execution.go` | Run orchestration/completion, failure tokens, persistence timeout, and scoped fault cleanup. |
| `schedule.go` | Cron Trigger and sampled schedule tick models. |
| `schedule_matching.go` | Compile cron expressions and match prepared schedules in household time. |
| `schedule_processing.go` | Schedule activation and current-minute admission with bounded snapshot preparation. |
| `service.go` | Service construction, admission/execution gates, drain, and recovery entry points. |
| `skip.go` | Skip identity, reason, snapshot, and provenance model. |
| `snapshot_steps.go` | Resolve Step identities in unchanged prepared immutable snapshots. |
| `step_execution.go` | Closed reached/completion evidence, canonical execution details, and derived properties. |
| `step_execution_codec.go` | Shared execution envelope, kind dispatch, public projection, and strict JSON helpers. |
| `step_execution_trace.go` | Validate sparse selected-path order, terminal consistency, and successful completeness. |
| `step_execution_transitions.go` | Validate/apply initial and terminal evidence while preserving reservations. |
| `trigger.go` | Closed Trigger families and intrinsic Trigger validation. |
| `trigger_matching.go` | Match validated Device Facts and retain owned matched Trigger snapshots. |

## Domain/service tests and support

Tests stay with the behavior they protect. `integration` files use real boundaries;
helper/fixture/assertion files supply support rather than separate runtime owners.

| File | Purpose |
| --- | --- |
| `admission_drain_helpers_test.go` | Blocking admission repository fixtures. |
| `admission_drain_integration_test.go` | Drain joins in-flight manual, Fact, and fan-out admissions. |
| `admission_logging_helpers_test.go` | Blocked logging and drain coordination fixtures. |
| `admission_logging_integration_test.go` | Blocked admission diagnostics do not prevent drain completion. |
| `branch_decision_test.go` | Decision shape, strict codec, immutable snapshot, and exact evidence contracts. |
| `branch_evaluation_test.go` | Prepared branch errors and evaluated-prefix preservation. |
| `branch_execution_helpers_test.go` | Branch builders and persistence-fault/malformed-Run doubles. |
| `branch_execution_integration_test.go` | Selected routing, fresh reads, unknowns, persistence gates, faults, and drain. |
| `branch_test_helpers_test.go` | Typed branch-result and evidence mutation helpers. |
| `branching_conditions_test.go` | Trigger Conditions consume the immutable matched set. |
| `branching_definition_integration_test.go` | Tree and normalized-size limits across definition boundaries. |
| `branching_definition_test.go` | Recursive definition wire shape, leaf order, bounds, cycles, and references. |
| `comparison_test.go` | Comparison semantics, JSON pointers, invalid input, and integer oracle. |
| `conditions_admission_integration_test.go` | Snapshot scope, eligibility, bypass, corruption, edits, and drain. |
| `conditions_admission_test.go` | Admission decision matrix and Condition fixtures/doubles. |
| `conditions_decision_codec_test.go` | Decision wire variants, exact nulls, strictness, and snapshot retention. |
| `conditions_decision_evidence_codec_test.go` | Compact admission evidence and malformed-configuration rejection. |
| `conditions_definition_test.go` | Condition definition wire shape, bounds, omission, and cycles. |
| `conditions_evaluation_test.go` | Three-valued truth tables, traversal, State evidence, and unknown reasons. |
| `conditions_validation_test.go` | Typed-tree validation, owned copies, and Entity collection. |
| `definition_codec_test.go` | Strict definition JSON, normalization, schema, bounds, and round trips. |
| `definition_management_helpers_test.go` | Devices validation double for definition service tests. |
| `definition_management_integration_test.go` | Service reference validation and definition-management boundaries. |
| `definition_test_helpers_test.go` | Typed Entity identity builder. |
| `definition_validation_test.go` | Typed definition normalization, ownership, invalid bodies, and reference checks. |
| `delay_definition_integration_test.go` | Service rejects invalid typed delays before writing. |
| `delay_definition_test.go` | Delay duration/wire bounds, owned payloads, and stable Command positions. |
| `delay_execution_helpers_test.go` | Delay write faults, synchronization, and retained-state assertions. |
| `delay_execution_integration_test.go` | Delay timing, selected traversal, busy/drain, snapshots, faults, and recovery. |
| `execution_assertions_test.go` | Cross-kind retained execution lookup/assertions. |
| `execution_helpers_test.go` | Terminal Command evidence builder. |
| `execution_integration_test.go` | Sequential Commands, first-failure stop, ownership reconciliation, and faults. |
| `execution_state_contract_test.go` | Explicit cross-kind lifecycle wire and timestamp fixtures. |
| `executor_fault_helpers_test.go` | Inject runtime persistence faults and assert scoped cleanup. |
| `executor_fault_integration_test.go` | Executor write faults stop only the affected Run. |
| `export_test.go` | Test-only worker-join access for external-package tests. |
| `fact_model_test.go` | Reject invalid Fact value representations. |
| `fact_processing_integration_test.go` | Fact fan-out, deduplication, freshness, busy, and malformed input. |
| `fact_processing_validation_test.go` | Fact validation precedes repository/dependency reads. |
| `function_definition_integration_test.go` | Validate Function references in all arms and preserve operational errors. |
| `function_definition_test.go` | Function definition exact round trips and structural bounds. |
| `function_evidence_test.go` | Function outcome distinctions and contradictory evidence rejection. |
| `function_execution_helpers_test.go` | Scripted Function validation/invocation/reconciliation seam. |
| `function_execution_integration_test.go` | Function outcomes, selected dispatch, clock correction, faults, and shutdown. |
| `held_state_clock_test.go` | Sample held admission time after State reads without moving cutoff. |
| `held_state_processing_helpers_test.go` | Capture held-state startup cutoff. |
| `held_state_processing_integration_test.go` | Construction establishes the held-state startup cutoff. |
| `held_state_test.go` | Held Trigger wire validation and immediate-vs-held matching. |
| `history_integration_test.go` | Retention cutoff preserves active Runs and receipts. |
| `history_schema_test.go` | Public history schema accepts/rejects evidence variants. |
| `history_test.go` | Retention policy, batching, cancellation, and diagnostic behavior. |
| `lifecycle_integration_test.go` | Service admission, drain, and startup recovery lifecycle. |
| `logging_helpers_test.go` | Diagnostic capture and logging scenario support. |
| `logging_integration_test.go` | Stable, bounded Automation diagnostics across runtime scenarios. |
| `manual_runs_integration_test.go` | Manual admission, disabled definitions, distinct invocations, and busy behavior. |
| `notification_attempt_fault_integration_test.go` | Attempt write faults and identity collisions never adopt unrelated evidence. |
| `notification_definition_integration_test.go` | Destination reference errors and unavailable-vs-missing semantics. |
| `notification_definition_test.go` | Notification wire/message bounds, side-effect limits, and Command positions. |
| `notification_execution_helpers_test.go` | Override Attempt reads and Send results for reconciliation tests. |
| `notification_execution_integration_test.go` | Notification outcomes, selected dispatch, unverifiable evidence, and independent pruning. |
| `notification_integration_helpers_test.go` | Real Notifications service/runtime fixture assembly. |
| `notification_lifecycle_integration_test.go` | Scoped notification persistence faults and no-resend crash recovery. |
| `notification_snapshot_integration_test.go` | Admitted notification snapshots and busy slots survive concurrent edits. |
| `notification_test_helpers_test.go` | Sender function, definition, and retained notification Run helpers. |
| `previous_comparisons_integration_test.go` | Unmatched transitions create neither receipts nor history. |
| `previous_comparisons_test.go` | Previous/current comparison semantics, independent bounds, and null handling. |
| `run_model_test.go` | Run completion outcomes and initially empty sparse executions. |
| `runtime_integration_helpers_test.go` | Real SQLite repository and runtime Service assembly. |
| `runtime_test_helper_test.go` | Scripted Devices, State snapshots, and runtime coordination. |
| `schedule_matching_test.go` | Cron clock/DST, prepared definitions, grouping, and invalid inputs. |
| `schedule_processing_helpers_test.go` | Scheduled definition builders and admission repository probes. |
| `schedule_processing_integration_test.go` | Current-minute admission, rollover, retries, bounded workload, and drain. |
| `step_execution_codec_test.go` | Cross-kind codec variants, reservations, strictness, and branch outcomes. |
| `step_execution_schema_test.go` | Public lifecycle/branch schema and sequence bounds. |
| `step_execution_test.go` | Delay transition behavior and envelope disagreement rejection. |
| `step_execution_trace_test.go` | Selected-path completeness, sparse ordering, and public typed evidence. |
| `step_trace_performance_integration_test.go` | Opt-in bounded execution workload and SQL/transaction measurements. |
| `variant_test_helpers_test.go` | Typed history/provenance/state projections used by tests. |
| `testdata/rapid/TestAutomationConditionRapidProperties/TestAutomationConditionRapidProperties-20260916155550-2195902.fail` | Retained historical Rapid failure corpus; not a current maintained property-test entry point. |

## HTTP transport

| File | Purpose |
| --- | --- |
| `httpapi/automation_contracts.go` | Compose and relocate canonical output schemas for HTTP publication. |
| `httpapi/automation_models.go` | Definition/record response models using canonical definition encoding. |
| `httpapi/conditions.go` | Encode Condition decision response bodies. |
| `httpapi/create_automation.go` | Register and handle definition creation. |
| `httpapi/definition_codec.go` | Decode HTTP definition bodies through the canonical codec. |
| `httpapi/delete_automation.go` | Register and handle revision-checked deletion. |
| `httpapi/errors.go` | Map domain errors into safe structured HTTP problems. |
| `httpapi/get_automation.go` | Register and handle definition detail reads. |
| `httpapi/get_automation_history_entry.go` | Register and handle history detail reads. |
| `httpapi/handler.go` | HTTP-facing service seam and handler dependencies. |
| `httpapi/lifecycle.go` | Concrete HTTP Run lifecycle response variants. |
| `httpapi/list_automation_history.go` | Register and handle paginated history summaries. |
| `httpapi/list_automations.go` | Register and handle paginated definition listing. |
| `httpapi/provenance.go` | HTTP Cause and Device Fact response DTOs. |
| `httpapi/register.go` | Assemble Automation HTTP routes. |
| `httpapi/replace_automation.go` | Register and handle revision-checked replacement. |
| `httpapi/run_mapping.go` | Map Run/Skip detail and summary values into HTTP responses. |
| `httpapi/run_models.go` | HTTP history, execution, and manual-Run receipt models. |
| `httpapi/schema_publication.go` | Publish the canonical definition schema with its resource identity intact. |
| `httpapi/start_automation_run.go` | Strict bounded manual-Run request decoding and operation handling. |
| `httpapi/branching_contract_helpers_test.go` | Exact nested branch document and published-schema assertions. |
| `httpapi/branching_contract_integration_test.go` | Branch HTTP round trips, evidence, rejection, and schema discovery. |
| `httpapi/conditions_helpers_test.go` | Condition request, State, problem, and history helpers. |
| `httpapi/conditions_integration_test.go` | Condition DTOs, bypass/blocking, body strictness, and safe failures. |
| `httpapi/cron_fixture_test.go` | Cron definition/history assertions and no-write rejection helpers. |
| `httpapi/cron_integration_test.go` | Cron HTTP round trips, schedule history, and invalid definitions. |
| `httpapi/delay_contract_helpers_test.go` | Delay schema, request, and completed-Run assertions. |
| `httpapi/delay_contract_integration_test.go` | Delay input/discovery, completed history, busy, and snapshots. |
| `httpapi/function_contract_integration_test.go` | Function evidence/discovery and delay-only execution shape. |
| `httpapi/held_state_mapping_test.go` | Held-state provenance response mapping. |
| `httpapi/lifecycle_mapping_test.go` | HTTP lifecycle mapping and invalid state rejection. |
| `httpapi/manual_run_body_test.go` | Manual request decoding without infrastructure. |
| `httpapi/notification_contract_helpers_test.go` | Notification definition/history response helpers. |
| `httpapi/notification_contract_integration_test.go` | Notification HTTP definition and retained evidence contracts. |
| `httpapi/observation_transition_http_helpers_test.go` | Previous/current Observation request and history assertions. |
| `httpapi/observation_transition_http_integration_test.go` | Transition evidence survives HTTP definition/history boundaries. |
| `httpapi/openapi_helpers_test.go` | Published OpenAPI schema compilation and assertions. |
| `httpapi/openapi_integration_test.go` | Discoverable HTTP operations and valid published contracts. |
| `httpapi/pagination_scenarios_helpers_test.go` | Definition/history pagination scenario builders. |
| `httpapi/pagination_scenarios_integration_test.go` | HTTP page traversal and invalid cursor behavior. |
| `httpapi/previous_value_mapping_test.go` | Distinguish absent and JSON-null previous values in HTTP output. |
| `httpapi/register_helpers_test.go` | HTTP registration test setup. |
| `httpapi/register_integration_test.go` | Registered route behavior and transport error contracts. |
| `httpapi/scenario_fixture_test.go` | HTTP scenario runner and operation-contract fixtures. |
| `httpapi/schema_fixture_test.go` | HTTP schema-validation fixtures. |
| `httpapi/sqlite_helpers_test.go` | Real SQLite-backed HTTP Service setup. |
| `httpapi/test_helpers_test.go` | HTTP Devices double and shared request/response helpers. |
| `httpapi/wire_read_fixture_test.go` | Test-only response readers, independent of production DTOs. |

## MCP transport

| File | Purpose |
| --- | --- |
| `mcpapi/automation_arguments.go` | Strict MCP scalar argument parsing and normalization. |
| `mcpapi/automation_contracts.go` | MCP record/page output contract construction. |
| `mcpapi/automation_models.go` | MCP definition/record output mapping. |
| `mcpapi/create_automation.go` | Register and handle the create tool. |
| `mcpapi/definition_arguments.go` | Decode tool definition arguments through canonical validation. |
| `mcpapi/definition_contracts.go` | MCP definition input schemas. |
| `mcpapi/delete_automation.go` | Register and handle revision-checked deletion. |
| `mcpapi/errors.go` | Safe domain-to-MCP error classification. |
| `mcpapi/get_automation.go` | Register and handle definition retrieval. |
| `mcpapi/get_automation_history_entry.go` | History-detail tool, Run/Skip DTOs, provenance mapping, and canonical evidence encoding. |
| `mcpapi/handler.go` | MCP-facing service seam and handler dependencies. |
| `mcpapi/list_automation_history.go` | Register and handle history-summary pagination. |
| `mcpapi/list_automations.go` | Register and handle definition pagination. |
| `mcpapi/pagination_arguments.go` | Strict MCP page-limit decoding. |
| `mcpapi/register.go` | Assemble Automation MCP tools and schema dependencies. |
| `mcpapi/replace_automation.go` | Register and handle revision-checked replacement. |
| `mcpapi/run_contracts.go` | MCP manual-Run receipt lifecycle schema. |
| `mcpapi/run_models.go` | Map returned Run state into MCP receipt variants. |
| `mcpapi/start_automation_run.go` | Register and handle manual Run admission/bypass. |
| `mcpapi/argument_scalars_test.go` | Scalar argument strictness and bounds. |
| `mcpapi/assertion_fixture_test.go` | Exact structured result/error assertions. |
| `mcpapi/automation_contracts_test.go` | MCP output-schema composition contracts. |
| `mcpapi/branching_integration_test.go` | Nested branches, exact definitions, and retained MCP evidence. |
| `mcpapi/conditions_integration_test.go` | Condition admission, bypass, blocking, and evidence contracts. |
| `mcpapi/contract_fixture_test.go` | MCP schema compilation and validation helpers. |
| `mcpapi/cron_fixture_test.go` | Cron definition/history and no-write assertions. |
| `mcpapi/cron_integration_test.go` | Cron tools, invalid definitions, and schedule history. |
| `mcpapi/delay_integration_test.go` | Delay tool discovery, input, execution, and history contracts. |
| `mcpapi/errors_test.go` | MCP error mapping and safe messages. |
| `mcpapi/function_contract_integration_test.go` | Function tool discovery and real retained history evidence. |
| `mcpapi/function_history_contract_test.go` | Function history wire/schema variants without infrastructure. |
| `mcpapi/get_automation_history_entry_test.go` | History detail handler output and error behavior. |
| `mcpapi/mcp_helpers_test.go` | MCP client/server invocation helpers. |
| `mcpapi/mcp_integration_test.go` | Registered Automation tool journeys. |
| `mcpapi/mcp_precision_integration_test.go` | Exact raw JSON numbers and nulls survive MCP output. |
| `mcpapi/notification_fixture_test.go` | Notification Attempt and history assertions. |
| `mcpapi/notification_integration_test.go` | Notification definitions and direct history contracts. |
| `mcpapi/pagination_integration_test.go` | Definition/history page traversal and invalid cursors. |
| `mcpapi/run_models_test.go` | Returned lifecycle and registration-enforced receipt schema. |
| `mcpapi/service_fixture_test.go` | Scripted Devices and definition/runtime fixtures. |
| `mcpapi/sqlite_fixture_test.go` | Real SQLite-backed MCP Service setup. |
| `mcpapi/wire_read_fixture_test.go` | Test-only MCP response readers. |

## Private transport cursor

| File | Purpose |
| --- | --- |
| `internal/apicursor/cursor.go` | Encode/decode module-private definition and history keyset cursors. |
| `internal/apicursor/cursor_test.go` | Cursor round trips, validation, and malformed input rejection. |

## NATS transport

| File | Purpose |
| --- | --- |
| `nats/consumer.go` | Consume Device Facts; durable admission, acknowledgement, retries, and terminal handling. |
| `nats/device_fact_mapping.go` | Strict wire-envelope validation and domain Fact mapping with stable rejection codes. |
| `nats/resources.go` | Provision/validate the durable Device Fact consumer configuration. |
| `nats/broker_helpers_test.go` | Real broker, publish, and consumer coordination fixtures. |
| `nats/consumer_disposition_test.go` | Message disposition recording double and acknowledgement assertions. |
| `nats/consumer_integration_test.go` | Broker delivery, restart floor, redelivery, malformed input, and drain. |
| `nats/consumer_termination_integration_test.go` | Deleted broker consumer closes Automation admission. |
| `nats/consumer_termination_test.go` | Unexpected termination vs intentional drain admission-gate policy. |
| `nats/consumer_test.go` | Service seam conformance and Fact assertions. |
| `nats/device_fact_mapping_test.go` | Exact wire mapping, predecessor nulls, and deterministic rejection. |
| `nats/resources_integration_test.go` | Consumer creation, idempotence, and existing-configuration checks. |
| `nats/resources_test.go` | Pure consumer configuration and stream-name validation. |
| `nats/test_support_test.go` | Synthetic Fact messages, receiver double, and contract validator fixtures. |

## SQLite production, SQL, and generated files

| File | Purpose |
| --- | --- |
| `sqlite/admission.go` | Atomic Fact/manual planning and persistence of Runs, Skips, receipts, and decisions. |
| `sqlite/condition_summary.go` | Decode and validate materialized Condition summary columns. |
| `sqlite/definition_mapping.go` | Convert stored current definitions into validated domain records. |
| `sqlite/definitions.go` | Revision-checked definition CRUD and keyset listing. |
| `sqlite/execution.go` | Run completion and atomic scoped/startup child-before-parent interruption. |
| `sqlite/fact_mapping.go` | Persist/reconstruct Cause and Fact evidence, including predecessor presence. |
| `sqlite/held_state.go` | Atomic hold cursor updates, due eligibility, admission, and cancellation. |
| `sqlite/history.go` | Detail/summary reads and bounded retention/revision collection. |
| `sqlite/history_mapping.go` | Decode retained Run/Skip details and validate summary projections. |
| `sqlite/history_summary_row.go` | Shared typed view of summary-only query columns. |
| `sqlite/lifecycle_mapping.go` | Reconstruct Run states and reject contradictory retained lifecycle columns. |
| `sqlite/null_mapping.go` | Convert optional strings to/from nullable SQL values. |
| `sqlite/repository.go` | Repository construction and transaction orchestration. |
| `sqlite/schedule.go` | Atomic schedule activation, sampled-minute admission, and durable watermark. |
| `sqlite/schedule_history.go` | Validate retained schedule provenance, Triggers, and decisions. |
| `sqlite/step_executions.go` | Atomic Step appends/completions, targeted control reads, and retained execution decoding. |
| `sqlite/time_mapping.go` | Encode/decode required and nullable Automation timestamps. |
| `sqlite/trigger_mapping.go` | Encode/decode retained matched Trigger identity lists. |
| `sqlite/dbqueries/automations.sql` | sqlc query source for definitions, admissions, history, holds, schedules, and retention. |
| `sqlite/dbqueries/step_executions.sql` | sqlc query source for unified executions and targeted lifecycle/control reads. |
| `sqlite/dbsqlc/automations.sql.go` | Generated typed Automation queries; regenerate, do not hand-edit. |
| `sqlite/dbsqlc/db.go` | Generated database/transaction query wrapper. |
| `sqlite/dbsqlc/models.go` | Generated schema row models, including shared database tables. |
| `sqlite/dbsqlc/step_executions.sql.go` | Generated typed unified execution queries. |

## SQLite tests and support

| File | Purpose |
| --- | --- |
| `sqlite/admission_cause_regression_integration_test.go` | Reject Fact evidence contradicting matched Trigger provenance. |
| `sqlite/admission_definition_filtering_integration_test.go` | Filter disabled definitions before decoding; reject invalid enablement. |
| `sqlite/automation_repository_integration_test.go` | CRUD revisions, keyset ordering, invalid definitions, and schema invariants. |
| `sqlite/branch_decisions_integration_test.go` | Sparse immutable branch evidence, valid appends, atomic failure, and corruption. |
| `sqlite/branch_migration_integration_test.go` | Historical branch migration preservation and constraints. |
| `sqlite/command_kind_size_migration_integration_test.go` | Maximum-size Command upgrades and parent-rebuild cascade preservation. |
| `sqlite/command_run_fixture_test.go` | Complete stored Command Runs for persistence tests. |
| `sqlite/command_run_invariants_integration_test.go` | Command evidence gates Run completion and selected-path history. |
| `sqlite/condition_decision_helpers_test.go` | Condition leaves and State snapshot builders. |
| `sqlite/condition_decision_test.go` | Selected JSON null remains distinct retained evidence. |
| `sqlite/condition_summary_integration_test.go` | Validate summary columns without full decision decoding. |
| `sqlite/conditions_admission_integration_test.go` | Transactional eligibility, snapshot coverage, Condition decisions, and rollback. |
| `sqlite/conditions_migration_integration_test.go` | Historical Condition/provenance constraints and summary independence. |
| `sqlite/definition_revisions_fixture_test.go` | Migration setup and Automation/other-module reset fixtures. |
| `sqlite/definition_revisions_integration_test.go` | Shared immutable revisions, edits/deletion/restart, and reference constraints. |
| `sqlite/definition_revisions_reset_integration_test.go` | Revision cutover resets only Automation data and rolls back on failure. |
| `sqlite/definition_revisions_storage_integration_test.go` | Bounded revision-sharing storage comparison. |
| `sqlite/delay_migration_integration_test.go` | Historical Delay migration preservation and constraints. |
| `sqlite/delays_integration_test.go` | Delay transitions, snapshots, retention, atomic interruption, rollback, and corruption. |
| `sqlite/fact_mapping_test.go` | Absent vs JSON-null predecessor mapping. |
| `sqlite/function_boundaries_integration_test.go` | Function cross-kind reservations, recovery, retention, corruption, and Run guards. |
| `sqlite/function_migration_integration_test.go` | Historical Function migration preservation and evidence constraints. |
| `sqlite/functions_integration_test.go` | Function starts/completions, targeted reads, active guards, and atomic recovery. |
| `sqlite/held_state_admission_integration_test.go` | Hold receive-order, current-State eligibility, due Conditions, and startup reset. |
| `sqlite/held_state_persistence_integration_test.go` | Hold cleanup/cascades and retained held-state history. |
| `sqlite/history_listing_benchmark_integration_test.go` | Summary listing benchmark and captured query/payload measurements. |
| `sqlite/history_pagination_integration_test.go` | Exclusive time/identity keysets survive missing or pruned cursor rows. |
| `sqlite/history_retention_integration_test.go` | Eligible retention batches preserve active and surviving evidence. |
| `sqlite/history_retention_migration_integration_test.go` | Historical retention migration preserves data and schema. |
| `sqlite/history_summary_projection_integration_test.go` | Summary-only queries preserve values and reject contradictory provenance. |
| `sqlite/history_test_fixtures_test.go` | Retained history, child evidence, cutoff, and pagination seed data. |
| `sqlite/legacy_definition_migration_integration_test.go` | Legacy Command-kind upgrades, nested definitions, and size-failure rollback. |
| `sqlite/lifecycle_regression_integration_test.go` | Backward wall-clock evidence and failed-Command reservation invariants. |
| `sqlite/lifecycle_variants_integration_test.go` | Pre-reservation interruption and partial-reservation corruption. |
| `sqlite/migration_preservation_fixture_test.go` | Snapshot historical metadata and cascading evidence for migration checks. |
| `sqlite/notification_migration_integration_test.go` | Historical Notification migration preservation and constraints. |
| `sqlite/notifications_integration_test.go` | Notification transitions, rollback, corruption, and logical reservation links. |
| `sqlite/observation_transition_history_integration_test.go` | Persist predecessor evidence on both Run and Skip history. |
| `sqlite/persistence_test_helpers_test.go` | Migrated databases, typed identities, stored revisions, and corruption fixtures. |
| `sqlite/run_evidence_integration_test.go` | Active evidence prevents concurrent execution of other kinds. |
| `sqlite/run_interruption_integration_test.go` | Atomic, scoped, idempotent interruption and startup reason validation. |
| `sqlite/schedule_integration_test.go` | Watermarks, current-minute eligibility, grouped admission, DST, restart, and rollback. |
| `sqlite/schedule_migration_integration_test.go` | Schedule migration preservation, safe downgrade refusal, and provenance validation. |
| `sqlite/step_execution_reset_integration_test.go` | Unified execution cutover isolation, rollback, restart, cascades, and constraints. |
| `sqlite/unified_test_helpers_test.go` | Record/complete/lookup helpers for canonical cross-kind executions. |
| `sqlite/variant_test_helpers_test.go` | Typed Run/Skip/Cause/lifecycle projections for persistence assertions. |

## Validation checklist

- Every module file has exactly one purpose row; inventory includes tracked files
  still present and intended new files, not arbitrary untracked artifacts.
- Run model/state/labels have one owner; Run runtime is separate from Command runtime.
- Structural definition preparation has no live dependency calls.
- Step types/projections, transitions, trace validation, and serialization have
  separate responsibilities within the same domain package.
- Each per-kind codec owns its wire DTO; shared envelope dispatch remains shared.
- No transport or SQLite dependency is introduced into the domain package.
- Public schemas, query sources/generated code, tests, and transaction boundaries
  are unchanged by this file-only cleanup.
