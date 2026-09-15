package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func appendHealthTransition(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params dbsqlc.InsertHealthTransitionParams,
) (int64, error) {
	receiveOrder, err := queries.InsertHealthTransition(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("insert health transition: %w", err)
	}
	return receiveOrder, nil
}

func appendAdapterHealthTransition(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params dbsqlc.InsertHealthTransitionParams,
) error {
	if _, err := appendHealthTransition(ctx, queries, params); err != nil {
		return err
	}

	status := devices.EntityAvailabilityUnknown
	source := availabilitySourceAdapterHealth
	reasonCode := params.ReasonCode
	sourceObservedAt := params.SourceObservedAt
	switch devices.AdapterHealthStatus(params.Status) {
	case devices.AdapterHealthHealthy:
		source = healthSourceCore
		reasonCode = nullableText("hearth.awaiting_entity_report")
		sourceObservedAt = sql.NullString{}
	case devices.AdapterHealthUnhealthy:
		status = devices.EntityAvailabilityUnavailable
	case devices.AdapterHealthUnknown:
	default:
		return errors.New("cannot materialize invalid Adapter health transition")
	}

	if err := queries.InsertAdapterEntityAvailabilityTransitions(
		ctx,
		dbsqlc.InsertAdapterEntityAvailabilityTransitionsParams{
			RuntimeID: params.RuntimeID, Status: string(status), Source: source,
			ReasonCode: reasonCode, SourceObservedAt: sourceObservedAt,
			ObservedAt: params.ObservedAt, AdapterID: params.AdapterID,
		},
	); err != nil {
		return fmt.Errorf("insert effective Entity availability transitions: %w", err)
	}
	return nil
}

func invalidateAdapterEntityAvailability(
	ctx context.Context,
	queries *dbsqlc.Queries,
	instance dbsqlc.AdapterInstance,
	nextStatus devices.AdapterHealthStatus,
) error {
	if devices.AdapterHealthStatus(instance.HealthStatus) != devices.AdapterHealthHealthy ||
		nextStatus == devices.AdapterHealthHealthy {
		return nil
	}
	if err := queries.DeleteAdapterEntityAvailability(ctx, dbsqlc.DeleteAdapterEntityAvailabilityParams{
		AdapterID: instance.AdapterID,
	}); err != nil {
		return fmt.Errorf("invalidate Adapter Entity availability: %w", err)
	}
	return nil
}

func (repository *DeviceRepository) ReportEntityAvailability(
	ctx context.Context,
	write devices.AvailabilityBatchWrite,
) (time.Time, error) {
	if err := devices.ValidateAvailabilityRequestID(write.RequestID); err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid request identity", devices.ErrInvalidAvailabilityRequest)
	}
	if len(write.Reports) == 0 || len(write.Reports) > 256 {
		return time.Time{}, fmt.Errorf(
			"%w: Entity availability batch must contain 1-256 reports",
			devices.ErrInvalidAvailabilityRequest,
		)
	}
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
	if instance.HealthStatus != string(devices.AdapterHealthHealthy) {
		return time.Time{}, devices.ErrAdapterUnhealthy
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
		if persistErr := persistEntityAvailability(ctx, queries, write, report); persistErr != nil {
			return time.Time{}, persistErr
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
		RuntimeID devices.RuntimeID   `json:"runtime_id"`
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
	changed := errors.Is(err, sql.ErrNoRows) || current.Status != string(report.Status) ||
		current.ReasonCode.String != healthReasonCode(report.Reason)
	since := write.ReportedAt
	if !changed {
		since, err = parseTime(current.CurrentSince)
		if err != nil {
			return fmt.Errorf("parse Entity availability since: %w", err)
		}
	}
	var receiveOrder sql.NullInt64
	if changed {
		order, transitionErr := appendHealthTransition(ctx, queries, dbsqlc.InsertHealthTransitionParams{
			ResourceKind:     "entity",
			AdapterID:        write.AdapterID,
			EntityID:         nullableText(string(report.EntityID)),
			RuntimeID:        nullableText(string(write.RuntimeID)),
			Status:           string(report.Status),
			Source:           "entity_report",
			ReasonCode:       nullableReasonCode(report.Reason),
			SourceObservedAt: formatNullableTime(report.SourceObservedAt),
			ObservedAt:       formatTime(write.ReportedAt),
		})
		if transitionErr != nil {
			return transitionErr
		}
		receiveOrder = sql.NullInt64{Int64: order, Valid: true}
	} else {
		receiveOrder = current.LatestTransitionReceiveOrder
	}
	if upsertErr := queries.UpsertEntityAvailabilityCurrent(ctx, dbsqlc.UpsertEntityAvailabilityCurrentParams{
		EntityID:                     string(report.EntityID),
		AdapterID:                    write.AdapterID,
		RuntimeID:                    string(write.RuntimeID),
		Status:                       string(report.Status),
		ReasonCode:                   nullableReasonCode(report.Reason),
		SourceObservedAt:             formatTime(report.SourceObservedAt),
		EvidenceAt:                   formatTime(write.ReportedAt),
		CurrentSince:                 formatTime(since),
		LatestTransitionReceiveOrder: receiveOrder,
	}); upsertErr != nil {
		return fmt.Errorf("upsert Entity availability: %w", upsertErr)
	}
	return nil
}
