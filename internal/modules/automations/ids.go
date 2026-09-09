package automations

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// NewAutomationID generates a canonical UUIDv7 automation identity.
func NewAutomationID() (AutomationID, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("automation ID generation: %w", err)
	}
	return AutomationID("aut_" + value.String()), nil
}

// NewAutomationRunID generates a canonical UUIDv7 automation run identity.
func NewAutomationRunID() (AutomationRunID, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("automation run ID generation: %w", err)
	}
	return AutomationRunID("arn_" + value.String()), nil
}

// ParseAutomationID rejects noncanonical identities without echoing their contents.
func ParseAutomationID(value string) (AutomationID, error) {
	if !validAutomationIdentity(value, "aut_") {
		return "", fmt.Errorf("%w: automation ID", ErrInvalidAutomation)
	}
	return AutomationID(value), nil
}

// ParseAutomationRunID rejects adapter runtime IDs and noncanonical UUIDs.
func ParseAutomationRunID(value string) (AutomationRunID, error) {
	if !validAutomationIdentity(value, "arn_") {
		return "", fmt.Errorf("%w: automation run ID", ErrInvalidAutomation)
	}
	return AutomationRunID(value), nil
}
func validAutomationIdentity(value, prefix string) bool {
	text, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return false
	}
	id, err := uuid.Parse(text)
	return err == nil && id.String() == text && id.Version() == 7 && id.Variant() == uuid.RFC4122
}

// ValidateAutomationIdempotencyKey permits 1–128 printable ASCII nonspace bytes.
func ValidateAutomationIdempotencyKey(key string) error {
	if len(key) < 1 || len(key) > 128 {
		return fmt.Errorf("%w: idempotency key length", ErrInvalidAutomation)
	}
	for i := range len(key) {
		if key[i] < 33 || key[i] > 126 {
			return fmt.Errorf("%w: idempotency key characters", ErrInvalidAutomation)
		}
	}
	return nil
}
