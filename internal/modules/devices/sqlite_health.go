package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	healthsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/health"
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
	queries := healthsqlc.New(tx)

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
	instance, err = expireRuntimeBeforeClaim(ctx, queries, instance, write)
	if err != nil {
		return RuntimeClaim{}, err
	}

	if insertErr := queries.InsertRuntime(ctx, healthsqlc.InsertRuntimeParams{
		RuntimeID: string(write.RuntimeID), ClaimID: write.ClaimID, AdapterID: write.AdapterID,
		SoftwareName: write.SoftwareName, SoftwareVersion: write.SoftwareVersion,
		ClaimedAt: formatTime(write.ClaimedAt), LeaseExpiresAt: formatTime(write.LeaseExpiresAt),
	}); insertErr != nil {
		return RuntimeClaim{}, fmt.Errorf("insert Adapter runtime: %w", insertErr)
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, healthsqlc.UpdateAdapterCurrentHealthParams{
		ActiveRuntimeID:   nullableText(string(write.RuntimeID)),
		HealthRuntimeID:   nullableText(string(write.RuntimeID)),
		HealthStatus:      nullableText(string(AdapterHealthUnknown)),
		HealthReasonCode:  nullableText("hearth.awaiting_health"),
		HealthSince:       formatNullableTime(write.ClaimedAt),
		HealthEvidenceAt:  formatNullableTime(write.ClaimedAt),
		AvailabilityEpoch: instance.AvailabilityEpoch,
		UpdatedAt:         formatTime(write.ClaimedAt),
		AdapterID:         write.AdapterID,
	}); updateErr != nil {
		return RuntimeClaim{}, fmt.Errorf("activate Adapter runtime: %w", updateErr)
	}
	if _, transitionErr := appendHealthTransition(ctx, queries, healthsqlc.InsertHealthTransitionParams{
		ResourceKind: healthResourceAdapter, AdapterID: write.AdapterID,
		RuntimeID: nullableText(string(write.RuntimeID)),
		Status:    string(AdapterHealthUnknown), Source: "core", ReasonCode: nullableText("hearth.awaiting_health"),
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
	queries *healthsqlc.Queries,
	write ClaimRuntimeWrite,
) (RuntimeClaim, bool, error) {
	previous, err := queries.GetRuntimeByClaimID(ctx, healthsqlc.GetRuntimeByClaimIDParams{ClaimID: write.ClaimID})
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
	queries *healthsqlc.Queries,
	write ClaimRuntimeWrite,
) (healthsqlc.AdapterInstance, error) {
	instance, err := queries.GetAdapterInstance(ctx, healthsqlc.GetAdapterInstanceParams{AdapterID: write.AdapterID})
	if err == nil {
		if instance.ArchivedAt.Valid {
			return healthsqlc.AdapterInstance{}, ErrAdapterArchived
		}
		return instance, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return healthsqlc.AdapterInstance{}, fmt.Errorf("get Adapter instance for claim: %w", err)
	}
	if createErr := queries.CreateAdapterInstance(ctx, healthsqlc.CreateAdapterInstanceParams{
		AdapterID:        write.AdapterID,
		CreatedAt:        formatTime(write.ClaimedAt),
		UpdatedAt:        formatTime(write.ClaimedAt),
		HealthStatus:     nullableText(string(AdapterHealthUnknown)),
		HealthReasonCode: nullableText("hearth.awaiting_health"),
		HealthSince:      formatNullableTime(write.ClaimedAt),
		HealthEvidenceAt: formatNullableTime(write.ClaimedAt),
	}); createErr != nil {
		return healthsqlc.AdapterInstance{}, fmt.Errorf("create Adapter instance: %w", createErr)
	}
	instance, err = queries.GetAdapterInstance(ctx, healthsqlc.GetAdapterInstanceParams{AdapterID: write.AdapterID})
	if err != nil {
		return healthsqlc.AdapterInstance{}, fmt.Errorf("get created Adapter instance: %w", err)
	}
	return instance, nil
}

func expireRuntimeBeforeClaim(
	ctx context.Context,
	queries *healthsqlc.Queries,
	instance healthsqlc.AdapterInstance,
	write ClaimRuntimeWrite,
) (healthsqlc.AdapterInstance, error) {
	if !instance.ActiveRuntimeID.Valid {
		return instance, nil
	}
	active, err := queries.GetRuntime(ctx, healthsqlc.GetRuntimeParams{
		RuntimeID: instance.ActiveRuntimeID.String,
	})
	if err != nil {
		return healthsqlc.AdapterInstance{}, fmt.Errorf("get active Adapter runtime: %w", err)
	}
	leaseExpiresAt, err := parseTime(active.LeaseExpiresAt)
	if err != nil {
		return healthsqlc.AdapterInstance{}, fmt.Errorf("parse active Adapter lease expiry: %w", err)
	}
	if leaseExpiresAt.After(write.ClaimedAt) {
		return healthsqlc.AdapterInstance{}, &AdapterActiveError{RetryAfter: leaseExpiresAt}
	}
	if expireErr := expireRuntime(ctx, queries, instance, active, write.ClaimedAt); expireErr != nil {
		return healthsqlc.AdapterInstance{}, expireErr
	}
	reloaded, err := queries.GetAdapterInstance(ctx, healthsqlc.GetAdapterInstanceParams{AdapterID: write.AdapterID})
	if err != nil {
		return healthsqlc.AdapterInstance{}, fmt.Errorf("reload Adapter after lease expiry: %w", err)
	}
	return reloaded, nil
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
	queries := healthsqlc.New(tx)
	instance, err := activeAdapterInstance(ctx, queries, write.AdapterID, write.RuntimeID)
	if err != nil {
		return HeartbeatResult{}, err
	}
	runtime, err := queries.GetRuntime(ctx, healthsqlc.GetRuntimeParams{RuntimeID: string(write.RuntimeID)})
	if err != nil {
		return HeartbeatResult{}, fmt.Errorf("get Adapter runtime for heartbeat: %w", err)
	}
	overdue, err := expireRuntimeIfOverdue(ctx, queries, instance, runtime, write.ReceivedAt)
	if err != nil {
		return HeartbeatResult{}, err
	}
	if overdue {
		return HeartbeatResult{}, commitExpiredRuntime(tx, "overdue Adapter heartbeat")
	}
	if write.ExternalStatus == AdapterHealthUnknown && instance.ExternalSystemStatus.Valid &&
		instance.ExternalSystemStatus.String != string(AdapterHealthUnknown) {
		return HeartbeatResult{}, errors.New("external-system health cannot return to unknown in one runtime")
	}
	rows, err := queries.UpdateRuntimeHeartbeat(ctx, healthsqlc.UpdateRuntimeHeartbeatParams{
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
		since, err = parseRequiredTime(instance.HealthSince, "Adapter health since")
		if err != nil {
			return HeartbeatResult{}, err
		}
	}
	epoch := instance.AvailabilityEpoch
	becameHealthy := write.ExternalStatus == AdapterHealthHealthy &&
		AdapterHealthStatus(instance.HealthStatus.String) != AdapterHealthHealthy
	if becameHealthy {
		epoch++
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, healthsqlc.UpdateAdapterCurrentHealthParams{
		ActiveRuntimeID:                nullableText(string(write.RuntimeID)),
		HealthRuntimeID:                nullableText(string(write.RuntimeID)),
		HealthStatus:                   nullableText(string(write.ExternalStatus)),
		HealthReasonCode:               nullableReasonCode(reason),
		HealthReasonDetail:             nullableReasonDetail(reason),
		HealthSince:                    formatNullableTime(since),
		HealthEvidenceAt:               formatNullableTime(write.ReceivedAt),
		ExternalSystemStatus:           nullableText(string(write.ExternalStatus)),
		ExternalSystemReasonCode:       nullableReasonCode(write.Reason),
		ExternalSystemReasonDetail:     nullableReasonDetail(write.Reason),
		ExternalSystemSourceObservedAt: formatNullableTime(write.SourceObservedAt),
		ExternalSystemEvidenceAt:       formatNullableTime(write.ReceivedAt),
		AvailabilityEpoch:              epoch,
		UpdatedAt:                      formatTime(write.ReceivedAt),
		AdapterID:                      write.AdapterID,
	}); updateErr != nil {
		return HeartbeatResult{}, fmt.Errorf("update Adapter heartbeat health: %w", updateErr)
	}
	if changed {
		if _, transitionErr := appendHealthTransition(ctx, queries, healthsqlc.InsertHealthTransitionParams{
			ResourceKind:     healthResourceAdapter,
			AdapterID:        write.AdapterID,
			RuntimeID:        nullableText(string(write.RuntimeID)),
			Status:           string(write.ExternalStatus),
			Source:           "external_system",
			ReasonCode:       nullableReasonCode(reason),
			ReasonDetail:     nullableReasonDetail(reason),
			SourceObservedAt: formatNullableTime(write.SourceObservedAt),
			ObservedAt:       formatTime(write.ReceivedAt),
		}); transitionErr != nil {
			return HeartbeatResult{}, transitionErr
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return HeartbeatResult{}, fmt.Errorf("commit Adapter heartbeat: %w", commitErr)
	}
	return HeartbeatResult{
		LeaseExpiresAt:            write.LeaseExpiresAt.UTC(),
		RefreshEntityAvailability: becameHealthy,
	}, nil
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
	queries := healthsqlc.New(tx)
	runtime, err := queries.GetRuntime(ctx, healthsqlc.GetRuntimeParams{RuntimeID: string(write.RuntimeID)})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && runtime.AdapterID != write.AdapterID) {
		return ErrRuntimeFenced
	}
	if err != nil {
		return fmt.Errorf("get Adapter runtime for release: %w", err)
	}
	if runtime.EndedAt.Valid {
		latest, latestErr := queries.GetLatestRuntimeForAdapter(ctx, healthsqlc.GetLatestRuntimeForAdapterParams{
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
	overdue, err := expireRuntimeIfOverdue(ctx, queries, instance, runtime, write.ReleasedAt)
	if err != nil {
		return err
	}
	if overdue {
		return commitExpiredRuntime(tx, "overdue Adapter release")
	}
	if rows, endErr := queries.EndRuntime(ctx, healthsqlc.EndRuntimeParams{
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
	queries := healthsqlc.New(tx)
	runtimes, err := queries.ListExpiredRuntimes(ctx, healthsqlc.ListExpiredRuntimesParams{
		ExpiresAt: formatTime(write.ExpiresAt),
	})
	if err != nil {
		return fmt.Errorf("list expired Adapter runtimes: %w", err)
	}
	for _, runtime := range runtimes {
		instance, instanceErr := queries.GetAdapterInstance(ctx, healthsqlc.GetAdapterInstanceParams{
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

func expireRuntimeIfOverdue(
	ctx context.Context,
	queries *healthsqlc.Queries,
	instance healthsqlc.AdapterInstance,
	runtime healthsqlc.AdapterRuntime,
	receivedAt time.Time,
) (bool, error) {
	leaseExpiresAt, err := parseTime(runtime.LeaseExpiresAt)
	if err != nil {
		return false, fmt.Errorf("parse Adapter runtime lease expiry: %w", err)
	}
	if leaseExpiresAt.After(receivedAt) {
		return false, nil
	}
	if expireErr := expireRuntime(ctx, queries, instance, runtime, receivedAt); expireErr != nil {
		return false, expireErr
	}
	return true, nil
}

func commitExpiredRuntime(tx *sql.Tx, operation string) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s expiry: %w", operation, err)
	}
	return ErrRuntimeFenced
}

func expireRuntime(
	ctx context.Context,
	queries *healthsqlc.Queries,
	instance healthsqlc.AdapterInstance,
	runtime healthsqlc.AdapterRuntime,
	expiredAt time.Time,
) error {
	rows, err := queries.EndRuntime(ctx, healthsqlc.EndRuntimeParams{
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
	queries *healthsqlc.Queries,
	instance healthsqlc.AdapterInstance,
	runtime healthsqlc.AdapterRuntime,
	observedAt time.Time,
	reasonCode string,
) error {
	changed := !instance.HealthStatus.Valid || instance.HealthStatus.String != string(AdapterHealthUnhealthy) ||
		instance.HealthReasonCode.String != reasonCode
	since := observedAt
	if !changed {
		var err error
		since, err = parseRequiredTime(instance.HealthSince, "Adapter health since")
		if err != nil {
			return err
		}
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, healthsqlc.UpdateAdapterCurrentHealthParams{
		HealthRuntimeID:                nullableText(runtime.RuntimeID),
		HealthStatus:                   nullableText(string(AdapterHealthUnhealthy)),
		HealthReasonCode:               nullableText(reasonCode),
		HealthSince:                    formatNullableTime(since),
		HealthEvidenceAt:               formatNullableTime(observedAt),
		ExternalSystemStatus:           instance.ExternalSystemStatus,
		ExternalSystemReasonCode:       instance.ExternalSystemReasonCode,
		ExternalSystemReasonDetail:     instance.ExternalSystemReasonDetail,
		ExternalSystemSourceObservedAt: instance.ExternalSystemSourceObservedAt,
		ExternalSystemEvidenceAt:       instance.ExternalSystemEvidenceAt,
		AvailabilityEpoch:              instance.AvailabilityEpoch,
		UpdatedAt:                      formatTime(observedAt),
		AdapterID:                      instance.AdapterID,
	}); updateErr != nil {
		return fmt.Errorf("mark Adapter runtime offline: %w", updateErr)
	}
	if !changed {
		return nil
	}
	_, err := appendHealthTransition(ctx, queries, healthsqlc.InsertHealthTransitionParams{
		ResourceKind: healthResourceAdapter,
		AdapterID:    instance.AdapterID,
		RuntimeID:    nullableText(runtime.RuntimeID),
		Status:       string(AdapterHealthUnhealthy),
		Source:       "core",
		ReasonCode:   nullableText(reasonCode),
		ObservedAt:   formatTime(observedAt),
	})
	return err
}

func activeAdapterInstance(
	ctx context.Context,
	queries *healthsqlc.Queries,
	adapterID string,
	runtimeID RuntimeID,
) (healthsqlc.AdapterInstance, error) {
	instance, err := queries.GetAdapterInstance(ctx, healthsqlc.GetAdapterInstanceParams{AdapterID: adapterID})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!instance.ActiveRuntimeID.Valid ||
		instance.ActiveRuntimeID.String != string(runtimeID))) {
		return healthsqlc.AdapterInstance{}, ErrRuntimeFenced
	}
	if err != nil {
		return healthsqlc.AdapterInstance{}, fmt.Errorf("get active Adapter runtime: %w", err)
	}
	return instance, nil
}

func appendHealthTransition(
	ctx context.Context,
	queries *healthsqlc.Queries,
	params healthsqlc.InsertHealthTransitionParams,
) (int64, error) {
	receiveOrder, err := queries.InsertHealthTransition(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("insert health transition: %w", err)
	}
	if params.ResourceKind == "adapter" {
		if updateErr := queries.SetLatestAdapterTransition(ctx, healthsqlc.SetLatestAdapterTransitionParams{
			LatestTransitionReceiveOrder: sql.NullInt64{Int64: receiveOrder, Valid: true},
			AdapterID:                    params.AdapterID,
		}); updateErr != nil {
			return 0, fmt.Errorf("update latest Adapter transition: %w", updateErr)
		}
	}
	return receiveOrder, nil
}

func healthChanged(
	instance healthsqlc.AdapterInstance,
	status AdapterHealthStatus,
	reason *HealthReason,
) bool {
	return !instance.HealthStatus.Valid || instance.HealthStatus.String != string(status) ||
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
	queries := healthsqlc.New(tx)
	instance, err := activeAdapterInstance(ctx, queries, write.AdapterID, write.RuntimeID)
	if err != nil {
		return time.Time{}, err
	}
	if instance.HealthStatus.String != string(AdapterHealthHealthy) {
		return time.Time{}, ErrAdapterUnhealthy
	}
	seen := make(map[EntityID]struct{}, len(write.Reports))
	for _, report := range write.Reports {
		if _, duplicate := seen[report.EntityID]; duplicate {
			return time.Time{}, fmt.Errorf("duplicate Entity availability report for %s", report.EntityID)
		}
		seen[report.EntityID] = struct{}{}
		if persistErr := persistEntityAvailability(ctx, queries, instance, write, report); persistErr != nil {
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
	queries *healthsqlc.Queries,
	instance healthsqlc.AdapterInstance,
	write AvailabilityBatchWrite,
	report EntityAvailabilityReport,
) error {
	owner, err := queries.GetEntityOwner(ctx, healthsqlc.GetEntityOwnerParams{EntityID: string(report.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrEntityNotFound, report.EntityID)
	}
	if err != nil {
		return fmt.Errorf("get Entity availability owner: %w", err)
	}
	if owner != write.AdapterID {
		return fmt.Errorf("%w: %s", ErrEntityWrongAdapter, report.EntityID)
	}
	current, err := queries.GetEntityAvailabilityCurrent(ctx, healthsqlc.GetEntityAvailabilityCurrentParams{
		EntityID: string(report.EntityID),
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("get current Entity availability: %w", err)
	}
	changed := errors.Is(err, sql.ErrNoRows) || current.AvailabilityEpoch != instance.AvailabilityEpoch ||
		current.Status != string(report.Status) || current.ReasonCode.String != healthReasonCode(report.Reason)
	since := write.ReportedAt
	if !changed {
		since, err = parseTime(current.CurrentSince)
		if err != nil {
			return fmt.Errorf("parse Entity availability since: %w", err)
		}
	}
	var receiveOrder sql.NullInt64
	if changed {
		order, transitionErr := appendHealthTransition(ctx, queries, healthsqlc.InsertHealthTransitionParams{
			ResourceKind:     "entity",
			AdapterID:        write.AdapterID,
			EntityID:         nullableText(string(report.EntityID)),
			RuntimeID:        nullableText(string(write.RuntimeID)),
			Status:           string(report.Status),
			Source:           "entity_report",
			ReasonCode:       nullableReasonCode(report.Reason),
			ReasonDetail:     nullableReasonDetail(report.Reason),
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
	if upsertErr := queries.UpsertEntityAvailabilityCurrent(ctx, healthsqlc.UpsertEntityAvailabilityCurrentParams{
		EntityID:                     string(report.EntityID),
		AdapterID:                    write.AdapterID,
		RuntimeID:                    string(write.RuntimeID),
		AvailabilityEpoch:            instance.AvailabilityEpoch,
		Status:                       string(report.Status),
		ReasonCode:                   nullableReasonCode(report.Reason),
		ReasonDetail:                 nullableReasonDetail(report.Reason),
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
	archivedAt               sql.NullString
	healthStatus             sql.NullString
	healthReasonCode         sql.NullString
	healthReasonDetail       sql.NullString
	healthSince              sql.NullString
	healthEvidenceAt         sql.NullString
	externalStatus           sql.NullString
	externalReasonCode       sql.NullString
	externalReasonDetail     sql.NullString
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
	row, err := healthsqlc.New(repository.database).GetAdapterView(ctx, healthsqlc.GetAdapterViewParams{
		AdapterID: adapterID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return AdapterInstance{}, ErrAdapterNotFound
	}
	if err != nil {
		return AdapterInstance{}, fmt.Errorf("get Adapter: %w", err)
	}
	return adapterInstanceFromView(sqliteAdapterView{
		adapterID: row.AdapterID, archivedAt: row.ArchivedAt, healthStatus: row.HealthStatus,
		healthReasonCode: row.HealthReasonCode, healthReasonDetail: row.HealthReasonDetail,
		healthSince: row.HealthSince, healthEvidenceAt: row.HealthEvidenceAt,
		externalStatus: row.ExternalSystemStatus, externalReasonCode: row.ExternalSystemReasonCode,
		externalReasonDetail:     row.ExternalSystemReasonDetail,
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
	queries := healthsqlc.New(repository.database)
	limit := int64(params.Limit + 1)
	var views []sqliteAdapterView
	if params.AfterID == nil {
		rows, err := queries.ListAdapterViewsFirstPage(ctx, healthsqlc.ListAdapterViewsFirstPageParams{
			IncludeArchived: boolToInt64(params.IncludeArchived), PageLimit: limit,
		})
		if err != nil {
			return Page[AdapterInstance]{}, fmt.Errorf("list Adapters: %w", err)
		}
		views = make([]sqliteAdapterView, len(rows))
		for index, row := range rows {
			views[index] = sqliteAdapterView{
				adapterID: row.AdapterID, archivedAt: row.ArchivedAt, healthStatus: row.HealthStatus,
				healthReasonCode: row.HealthReasonCode, healthReasonDetail: row.HealthReasonDetail,
				healthSince: row.HealthSince, healthEvidenceAt: row.HealthEvidenceAt,
				externalStatus: row.ExternalSystemStatus, externalReasonCode: row.ExternalSystemReasonCode,
				externalReasonDetail:     row.ExternalSystemReasonDetail,
				externalSourceObservedAt: row.ExternalSystemSourceObservedAt,
				externalEvidenceAt:       row.ExternalSystemEvidenceAt, runtimeID: row.RuntimeID,
				softwareName: row.SoftwareName, softwareVersion: row.SoftwareVersion, claimedAt: row.ClaimedAt,
				lastHeartbeatAt: row.LastHeartbeatAt, leaseExpiresAt: row.LeaseExpiresAt, endedAt: row.EndedAt,
			}
		}
	} else {
		rows, err := queries.ListAdapterViewsAfter(ctx, healthsqlc.ListAdapterViewsAfterParams{
			AfterAdapterID: *params.AfterID, IncludeArchived: boolToInt64(params.IncludeArchived), PageLimit: limit,
		})
		if err != nil {
			return Page[AdapterInstance]{}, fmt.Errorf("list Adapters after cursor: %w", err)
		}
		views = make([]sqliteAdapterView, len(rows))
		for index, row := range rows {
			views[index] = sqliteAdapterView{
				adapterID: row.AdapterID, archivedAt: row.ArchivedAt, healthStatus: row.HealthStatus,
				healthReasonCode: row.HealthReasonCode, healthReasonDetail: row.HealthReasonDetail,
				healthSince: row.HealthSince, healthEvidenceAt: row.HealthEvidenceAt,
				externalStatus: row.ExternalSystemStatus, externalReasonCode: row.ExternalSystemReasonCode,
				externalReasonDetail:     row.ExternalSystemReasonDetail,
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
	archived, err := parseOptionalTime(view.archivedAt)
	if err != nil {
		return AdapterInstance{}, fmt.Errorf("parse Adapter archived_at: %w", err)
	}
	instance := AdapterInstance{ID: view.adapterID, ArchivedAt: archived}
	if archived != nil {
		return instance, nil
	}
	since, err := parseRequiredTime(view.healthSince, "Adapter health since")
	if err != nil {
		return AdapterInstance{}, err
	}
	evidenceAt, err := parseRequiredTime(view.healthEvidenceAt, "Adapter health evidence")
	if err != nil {
		return AdapterInstance{}, err
	}
	instance.Health = &AdapterHealth{
		Status: AdapterHealthStatus(view.healthStatus.String), Since: since, EvidenceAt: evidenceAt,
		Reason: healthReasonFromNulls(view.healthReasonCode, view.healthReasonDetail),
	}
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
			Reason:     healthReasonFromNulls(view.externalReasonCode, view.externalReasonDetail),
		}
	}
	return instance, nil
}

func (repository *SQLiteRepository) ArchiveAdapter(ctx context.Context, params ArchiveAdapterParams) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin Adapter archival: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := healthsqlc.New(tx)
	instance, err := queries.GetAdapterInstance(ctx, healthsqlc.GetAdapterInstanceParams{AdapterID: params.AdapterID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAdapterNotFound
	}
	if err != nil {
		return fmt.Errorf("get Adapter for archival: %w", err)
	}
	if instance.ArchivedAt.Valid {
		return nil
	}
	if instance.ActiveRuntimeID.Valid {
		return ErrAdapterActive
	}
	bindings, err := queries.CountAdapterBindings(
		ctx,
		healthsqlc.CountAdapterBindingsParams{AdapterID: params.AdapterID},
	)
	if err != nil {
		return fmt.Errorf("count Adapter bindings: %w", err)
	}
	if bindings != 0 {
		return ErrAdapterHasBindings
	}
	if archiveErr := queries.ArchiveAdapter(ctx, healthsqlc.ArchiveAdapterParams{
		ArchivedAt: formatNullableTime(params.ArchivedAt), UpdatedAt: formatTime(params.ArchivedAt),
		AdapterID: params.AdapterID,
	}); archiveErr != nil {
		return fmt.Errorf("archive Adapter: %w", archiveErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit Adapter archival: %w", commitErr)
	}
	return nil
}

func (repository *SQLiteRepository) ListAdapterHealthHistory(
	ctx context.Context,
	params ListAdapterHealthParams,
) (Page[HealthTransition], error) {
	if _, err := repository.GetAdapter(ctx, params.AdapterID); err != nil {
		return Page[HealthTransition]{}, err
	}
	queries := healthsqlc.New(repository.database)
	transitions, err := listAdapterTransitions(ctx, queries, params, int64(params.Limit+1))
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return trimTransitionPage(transitions, params.Limit), nil
}

func listAdapterTransitions(
	ctx context.Context,
	queries *healthsqlc.Queries,
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
	queries *healthsqlc.Queries,
	adapterID string,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListAdapterHealthHistoryFirstPage(ctx, healthsqlc.ListAdapterHealthHistoryFirstPageParams{
		AdapterID: adapterID, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list Adapter health history: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.ReasonDetail, row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func listAdapterTransitionsBefore(
	ctx context.Context,
	queries *healthsqlc.Queries,
	adapterID string,
	before int64,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListAdapterHealthHistoryBefore(ctx, healthsqlc.ListAdapterHealthHistoryBeforeParams{
		AdapterID: adapterID, ReceiveOrder: before, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list Adapter health history before cursor: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.ReasonDetail, row.SourceObservedAt, row.ObservedAt,
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
	queries := healthsqlc.New(repository.database)
	transitions, err := listEntityTransitions(ctx, queries, params, int64(params.Limit+1))
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return trimTransitionPage(transitions, params.Limit), nil
}

func listEntityTransitions(
	ctx context.Context,
	queries *healthsqlc.Queries,
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
	queries *healthsqlc.Queries,
	entityID EntityID,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListEntityAvailabilityHistoryFirstPage(
		ctx,
		healthsqlc.ListEntityAvailabilityHistoryFirstPageParams{
			EntityID: nullableText(string(entityID)), Limit: limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list Entity availability history: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.ReasonDetail, row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func listEntityTransitionsBefore(
	ctx context.Context,
	queries *healthsqlc.Queries,
	entityID EntityID,
	before int64,
	limit int64,
) ([]HealthTransition, error) {
	rows, err := queries.ListEntityAvailabilityHistoryBefore(
		ctx,
		healthsqlc.ListEntityAvailabilityHistoryBeforeParams{
			EntityID: nullableText(string(entityID)), ReceiveOrder: before, Limit: limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list Entity availability history before cursor: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.ReasonDetail, row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

type sqliteTransition struct {
	receiveOrder     int64
	status           string
	source           string
	reasonCode       sql.NullString
	reasonDetail     sql.NullString
	sourceObservedAt sql.NullString
	observedAt       string
}

func newSQLiteTransition(
	receiveOrder int64,
	status string,
	source string,
	reasonCode sql.NullString,
	reasonDetail sql.NullString,
	sourceObservedAt sql.NullString,
	observedAt string,
) sqliteTransition {
	return sqliteTransition{
		receiveOrder: receiveOrder, status: status, source: source,
		reasonCode: reasonCode, reasonDetail: reasonDetail,
		sourceObservedAt: sourceObservedAt, observedAt: observedAt,
	}
}

func healthTransitionsFromValues(values []sqliteTransition) ([]HealthTransition, error) {
	transitions := make([]HealthTransition, len(values))
	for index, value := range values {
		transition, err := healthTransitionFromValues(
			value.receiveOrder, value.status, value.source, value.reasonCode,
			value.reasonDetail, value.sourceObservedAt, value.observedAt,
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
	reasonCode, reasonDetail, sourceObservedAt sql.NullString,
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
		Reason: healthReasonFromNulls(reasonCode, reasonDetail), SourceObservedAt: sourceObserved,
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

func nullableReasonDetail(reason *HealthReason) sql.NullString {
	if reason == nil || reason.Detail == nil {
		return sql.NullString{}
	}
	return nullableText(*reason.Detail)
}

func healthReasonCode(reason *HealthReason) string {
	if reason == nil {
		return ""
	}
	return reason.Code
}

func healthReasonFromNulls(code, detail sql.NullString) *HealthReason {
	if !code.Valid {
		return nil
	}
	reason := &HealthReason{Code: code.String}
	if detail.Valid {
		value := detail.String
		reason.Detail = &value
	}
	return reason
}

func copyHealthReason(reason *HealthReason) *HealthReason {
	if reason == nil {
		return nil
	}
	copied := &HealthReason{Code: reason.Code}
	if reason.Detail != nil {
		detail := *reason.Detail
		copied.Detail = &detail
	}
	return copied
}
