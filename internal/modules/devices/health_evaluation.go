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

type leaseExpiryState struct {
	mutex      sync.RWMutex
	paused     bool
	graceUntil time.Time
}

func newLeaseExpiryState() leaseExpiryState {
	return leaseExpiryState{paused: true}
}

func (service *Service) PauseAdapterLeaseExpiry() {
	service.leaseExpiry.mutex.Lock()
	defer service.leaseExpiry.mutex.Unlock()
	service.leaseExpiry.paused = true
}

func (service *Service) ResumeAdapterLeaseExpiry(resumedAt time.Time) {
	service.leaseExpiry.mutex.Lock()
	defer service.leaseExpiry.mutex.Unlock()
	if !service.leaseExpiry.paused {
		return
	}
	service.leaseExpiry.paused = false
	service.leaseExpiry.graceUntil = resumedAt.UTC().Add(adapterLeaseDuration)
}

func (service *Service) ClaimAdapterRuntime(
	ctx context.Context,
	params ClaimAdapterRuntimeParams,
) (RuntimeClaim, error) {
	if validationErr := validateClaimAdapterRuntime(params); validationErr != nil {
		return RuntimeClaim{}, validationErr
	}
	runtimeID, err := service.dependencies.NewRuntimeID()
	if err != nil {
		return RuntimeClaim{}, fmt.Errorf("generate Adapter runtime ID: %w", err)
	}
	claimedAt := service.dependencies.Now().UTC()
	return service.stores.Runtimes.ClaimAdapterRuntime(ctx, ClaimRuntimeWrite{
		ClaimID: params.ClaimID, RuntimeID: runtimeID, AdapterID: params.AdapterID,
		SoftwareName: params.SoftwareName, SoftwareVersion: params.SoftwareVersion,
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(adapterLeaseDuration),
		LeaseGraceUntil: service.leaseGraceUntil(claimedAt),
	})
}

func (service *Service) RecordAdapterHeartbeat(
	ctx context.Context,
	heartbeat AdapterHeartbeat,
) (HeartbeatResult, error) {
	if validateErr := validateAdapterHeartbeat(heartbeat); validateErr != nil {
		return HeartbeatResult{}, validateErr
	}
	if heartbeat.Reason != nil && strings.HasPrefix(heartbeat.Reason.Code, "adapter.") {
		softwareName, softwareErr := runtimeSoftwareName(
			ctx, service.stores.Adapters, heartbeat.AdapterID, heartbeat.RuntimeID,
		)
		if softwareErr != nil {
			return HeartbeatResult{}, softwareErr
		}
		if namespaceErr := validateHealthReasonNamespace(heartbeat.Reason.Code, softwareName); namespaceErr != nil {
			return HeartbeatResult{}, namespaceErr
		}
	}
	receivedAt := service.dependencies.Now().UTC()
	return service.stores.Runtimes.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: heartbeat.AdapterID, RuntimeID: heartbeat.RuntimeID,
		ExternalStatus: heartbeat.ExternalStatus, SourceObservedAt: heartbeat.SourceObservedAt.UTC(),
		Reason: copyHealthReason(heartbeat.Reason), ReceivedAt: receivedAt,
		LeaseExpiresAt:  receivedAt.Add(adapterLeaseDuration),
		LeaseGraceUntil: service.leaseGraceUntil(receivedAt),
	})
}

func (service *Service) ReleaseAdapterRuntime(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
) error {
	if !registrationSlugPattern.MatchString(adapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	if _, parseErr := ParseRuntimeID(string(runtimeID)); parseErr != nil {
		return fmt.Errorf("parse Adapter runtime ID: %w", parseErr)
	}
	releasedAt := service.dependencies.Now().UTC()
	return service.stores.Runtimes.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
		AdapterID: adapterID, RuntimeID: runtimeID, ReleasedAt: releasedAt,
		LeaseGraceUntil: service.leaseGraceUntil(releasedAt),
	})
}

func (service *Service) ExpireAdapterLeases(ctx context.Context, expiresAt time.Time) error {
	if expiresAt.IsZero() {
		return errors.New("adapter lease evaluation time is required")
	}
	expiresAt = expiresAt.UTC()
	if service.leaseExpiryDeferred(expiresAt) {
		return nil
	}
	return service.stores.Runtimes.ExpireAdapterLeases(ctx, ExpireLeasesWrite{ExpiresAt: expiresAt})
}

func (service *Service) leaseGraceUntil(at time.Time) time.Time {
	service.leaseExpiry.mutex.RLock()
	defer service.leaseExpiry.mutex.RUnlock()
	if service.leaseExpiry.paused {
		return at.UTC().Add(adapterLeaseDuration)
	}
	if at.Before(service.leaseExpiry.graceUntil) {
		return service.leaseExpiry.graceUntil
	}
	return time.Time{}
}

func (service *Service) leaseExpiryDeferred(at time.Time) bool {
	service.leaseExpiry.mutex.RLock()
	defer service.leaseExpiry.mutex.RUnlock()
	return service.leaseExpiry.paused || at.Before(service.leaseExpiry.graceUntil)
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

func runtimeSoftwareName(
	ctx context.Context,
	repository AdapterReader,
	adapterID string,
	runtimeID RuntimeID,
) (string, error) {
	instance, err := repository.GetAdapter(ctx, adapterID)
	if err != nil {
		return "", err
	}
	if instance.Health == nil || instance.Health.Runtime == nil ||
		instance.Health.Runtime.ID != runtimeID || instance.Health.Runtime.Status != runtimeStatusOnline {
		return "", ErrRuntimeFenced
	}
	return instance.Health.Runtime.SoftwareName, nil
}
