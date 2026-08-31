package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EntityAvailabilityReportError identifies the Entity that caused a batch rejection.
type EntityAvailabilityReportError struct {
	EntityID EntityID
	Err      error
}

func (err *EntityAvailabilityReportError) Error() string {
	return fmt.Sprintf("entity availability report for %s: %v", err.EntityID, err.Err)
}

func (err *EntityAvailabilityReportError) Unwrap() error {
	return err.Err
}

func (service *Service) ReportEntityAvailability(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
	reports []EntityAvailabilityReport,
) (time.Time, error) {
	unlock, err := service.beginHealthEvaluation()
	if err != nil {
		return time.Time{}, err
	}
	defer unlock()

	if validationErr := validateAvailabilityBatch(adapterID, runtimeID, reports); validationErr != nil {
		return time.Time{}, validationErr
	}
	if service.healthEvaluation.recovering {
		if _, refreshed := service.healthEvaluation.refreshed[runtimeID]; !refreshed {
			return time.Time{}, ErrAdapterUnhealthy
		}
	}
	if availabilityBatchUsesAdapterReason(reports) {
		softwareName, softwareErr := service.runtimeSoftwareName(ctx, adapterID, runtimeID)
		if softwareErr != nil {
			return time.Time{}, softwareErr
		}
		for _, report := range reports {
			if report.Reason == nil || !strings.HasPrefix(report.Reason.Code, "adapter.") {
				continue
			}
			if namespaceErr := validateHealthReasonNamespace(
				report.Reason.Code,
				softwareName,
			); namespaceErr != nil {
				return time.Time{}, namespaceErr
			}
		}
	}

	reportedAt := service.dependencies.Now().UTC()
	owned := make([]EntityAvailabilityReport, len(reports))
	for index, report := range reports {
		owned[index] = report
		owned[index].SourceObservedAt = report.SourceObservedAt.UTC()
		owned[index].Reason = copyHealthReason(report.Reason)
	}
	return service.repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: adapterID, RuntimeID: runtimeID, Reports: owned, ReportedAt: reportedAt,
	})
}

func validateAvailabilityBatch(
	adapterID string,
	runtimeID RuntimeID,
	reports []EntityAvailabilityReport,
) error {
	if !registrationSlugPattern.MatchString(adapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	if _, err := ParseRuntimeID(string(runtimeID)); err != nil {
		return fmt.Errorf("parse Adapter runtime ID: %w", err)
	}
	if len(reports) < 1 || len(reports) > 256 {
		return errors.New("entity availability batch must contain 1-256 reports")
	}
	seen := make(map[EntityID]struct{}, len(reports))
	for _, report := range reports {
		if _, duplicate := seen[report.EntityID]; duplicate {
			return fmt.Errorf("duplicate Entity availability report for %s", report.EntityID)
		}
		seen[report.EntityID] = struct{}{}
		if err := validateAvailabilityReport(report); err != nil {
			return err
		}
	}
	return nil
}

func validateAvailabilityReport(report EntityAvailabilityReport) error {
	if _, err := ParseEntityID(string(report.EntityID)); err != nil {
		return fmt.Errorf("parse Entity availability ID: %w", err)
	}
	if report.SourceObservedAt.IsZero() {
		return fmt.Errorf("entity availability source observation time is required for %s", report.EntityID)
	}
	switch report.Status {
	case EntityAvailabilityAvailable:
		if report.Reason != nil {
			return fmt.Errorf("available Entity %s must omit a reason", report.EntityID)
		}
		return nil
	case EntityAvailabilityUnavailable:
		if report.Reason == nil {
			return fmt.Errorf("unavailable Entity %s requires a reason", report.EntityID)
		}
	case EntityAvailabilityUnknown:
		return fmt.Errorf("entity availability status is invalid for %s", report.EntityID)
	default:
		return fmt.Errorf("entity availability status is invalid for %s", report.EntityID)
	}
	if err := validateHealthReason(report.Reason); err != nil {
		return fmt.Errorf("validate Entity availability reason for %s: %w", report.EntityID, err)
	}
	if !strings.HasPrefix(report.Reason.Code, "hearth.") &&
		!strings.HasPrefix(report.Reason.Code, "adapter.") {
		return fmt.Errorf(
			"entity availability reason for %s must use the hearth or adapter namespace",
			report.EntityID,
		)
	}
	return nil
}

func availabilityBatchUsesAdapterReason(reports []EntityAvailabilityReport) bool {
	for _, report := range reports {
		if report.Reason != nil && strings.HasPrefix(report.Reason.Code, "adapter.") {
			return true
		}
	}
	return false
}
