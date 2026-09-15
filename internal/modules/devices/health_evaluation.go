package devices

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maximumHealthReasonCodeLength = 128

var healthReasonCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)+$`)

func (service *Service) ClaimAdapterRuntime(
	ctx context.Context,
	params ClaimAdapterRuntimeParams,
) error {
	if validationErr := validateClaimAdapterRuntime(params); validationErr != nil {
		return validationErr
	}
	claimedAt := service.dependencies.Now().UTC()
	return service.stores.Runtimes.ClaimAdapterRuntime(ctx, ClaimRuntimeWrite{
		RuntimeID: params.RuntimeID, AdapterID: params.AdapterID,
		SoftwareName: params.SoftwareName, SoftwareVersion: params.SoftwareVersion,
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(adapterLeaseDuration),
	})
}

func (service *Service) RecordAdapterHeartbeat(
	ctx context.Context,
	heartbeat AdapterHeartbeat,
) (HeartbeatResult, error) {
	if validateErr := validateAdapterHeartbeat(heartbeat); validateErr != nil {
		return HeartbeatResult{}, validateErr
	}
	receivedAt := service.dependencies.Now().UTC()
	return service.stores.Runtimes.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: heartbeat.AdapterID, RuntimeID: heartbeat.RuntimeID,
		ExternalStatus: heartbeat.ExternalStatus, SourceObservedAt: heartbeat.SourceObservedAt.UTC(),
		Reason: CopyHealthReason(heartbeat.Reason), ReceivedAt: receivedAt,
		LeaseExpiresAt: receivedAt.Add(adapterLeaseDuration),
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
	})
}

func (service *Service) ExpireAdapterLeases(ctx context.Context, expiresAt time.Time) error {
	if expiresAt.IsZero() {
		return errors.New("adapter lease evaluation time is required")
	}
	return service.stores.Runtimes.ExpireAdapterLeases(ctx, ExpireLeasesWrite{ExpiresAt: expiresAt.UTC()})
}

func validateClaimAdapterRuntime(params ClaimAdapterRuntimeParams) error {
	if !registrationSlugPattern.MatchString(params.AdapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	if _, err := ParseRuntimeID(string(params.RuntimeID)); err != nil {
		return fmt.Errorf("parse Adapter runtime ID: %w", err)
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
	return nil
}
