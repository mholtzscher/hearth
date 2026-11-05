//nolint:goconst,mnd // Notification, function, and dashboard evidence uses explicit persisted vocabulary.
package dbload

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

// insertNotifications seeds registered destinations and retained attempts,
// including one open attempt per destination.
func insertNotifications(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		destinationInsert, err := prepareStatement(ctx, tx, `
INSERT INTO notification_destinations (id, adapter_id, local_key, runtime_id, name)
VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = destinationInsert.Close() }()
		attemptInsert, err := prepareStatement(ctx, tx, attemptInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = attemptInsert.Close() }()
		for destinationIndex := range plan.destinations {
			if err = insertDestination(
				ctx, plan, destinationIndex, destinationInsert, attemptInsert,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

const attemptInsertSQL = `
INSERT INTO notification_attempts (
  id, destination_id, destination_name, adapter_id, local_key, runtime_id,
  title, message, started_at, deadline, completed_at, outcome, reason_code, receipt
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func attemptOutcome(index int) string {
	switch index % 4 {
	case 0:
		return "accepted"
	case 1:
		return "rejected"
	case 2:
		return "not_sent"
	default:
		return "unknown"
	}
}

func insertDestination(
	ctx context.Context,
	plan fixturePlan,
	destinationIndex int,
	destinationInsert, attemptInsert *sql.Stmt,
) error {
	adapterIndex := destinationIndex / destinationsPerAdapter
	adapterID := adapterIDFor(adapterIndex)
	runtimeID := runtimeIDFor(adapterIndex)
	destinationID := fixtureID("ndst_", fixtureIndex(destinationIndex))
	destinationName := fmt.Sprintf("Fixture Destination %d", destinationIndex)
	localKey := fmt.Sprintf("fixture-destination-%d", destinationIndex)
	if _, err := destinationInsert.ExecContext(
		ctx, destinationID, adapterID, localKey, runtimeID, destinationName,
	); err != nil {
		return fmt.Errorf("insert notification destination: %w", err)
	}
	total := plan.terminalAttempts + 1
	for attempt := range total {
		index := destinationIndex*total + attempt
		startedOffset := -time.Duration(total-attempt) * time.Minute
		outcome, reason, receipt, completed := attemptEvidence(attempt, plan.terminalAttempts)
		if _, err := attemptInsert.ExecContext(
			ctx, fixtureID("ntf_", fixtureIndex(index)), destinationID, destinationName,
			adapterID, localKey, runtimeID, "Fixture notification", "Fixture message body",
			fixtureStamp(startedOffset), fixtureStamp(startedOffset+10*time.Second), completed,
			outcome, reason, receipt,
		); err != nil {
			return fmt.Errorf("insert notification attempt: %w", err)
		}
	}
	return nil
}

func attemptEvidence(attempt, terminal int) (string, string, string, any) {
	if attempt >= terminal {
		return "", "", "", nil
	}
	switch attemptOutcome(attempt) {
	case "accepted":
		return "accepted", "", "accepted", fixtureStamp(
			-time.Duration(terminal-attempt)*time.Minute + 2*time.Second,
		)
	case "rejected":
		return "rejected", "provider_rejected", "", fixtureStamp(
			-time.Duration(terminal-attempt)*time.Minute + 2*time.Second,
		)
	case "not_sent":
		return "not_sent", "destination_unavailable", "", fixtureStamp(
			-time.Duration(terminal-attempt)*time.Minute + 2*time.Second,
		)
	default:
		return "unknown", "outcome_timeout", "", fixtureStamp(
			-time.Duration(terminal-attempt)*time.Minute + 2*time.Second,
		)
	}
}

// insertFunctions seeds one validated catalog per Adapter and retained
// invocations with terminal and running evidence.
func insertFunctions(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		catalogInsert, err := prepareStatement(ctx, tx, `
INSERT INTO adapter_function_catalogs (
  adapter_id, catalog_revision, runtime_id, registered_at, functions_json
) VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = catalogInsert.Close() }()
		invocationInsert, err := prepareStatement(ctx, tx, invocationInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = invocationInsert.Close() }()
		for adapterIndex := range plan.adapters {
			if err = insertAdapterFunctions(
				ctx, plan, adapterIndex, catalogInsert, invocationInsert,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

const invocationInsertSQL = `
INSERT INTO adapter_function_invocations (
  id, adapter_id, runtime_id, catalog_revision, function_key, descriptor_json,
  arguments_json, started_at, deadline, status, result_json, reason_code, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func insertAdapterFunctions(
	ctx context.Context,
	plan fixturePlan,
	adapterIndex int,
	catalogInsert, invocationInsert *sql.Stmt,
) error {
	adapterID := adapterIDFor(adapterIndex)
	runtimeID := runtimeIDFor(adapterIndex)
	catalog, catalogJSON, err := fixtureFunctionCatalog(adapterIndex, adapterID, runtimeID)
	if err != nil {
		return err
	}
	if _, err = catalogInsert.ExecContext(
		ctx, adapterID, string(catalog.ID), runtimeID, fixtureStamp(-2*time.Hour), string(catalogJSON),
	); err != nil {
		return fmt.Errorf("insert function catalog: %w", err)
	}
	deviceIndex := adapterIndex * plan.devicesPerAdapter
	arguments, err := json.Marshal(map[string]any{"entity_id": entityIDFor(deviceIndex, rolePower)})
	if err != nil {
		return fmt.Errorf("encode function arguments: %w", err)
	}
	for functionIndex, descriptor := range catalog.Functions {
		if err = insertFunctionInvocations(
			ctx, plan, adapterIndex, catalog.ID, functionIndex, descriptor, arguments, invocationInsert,
		); err != nil {
			return err
		}
	}
	return nil
}

func insertFunctionInvocations(
	ctx context.Context,
	plan fixturePlan,
	adapterIndex int,
	catalogID adapters.CatalogID,
	functionIndex int,
	descriptor adapters.Descriptor,
	arguments []byte,
	insert *sql.Stmt,
) error {
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		return fmt.Errorf("encode function descriptor: %w", err)
	}
	base := adapterIndex * functionsPerCatalog * plan.invocationsPerFunction
	for invocation := range plan.invocationsPerFunction {
		index := base + functionIndex*plan.invocationsPerFunction + invocation
		offset := -time.Duration(plan.invocationsPerFunction-invocation) * time.Minute
		status, result, reason, completed := invocationEvidence(invocation, plan.invocationsPerFunction)
		if _, err = insert.ExecContext(
			ctx, fixtureID("afi_", fixtureIndex(index)), adapterIDFor(adapterIndex),
			runtimeIDFor(adapterIndex), string(catalogID), string(descriptor.Key),
			string(descriptorJSON), string(arguments), fixtureStamp(offset),
			fixtureStamp(offset+time.Second), status, result, reason, completed,
		); err != nil {
			return fmt.Errorf("insert function invocation: %w", err)
		}
	}
	return nil
}

func invocationEvidence(invocation, total int) (string, any, any, any) {
	if invocation >= total-1 {
		return "running", nil, nil, nil
	}
	offset := -time.Duration(total-invocation)*time.Minute + 100*time.Millisecond
	completed := fixtureStamp(offset)
	switch invocation % 4 {
	case 0:
		return "succeeded", `{"ok":true}`, nil, completed
	case 1:
		return "failed", nil, "adapter.error", completed
	case 2:
		return "not_executed", nil, "adapter.unavailable", completed
	default:
		return "unknown", nil, "adapter.timeout", completed
	}
}

// insertDashboards seeds saved dashboards whose definitions pass the production
// view codec and bind to real temperature Entities.
func insertDashboards(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		insert, err := prepareStatement(ctx, tx, `
INSERT INTO dashboards (id, revision, definition_json, created_at, updated_at)
VALUES (?, 1, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = insert.Close() }()
		for dashboard := range plan.dashboards {
			deviceIndex := dashboard % plan.devices
			definition, definitionErr := fixtureDashboardDefinition(
				dashboard, entityTemperatureID(deviceIndex),
			)
			if definitionErr != nil {
				return definitionErr
			}
			if _, err = insert.ExecContext(
				ctx, fixtureID("dash_", fixtureIndex(dashboard)), string(definition),
				fixtureStamp(-24*time.Hour), fixtureStamp(-time.Hour),
			); err != nil {
				return fmt.Errorf("insert dashboard: %w", err)
			}
		}
		return nil
	})
}
