//nolint:goconst,mnd // Row literals mirror the persisted SQL vocabulary and explicit fixture dimensions.
package dbload

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// fixtureTargets are the stable identities the manifest exposes for workloads.
type fixtureTargets struct {
	AdapterID      string
	RuntimeID      string
	DeviceID       string
	EntityID       string
	EventEntityID  string
	AutomationID   string
	ConversationID string
	DestinationID  string
	FunctionName   string
	DashboardID    string
}

func withFixtureTx(ctx context.Context, database *sql.DB, action func(*sql.Tx) error) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fixture transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if actionErr := action(transaction); actionErr != nil {
		return actionErr
	}
	if commitErr := transaction.Commit(); commitErr != nil {
		return fmt.Errorf("commit fixture transaction: %w", commitErr)
	}
	return nil
}

func prepareStatement(ctx context.Context, transaction *sql.Tx, statement string) (*sql.Stmt, error) {
	stmt, err := transaction.PrepareContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("prepare fixture insert: %w", err)
	}
	return stmt, nil
}

func entityIDFor(deviceIndex, role int) string {
	return fixtureID("ent_", fixtureIndex(deviceIndex*entitiesPerDevice+role))
}

func deviceIDFor(deviceIndex int) string {
	return fixtureID("dev_", fixtureIndex(deviceIndex))
}

func adapterIDFor(adapterIndex int) string {
	return fmt.Sprintf("fixture-adapter-%d", adapterIndex)
}

func runtimeIDFor(adapterIndex int) string {
	return fixtureID("run_", fixtureIndex(adapterIndex))
}

func stateOrdinalFor(deviceIndex, role int) int {
	return deviceIndex*stateRolesPerDevice + role
}

func observationIDFor(deviceIndex, role, position, history int) string {
	return fixtureID("obs_", fixtureIndex(stateOrdinalFor(deviceIndex, role)*history+position))
}

func entityTemperatureID(deviceIndex int) string {
	return entityIDFor(deviceIndex, roleTemperature)
}

