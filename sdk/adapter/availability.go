package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const maximumAvailabilityBatchSize = 256

type entityAvailabilityRequest struct {
	Entities []entityAvailabilityEntry `json:"entities"`
}

type entityAvailabilityEntry struct {
	EntityID         string        `json:"entity_id"`
	Status           string        `json:"status"`
	SourceObservedAt string        `json:"source_observed_at"`
	Reason           *healthReason `json:"reason,omitempty"`
}

type entityAvailabilityResponse struct {
	Status     string                   `json:"status"`
	ReportedAt string                   `json:"reported_at,omitempty"`
	Count      int                      `json:"count,omitempty"`
	Error      *entityAvailabilityError `json:"error,omitempty"`
}

type entityAvailabilityError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	EntityID string `json:"entity_id,omitempty"`
}

func (session *Session) ReportEntityAvailability(
	ctx context.Context,
	reports []EntityAvailabilityReport,
) error {
	if err := session.sessionError(); err != nil {
		return err
	}
	normalized, err := session.validateAvailabilityReports(reports)
	if err != nil {
		return &ValidationError{Err: err}
	}
	if acquireErr := session.acquireAvailability(ctx); acquireErr != nil {
		return acquireErr
	}
	defer session.releaseAvailability()
	return session.reportAvailabilityBatch(ctx, normalized)
}

func (session *Session) validateAvailabilityReports(
	reports []EntityAvailabilityReport,
) ([]EntityAvailabilityReport, error) {
	if len(reports) < 1 || len(reports) > maximumAvailabilityBatchSize {
		return nil, errors.New("entity availability batch must contain 1-256 reports")
	}
	normalized := make([]EntityAvailabilityReport, len(reports))
	seen := make(map[string]struct{}, len(reports))
	for index, report := range reports {
		if _, duplicate := seen[report.EntityID]; duplicate {
			return nil, fmt.Errorf("duplicate Entity availability report for %s", report.EntityID)
		}
		seen[report.EntityID] = struct{}{}
		if err := session.validateAvailabilityReport(report); err != nil {
			return nil, err
		}
		normalized[index] = report
		normalized[index].SourceObservedAt = report.SourceObservedAt.UTC()
	}
	return normalized, nil
}

func (session *Session) validateAvailabilityReport(report EntityAvailabilityReport) error {
	if _, err := natswire.ObservationSubject(
		session.adapterID,
		session.runtimeID,
		report.EntityID,
	); err != nil {
		return err
	}
	if report.SourceObservedAt.IsZero() {
		return fmt.Errorf("entity availability source observation time is required for %s", report.EntityID)
	}
	switch report.Status {
	case AvailabilityAvailable:
		if report.ReasonCode != "" {
			return fmt.Errorf("available Entity %s must omit a reason", report.EntityID)
		}
		return nil
	case AvailabilityUnavailable:
		if report.ReasonCode == "" {
			return fmt.Errorf("unavailable Entity %s requires a reason code", report.EntityID)
		}
	default:
		return fmt.Errorf("entity availability status is invalid for %s", report.EntityID)
	}
	if err := validateHealthReport(HealthReport{
		Status: HealthUnhealthy, SourceObservedAt: report.SourceObservedAt,
		ReasonCode: report.ReasonCode,
	}); err != nil {
		return fmt.Errorf("validate Entity availability reason for %s: %w", report.EntityID, err)
	}
	return nil
}

func (session *Session) reportAvailabilityBatch(
	ctx context.Context,
	reports []EntityAvailabilityReport,
) error {
	request, err := session.prepareAvailabilityRequest(reports)
	if err != nil {
		return err
	}
	for {
		attemptContext, cancelAttempt := context.WithTimeout(ctx, requestAttemptTimeout)
		response, requestErr := sendSessionRequest[entityAvailabilityResponse](attemptContext, session, request)
		cancelAttempt()
		if requestErr != nil {
			if retryErr := waitForRequestRetry(ctx, requestErr); retryErr != nil {
				return retryErr
			}
			continue
		}
		if response.Data.Status == statusRejected {
			return session.handleAvailabilityRejection(response.Data.Error)
		}
		if response.Data.Count != len(reports) {
			return fmt.Errorf(
				"entity availability response count = %d, want %d",
				response.Data.Count,
				len(reports),
			)
		}
		if _, parseErr := time.Parse(time.RFC3339Nano, response.Data.ReportedAt); parseErr != nil {
			return fmt.Errorf("parse Entity availability report time: %w", parseErr)
		}
		if terminalErr := session.sessionError(); terminalErr != nil {
			return terminalErr
		}
		return nil
	}
}

func (session *Session) prepareAvailabilityRequest(
	reports []EntityAvailabilityReport,
) (preparedRequest, error) {
	subject, err := natswire.EntityAvailabilitySubject(session.adapterID, session.runtimeID)
	if err != nil {
		return preparedRequest{}, &ValidationError{Err: err}
	}
	entities := make([]entityAvailabilityEntry, len(reports))
	for index, report := range reports {
		entities[index] = entityAvailabilityEntry{
			EntityID: report.EntityID, Status: string(report.Status),
			SourceObservedAt: report.SourceObservedAt.UTC().Format(time.RFC3339Nano),
		}
		if report.ReasonCode != "" {
			entities[index].Reason = &healthReason{Code: report.ReasonCode}
		}
	}
	return prepareRequest(
		session, "avl", contractsv1.EntityAvailabilityRequestSchemaID,
		contractsv1.EntityAvailabilityResponseSchemaID, "Entity availability", subject,
		entityAvailabilityRequest{Entities: entities},
	)
}

func (session *Session) handleAvailabilityRejection(rejection *entityAvailabilityError) error {
	if rejection == nil {
		return errors.New("entity availability rejection omitted error")
	}
	code := EntityAvailabilityRejectionCode(rejection.Code)
	switch code {
	case entityAvailabilityRuntimeFenced:
		session.markFenced()
		return ErrRuntimeFenced
	case EntityAvailabilityAdapterUnhealthy,
		EntityAvailabilityInvalidRequest,
		EntityAvailabilityUnknownEntity,
		EntityAvailabilityWrongAdapter:
		return &EntityAvailabilityRejectedError{
			Code: code, Message: rejection.Message, EntityID: rejection.EntityID,
		}
	default:
		return fmt.Errorf("unknown Entity availability rejection %q", rejection.Code)
	}
}

func (session *Session) acquireAvailability(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-session.availabilityGate:
	}
	if err := session.sessionError(); err != nil {
		session.releaseAvailability()
		return err
	}
	return nil
}

func (session *Session) releaseAvailability() {
	session.availabilityGate <- struct{}{}
}
