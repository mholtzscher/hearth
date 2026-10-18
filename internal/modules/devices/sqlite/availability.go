package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) ReportEntityAvailability(
	ctx context.Context,
	write devices.AvailabilityBatchWrite,
) (time.Time, error) {
	prepared, err := devices.NormalizeAvailabilityWrite(write)
	if err != nil {
		return time.Time{}, err
	}
	write = prepared
	fingerprint, err := availabilityRequestFingerprint(write)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %w", devices.ErrInvalidAvailabilityRequest, err)
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return time.Time{}, fmt.Errorf("begin Entity availability report: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	reportedAt, repeated, repeatErr := repeatedAvailabilityRequest(ctx, queries, write.RequestID, fingerprint)
	if repeatErr != nil {
		return time.Time{}, repeatErr
	}
	if repeated {
		return reportedAt, nil
	}
	instance, err := activeAdapterInstance(ctx, queries, write.AdapterID, write.RuntimeID)
	if err != nil {
		return time.Time{}, err
	}
	if instance.HealthStatus != string(adapters.AdapterHealthHealthy) {
		return time.Time{}, adapters.ErrAdapterUnhealthy
	}
	revision, err := queries.ReadAuthoritativeHealthRevision(
		ctx,
		dbsqlc.ReadAuthoritativeHealthRevisionParams{AdapterID: write.AdapterID},
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("read report health generation: %w", err)
	}
	if revision < 1 {
		return time.Time{}, corruptProjection("authoritative report generation")
	}
	projected, _, err := projectedAdapterHealth(ctx, queries, write.AdapterID)
	if err != nil {
		return time.Time{}, err
	}
	seen := make(map[devices.EntityID]struct{}, len(write.Reports))
	for _, report := range write.Reports {
		if _, duplicate := seen[report.EntityID]; duplicate {
			return time.Time{}, fmt.Errorf(
				"%w: duplicate Entity availability report for %s",
				devices.ErrInvalidAvailabilityRequest,
				report.EntityID,
			)
		}
		seen[report.EntityID] = struct{}{}
		if persistErr := persistEntityAvailability(ctx, queries, write, report, revision); persistErr != nil {
			return time.Time{}, persistErr
		}
		if err = recomputeEffectiveAvailability(
			ctx,
			queries,
			write.AdapterID,
			report.EntityID,
			projected,
			write.ReportedAt,
			"",
		); err != nil {
			return time.Time{}, err
		}
	}
	if receiptErr := queries.InsertEntityAvailabilityReceipt(ctx, dbsqlc.InsertEntityAvailabilityReceiptParams{
		RequestID: write.RequestID, Fingerprint: fingerprint, ReportedAt: formatTime(write.ReportedAt),
	}); receiptErr != nil {
		return time.Time{}, fmt.Errorf("insert Entity availability receipt: %w", receiptErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return time.Time{}, fmt.Errorf("commit Entity availability report: %w", commitErr)
	}
	return write.ReportedAt.UTC(), nil
}

func repeatedAvailabilityRequest(
	ctx context.Context,
	queries *dbsqlc.Queries,
	requestID string,
	fingerprint string,
) (time.Time, bool, error) {
	receipt, err := queries.GetEntityAvailabilityReceipt(ctx, dbsqlc.GetEntityAvailabilityReceiptParams{
		RequestID: requestID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get Entity availability receipt: %w", err)
	}
	if receipt.Fingerprint != fingerprint {
		return time.Time{}, false, fmt.Errorf(
			"%w: request ID was already used with different availability data",
			devices.ErrInvalidAvailabilityRequest,
		)
	}
	reportedAt, err := parseTime(receipt.ReportedAt)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse Entity availability receipt time: %w", err)
	}
	return reportedAt, true, nil
}

func availabilityRequestFingerprint(write devices.AvailabilityBatchWrite) (string, error) {
	type fingerprintReport struct {
		EntityID         devices.EntityID                 `json:"entity_id"`
		Status           devices.EntityAvailabilityStatus `json:"status"`
		SourceObservedAt time.Time                        `json:"source_observed_at"`
		ReasonCode       string                           `json:"reason_code,omitempty"`
	}
	request := struct {
		AdapterID string              `json:"adapter_id"`
		RuntimeID adapters.RuntimeID  `json:"runtime_id"`
		Reports   []fingerprintReport `json:"reports"`
	}{AdapterID: write.AdapterID, RuntimeID: write.RuntimeID, Reports: make([]fingerprintReport, len(write.Reports))}
	for index, report := range write.Reports {
		request.Reports[index] = fingerprintReport{
			EntityID: report.EntityID, Status: report.Status, SourceObservedAt: report.SourceObservedAt.UTC(),
		}
		if report.Reason != nil {
			request.Reports[index].ReasonCode = report.Reason.Code
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode Entity availability request fingerprint: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(payload)), nil
}

func persistEntityAvailability(
	ctx context.Context,
	queries *dbsqlc.Queries,
	write devices.AvailabilityBatchWrite,
	report devices.EntityAvailabilityReport,
	revision int64,
) error {
	owner, err := queries.GetEntityOwner(ctx, dbsqlc.GetEntityOwnerParams{EntityID: string(report.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return &devices.EntityAvailabilityReportError{EntityID: report.EntityID, Err: devices.ErrEntityNotFound}
	}
	if err != nil {
		return fmt.Errorf("get Entity availability owner: %w", err)
	}
	if owner != write.AdapterID {
		return &devices.EntityAvailabilityReportError{EntityID: report.EntityID, Err: devices.ErrEntityWrongAdapter}
	}
	current, err := queries.GetEntityAvailabilityCurrent(ctx, dbsqlc.GetEntityAvailabilityCurrentParams{
		EntityID: string(report.EntityID),
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("get current Entity availability: %w", err)
	}
	if err == nil {
		if retainedErr := validateRetainedAvailabilityReport(current, write.AdapterID); retainedErr != nil {
			return retainedErr
		}
	}
	changed := errors.Is(err, sql.ErrNoRows) || current.Status != string(report.Status) ||
		current.ReasonCode.String != healthReasonCode(
			report.Reason,
		) || current.RuntimeID != string(write.RuntimeID) || current.HealthRevision != revision
	since := write.ReportedAt
	if !changed {
		since, err = parseTime(current.CurrentSince)
		if err != nil {
			return fmt.Errorf("parse Entity availability since: %w", err)
		}
	}
	if upsertErr := queries.UpsertEntityAvailabilityCurrent(ctx, dbsqlc.UpsertEntityAvailabilityCurrentParams{
		EntityID:         string(report.EntityID),
		AdapterID:        write.AdapterID,
		RuntimeID:        string(write.RuntimeID),
		Status:           string(report.Status),
		ReasonCode:       nullableReasonCode(report.Reason),
		SourceObservedAt: formatTime(report.SourceObservedAt),
		EvidenceAt:       formatTime(write.ReportedAt),
		CurrentSince:     formatTime(since),
		HealthRevision:   revision,
	}); upsertErr != nil {
		return fmt.Errorf("upsert Entity availability: %w", upsertErr)
	}
	return nil
}
