package adapters

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

type RuntimeID string
type HealthUpdateID string

var registrationSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func ValidateAdapterID(value string) error {
	if !registrationSlugPattern.MatchString(value) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	return nil
}

func ParseRuntimeID(value string) (RuntimeID, error) {
	if err := validateID(value, "run"); err != nil {
		return "", err
	}
	return RuntimeID(value), nil
}

func ParseHealthUpdateID(value string) (HealthUpdateID, error) {
	if err := validateID(value, "ahu"); err != nil {
		return "", err
	}
	return HealthUpdateID(value), nil
}

func NewHealthUpdateID() (HealthUpdateID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate health update ID: %w", err)
	}
	return HealthUpdateID("ahu_" + id.String()), nil
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
	if parsed.String() != text || parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 {
		return fmt.Errorf("invalid %s ID: canonical lowercase RFC 4122 UUIDv7 required", prefix)
	}
	return nil
}
