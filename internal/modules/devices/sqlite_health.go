package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices/dbsqlc"
)

const (
	adapterHeartbeatInterval = 5 * time.Second
	adapterLeaseDuration     = 15 * time.Second
	healthResourceAdapter    = "adapter"
)

type AdapterActiveError struct {
	RetryAfter time.Time
}

func (err *AdapterActiveError) Error() string {
	return fmt.Sprintf("%s until %s", ErrAdapterActive, err.RetryAfter.Format(time.RFC3339Nano))
}

func (*AdapterActiveError) Unwrap() error {
	return ErrAdapterActive
}

func (repository *SQLiteRepository) ClaimAdapterRuntime(
	ctx context.Context,
	write ClaimRuntimeWrite,
) (RuntimeClaim, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return RuntimeClaim{}, fmt.Errorf("begin Adapter runtime claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)

	previous, repeated, err := repeatedRuntimeClaim(ctx, queries, write)
	if err != nil {
		return RuntimeClaim{}, err
	}
	if repeated {
		return previous, nil
	}
	instance, err := claimAdapterInstance(ctx, queries, write)
	if err != nil {
		return RuntimeClaim{}, err
	}
	if activeErr := rejectActiveRuntimeClaim(ctx, queries, instance); activeErr != nil {
		return RuntimeClaim{}, activeErr
	}

	if insertErr := queries.InsertRuntime(ctx, dbsqlc.InsertRuntimeParams{
		RuntimeID: string(write.RuntimeID), ClaimID: write.ClaimID, AdapterID: write.AdapterID,
		SoftwareName: write.SoftwareName, SoftwareVersion: write.SoftwareVersion,
		ClaimedAt: formatTime(write.ClaimedAt), LeaseExpiresAt: formatTime(write.LeaseExpiresAt),
	}); insertErr != nil {
		return RuntimeClaim{}, fmt.Errorf("insert Adapter runtime: %w", insertErr)
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, dbsqlc.UpdateAdapterCurrentHealthParams{
		ActiveRuntimeID:  nullableText(string(write.RuntimeID)),
		HealthRuntimeID:  nullableText(string(write.RuntimeID)),
		HealthStatus:     string(AdapterHealthUnknown),
		HealthReasonCode: nullableText("hearth.awaiting_health"),
		HealthSince:      formatTime(write.ClaimedAt),
		HealthEvidenceAt: formatTime(write.ClaimedAt),
		AdapterID:        write.AdapterID,
	}); updateErr != nil {
		return RuntimeClaim{}, fmt.Errorf("activate Adapter runtime: %w", updateErr)
	}
	if _, transitionErr := appendHealthTransition(ctx, queries, dbsqlc.InsertHealthTransitionParams{
		ResourceKind: healthResourceAdapter, AdapterID: write.AdapterID,
		RuntimeID:  nullableText(string(write.RuntimeID)),
		Status:     string(AdapterHealthUnknown),
		Source:     healthSourceCore,
		ReasonCode: nullableText("hearth.awaiting_health"),
		ObservedAt: formatTime(write.ClaimedAt),
	}); transitionErr != nil {
		return RuntimeClaim{}, transitionErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return RuntimeClaim{}, fmt.Errorf("commit Adapter runtime claim: %w", commitErr)
	}
	return runtimeClaim(write.RuntimeID), nil
}

func repeatedRuntimeClaim(
	ctx context.Context,
	queries *dbsqlc.Queries,
	write ClaimRuntimeWrite,
) (RuntimeClaim, bool, error) {
	previous, err := queries.GetRuntimeByClaimID(ctx, dbsqlc.GetRuntimeByClaimIDParams{ClaimID: write.ClaimID})
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeClaim{}, false, nil
	}
	if err != nil {
		return RuntimeClaim{}, false, fmt.Errorf("get claim retry: %w", err)
	}
	if previous.AdapterID != write.AdapterID || previous.SoftwareName != write.SoftwareName ||
		previous.SoftwareVersion != write.SoftwareVersion {
		return RuntimeClaim{}, false, errors.New("claim ID was already used with different Adapter metadata")
	}
	return runtimeClaim(RuntimeID(previous.RuntimeID)), true, nil
}

