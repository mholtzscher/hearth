package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) ClaimAdapterRuntime(
	ctx context.Context,
	write devices.ClaimRuntimeWrite,
) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin Adapter runtime claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)

	repeated, err := repeatedRuntimeClaim(ctx, queries, write)
	if err != nil {
		return err
	}
	if repeated {
		return nil
	}
	instance, err := claimAdapterInstance(ctx, queries, write)
	if err != nil {
		return err
	}
	if activeErr := rejectActiveRuntimeClaim(ctx, queries, instance); activeErr != nil {
		return activeErr
	}

	if insertErr := queries.InsertRuntime(ctx, dbsqlc.InsertRuntimeParams{
		RuntimeID: string(write.RuntimeID), AdapterID: write.AdapterID,
		SoftwareName: write.SoftwareName, SoftwareVersion: write.SoftwareVersion,
		ClaimedAt: formatTime(write.ClaimedAt), LeaseExpiresAt: formatTime(write.LeaseExpiresAt),
	}); insertErr != nil {
		return fmt.Errorf("insert Adapter runtime: %w", insertErr)
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, dbsqlc.UpdateAdapterCurrentHealthParams{
		ActiveRuntimeID:  nullableText(string(write.RuntimeID)),
		HealthRuntimeID:  nullableText(string(write.RuntimeID)),
		HealthStatus:     string(devices.AdapterHealthUnknown),
		HealthReasonCode: nullableText("hearth.awaiting_health"),
		HealthSource:     healthSourceCore,
		HealthSince:      formatTime(write.ClaimedAt),
		HealthEvidenceAt: formatTime(write.ClaimedAt),
		AdapterID:        write.AdapterID,
	}); updateErr != nil {
		return fmt.Errorf("activate Adapter runtime: %w", updateErr)
	}
	if transitionErr := appendAdapterHealthTransition(ctx, queries, dbsqlc.InsertHealthTransitionParams{
		ResourceKind: healthResourceAdapter, AdapterID: write.AdapterID,
		RuntimeID:  nullableText(string(write.RuntimeID)),
		Status:     string(devices.AdapterHealthUnknown),
		Source:     healthSourceCore,
		ReasonCode: nullableText("hearth.awaiting_health"),
		ObservedAt: formatTime(write.ClaimedAt),
	}); transitionErr != nil {
		return transitionErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit Adapter runtime claim: %w", commitErr)
	}
	return nil
}

func repeatedRuntimeClaim(
	ctx context.Context,
	queries *dbsqlc.Queries,
	write devices.ClaimRuntimeWrite,
) (bool, error) {
	previous, err := queries.GetRuntime(ctx, dbsqlc.GetRuntimeParams{RuntimeID: string(write.RuntimeID)})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get claim retry: %w", err)
	}
	if previous.AdapterID != write.AdapterID || previous.SoftwareName != write.SoftwareName ||
		previous.SoftwareVersion != write.SoftwareVersion {
		return false, fmt.Errorf(
			"%w: runtime ID was already used with different Adapter metadata",
			devices.ErrRuntimeClaimConflict,
		)
	}
	instance, err := queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{AdapterID: write.AdapterID})
	if err != nil {
		return false, fmt.Errorf("get Adapter instance for claim retry: %w", err)
	}
	if previous.EndedAt.Valid || !instance.ActiveRuntimeID.Valid ||
		instance.ActiveRuntimeID.String != string(write.RuntimeID) {
		return false, fmt.Errorf("%w: runtime is no longer active", devices.ErrRuntimeClaimConflict)
	}
	return true, nil
}

func claimAdapterInstance(
	ctx context.Context,
	queries *dbsqlc.Queries,
	write devices.ClaimRuntimeWrite,
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
		HealthStatus:     string(devices.AdapterHealthUnknown),
		HealthReasonCode: nullableText("hearth.awaiting_health"),
		HealthSource:     healthSourceCore,
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
	return &devices.AdapterActiveError{RetryAfter: leaseExpiresAt}
}

