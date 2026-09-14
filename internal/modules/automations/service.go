package automations

import (
	"context"
	"log/slog"
	"sync"
)

// Service owns automation behavior. Definition management validates every
// current reference before one short persistence transaction; admission gating
// and worker registration build on the same service.
type Service struct {
	repository   AutomationRepository
	devices      AutomationDevices
	dependencies AutomationDependencies

	// gate serializes admission gating, in-flight admission tracking, and
	// worker registration. idle is closed whenever nothing is admitted: no
	// admission is in flight and no Run worker is registered.
	gate          sync.Mutex
	admissionOpen bool
	admitting     int
	workers       int
	idle          chan struct{}
}

// NewService assembles the automation service from its persistence seam, the
// devices-facing seam, and process-owned collaborators. Zero-valued dependency
// fields fall back to production defaults.
func NewService(
	repository AutomationRepository,
	automationDevices AutomationDevices,
	dependencies AutomationDependencies,
) *Service {
	idle := make(chan struct{})
	close(idle)
	return &Service{
		repository:    repository,
		devices:       automationDevices,
		dependencies:  dependencies.withDefaults(),
		admissionOpen: true,
		idle:          idle,
	}
}

// CreateAutomation validates every current reference and persists one new
// definition at revision 1.
func (service *Service) CreateAutomation(
	ctx context.Context,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	validated, err := ValidateAutomationDefinition(ctx, service.devices, definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	record, err := service.repository.CreateAutomation(ctx, validated)
	if err != nil {
		return AutomationRecord{}, err
	}
	service.logDefinition(ctx, "automation.created", record)
	return record, nil
}

// GetAutomation returns one current definition or ErrAutomationNotFound.
func (service *Service) GetAutomation(ctx context.Context, id AutomationID) (AutomationRecord, error) {
	return service.repository.GetAutomation(ctx, id)
}

// ListAutomations returns one ID-ascending keyset page of current definitions.
func (service *Service) ListAutomations(
	ctx context.Context,
	params ListAutomationsParams,
) (AutomationPage[AutomationRecord], error) {
	return service.repository.ListAutomations(ctx, params)
}

// ReplaceAutomation validates every current reference and atomically replaces
// one definition when the expected revision is still current.
func (service *Service) ReplaceAutomation(
	ctx context.Context,
	id AutomationID,
	expectedRevision int64,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	validated, err := ValidateAutomationDefinition(ctx, service.devices, definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	record, err := service.repository.ReplaceAutomation(ctx, id, expectedRevision, validated)
	if err != nil {
		return AutomationRecord{}, err
	}
	service.logDefinition(ctx, "automation.replaced", record)
	return record, nil
}

// DeleteAutomation hard-deletes one definition under the expected revision. An
// active Run continues from its snapshot and retained history stays queryable.
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

// logDefinition records one definition mutation using only safe structured
// attributes: identity and revision, never definition JSON or names.
func (service *Service) logDefinition(ctx context.Context, event string, record AutomationRecord) {
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation definition changed",
		slog.String("event", event),
		slog.String("automation_id", string(record.ID)),
		slog.Int64("revision", record.Revision),
		slog.Time("updated_at", record.UpdatedAt),
	)
}