func claimAdapterInstance(
	ctx context.Context,
	queries *dbsqlc.Queries,
	write ClaimRuntimeWrite,
) (dbsqlc.AdapterInstance, error) {
	instance, err := queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{AdapterID: write.AdapterID})
	if err == nil {
		return instance, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return dbsqlc.AdapterInstance{}, fmt.Errorf("get Adapter instance for claim: %w", err)
	}
	if createErr := queries.CreateAdapterInstance(ctx, dbsqlc.CreateAdapterInstanceParams{
		AdapterID:        write.AdapterID,
		HealthStatus:     string(AdapterHealthUnknown),
		HealthReasonCode: nullableText("hearth.awaiting_health"),
		HealthSince:      formatTime(write.ClaimedAt),
		HealthEvidenceAt: formatTime(write.ClaimedAt),
	}); createErr != nil {
		return dbsqlc.AdapterInstance{}, fmt.Errorf("create Adapter instance: %w", createErr)
	}
	instance, err = queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{AdapterID: write.AdapterID})
	if err != nil {
		return dbsqlc.AdapterInstance{}, fmt.Errorf("get created Adapter instance: %w", err)
	}
	return instance, nil
}

func rejectActiveRuntimeClaim(
	ctx context.Context,
	queries *dbsqlc.Queries,
	instance dbsqlc.AdapterInstance,
) error {
	if !instance.ActiveRuntimeID.Valid {
		return nil
	}
	active, err := queries.GetRuntime(ctx, dbsqlc.GetRuntimeParams{
		RuntimeID: instance.ActiveRuntimeID.String,
	})
	if err != nil {
		return fmt.Errorf("get active Adapter runtime: %w", err)
	}
	leaseExpiresAt, err := parseTime(active.LeaseExpiresAt)
	if err != nil {
		return fmt.Errorf("parse active Adapter runtime lease expiry: %w", err)
	}
	return &AdapterActiveError{RetryAfter: leaseExpiresAt}
}

func runtimeClaim(runtimeID RuntimeID) RuntimeClaim {
	return RuntimeClaim{
		RuntimeID: runtimeID, HeartbeatInterval: adapterHeartbeatInterval, LeaseDuration: adapterLeaseDuration,
	}
}

