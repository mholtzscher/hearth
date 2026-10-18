package adapters

import (
	"errors"
	"fmt"
)

// ValidateClaimRuntimeWrite validates the independently callable runtime store.
func ValidateClaimRuntimeWrite(write ClaimRuntimeWrite) error {
	if err := validateClaimAdapterRuntime(
		ClaimAdapterRuntimeParams{
			AdapterID:       write.AdapterID,
			RuntimeID:       write.RuntimeID,
			SoftwareName:    write.SoftwareName,
			SoftwareVersion: write.SoftwareVersion,
		},
	); err != nil {
		return err
	}
	if err := ValidateUTCTime(write.ClaimedAt); err != nil {
		return err
	}
	if err := ValidateUTCTime(write.LeaseExpiresAt); err != nil {
		return err
	}
	if !write.LeaseExpiresAt.Equal(write.ClaimedAt.Add(adapterLeaseDuration)) {
		return errors.New("runtime claim lease must be fifteen seconds")
	}
	return nil
}

// NormalizeHeartbeatWrite validates and detaches arbitrary heartbeat input.
func NormalizeHeartbeatWrite(write HeartbeatWrite) (HeartbeatWrite, error) {
	if err := validateAdapterHeartbeat(
		AdapterHeartbeat{
			AdapterID:        write.AdapterID,
			RuntimeID:        write.RuntimeID,
			ExternalStatus:   write.ExternalStatus,
			SourceObservedAt: write.SourceObservedAt,
			Reason:           write.Reason,
		},
	); err != nil {
		return HeartbeatWrite{}, err
	}
	for _, value := range []struct {
		name string
		err  error
	}{
		{"received", ValidateUTCTime(write.ReceivedAt)}, {"lease expiry", ValidateUTCTime(write.LeaseExpiresAt)},
	} {
		if value.err != nil {
			return HeartbeatWrite{}, fmt.Errorf("%s: %w", value.name, value.err)
		}
	}
	if !write.LeaseExpiresAt.Equal(write.ReceivedAt.Add(adapterLeaseDuration)) {
		return HeartbeatWrite{}, errors.New("heartbeat lease must be fifteen seconds")
	}
	write.Reason = CopyHealthReason(write.Reason)
	return write, nil
}

// ValidateReleaseRuntimeWrite validates an independently supplied release.
func ValidateReleaseRuntimeWrite(write ReleaseRuntimeWrite) error {
	if !registrationSlugPattern.MatchString(write.AdapterID) {
		return errors.New("invalid Adapter ID")
	}
	if _, err := ParseRuntimeID(string(write.RuntimeID)); err != nil {
		return err
	}
	return ValidateUTCTime(write.ReleasedAt)
}
