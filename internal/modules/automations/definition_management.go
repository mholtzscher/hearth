package automations

import (
	"context"
	"log/slog"
)

// CreateAutomation validates every current reference and persists one new definition.
func (service *Service) CreateAutomation(
	ctx context.Context,
	definition Definition,
) (Record, error) {
	validated, err := ValidateDefinition(ctx, service.devices, definition)
	if err != nil {
		return Record{}, err
	}
	record, err := service.repository.CreateAutomation(ctx, validated)
	if err != nil {
		return Record{}, err
	}
	service.logDefinition(ctx, "automation.created", record)
	return record, nil
}

// GetAutomation returns one current definition or ErrAutomationNotFound.
func (service *Service) GetAutomation(ctx context.Context, id AutomationID) (Record, error) {
	return service.repository.GetAutomation(ctx, id)
}

// ListAutomations returns one ID-ascending keyset page of current definitions.
func (service *Service) ListAutomations(
	ctx context.Context,
	params ListAutomationsParams,
) (Page[Record], error) {
	return service.repository.ListAutomations(ctx, params)
}

// ReplaceAutomation validates every current reference and replaces one definition
// when the expected revision is still current.
func (service *Service) ReplaceAutomation(
	ctx context.Context,
	id AutomationID,
	expectedRevision int64,
	definition Definition,
) (Record, error) {
	validated, err := ValidateDefinition(ctx, service.devices, definition)
	if err != nil {
		return Record{}, err
	}
	record, err := service.repository.ReplaceAutomation(ctx, id, expectedRevision, validated)
	if err != nil {
		return Record{}, err
	}
	service.logDefinition(ctx, "automation.replaced", record)
	return record, nil
}

// DeleteAutomation hard-deletes one definition under the expected revision.
func (service *Service) DeleteAutomation(ctx context.Context, id AutomationID, expectedRevision int64) error {
	if err := service.repository.DeleteAutomation(ctx, id, expectedRevision); err != nil {
		return err
	}
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation definition deleted",
		slog.String("event", "automation.deleted"),
		slog.String("automation_id", string(id)),
		slog.Int64("revision", expectedRevision),
	)
	return nil
}

// logDefinition records one definition mutation with identity and revision only.
func (service *Service) logDefinition(ctx context.Context, event string, record Record) {
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation definition changed",
		slog.String("event", event),
		slog.String("automation_id", string(record.ID)),
		slog.Int64("revision", record.Revision),
		slog.Time("updated_at", record.UpdatedAt),
	)
}