func (repository *SQLiteRepository) RecordAdapterHeartbeat(
	ctx context.Context,
	write HeartbeatWrite,
) (HeartbeatResult, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return HeartbeatResult{}, fmt.Errorf("begin Adapter heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	instance, err := activeAdapterInstance(ctx, queries, write.AdapterID, write.RuntimeID)
	if err != nil {
		return HeartbeatResult{}, err
	}
	if write.ExternalStatus == AdapterHealthUnknown && instance.ExternalSystemStatus.Valid &&
		instance.ExternalSystemStatus.String != string(AdapterHealthUnknown) {
		return HeartbeatResult{}, errors.New("external-system health cannot return to unknown in one runtime")
	}
	rows, err := queries.UpdateRuntimeHeartbeat(ctx, dbsqlc.UpdateRuntimeHeartbeatParams{
		LastHeartbeatAt: formatNullableTime(write.ReceivedAt), LeaseExpiresAt: formatTime(write.LeaseExpiresAt),
		RuntimeID: string(write.RuntimeID), AdapterID: write.AdapterID,
	})
	if err != nil {
		return HeartbeatResult{}, fmt.Errorf("renew Adapter heartbeat lease: %w", err)
	}
	if rows != 1 {
		return HeartbeatResult{}, ErrRuntimeFenced
	}

	reason := effectiveHeartbeatReason(write)
	changed := healthChanged(instance, write.ExternalStatus, reason)
	since := write.ReceivedAt
	if !changed {
		since, err = parseTime(instance.HealthSince)
		if err != nil {
			return HeartbeatResult{}, fmt.Errorf("parse Adapter health since: %w", err)
		}
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, dbsqlc.UpdateAdapterCurrentHealthParams{
		ActiveRuntimeID:                nullableText(string(write.RuntimeID)),
		HealthRuntimeID:                nullableText(string(write.RuntimeID)),
		HealthStatus:                   string(write.ExternalStatus),
		HealthReasonCode:               nullableReasonCode(reason),
		HealthSince:                    formatTime(since),
		HealthEvidenceAt:               formatTime(write.ReceivedAt),
		ExternalSystemStatus:           nullableText(string(write.ExternalStatus)),
		ExternalSystemReasonCode:       nullableReasonCode(write.Reason),
		ExternalSystemSourceObservedAt: formatNullableTime(write.SourceObservedAt),
		ExternalSystemEvidenceAt:       formatNullableTime(write.ReceivedAt),
		AdapterID:                      write.AdapterID,
	}); updateErr != nil {
		return HeartbeatResult{}, fmt.Errorf("update Adapter heartbeat health: %w", updateErr)
	}
	if invalidateErr := invalidateAdapterEntityAvailability(
		ctx, queries, instance, write.ExternalStatus,
	); invalidateErr != nil {
		return HeartbeatResult{}, invalidateErr
	}
	if changed {
		if _, transitionErr := appendHealthTransition(ctx, queries, dbsqlc.InsertHealthTransitionParams{
			ResourceKind:     healthResourceAdapter,
			AdapterID:        write.AdapterID,
			RuntimeID:        nullableText(string(write.RuntimeID)),
			Status:           string(write.ExternalStatus),
			Source:           "external_system",
			ReasonCode:       nullableReasonCode(reason),
			SourceObservedAt: formatNullableTime(write.SourceObservedAt),
			ObservedAt:       formatTime(write.ReceivedAt),
		}); transitionErr != nil {
			return HeartbeatResult{}, transitionErr
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return HeartbeatResult{}, fmt.Errorf("commit Adapter heartbeat: %w", commitErr)
	}
	return HeartbeatResult{LeaseExpiresAt: write.LeaseExpiresAt.UTC()}, nil
}

func effectiveHeartbeatReason(write HeartbeatWrite) *HealthReason {
	if write.ExternalStatus == AdapterHealthUnknown {
		return &HealthReason{Code: "hearth.awaiting_health"}
	}
	return copyHealthReason(write.Reason)
}

func (repository *SQLiteRepository) ReleaseAdapterRuntime(
	ctx context.Context,
	write ReleaseRuntimeWrite,
) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin Adapter runtime release: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	runtime, err := queries.GetRuntime(ctx, dbsqlc.GetRuntimeParams{RuntimeID: string(write.RuntimeID)})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && runtime.AdapterID != write.AdapterID) {
		return ErrRuntimeFenced
	}
	if err != nil {
		return fmt.Errorf("get Adapter runtime for release: %w", err)
	}
	if runtime.EndedAt.Valid {
		latest, latestErr := queries.GetLatestRuntimeForAdapter(ctx, dbsqlc.GetLatestRuntimeForAdapterParams{
			AdapterID: write.AdapterID,
		})
		if latestErr != nil {
			return fmt.Errorf("get latest Adapter runtime: %w", latestErr)
		}
		if runtime.EndReason.String != "stopped" || latest.RuntimeID != runtime.RuntimeID {
			return ErrRuntimeFenced
		}
		return nil
	}
	instance, err := activeAdapterInstance(ctx, queries, write.AdapterID, write.RuntimeID)
	if err != nil {
		return err
	}
	if rows, endErr := queries.EndRuntime(ctx, dbsqlc.EndRuntimeParams{
		EndedAt: formatNullableTime(write.ReleasedAt), EndReason: nullableText("stopped"),
		RuntimeID: string(write.RuntimeID), AdapterID: write.AdapterID,
	}); endErr != nil {
		return fmt.Errorf("end released Adapter runtime: %w", endErr)
	} else if rows != 1 {
		return ErrRuntimeFenced
	}
	if transitionErr := setOfflineAdapterHealth(
		ctx, queries, instance, runtime, write.ReleasedAt, "hearth.stopped",
	); transitionErr != nil {
		return transitionErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit Adapter runtime release: %w", commitErr)
	}
	return nil
}

