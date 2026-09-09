package automations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationCommands preserves device validation and the detached command lifecycle.
type AutomationCommands interface {
	ValidateCommand(context.Context, devices.CommandInput) (devices.CommandParameters, error)
	ExecuteCommand(context.Context, devices.CommandInput) (devices.CommandResult, error)
}

// AutomationCommandRecords supplies authoritative durable ownership and outcomes.
type AutomationCommandRecords interface {
	GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
}

// Service owns automation management and the shared Run/next-Step admission gate.
type Service struct {
	repo             *SQLiteRepository
	commands         AutomationCommands
	commandRecords   AutomationCommandRecords
	definitions      *AutomationDefinitionCodec
	timezone         *time.Location
	logger           *slog.Logger
	gate             sync.Mutex
	admissionOpen    bool
	executorFault    bool
	workers          int
	idle             chan struct{}
	newCommandID     func() (devices.CommandID, error)
	newCorrelationID func() (devices.CorrelationID, error)
}

// NewService opens admission only after application-owned startup recovery.
func NewService(repo *SQLiteRepository, commands AutomationCommands, commandRecords AutomationCommandRecords,
	definitions *AutomationDefinitionCodec, timezone *time.Location, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	idle := make(chan struct{})
	close(idle)
	return &Service{repo: repo, commands: commands, commandRecords: commandRecords, definitions: definitions,
		timezone: timezone, logger: logger, admissionOpen: true, idle: idle,
		newCommandID: devices.NewCommandID, newCorrelationID: devices.NewCorrelationID}
}