func (repository *DeviceRepository) RecordAdapterHeartbeat(
	ctx context.Context,
	write devices.HeartbeatWrite,
) (devices.HeartbeatResult, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.HeartbeatResult{}, fmt.Errorf("begin Adapter heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	instance, err := activeAdapterInstance(ctx, queries, write.AdapterID, write.RuntimeID)
	if err != nil {
		return devices.HeartbeatResult{}, err
	}
	if write.ExternalStatus == devices.AdapterHealthUnknown &&
		instance.HealthStatus != string(devices.AdapterHealthUnknown) {
		return devices.HeartbeatResult{}, fmt.Errorf(
			"%w: adapter health cannot return to unknown in one runtime",
			devices.ErrInvalidHealthTransition,
		)
	}
	rows, err := queries.UpdateRuntimeHeartbeat(ctx, dbsqlc.UpdateRuntimeHeartbeatParams{
		LastHeartbeatAt: formatNullableTime(write.ReceivedAt), LeaseExpiresAt: formatTime(write.LeaseExpiresAt),
		RuntimeID: string(write.RuntimeID), AdapterID: write.AdapterID,
	})
	if err != nil {
		return devices.HeartbeatResult{}, fmt.Errorf("renew Adapter heartbeat lease: %w", err)
	}
	if rows != 1 {
		return devices.HeartbeatResult{}, devices.ErrRuntimeFenced
	}

	reason := effectiveHeartbeatReason(write)
	changed := healthChanged(instance, write.ExternalStatus, reason)
	since := write.ReceivedAt
	if !changed {
		since, err = parseTime(instance.HealthSince)
		if err != nil {
			return devices.HeartbeatResult{}, fmt.Errorf("parse Adapter health since: %w", err)
		}
	}
	if updateErr := queries.UpdateAdapterCurrentHealth(ctx, dbsqlc.UpdateAdapterCurrentHealthParams{
		ActiveRuntimeID:        nullableText(string(write.RuntimeID)),
		HealthRuntimeID:        nullableText(string(write.RuntimeID)),
		HealthStatus:           string(write.ExternalStatus),
		HealthReasonCode:       nullableReasonCode(reason),
		HealthSource:           healthSourceAdapter,
		HealthSince:            formatTime(since),
		HealthEvidenceAt:       formatTime(write.ReceivedAt),
		HealthSourceObservedAt: formatNullableTime(write.SourceObservedAt),
		AdapterID:              write.AdapterID,
	}); updateErr != nil {
		return devices.HeartbeatResult{}, fmt.Errorf("update Adapter heartbeat health: %w", updateErr)
	}
	if invalidateErr := invalidateAdapterEntityAvailability(
		ctx, queries, instance, write.ExternalStatus,
	); invalidateErr != nil {
		return devices.HeartbeatResult{}, invalidateErr
	}
	if changed {
		if transitionErr := appendAdapterHealthTransition(ctx, queries, dbsqlc.InsertHealthTransitionParams{
			ResourceKind:     healthResourceAdapter,
			AdapterID:        write.AdapterID,
			RuntimeID:        nullableText(string(write.RuntimeID)),
			Status:           string(write.ExternalStatus),
			Source:           healthSourceAdapter,
			ReasonCode:       nullableReasonCode(reason),
			SourceObservedAt: formatNullableTime(write.SourceObservedAt),
			ObservedAt:       formatTime(write.ReceivedAt),
		}); transitionErr != nil {
			return devices.HeartbeatResult{}, transitionErr
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return devices.HeartbeatResult{}, fmt.Errorf("commit Adapter heartbeat: %w", commitErr)
	}
	return devices.HeartbeatResult{LeaseExpiresAt: write.LeaseExpiresAt.UTC()}, nil
}

func effectiveHeartbeatReason(write devices.HeartbeatWrite) *devices.HealthReason {
	if write.ExternalStatus == devices.AdapterHealthUnknown {
		return &devices.HealthReason{Code: "hearth.awaiting_health"}
	}
	return devices.CopyHealthReason(write.Reason)
}

func (repository *DeviceRepository) ReleaseAdapterRuntime(
	ctx context.Context,
	write devices.ReleaseRuntimeWrite,
) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin Adapter runtime release: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	runtime, err := queries.GetRuntime(ctx, dbsqlc.GetRuntimeParams{RuntimeID: string(write.RuntimeID)})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && runtime.AdapterID != write.AdapterID) {
		return devices.ErrRuntimeFenced
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
			return devices.ErrRuntimeFenced
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
		return devices.ErrRuntimeFenced
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

func (repository *DeviceRepository) ExpireAdapterLeases(
	ctx context.Context,
	write devices.ExpireLeasesWrite,
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
		return devices.ErrRuntimeFenced
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
	changed := instance.HealthStatus != string(devices.AdapterHealthUnhealthy) ||
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
		HealthRuntimeID:  nullableText(runtime.RuntimeID),
		HealthStatus:     string(devices.AdapterHealthUnhealthy),
		HealthReasonCode: nullableText(reasonCode),
		HealthSource:     healthSourceCore,
		HealthSince:      formatTime(since),
		HealthEvidenceAt: formatTime(observedAt),
		AdapterID:        instance.AdapterID,
	}); updateErr != nil {
		return fmt.Errorf("mark Adapter runtime offline: %w", updateErr)
	}
	if invalidateErr := invalidateAdapterEntityAvailability(
		ctx, queries, instance, devices.AdapterHealthUnhealthy,
	); invalidateErr != nil {
		return invalidateErr
	}
	if !changed {
		return nil
	}
	return appendAdapterHealthTransition(ctx, queries, dbsqlc.InsertHealthTransitionParams{
		ResourceKind: healthResourceAdapter,
		AdapterID:    instance.AdapterID,
		RuntimeID:    nullableText(runtime.RuntimeID),
		Status:       string(devices.AdapterHealthUnhealthy),
		Source:       healthSourceCore,
		ReasonCode:   nullableText(reasonCode),
		ObservedAt:   formatTime(observedAt),
	})
}

func activeAdapterInstance(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	runtimeID devices.RuntimeID,
) (dbsqlc.AdapterInstance, error) {
	instance, err := queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{AdapterID: adapterID})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!instance.ActiveRuntimeID.Valid ||
		instance.ActiveRuntimeID.String != string(runtimeID))) {
		return dbsqlc.AdapterInstance{}, devices.ErrRuntimeFenced
	}
	if err != nil {
		return dbsqlc.AdapterInstance{}, fmt.Errorf("get active Adapter runtime: %w", err)
	}
	return instance, nil
}

func healthChanged(
	instance dbsqlc.AdapterInstance,
	status devices.AdapterHealthStatus,
	reason *devices.HealthReason,
) bool {
	return instance.HealthStatus != string(status) ||
		instance.HealthReasonCode.String != healthReasonCode(reason)
}