func (repository *SQLiteRepository) ExpireAdapterLeases(
	ctx context.Context,
	write ExpireLeasesWrite,
) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin Adapter lease expiry: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	runtimes, err := queries.ListExpiredRuntimes(ctx, dbsqlc.ListExpiredRuntimesParams{
		ExpiresAt: formatTime(write.ExpiresAt),
	})
	if err != nil {
		return fmt.Errorf("list expired Adapter runtimes: %w", err)
	}
	for _, runtime := range runtimes {
		instance, instanceErr := queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{
			AdapterID: runtime.AdapterID,
		})
		if instanceErr != nil {
			return fmt.Errorf("get Adapter for lease expiry: %w", instanceErr)
		}
		if !instance.ActiveRuntimeID.Valid || instance.ActiveRuntimeID.String != runtime.RuntimeID {
			continue
		}
		if expireErr := expireRuntime(ctx, queries, instance, runtime, write.ExpiresAt); expireErr != nil {
			return expireErr
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit Adapter lease expiry: %w", commitErr)
	}
	return nil
}

func expireRuntime(
	ctx context.Context,
	queries *dbsqlc.Queries,
	instance dbsqlc.AdapterInstance,
	runtime dbsqlc.AdapterRuntime,
	expiredAt time.Time,
) error {
	rows, err := queries.EndRuntime(ctx, dbsqlc.EndRuntimeParams{
		EndedAt: formatNullableTime(expiredAt), EndReason: nullableText("heartbeat_expired"),
		RuntimeID: runtime.RuntimeID, AdapterID: runtime.AdapterID,
	})
	if err != nil {
		return fmt.Errorf("end expired Adapter runtime: %w", err)
	}
	if rows != 1 {
		return ErrRuntimeFenced
	}
	return setOfflineAdapterHealth(ctx, queries, instance, runtime, expiredAt, "hearth.heartbeat_expired")
}

func setOfflineAdapterHealth(
	ctx context.Context,
	queries *dbsqlc.Queries,
	instance dbsqlc.AdapterInstance,
	runtime dbsqlc.AdapterRuntime,
	observedAt time.Time,
	reasonCode string,
) error {
	changed := instance.HealthStatus != string(AdapterHealthUnhealthy) ||
		instance.HealthReasonCode.String != reasonCode
	since := observedAt
	if !changed {
		var err error
		since, err = parseTime(instance.HealthSince)
		if err != nil {
			return fmt.Errorf("parse Adapter health since: %w", err)
		}
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, dbsqlc.UpdateAdapterCurrentHealthParams{
		HealthRuntimeID:                nullableText(runtime.RuntimeID),
		HealthStatus:                   string(AdapterHealthUnhealthy),
		HealthReasonCode:               nullableText(reasonCode),
		HealthSince:                    formatTime(since),
		HealthEvidenceAt:               formatTime(observedAt),
		ExternalSystemStatus:           instance.ExternalSystemStatus,
		ExternalSystemReasonCode:       instance.ExternalSystemReasonCode,
		ExternalSystemSourceObservedAt: instance.ExternalSystemSourceObservedAt,
		ExternalSystemEvidenceAt:       instance.ExternalSystemEvidenceAt,
		AdapterID:                      instance.AdapterID,
	}); updateErr != nil {
		return fmt.Errorf("mark Adapter runtime offline: %w", updateErr)
	}
	if invalidateErr := invalidateAdapterEntityAvailability(
		ctx, queries, instance, AdapterHealthUnhealthy,
	); invalidateErr != nil {
		return invalidateErr
	}
	if !changed {
		return nil
	}
	_, err := appendHealthTransition(ctx, queries, dbsqlc.InsertHealthTransitionParams{
		ResourceKind: healthResourceAdapter,
		AdapterID:    instance.AdapterID,
		RuntimeID:    nullableText(runtime.RuntimeID),
		Status:       string(AdapterHealthUnhealthy),
		Source:       healthSourceCore,
		ReasonCode:   nullableText(reasonCode),
		ObservedAt:   formatTime(observedAt),
	})
	return err
}

func activeAdapterInstance(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	runtimeID RuntimeID,
) (dbsqlc.AdapterInstance, error) {
	instance, err := queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{AdapterID: adapterID})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!instance.ActiveRuntimeID.Valid ||
		instance.ActiveRuntimeID.String != string(runtimeID))) {
		return dbsqlc.AdapterInstance{}, ErrRuntimeFenced
	}
	if err != nil {
		return dbsqlc.AdapterInstance{}, fmt.Errorf("get active Adapter runtime: %w", err)
	}
	return instance, nil
}

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