// CreateAutomation validates every Step without dispatch, including disabled targets.
func (service *Service) CreateAutomation(
	ctx context.Context,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	normalized, err := service.validateAutomationDefinition(ctx, definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	return service.repo.CreateAutomation(ctx, normalized)
}

// UpdateAutomation replaces a whole revision; admitted snapshots remain immutable.
func (service *Service) UpdateAutomation(ctx context.Context, input AutomationUpdate) (AutomationRecord, error) {
	if _, err := ParseAutomationID(string(input.ID)); err != nil {
		return AutomationRecord{}, err
	}
	if input.ExpectedRevision < 1 {
		return AutomationRecord{}, fmt.Errorf("%w: expected revision", ErrInvalidAutomation)
	}
	normalized, err := service.validateAutomationDefinition(ctx, input.Definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	input.Definition = normalized
	return service.repo.UpdateAutomation(ctx, input)
}

func (service *Service) validateAutomationDefinition(
	ctx context.Context,
	definition AutomationDefinition,
) (AutomationDefinition, error) {
	if err := service.definitions.ValidateAutomationDefinition(definition); err != nil {
		return AutomationDefinition{}, err
	}
	definition = cloneAutomationDefinition(definition)
	definition.Name = strings.TrimSpace(definition.Name)
	if definition.Name == "" {
		return AutomationDefinition{}, fmt.Errorf("%w: /name must not be blank", ErrInvalidAutomation)
	}
	seen := make(map[AutomationTriggerID]bool)
	for index := range definition.Triggers {
		trigger := &definition.Triggers[index]
		if seen[trigger.ID] {
			return AutomationDefinition{}, fmt.Errorf("%w: /triggers/%d/id is duplicated", ErrInvalidAutomation, index)
		}
		seen[trigger.ID] = true
		trigger.Expression = strings.Join(strings.Fields(trigger.Expression), " ")
		if _, err := ParseCronSchedule(trigger.Expression); err != nil {
			return AutomationDefinition{}, fmt.Errorf(
				"%w: /triggers/%d/expression is invalid",
				ErrInvalidAutomation,
				index,
			)
		}
	}
	for index := range definition.Steps {
		step := &definition.Steps[index]
		parameters, err := service.commands.ValidateCommand(
			ctx,
			devices.CommandInput{
				EntityID:      step.EntityID,
				OperationName: step.OperationName,
				Parameters:    step.Parameters,
			},
		)
		if errors.Is(err, devices.ErrInvalidCommand) || errors.Is(err, devices.ErrEntityNotFound) {
			return AutomationDefinition{}, fmt.Errorf(
				"%w: /steps/%d target, operation or parameters are invalid",
				ErrInvalidAutomation,
				index,
			)
		}
		if err != nil {
			return AutomationDefinition{}, err
		}
		step.Parameters = bytes.Clone(parameters)
	}
	// Catalog normalization must not allow stored definitions to exceed the schema limits.
	if err := service.definitions.ValidateAutomationDefinition(definition); err != nil {
		return AutomationDefinition{}, err
	}
	return definition, nil
}

func cloneAutomationDefinition(definition AutomationDefinition) AutomationDefinition {
	definition.Triggers = append([]AutomationTrigger(nil), definition.Triggers...)
	definition.Steps = append([]AutomationStep(nil), definition.Steps...)
	for index := range definition.Steps {
		definition.Steps[index].Parameters = bytes.Clone(definition.Steps[index].Parameters)
	}
	return definition
}

// GetAutomation reads a live definition; deletion does not affect Run history.
func (service *Service) GetAutomation(ctx context.Context, id AutomationID) (AutomationRecord, error) {
	if _, err := ParseAutomationID(string(id)); err != nil {
		return AutomationRecord{}, err
	}
	return service.repo.GetAutomation(ctx, id)
}

// ListAutomations lists definitions in ascending canonical ID order.
func (service *Service) ListAutomations(
	ctx context.Context,
	input AutomationListParams,
) (AutomationPage[AutomationRecord], error) {
	if input.AfterID != nil {
		if _, err := ParseAutomationID(string(*input.AfterID)); err != nil {
			return AutomationPage[AutomationRecord]{}, err
		}
	}
	return service.repo.ListAutomations(ctx, input)
}

// DeleteAutomation checks the revision and active claim in one transaction.
func (service *Service) DeleteAutomation(ctx context.Context, id AutomationID, revision int64) error {
	if _, err := ParseAutomationID(string(id)); err != nil {
		return err
	}
	if revision < 1 {
		return fmt.Errorf("%w: expected revision", ErrInvalidAutomation)
	}
	return service.repo.DeleteAutomation(ctx, id, revision)
}

// StartManualRun commits and registers detached work under the lifecycle gate.
func (service *Service) StartManualRun(
	ctx context.Context,
	input AutomationManualRequest,
) (AutomationAdmission, error) {
	if _, err := ParseAutomationID(string(input.AutomationID)); err != nil {
		return AutomationAdmission{}, err
	}
	if err := ValidateAutomationIdempotencyKey(input.IdempotencyKey); err != nil {
		return AutomationAdmission{}, err
	}
	service.gate.Lock()
	defer service.gate.Unlock()
	if !service.admissionOpen {
		return AutomationAdmission{}, ErrAutomationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return AutomationAdmission{}, err
	}
	// Once admission starts, request cancellation cannot strand a committed Run
	// between transaction commit and worker registration. Bound persistence instead.
	admissionContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), automationPersistenceTimeout)
	defer cancel()
	admission, err := service.repo.AdmitManualRun(
		admissionContext,
		AutomationManualAdmission{Request: input, Timezone: service.timezone.String()},
	)
	if err != nil {
		if !errors.Is(err, ErrAutomationNotFound) && !errors.Is(err, ErrAutomationRunActive) {
			service.latchAutomationFaultLocked()
		}
		return AutomationAdmission{}, err
	}
	if !admission.Reused {
		if service.workers == 0 {
			service.idle = make(chan struct{})
		}
		service.workers++
		// Pass only owned execution inputs, never the response's mutable Run evidence.
		definition := cloneAutomationDefinition(admission.Run.Snapshot.Definition)
		//nolint:gosec // Admitted work must outlive request and shutdown cancellation.
		go service.executeAutomationRun(admission.Run.ID, definition)
	}
	return admission, nil
}

// GetAutomationRun exposes only ownership-verified Command evidence.
func (service *Service) GetAutomationRun(ctx context.Context, id AutomationRunID) (AutomationRunRecord, error) {
	if _, err := ParseAutomationRunID(string(id)); err != nil {
		return AutomationRunRecord{}, err
	}
	return service.repo.GetAutomationRun(ctx, id)
}

// ListAutomationRuns includes historical Runs of deleted definitions.
func (service *Service) ListAutomationRuns(
	ctx context.Context,
	input AutomationRunListParams,
) (AutomationPage[AutomationRunRecord], error) {
	if input.AutomationID != nil {
		if _, err := ParseAutomationID(string(*input.AutomationID)); err != nil {
			return AutomationPage[AutomationRunRecord]{}, err
		}
	}
	if input.BeforeID != nil {
		if _, err := ParseAutomationRunID(string(*input.BeforeID)); err != nil {
			return AutomationPage[AutomationRunRecord]{}, err
		}
	}
	if (input.BeforeID == nil) != (input.BeforeStartedAt == nil) {
		return AutomationPage[AutomationRunRecord]{}, fmt.Errorf("%w: incomplete run position", ErrInvalidAutomation)
	}
	return service.repo.ListAutomationRuns(ctx, input)
}
