package devices

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maximumHealthReasonCodeLength = 128
	maximumHealthReasonDetail     = 512
)

var healthReasonCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)+$`)

type healthEvaluationState struct {
	mutex         sync.RWMutex
	paused        bool
	recovering    bool
	epoch         uint64
	resumedAt     time.Time
	recoveryUntil time.Time
	refreshed     map[RuntimeID]struct{}
}

type healthEvaluationSnapshot struct {
	active        bool
	resumedAt     time.Time
	recoveryUntil time.Time
	refreshed     map[RuntimeID]struct{}
}

func newHealthEvaluationState() healthEvaluationState {
	return healthEvaluationState{paused: true, refreshed: make(map[RuntimeID]struct{})}
}

func (service *Service) PauseHealthEvaluation() {
	service.healthEvaluation.mutex.Lock()
	defer service.healthEvaluation.mutex.Unlock()
	service.healthEvaluation.paused = true
}

func (service *Service) ResumeHealthEvaluation(resumedAt time.Time) {
	service.healthEvaluation.mutex.Lock()
	defer service.healthEvaluation.mutex.Unlock()
	if !service.healthEvaluation.paused {
		return
	}
	resumedAt = resumedAt.UTC()
	service.healthEvaluation.paused = false
	service.healthEvaluation.recovering = true
	service.healthEvaluation.epoch++
	service.healthEvaluation.resumedAt = resumedAt
	service.healthEvaluation.recoveryUntil = resumedAt.Add(adapterLeaseDuration)
	service.healthEvaluation.refreshed = make(map[RuntimeID]struct{})
}

func (service *Service) ClaimAdapterRuntime(
	ctx context.Context,
	params ClaimAdapterRuntimeParams,
) (RuntimeClaim, error) {
	unlock, err := service.beginHealthEvaluation()
	if err != nil {
		return RuntimeClaim{}, err
	}
	defer unlock()
	if validationErr := validateClaimAdapterRuntime(params); validationErr != nil {
		return RuntimeClaim{}, validationErr
	}
	runtimeID, err := service.dependencies.NewRuntimeID()
	if err != nil {
		return RuntimeClaim{}, fmt.Errorf("generate Adapter runtime ID: %w", err)
	}
	claimedAt := service.dependencies.Now().UTC()
	return service.repository.ClaimAdapterRuntime(ctx, ClaimRuntimeWrite{
		ClaimID: params.ClaimID, RuntimeID: runtimeID, AdapterID: params.AdapterID,
		SoftwareName: params.SoftwareName, SoftwareVersion: params.SoftwareVersion,
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(adapterLeaseDuration),
	})
}

func (service *Service) RecordAdapterHeartbeat(
	ctx context.Context,
	heartbeat AdapterHeartbeat,
) (HeartbeatResult, error) {
	var epoch uint64
	var needsRefresh bool
	result, err := func() (HeartbeatResult, error) {
		unlock, beginErr := service.beginHealthEvaluation()
		if beginErr != nil {
			return HeartbeatResult{}, beginErr
		}
		defer unlock()
		if validateErr := validateAdapterHeartbeat(heartbeat); validateErr != nil {
			return HeartbeatResult{}, validateErr
		}
		if heartbeat.Reason != nil && strings.HasPrefix(heartbeat.Reason.Code, "adapter.") {
			softwareName, softwareErr := service.runtimeSoftwareName(ctx, heartbeat.AdapterID, heartbeat.RuntimeID)
			if softwareErr != nil {
				return HeartbeatResult{}, softwareErr
			}
			if namespaceErr := validateHealthReasonNamespace(heartbeat.Reason.Code, softwareName); namespaceErr != nil {
				return HeartbeatResult{}, namespaceErr
			}
		}
		epoch, needsRefresh = service.heartbeatRecoveryState(heartbeat.RuntimeID)
		receivedAt := service.dependencies.Now().UTC()
		return service.repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
			AdapterID: heartbeat.AdapterID, RuntimeID: heartbeat.RuntimeID,
			ExternalStatus: heartbeat.ExternalStatus, SourceObservedAt: heartbeat.SourceObservedAt.UTC(),
			Reason: copyHealthReason(heartbeat.Reason), ReceivedAt: receivedAt,
			LeaseExpiresAt: receivedAt.Add(adapterLeaseDuration),
		})
	}()
	if err != nil {
		return HeartbeatResult{}, err
	}
	service.markRecoveryHeartbeat(epoch, heartbeat.RuntimeID)
	result.RefreshEntityAvailability = result.RefreshEntityAvailability || needsRefresh
	return result, nil
}

func (service *Service) ReleaseAdapterRuntime(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
) error {
	unlock, err := service.beginHealthEvaluation()
	if err != nil {
		return err
	}
	defer unlock()
	if !registrationSlugPattern.MatchString(adapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	if _, parseErr := ParseRuntimeID(string(runtimeID)); parseErr != nil {
		return fmt.Errorf("parse Adapter runtime ID: %w", parseErr)
	}
	return service.repository.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
		AdapterID: adapterID, RuntimeID: runtimeID, ReleasedAt: service.dependencies.Now().UTC(),
	})
}

func (service *Service) ExpireAdapterLeases(ctx context.Context, expiresAt time.Time) error {
	var epoch uint64
	var completesRecovery bool
	err := func() error {
		unlock, beginErr := service.beginHealthEvaluation()
		if beginErr != nil {
			return beginErr
		}
		defer unlock()
		if expiresAt.IsZero() {
			return errors.New("adapter lease evaluation time is required")
		}
		expiresAt = expiresAt.UTC()
		if service.healthEvaluation.recovering && expiresAt.Before(service.healthEvaluation.recoveryUntil) {
			return nil
		}
		epoch = service.healthEvaluation.epoch
		completesRecovery = service.healthEvaluation.recovering
		return service.repository.ExpireAdapterLeases(ctx, ExpireLeasesWrite{ExpiresAt: expiresAt})
	}()
	if err != nil {
		return err
	}
	if completesRecovery {
		service.completeRecovery(epoch)
	}
	return nil
}

func (service *Service) beginHealthEvaluation() (func(), error) {
	service.healthEvaluation.mutex.RLock()
	if service.healthEvaluation.paused {
		service.healthEvaluation.mutex.RUnlock()
		return nil, ErrHealthEvaluationPaused
	}
	return service.healthEvaluation.mutex.RUnlock, nil
}

func (service *Service) heartbeatRecoveryState(runtimeID RuntimeID) (uint64, bool) {
	_, refreshed := service.healthEvaluation.refreshed[runtimeID]
	return service.healthEvaluation.epoch, service.healthEvaluation.recovering && !refreshed
}

func (service *Service) markRecoveryHeartbeat(epoch uint64, runtimeID RuntimeID) {
	service.healthEvaluation.mutex.Lock()
	defer service.healthEvaluation.mutex.Unlock()
	if service.healthEvaluation.paused || !service.healthEvaluation.recovering ||
		service.healthEvaluation.epoch != epoch {
		return
	}
	service.healthEvaluation.refreshed[runtimeID] = struct{}{}
}

func (service *Service) completeRecovery(epoch uint64) {
	service.healthEvaluation.mutex.Lock()
	defer service.healthEvaluation.mutex.Unlock()
	if service.healthEvaluation.epoch != epoch {
		return
	}
	service.healthEvaluation.recovering = false
	service.healthEvaluation.refreshed = make(map[RuntimeID]struct{})
}

func (service *Service) healthEvaluationSnapshot() healthEvaluationSnapshot {
	service.healthEvaluation.mutex.RLock()
	defer service.healthEvaluation.mutex.RUnlock()
	snapshot := healthEvaluationSnapshot{
		active:    !service.healthEvaluation.paused && service.healthEvaluation.recovering,
		resumedAt: service.healthEvaluation.resumedAt, recoveryUntil: service.healthEvaluation.recoveryUntil,
		refreshed: make(map[RuntimeID]struct{}, len(service.healthEvaluation.refreshed)),
	}
	for runtimeID := range service.healthEvaluation.refreshed {
		snapshot.refreshed[runtimeID] = struct{}{}
	}
	return snapshot
}

func validateClaimAdapterRuntime(params ClaimAdapterRuntimeParams) error {
	if err := validateID(params.ClaimID, "clm"); err != nil {
		return fmt.Errorf("parse Adapter claim ID: %w", err)
	}
	if !registrationSlugPattern.MatchString(params.AdapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	if !registrationSlugPattern.MatchString(params.SoftwareName) {
		return errors.New("software name must be a subject-safe slug")
	}
	if !validLength(params.SoftwareVersion, maximumNameLength) {
		return errors.New("software version must contain 1 to 128 characters")
	}
	return nil
}

func validateAdapterHeartbeat(heartbeat AdapterHeartbeat) error {
	if !registrationSlugPattern.MatchString(heartbeat.AdapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	if _, err := ParseRuntimeID(string(heartbeat.RuntimeID)); err != nil {
		return fmt.Errorf("parse Adapter runtime ID: %w", err)
	}
	if heartbeat.SourceObservedAt.IsZero() {
		return errors.New("external-system source observation time is required")
	}
	switch heartbeat.ExternalStatus {
	case AdapterHealthUnknown, AdapterHealthHealthy:
		if heartbeat.Reason != nil {
			return fmt.Errorf("%s external-system health must omit a reason", heartbeat.ExternalStatus)
		}
	case AdapterHealthUnhealthy:
		if heartbeat.Reason == nil {
			return errors.New("unhealthy external-system health requires a reason")
		}
	default:
		return errors.New("external-system health status is invalid")
	}
	if heartbeat.Reason == nil {
		return nil
	}
	if err := validateHealthReason(heartbeat.Reason); err != nil {
		return err
	}
	if strings.HasPrefix(heartbeat.Reason.Code, "hearth.") {
		return nil
	}
	if strings.HasPrefix(heartbeat.Reason.Code, "adapter.") {
		return nil
	}
	return errors.New("health reason code must use the hearth or adapter namespace")
}

func validateHealthReason(reason *HealthReason) error {
	if !utf8.ValidString(reason.Code) || utf8.RuneCountInString(reason.Code) > maximumHealthReasonCodeLength ||
		!healthReasonCodePattern.MatchString(reason.Code) {
		return errors.New("health reason code must be a lowercase dotted identifier of at most 128 characters")
	}
	if reason.Detail != nil && !validLength(*reason.Detail, maximumHealthReasonDetail) {
		return errors.New("health reason detail must contain 1 to 512 characters")
	}
	return nil
}

func validateHealthReasonNamespace(code, softwareName string) error {
	if strings.HasPrefix(code, "hearth.") {
		return nil
	}
	if !strings.HasPrefix(code, "adapter."+softwareName+".") {
		return fmt.Errorf("adapter health reason must use adapter.%s", softwareName)
	}
	return nil
}

func (service *Service) runtimeSoftwareName(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
) (string, error) {
	instance, err := service.repository.GetAdapter(ctx, adapterID)
	if err != nil {
		return "", err
	}
	if instance.Health == nil || instance.Health.Runtime == nil ||
		instance.Health.Runtime.ID != runtimeID || instance.Health.Runtime.Status != runtimeStatusOnline {
		return "", ErrRuntimeFenced
	}
	return instance.Health.Runtime.SoftwareName, nil
}