func invalidateAdapterEntityAvailability(
	ctx context.Context,
	queries *dbsqlc.Queries,
	instance dbsqlc.AdapterInstance,
	nextStatus AdapterHealthStatus,
) error {
	if AdapterHealthStatus(instance.HealthStatus) != AdapterHealthHealthy ||
		nextStatus == AdapterHealthHealthy {
		return nil
	}
	if err := queries.DeleteAdapterEntityAvailability(ctx, dbsqlc.DeleteAdapterEntityAvailabilityParams{
		AdapterID: instance.AdapterID,
	}); err != nil {
		return fmt.Errorf("invalidate Adapter Entity availability: %w", err)
	}
	return nil
}

func healthChanged(
	instance dbsqlc.AdapterInstance,
	status AdapterHealthStatus,
	reason *HealthReason,
) bool {
	return instance.HealthStatus != string(status) ||
		instance.HealthReasonCode.String != healthReasonCode(reason)
}

func (repository *SQLiteRepository) ReportEntityAvailability(
	ctx context.Context,
	write AvailabilityBatchWrite,
) (time.Time, error) {
	if len(write.Reports) == 0 || len(write.Reports) > 256 {
		return time.Time{}, errors.New("Entity availability batch must contain 1-256 reports")
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return time.Time{}, fmt.Errorf("begin Entity availability report: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	instance, err := activeAdapterInstance(ctx, queries, write.AdapterID, write.RuntimeID)
	if err != nil {
		return time.Time{}, err
	}
	if instance.HealthStatus != string(AdapterHealthHealthy) {
		return time.Time{}, ErrAdapterUnhealthy
	}
	seen := make(map[EntityID]struct{}, len(write.Reports))
	for _, report := range write.Reports {
		if _, duplicate := seen[report.EntityID]; duplicate {
			return time.Time{}, fmt.Errorf("duplicate Entity availability report for %s", report.EntityID)
		}
		seen[report.EntityID] = struct{}{}
		if persistErr := persistEntityAvailability(ctx, queries, write, report); persistErr != nil {
			return time.Time{}, persistErr
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return time.Time{}, fmt.Errorf("commit Entity availability report: %w", commitErr)
	}
	return write.ReportedAt.UTC(), nil
}

func persistEntityAvailability(
	ctx context.Context,
	queries *dbsqlc.Queries,
	write AvailabilityBatchWrite,
	report EntityAvailabilityReport,
) error {
	owner, err := queries.GetEntityOwner(ctx, dbsqlc.GetEntityOwnerParams{EntityID: string(report.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return &EntityAvailabilityReportError{EntityID: report.EntityID, Err: ErrEntityNotFound}
	}
	if err != nil {
		return fmt.Errorf("get Entity availability owner: %w", err)
	}
	if owner != write.AdapterID {
		return &EntityAvailabilityReportError{EntityID: report.EntityID, Err: ErrEntityWrongAdapter}
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

type sqliteAdapterView struct {
	adapterID                string
	healthStatus             string
	healthReasonCode         sql.NullString
	healthSince              string
	healthEvidenceAt         string
	externalStatus           sql.NullString
	externalReasonCode       sql.NullString
	externalSourceObservedAt sql.NullString
	externalEvidenceAt       sql.NullString
	runtimeID                sql.NullString
	softwareName             sql.NullString
	softwareVersion          sql.NullString
	claimedAt                sql.NullString
	lastHeartbeatAt          sql.NullString
	leaseExpiresAt           sql.NullString
	endedAt                  sql.NullString
}

func (repository *SQLiteRepository) GetAdapter(ctx context.Context, adapterID string) (AdapterInstance, error) {
	row, err := repository.queries.GetAdapterView(ctx, dbsqlc.GetAdapterViewParams{
		AdapterID: adapterID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return AdapterInstance{}, ErrAdapterNotFound
	}
	if err != nil {
		return AdapterInstance{}, fmt.Errorf("get Adapter: %w", err)
	}
	return adapterInstanceFromView(sqliteAdapterView{
		adapterID: row.AdapterID, healthStatus: row.HealthStatus,
		healthReasonCode: row.HealthReasonCode, healthSince: row.HealthSince,
		healthEvidenceAt: row.HealthEvidenceAt, externalStatus: row.ExternalSystemStatus,
		externalReasonCode:       row.ExternalSystemReasonCode,
		externalSourceObservedAt: row.ExternalSystemSourceObservedAt,
		externalEvidenceAt:       row.ExternalSystemEvidenceAt, runtimeID: row.RuntimeID,
		softwareName: row.SoftwareName, softwareVersion: row.SoftwareVersion, claimedAt: row.ClaimedAt,
		lastHeartbeatAt: row.LastHeartbeatAt, leaseExpiresAt: row.LeaseExpiresAt, endedAt: row.EndedAt,
	})
}

func (repository *SQLiteRepository) ListAdapters(
	ctx context.Context,
	params ListAdaptersParams,
) (Page[AdapterInstance], error) {
	if params.Limit < 1 {
		return Page[AdapterInstance]{}, ErrInvalidPage
	}
	queries := repository.queries
	limit := int64(params.Limit + 1)
	var views []sqliteAdapterView
	if params.AfterID == nil {
		rows, err := queries.ListAdapterViewsFirstPage(ctx, dbsqlc.ListAdapterViewsFirstPageParams{
			PageLimit: limit,
		})
		if err != nil {
			return Page[AdapterInstance]{}, fmt.Errorf("list Adapters: %w", err)
		}
		views = make([]sqliteAdapterView, len(rows))
		for index, row := range rows {
			views[index] = sqliteAdapterView{
				adapterID: row.AdapterID, healthStatus: row.HealthStatus,
				healthReasonCode: row.HealthReasonCode, healthSince: row.HealthSince,
				healthEvidenceAt: row.HealthEvidenceAt, externalStatus: row.ExternalSystemStatus,
				externalReasonCode:       row.ExternalSystemReasonCode,
				externalSourceObservedAt: row.ExternalSystemSourceObservedAt,
				externalEvidenceAt:       row.ExternalSystemEvidenceAt, runtimeID: row.RuntimeID,
				softwareName: row.SoftwareName, softwareVersion: row.SoftwareVersion, claimedAt: row.ClaimedAt,
				lastHeartbeatAt: row.LastHeartbeatAt, leaseExpiresAt: row.LeaseExpiresAt, endedAt: row.EndedAt,
			}
		}
	} else {
		rows, err := queries.ListAdapterViewsAfter(ctx, dbsqlc.ListAdapterViewsAfterParams{
			AfterAdapterID: *params.AfterID, PageLimit: limit,
		})
		if err != nil {
			return Page[AdapterInstance]{}, fmt.Errorf("list Adapters after cursor: %w", err)
		}
		views = make([]sqliteAdapterView, len(rows))
		for index, row := range rows {
			views[index] = sqliteAdapterView{
				adapterID: row.AdapterID, healthStatus: row.HealthStatus,
				healthReasonCode: row.HealthReasonCode, healthSince: row.HealthSince,
				healthEvidenceAt: row.HealthEvidenceAt, externalStatus: row.ExternalSystemStatus,
				externalReasonCode:       row.ExternalSystemReasonCode,
				externalSourceObservedAt: row.ExternalSystemSourceObservedAt,
				externalEvidenceAt:       row.ExternalSystemEvidenceAt, runtimeID: row.RuntimeID,
				softwareName: row.SoftwareName, softwareVersion: row.SoftwareVersion, claimedAt: row.ClaimedAt,
				lastHeartbeatAt: row.LastHeartbeatAt, leaseExpiresAt: row.LeaseExpiresAt, endedAt: row.EndedAt,
			}
		}
	}
	page := Page[AdapterInstance]{HasMore: len(views) > params.Limit}
	if page.HasMore {
		views = views[:params.Limit]
	}
	page.Items = make([]AdapterInstance, len(views))
	for index, view := range views {
		instance, err := adapterInstanceFromView(view)
		if err != nil {
			return Page[AdapterInstance]{}, fmt.Errorf("map listed Adapter: %w", err)
		}
		page.Items[index] = instance
	}
	return page, nil
}

func adapterInstanceFromView(view sqliteAdapterView) (AdapterInstance, error) {
	since, err := parseTime(view.healthSince)
	if err != nil {
		return AdapterInstance{}, fmt.Errorf("parse Adapter health since: %w", err)
	}
	evidenceAt, err := parseTime(view.healthEvidenceAt)
	if err != nil {
		return AdapterInstance{}, fmt.Errorf("parse Adapter health evidence: %w", err)
	}
	instance := AdapterInstance{ID: view.adapterID, Health: AdapterHealth{
		Status: AdapterHealthStatus(view.healthStatus), Since: since, EvidenceAt: evidenceAt,
		Reason: healthReasonFromNull(view.healthReasonCode),
	}}
	if view.runtimeID.Valid {
		claimed, parseErr := parseRequiredTime(view.claimedAt, "Adapter runtime claimed_at")
		if parseErr != nil {
			return AdapterInstance{}, parseErr
		}
		lease, parseErr := parseRequiredTime(view.leaseExpiresAt, "Adapter runtime lease_expires_at")
		if parseErr != nil {
			return AdapterInstance{}, parseErr
		}
		lastHeartbeat, parseErr := parseOptionalTime(view.lastHeartbeatAt)
		if parseErr != nil {
			return AdapterInstance{}, fmt.Errorf("parse Adapter runtime last_heartbeat_at: %w", parseErr)
		}
		status := runtimeStatusOnline
		if view.endedAt.Valid {
			status = runtimeStatusOffline
		}
		instance.Health.Runtime = &RuntimeEvidence{
			ID: RuntimeID(view.runtimeID.String), Status: status, SoftwareName: view.softwareName.String,
			SoftwareVersion: view.softwareVersion.String, ClaimedAt: claimed, LastHeartbeatAt: lastHeartbeat,
			LeaseExpiresAt: lease,
		}
	}
	if view.externalStatus.Valid {
		sourceObservedAt, parseErr := parseRequiredTime(view.externalSourceObservedAt, "external-system source time")
		if parseErr != nil {
			return AdapterInstance{}, parseErr
		}
		externalObservedAt, parseErr := parseRequiredTime(view.externalEvidenceAt, "external-system evidence time")
		if parseErr != nil {
			return AdapterInstance{}, parseErr
		}
		instance.Health.ExternalSystem = &ExternalSystemEvidence{
			Status: AdapterHealthStatus(view.externalStatus.String), SourceObservedAt: sourceObservedAt,
			EvidenceAt: externalObservedAt,
			Reason:     healthReasonFromNull(view.externalReasonCode),
		}
	}
	return instance, nil
}

func (repository *SQLiteRepository) ListAdapterHealthHistory(
	ctx context.Context,
	params ListAdapterHealthParams,
) (Page[HealthTransition], error) {
	if _, err := repository.GetAdapter(ctx, params.AdapterID); err != nil {
		return Page[HealthTransition]{}, err
	}
	queries := repository.queries
	transitions, err := listAdapterTransitions(ctx, queries, params, int64(params.Limit+1))
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return trimTransitionPage(transitions, params.Limit), nil
}

func listAdapterTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params ListAdapterHealthParams,
	limit int64,
) ([]HealthTransition, error) {
	if params.BeforeReceiveOrder == nil {
		return listFirstAdapterTransitions(ctx, queries, params.AdapterID, limit)
	}
	return listAdapterTransitionsBefore(ctx, queries, params.AdapterID, *params.BeforeReceiveOrder, limit)
}

func listFirstAdapterTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListAdapterHealthHistoryFirstPage(ctx, dbsqlc.ListAdapterHealthHistoryFirstPageParams{
		AdapterID: adapterID, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list Adapter health history: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func listAdapterTransitionsBefore(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	before int64,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListAdapterHealthHistoryBefore(ctx, dbsqlc.ListAdapterHealthHistoryBeforeParams{
		AdapterID: adapterID, ReceiveOrder: before, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list Adapter health history before cursor: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func (repository *SQLiteRepository) ListEntityAvailabilityHistory(
	ctx context.Context,
	params ListEntityAvailabilityParams,
) (Page[HealthTransition], error) {
	var exists int
	if err := repository.database.QueryRowContext(ctx, "SELECT 1 FROM entities WHERE id = ?", params.EntityID).
		Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Page[HealthTransition]{}, ErrEntityNotFound
	} else if err != nil {
		return Page[HealthTransition]{}, fmt.Errorf("get Entity for availability history: %w", err)
	}
	queries := repository.queries
	transitions, err := listEntityTransitions(ctx, queries, params, int64(params.Limit+1))
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return trimTransitionPage(transitions, params.Limit), nil
}

func listEntityTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params ListEntityAvailabilityParams,
	limit int64,
) ([]HealthTransition, error) {
	if params.BeforeReceiveOrder == nil {
		return listFirstEntityTransitions(ctx, queries, params.EntityID, limit)
	}
	return listEntityTransitionsBefore(ctx, queries, params.EntityID, *params.BeforeReceiveOrder, limit)
}

func listFirstEntityTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	entityID EntityID,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListEntityAvailabilityHistoryFirstPage(
		ctx,
		dbsqlc.ListEntityAvailabilityHistoryFirstPageParams{
			EntityID: nullableText(string(entityID)), PageLimit: limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list Entity availability history: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func listEntityTransitionsBefore(
	ctx context.Context,
	queries *dbsqlc.Queries,
	entityID EntityID,
	before int64,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListEntityAvailabilityHistoryBefore(
		ctx,
		dbsqlc.ListEntityAvailabilityHistoryBeforeParams{
			EntityID: nullableText(string(entityID)), BeforeReceiveOrder: before, PageLimit: limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list Entity availability history before cursor: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

type sqliteTransition struct {
	receiveOrder     int64
	status           string
	source           string
	reasonCode       sql.NullString
	sourceObservedAt sql.NullString
	observedAt       string
}

func newSQLiteTransition(
	receiveOrder int64,
	status string,
	source string,
	reasonCode sql.NullString,
	sourceObservedAt sql.NullString,
	observedAt string,
) sqliteTransition {
	return sqliteTransition{
		receiveOrder: receiveOrder, status: status, source: source,
		reasonCode: reasonCode, sourceObservedAt: sourceObservedAt, observedAt: observedAt,
	}
}

func healthTransitionsFromValues(values []sqliteTransition) ([]HealthTransition, error) {
	transitions := make([]HealthTransition, len(values))
	for index, value := range values {
		transition, err := healthTransitionFromValues(
			value.receiveOrder, value.status, value.source, value.reasonCode,
			value.sourceObservedAt, value.observedAt,
		)
		if err != nil {
			return nil, err
		}
		transitions[index] = transition
	}
	return transitions, nil
}

func healthTransitionFromValues(
	receiveOrder int64,
	status, source string,
	reasonCode, sourceObservedAt sql.NullString,
	observedAt string,
) (HealthTransition, error) {
	observed, err := parseTime(observedAt)
	if err != nil {
		return HealthTransition{}, fmt.Errorf("parse health transition observed_at: %w", err)
	}
	sourceObserved, err := parseOptionalTime(sourceObservedAt)
	if err != nil {
		return HealthTransition{}, fmt.Errorf("parse health transition source_observed_at: %w", err)
	}
	return HealthTransition{
		ReceiveOrder: receiveOrder, Status: status, Source: source,
		Reason: healthReasonFromNull(reasonCode), SourceObservedAt: sourceObserved,
		ObservedAt: observed,
	}, nil
}

func trimTransitionPage(transitions []HealthTransition, limit int) Page[HealthTransition] {
	page := Page[HealthTransition]{Items: transitions, HasMore: len(transitions) > limit}
	if page.HasMore {
		page.Items = page.Items[:limit]
	}
	return page
}

func parseRequiredTime(value sql.NullString, name string) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, fmt.Errorf("%s is missing", name)
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func formatNullableTime(value time.Time) sql.NullString {
	if value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(value), Valid: true}
}

func nullableReasonCode(reason *HealthReason) sql.NullString {
	if reason == nil {
		return sql.NullString{}
	}
	return nullableText(reason.Code)
}

func healthReasonCode(reason *HealthReason) string {
	if reason == nil {
		return ""
	}
	return reason.Code
}

func healthReasonFromNull(code sql.NullString) *HealthReason {
	if !code.Valid {
		return nil
	}
	return &HealthReason{Code: code.String}
}

func copyHealthReason(reason *HealthReason) *HealthReason {
	if reason == nil {
		return nil
	}
	return &HealthReason{Code: reason.Code}
}