// insertAdapters seeds instances, an active runtime, one ended runtime, health
// transitions, the publication head, the pending outbox, and the projection.
func insertAdapters(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	snapshotAt := fixtureClock.Add(-time.Minute)
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		instanceInsert, err := prepareStatement(ctx, tx, `
INSERT INTO adapter_instances (
  adapter_id, health_status, health_reason_code, health_source,
  health_since, health_evidence_at, health_source_observed_at
) VALUES (?, 'healthy', NULL, 'adapter', ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = instanceInsert.Close() }()
		runtimeInsert, err := prepareStatement(ctx, tx, `
INSERT INTO adapter_runtimes (
  runtime_id, adapter_id, software_name, software_version, claimed_at,
  last_heartbeat_at, lease_expires_at, ended_at, end_reason
) VALUES (?, ?, 'fixture-adapter', '1.0.0', ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = runtimeInsert.Close() }()
		instanceUpdate, err := prepareStatement(ctx, tx, `
UPDATE adapter_instances SET active_runtime_id = ?, health_runtime_id = ? WHERE adapter_id = ?`)
		if err != nil {
			return err
		}
		defer func() { _ = instanceUpdate.Close() }()
		transitionInsert, err := prepareStatement(ctx, tx, `
INSERT INTO health_transitions (
  resource_kind, adapter_id, entity_id, runtime_id, status, source, reason_code,
  source_observed_at, observed_at, source_health_revision, source_health_update_id
) VALUES ('adapter', ?, NULL, ?, ?, 'core', ?, NULL, ?, NULL, NULL)`)
		if err != nil {
			return err
		}
		defer func() { _ = transitionInsert.Close() }()
		headInsert, err := prepareStatement(ctx, tx, `
INSERT INTO adapter_health_heads (adapter_id, revision, snapshot_json, traceparent, tracestate)
VALUES (?, 1, ?, '', '')`)
		if err != nil {
			return err
		}
		defer func() { _ = headInsert.Close() }()
		outboxInsert, err := prepareStatement(ctx, tx, `
INSERT INTO adapter_health_outbox (
  message_id, adapter_id, revision, snapshot_json, enqueued_at, correlation_id,
  causation_id, traceparent, tracestate
) VALUES (?, ?, 1, ?, ?, ?, NULL, '', '')`)
		if err != nil {
			return err
		}
		defer func() { _ = outboxInsert.Close() }()
		projectionInsert, err := prepareStatement(ctx, tx, `
INSERT INTO device_adapter_health_projection (
  adapter_id, revision, snapshot_json, last_message_id, applied_at
) VALUES (?, 1, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = projectionInsert.Close() }()
		if err = insertAdapterRows(
			ctx, plan, snapshotAt,
			instanceInsert, runtimeInsert, instanceUpdate, transitionInsert,
			headInsert, outboxInsert, projectionInsert,
		); err != nil {
			return err
		}
		return nil
	})
}

func insertAdapterRows(
	ctx context.Context,
	plan fixturePlan,
	snapshotAt time.Time,
	instanceInsert, runtimeInsert, instanceUpdate, transitionInsert, headInsert,
	outboxInsert, projectionInsert *sql.Stmt,
) error {
	for adapterIndex := range plan.adapters {
		adapterID := adapterIDFor(adapterIndex)
		runtimeID := runtimeIDFor(adapterIndex)
		if _, err := instanceInsert.ExecContext(
			ctx, adapterID, fixtureStamp(-2*time.Minute), fixtureStamp(-time.Minute), fixtureStamp(-2*time.Minute),
		); err != nil {
			return fmt.Errorf("insert adapter instance: %w", err)
		}
		if _, err := runtimeInsert.ExecContext(
			ctx, runtimeID, adapterID, fixtureStamp(-2*time.Hour), fixtureStamp(-time.Minute),
			fixtureStamp(time.Minute), nil, nil,
		); err != nil {
			return fmt.Errorf("insert active runtime: %w", err)
		}
		endedRuntime := fixtureID("run_", fixtureIndex(plan.adapters+adapterIndex))
		if _, err := runtimeInsert.ExecContext(
			ctx, endedRuntime, adapterID, fixtureStamp(-4*time.Hour), fixtureStamp(-3*time.Hour),
			fixtureStamp(-2*time.Hour), fixtureStamp(-2*time.Hour), "stopped",
		); err != nil {
			return fmt.Errorf("insert ended runtime: %w", err)
		}
		if _, err := instanceUpdate.ExecContext(ctx, runtimeID, runtimeID, adapterID); err != nil {
			return fmt.Errorf("bind active runtime: %w", err)
		}
		if err := insertAdapterTransitions(ctx, plan, adapterID, runtimeID, transitionInsert); err != nil {
			return err
		}
		snapshot, err := fixtureHealthSnapshot(adapterID, runtimeID, 1, snapshotAt)
		if err != nil {
			return err
		}
		if _, err = headInsert.ExecContext(ctx, adapterID, string(snapshot)); err != nil {
			return fmt.Errorf("insert adapter health head: %w", err)
		}
		messageID := fixtureID("ahu_", fixtureIndex(adapterIndex))
		correlationID := fixtureID("cor_", fixtureIndex(adapterIndex))
		if _, err = outboxInsert.ExecContext(
			ctx, messageID, adapterID, string(snapshot), fixtureStamp(-time.Minute), correlationID,
		); err != nil {
			return fmt.Errorf("insert adapter health outbox: %w", err)
		}
		if _, err = projectionInsert.ExecContext(
			ctx, adapterID, string(snapshot), messageID, fixtureStamp(-time.Minute),
		); err != nil {
			return fmt.Errorf("insert adapter health projection: %w", err)
		}
	}
	return nil
}

func insertAdapterTransitions(
	ctx context.Context,
	plan fixturePlan,
	adapterID, runtimeID string,
	insert *sql.Stmt,
) error {
	for transition := range plan.adapterTransitions {
		status, reason := "healthy", any(nil)
		if transition%3 == 1 {
			status, reason = "unknown", "hearth.adapter.offline"
		}
		if _, err := insert.ExecContext(
			ctx, adapterID, runtimeID, status, reason,
			fixtureStamp(-time.Duration(plan.adapterTransitions-transition)*time.Minute),
		); err != nil {
			return fmt.Errorf("insert adapter transition: %w", err)
		}
	}
	return nil
}

// insertDevices seeds Devices, their Entities, and the Adapter binding/mapping
// rows the Entity read projection joins.
func insertDevices(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		deviceInsert, err := prepareStatement(ctx, tx, `
INSERT INTO devices (id, kind, name, created_at, updated_at) VALUES (?, 'fixture', ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = deviceInsert.Close() }()
		entityInsert, err := prepareStatement(ctx, tx, `
INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at, enabled)
VALUES (?, ?, ?, ?, ?, ?, ?, 1)`)
		if err != nil {
			return err
		}
		defer func() { _ = entityInsert.Close() }()
		bindingInsert, err := prepareStatement(ctx, tx, `
INSERT INTO adapter_bindings (adapter_id, binding_key, device_id, external_device_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = bindingInsert.Close() }()
		mappingInsert, err := prepareStatement(ctx, tx, `
INSERT INTO adapter_entity_mappings (
  adapter_id, binding_key, entity_key, entity_id, external_entity_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = mappingInsert.Close() }()
		for deviceIndex := range plan.devices {
			if err = insertDeviceRows(
				ctx, plan, deviceIndex, deviceInsert, entityInsert, bindingInsert, mappingInsert,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertDeviceRows(
	ctx context.Context,
	plan fixturePlan,
	deviceIndex int,
	deviceInsert, entityInsert, bindingInsert, mappingInsert *sql.Stmt,
) error {
	adapterIndex := deviceIndex / plan.devicesPerAdapter
	adapterID := adapterIDFor(adapterIndex)
	deviceID := deviceIDFor(deviceIndex)
	if _, err := deviceInsert.ExecContext(
		ctx, deviceID, fmt.Sprintf("Fixture Device %d", deviceIndex),
		fixtureStamp(-24*time.Hour), fixtureStamp(-24*time.Hour),
	); err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	bindingKey := fmt.Sprintf("binding-%d", deviceIndex)
	externalDevice := fmt.Sprintf("external-device-%d", deviceIndex)
	if _, err := bindingInsert.ExecContext(
		ctx, adapterID, bindingKey, deviceID, externalDevice,
		fixtureStamp(-24*time.Hour), fixtureStamp(-24*time.Hour),
	); err != nil {
		return fmt.Errorf("insert adapter binding: %w", err)
	}
	for role := range entitiesPerDevice {
		entityID := entityIDFor(deviceIndex, role)
		if _, err := entityInsert.ExecContext(
			ctx, entityID, deviceID, fmt.Sprintf("%s %d", fixtureEntityName(role), deviceIndex),
			fixtureEntityType(role), string(fixtureSupportJSON(role)),
			fixtureStamp(-24*time.Hour), fixtureStamp(-24*time.Hour),
		); err != nil {
			return fmt.Errorf("insert entity: %w", err)
		}
		entityKey := fmt.Sprintf("entity-%d-%d", deviceIndex, role)
		if _, err := mappingInsert.ExecContext(
			ctx, adapterID, bindingKey, entityKey, entityID,
			fmt.Sprintf("external-%d-%d", deviceIndex, role),
			fixtureStamp(-24*time.Hour), fixtureStamp(-24*time.Hour),
		); err != nil {
			return fmt.Errorf("insert adapter entity mapping: %w", err)
		}
	}
	return nil
}

// insertObservations seeds retained State evidence, current State anchors, and
// the deliberately unknown-Entity rejections that have no foreign key.
func insertObservations(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		observationInsert, err := prepareStatement(ctx, tx, `
INSERT INTO observations (
  observation_id, adapter_id, runtime_id, entity_id, disposition, rejection_code,
  state_value_json, adapter_received_at, source_updated_at, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = observationInsert.Close() }()
		stateInsert, err := prepareStatement(ctx, tx, `
INSERT INTO entity_states (
  entity_id, observation_id, value_json, adapter_received_at, source_updated_at,
  observed_at, receive_order
) VALUES (?, ?, ?, ?, NULL, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stateInsert.Close() }()
		if err = insertStateObservations(ctx, plan, observationInsert, stateInsert); err != nil {
			return err
		}
		return insertUnknownRejections(ctx, plan, observationInsert)
	})
}

func insertStateObservations(
	ctx context.Context,
	plan fixturePlan,
	observationInsert, stateInsert *sql.Stmt,
) error {
	for deviceIndex := range plan.devices {
		adapterID := adapterIDFor(deviceIndex / plan.devicesPerAdapter)
		runtimeID := runtimeIDFor(deviceIndex / plan.devicesPerAdapter)
		for role := range stateRolesPerDevice {
			entityID := entityIDFor(deviceIndex, role)
			for position := range plan.stateHistory {
				disposition, rejection, value := observationDisposition(position, role, plan.stateHistory)
				observedAt := fixtureStamp(-time.Duration(plan.stateHistory-position) * time.Minute)
				var stateValue any
				if value != nil {
					stateValue = string(value)
				}
				if _, err := observationInsert.ExecContext(
					ctx, observationIDFor(deviceIndex, role, position, plan.stateHistory), adapterID,
					runtimeID, entityID, disposition, rejection, stateValue, observedAt, observedAt,
				); err != nil {
					return fmt.Errorf("insert observation: %w", err)
				}
			}
			anchorPosition := plan.stateHistory - 1
			order := stateOrdinalFor(deviceIndex, role)*plan.stateHistory + plan.stateHistory
			if _, err := stateInsert.ExecContext(
				ctx, entityID, observationIDFor(deviceIndex, role, anchorPosition, plan.stateHistory),
				string(fixtureStateValue(role, anchorPosition)),
				fixtureStamp(-time.Minute), fixtureStamp(-time.Minute), order,
			); err != nil {
				return fmt.Errorf("insert current state: %w", err)
			}
		}
	}
	return nil
}

func observationDisposition(position, role, history int) (string, any, json.RawMessage) {
	switch position {
	case history / 5:
		return "unchanged", nil, fixtureStateValue(role, position)
	case history * 2 / 5:
		return "rejected", "wrong_adapter", nil
	case history * 3 / 5:
		return "rejected", "invalid_value", nil
	default:
		return "applied", nil, fixtureStateValue(role, position)
	}
}

func insertUnknownRejections(ctx context.Context, plan fixturePlan, insert *sql.Stmt) error {
	for rejection := range unknownRejectedObservations {
		adapterIndex := rejection % plan.adapters
		entityIndex := fixtureUnknownEntityBase + fixtureIndex(rejection)
		if _, err := insert.ExecContext(
			ctx, fixtureID("obs_", entityIndex), adapterIDFor(adapterIndex),
			runtimeIDFor(adapterIndex), fixtureID("ent_", entityIndex), "rejected",
			"unknown_entity", nil, fixtureStamp(-time.Duration(rejection+1)*time.Minute),
			fixtureStamp(-time.Duration(rejection+1)*time.Minute),
		); err != nil {
			return fmt.Errorf("insert unknown-Entity rejection: %w", err)
		}
	}
	return nil
}

// insertCommandsAndEvents seeds command lifecycle evidence for power Entities
// and retained events for event-source Entities.
func insertCommandsAndEvents(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		commandInsert, err := prepareStatement(ctx, tx, commandInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = commandInsert.Close() }()
		eventInsert, err := prepareStatement(ctx, tx, eventInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = eventInsert.Close() }()
		for deviceIndex := range plan.devices {
			if err = insertDeviceCommands(ctx, plan, deviceIndex, commandInsert); err != nil {
				return err
			}
			if err = insertDeviceEvents(ctx, plan, deviceIndex, eventInsert); err != nil {
				return err
			}
		}
		return nil
	})
}

const commandInsertSQL = `
INSERT INTO commands (
  id, entity_id, adapter_id, runtime_id, operation, parameters_json, correlation_id,
  status, requested_at, deadline_at, accepted_at, completed_at, outcome_observation_id,
  failure_code
) VALUES (?, ?, ?, ?, 'set', '{"value":true}', ?, ?, ?, ?, ?, ?, ?, ?)`

func insertDeviceCommands(
	ctx context.Context,
	plan fixturePlan,
	deviceIndex int,
	insert *sql.Stmt,
) error {
	entityID := entityIDFor(deviceIndex, rolePower)
	adapterID := adapterIDFor(deviceIndex / plan.devicesPerAdapter)
	runtimeID := runtimeIDFor(deviceIndex / plan.devicesPerAdapter)
	for command := range plan.commands {
		index := deviceIndex*plan.commands + command
		status := commandStatus(command)
		requested := fixtureStamp(-time.Duration(plan.commands-command) * time.Minute)
		deadline := fixtureStamp(-time.Duration(plan.commands-command)*time.Minute + 30*time.Second)
		accepted, completed, outcome, failure := any(nil), any(nil), any(nil), any(nil)
		if status != "requested" {
			accepted = requested
		}
		if status != "requested" && status != "accepted" {
			completed = fixtureStamp(-time.Duration(plan.commands-command)*time.Minute + 10*time.Second)
		}
		switch status {
		case "satisfied":
			observation := observationIDFor(deviceIndex, rolePower, command/commandStateCount, plan.stateHistory)
			outcome = observation
		case "rejected":
			failure = "upstream_rejected"
		case "interrupted":
			failure = "core_restarted"
		}
		if _, err := insert.ExecContext(
			ctx, fixtureID("cmd_", fixtureIndex(index)), entityID, adapterID, runtimeID,
			fixtureID("cor_", fixtureIndex(index)), status, requested, deadline, accepted, completed,
			outcome, failure,
		); err != nil {
			return fmt.Errorf("insert command: %w", err)
		}
	}
	return nil
}

const commandStateCount = 6

func commandStatus(index int) string {
	switch index % commandStateCount {
	case 0:
		return "requested"
	case 1:
		return "accepted"
	case 2:
		return "satisfied"
	case 3:
		return "rejected"
	case 4:
		return "interrupted"
	default:
		return "dispatched"
	}
}

const eventInsertSQL = `
INSERT INTO entity_events (
  event_id, adapter_id, runtime_id, entity_id, correlation_id, name, fingerprint,
  disposition, rejection_code, emitted_at, received_at, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func fixtureEventName(index int) string {
	switch index % 3 {
	case 0:
		return "single_press"
	case 1:
		return "double_press"
	default:
		return "long_press"
	}
}

func insertDeviceEvents(ctx context.Context, plan fixturePlan, deviceIndex int, insert *sql.Stmt) error {
	entityID := entityIDFor(deviceIndex, roleEvent)
	adapterID := adapterIDFor(deviceIndex / plan.devicesPerAdapter)
	runtimeID := runtimeIDFor(deviceIndex / plan.devicesPerAdapter)
	for event := range plan.entityEvents {
		index := deviceIndex*plan.entityEvents + event
		disposition, rejection := "accepted", any(nil)
		if event == plan.entityEvents-1 && plan.entityEvents > 1 {
			disposition, rejection = "rejected", "unsupported_event"
		}
		emitted := fixtureStamp(-time.Duration(plan.entityEvents-event) * time.Minute)
		if _, err := insert.ExecContext(
			ctx, fixtureID("evt_", fixtureIndex(index)), adapterID, runtimeID, entityID,
			fixtureID("cor_", fixtureIndex(index)), fixtureEventName(event),
			fixtureFingerprint(fixtureIndex(index)), disposition, rejection,
			emitted, fixtureStamp(-time.Duration(plan.entityEvents-event)*time.Minute+time.Second),
			fixtureStamp(-time.Duration(plan.entityEvents-event)*time.Minute+2*time.Second),
		); err != nil {
			return fmt.Errorf("insert entity event: %w", err)
		}
	}
	return nil
}
