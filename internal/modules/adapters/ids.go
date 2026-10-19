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

func ParseCatalogID(value string) (CatalogID, error) {
	if validateID(value, "afc") != nil {
		return "", ErrInvalidFunctionInput
	}
	return CatalogID(value), nil
}

func ParseInvocationID(value string) (InvocationID, error) {
	if validateID(value, "afi") != nil {
		return "", ErrInvalidFunctionInput
	}
	return InvocationID(value), nil
}

func NewInvocationID() (InvocationID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return InvocationID("afi_" + id.String()), nil
}

func ValidFunctionKey(key FunctionKey) bool { return registrationSlugPattern.MatchString(string(key)) }

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
