package devices

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const canonicalUUIDVersion = 7

func NewDeviceID() (DeviceID, error) {
	id, err := newID("dev")
	return DeviceID(id), err
}

func NewEntityID() (EntityID, error) {
	id, err := newID("ent")
	return EntityID(id), err
}

func NewObservationID() (ObservationID, error) {
	id, err := newID("obs")
	return ObservationID(id), err
}

func NewDeviceEventID() (DeviceEventID, error) {
	id, err := newID("evt")
	return DeviceEventID(id), err
}

func NewCommandID() (CommandID, error) {
	id, err := newID("cmd")
	return CommandID(id), err
}

func NewCorrelationID() (CorrelationID, error) {
	id, err := newID("cor")
	return CorrelationID(id), err
}

func ParseDeviceID(value string) (DeviceID, error) {
	if err := validateID(value, "dev"); err != nil {
		return "", err
	}
	return DeviceID(value), nil
}

func ParseEntityID(value string) (EntityID, error) {
	if err := validateID(value, "ent"); err != nil {
		return "", err
	}
	return EntityID(value), nil
}

func ParseObservationID(value string) (ObservationID, error) {
	if err := validateID(value, "obs"); err != nil {
		return "", err
	}
	return ObservationID(value), nil
}

func ParseDeviceEventID(value string) (DeviceEventID, error) {
	if err := validateID(value, "evt"); err != nil {
		return "", err
	}
	return DeviceEventID(value), nil
}

func ParseCommandID(value string) (CommandID, error) {
	if err := validateID(value, "cmd"); err != nil {
		return "", err
	}
	return CommandID(value), nil
}

func ParseCorrelationID(value string) (CorrelationID, error) {
	if err := validateID(value, "cor"); err != nil {
		return "", err
	}
	return CorrelationID(value), nil
}

func ParseRuntimeID(value string) (RuntimeID, error) {
	if err := validateID(value, "run"); err != nil {
		return "", err
	}
	return RuntimeID(value), nil
}

func newID(prefix string) (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate %s ID: %w", prefix, err)
	}
	return prefix + "_" + value.String(), nil
}

func validateID(value, prefix string) error {
	text, ok := strings.CutPrefix(value, prefix+"_")
	if !ok {
		return fmt.Errorf("invalid %s ID prefix", prefix)
	}
	parsed, err := uuid.Parse(text)
	if err != nil {
		return fmt.Errorf("invalid %s ID: %w", prefix, err)
	}
	if parsed.String() != text {
		return fmt.Errorf("invalid %s ID: UUID must be canonical lowercase", prefix)
	}
	if parsed.Version() != canonicalUUIDVersion {
		return fmt.Errorf("invalid %s ID: UUID must be version 7", prefix)
	}
	if parsed.Variant() != uuid.RFC4122 {
		return fmt.Errorf("invalid %s ID: UUID must use the RFC 4122 variant", prefix)
	}
	return nil
}
