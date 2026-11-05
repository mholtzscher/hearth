package dbload

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	fixtureAdapterFactStride = 3
	fixtureEventFactSlot     = 2
)

// insertHealth seeds Adapter and Entity health history, the current and
// effective Entity availability heads, and idempotent availability receipts.
func insertHealth(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		transitionInsert, err := prepareStatement(ctx, tx, `
INSERT INTO health_transitions (
  resource_kind, adapter_id, entity_id, runtime_id, status, source, reason_code,
  source_observed_at, observed_at, source_health_revision, source_health_update_id
) VALUES ('entity', ?, ?, ?, ?, 'entity_report', ?, ?, ?, NULL, NULL)`)
		if err != nil {
			return err
		}
		defer func() { _ = transitionInsert.Close() }()
		currentInsert, err := prepareStatement(ctx, tx, `
INSERT INTO entity_availability_current (
  entity_id, adapter_id, runtime_id, status, reason_code, source_observed_at,
  evidence_at, current_since, latest_transition_receive_order, health_revision
) VALUES (?, ?, ?, 'available', NULL, ?, ?, ?, ?, 1)`)
		if err != nil {
			return err
		}
		defer func() { _ = currentInsert.Close() }()
		effectiveInsert, err := prepareStatement(ctx, tx, `
INSERT INTO entity_availability_effective (
  entity_id, status, source, reason_code, current_since, evidence_at,
  source_observed_at, latest_transition_receive_order
) VALUES (?, 'available', 'entity_report', NULL, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = effectiveInsert.Close() }()
		for entityIndex := range plan.entities {
			if err = insertEntityAvailability(
				ctx, plan, entityIndex, transitionInsert, currentInsert, effectiveInsert,
			); err != nil {
				return err
			}
		}
		return insertAvailabilityReceipts(ctx, plan, tx)
	})
}

func insertEntityAvailability(
	ctx context.Context,
	plan fixturePlan,
	entityIndex int,
	transitionInsert, currentInsert, effectiveInsert *sql.Stmt,
) error {
	deviceIndex := entityIndex / entitiesPerDevice
	adapterIndex := deviceIndex / plan.devicesPerAdapter
	adapterID := adapterIDFor(adapterIndex)
	runtimeID := runtimeIDFor(adapterIndex)
	entityID := fixtureID("ent_", fixtureIndex(entityIndex))
	for transition := range plan.entityTransitions {
		status, reason := "available", any(nil)
		if transition < plan.entityTransitions-1 {
			status, reason = "unavailable", "entity.offline"
		}
		observed := fixtureStamp(-time.Duration(plan.entityTransitions-transition) * time.Minute)
		if _, err := transitionInsert.ExecContext(
			ctx, adapterID, entityID, runtimeID, status, reason,
			fixtureStamp(-time.Duration(plan.entityTransitions-transition)*time.Minute-time.Second),
			observed,
		); err != nil {
			return fmt.Errorf("insert entity health transition: %w", err)
		}
	}
	latest := int64(plan.adapters*plan.adapterTransitions +
		entityIndex*plan.entityTransitions + plan.entityTransitions)
	if _, err := currentInsert.ExecContext(
		ctx, entityID, adapterID, runtimeID, fixtureStamp(-2*time.Minute),
		fixtureStamp(-time.Minute), fixtureStamp(-24*time.Hour), latest,
	); err != nil {
		return fmt.Errorf("insert current availability: %w", err)
	}
	if _, err := effectiveInsert.ExecContext(
		ctx, entityID, fixtureStamp(-24*time.Hour), fixtureStamp(-time.Minute),
		fixtureStamp(-2*time.Minute), latest,
	); err != nil {
		return fmt.Errorf("insert effective availability: %w", err)
	}
	return nil
}

func insertAvailabilityReceipts(ctx context.Context, plan fixturePlan, tx *sql.Tx) error {
	insert, err := prepareStatement(ctx, tx, `
INSERT INTO entity_availability_receipts (request_id, fingerprint, reported_at) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	for receipt := range plan.adapters * 2 {
		if _, err = insert.ExecContext(
			ctx, fixtureID("avl_", fixtureIndex(receipt)), fixtureDigest(fixtureIndex(receipt)),
			fixtureStamp(-time.Duration(receipt+1)*time.Minute),
		); err != nil {
			return fmt.Errorf("insert availability receipt: %w", err)
		}
	}
	return nil
}

// insertFacts seeds the pending device-fact backlog. Outbox rows are work to
// publish, not retained history.
func insertFacts(ctx context.Context, database *sql.DB, plan fixturePlan) error {
	return withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		insert, err := prepareStatement(ctx, tx, `
INSERT INTO device_facts_outbox (
  fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
  traceparent, tracestate, value_json, adapter_received_at, source_updated_at,
  observed_at, reported_at, received_at, recorded_at, previous_value_json
) VALUES (?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, NULL, ?, ?, ?, ?, NULL)`)
		if err != nil {
			return err
		}
		defer func() { _ = insert.Close() }()
		for adapterIndex := range plan.adapters {
			if err = insertAdapterFacts(ctx, plan, adapterIndex, insert); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertAdapterFacts(
	ctx context.Context,
	plan fixturePlan,
	adapterIndex int,
	insert *sql.Stmt,
) error {
	firstDevice := adapterIndex * plan.devicesPerAdapter
	for _, role := range []int{rolePower, roleTemperature} {
		position := plan.stateHistory - 1
		factIndex := fixtureIndex(adapterIndex*fixtureAdapterFactStride + role)
		if _, err := insert.ExecContext(
			ctx, fixtureID("fct_", factIndex), "observation", entityIDFor(firstDevice, role),
			"applied", observationIDFor(firstDevice, role, position, plan.stateHistory),
			fixtureID("cor_", fixtureUnknownEntityBase+factIndex), fixtureStamp(-time.Minute),
			string(fixtureStateValue(role, position)), fixtureStamp(-time.Minute),
			fixtureStamp(-time.Minute), nil, nil, nil,
		); err != nil {
			return fmt.Errorf("insert observation fact: %w", err)
		}
	}
	eventIndex := firstDevice*plan.entityEvents + 0
	eventAt := fixtureStamp(-time.Duration(plan.entityEvents) * time.Minute)
	if _, err := insert.ExecContext(
		ctx,
		fixtureID("fct_", fixtureIndex(adapterIndex*fixtureAdapterFactStride+fixtureEventFactSlot)),
		"entity-event",
		entityIDFor(firstDevice, roleEvent),
		fixtureEventName(0),
		fixtureID(
			"evt_",
			fixtureIndex(eventIndex),
		),
		fixtureID(
			"cor_",
			fixtureUnknownEntityBase+fixtureIndex(adapterIndex*fixtureAdapterFactStride+fixtureEventFactSlot),
		),
		fixtureStamp(-time.Minute),
		nil,
		nil,
		nil,
		eventAt,
		fixtureStamp(-time.Duration(plan.entityEvents)*time.Minute+time.Second),
		fixtureStamp(-time.Duration(plan.entityEvents)*time.Minute+2*time.Second),
	); err != nil {
		return fmt.Errorf("insert entity-event fact: %w", err)
	}
	return nil
}
