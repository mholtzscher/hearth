package automations

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// canonicalUUIDVersion is the only UUID version accepted for durable automation
// identities, matching the rest of Hearth's UUIDv7 identities.
const canonicalUUIDVersion = 7

// subjectSlugPattern is the one subject-safe slug shape shared by Trigger IDs,
// Step IDs, and Entity Event names: 1–63 bytes, lowercase alphanumeric with
// internal dashes and underscores.
var subjectSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// NewAutomationID mints the durable identity of one Automation definition.
func NewAutomationID() (AutomationID, error) {
	value, err := newAutomationUUID("automation")
	if err != nil {
		return "", err
	}
	return AutomationID("aut_" + value), nil
}

// NewAutomationRunID mints the durable identity of one Automation Run. It is
// deliberately distinct from the adapter runtime identity that also uses a
// "run_" prefix.
func NewAutomationRunID() (AutomationRunID, error) {
	value, err := newAutomationUUID("automation run")
	if err != nil {
		return "", err
	}
	return AutomationRunID("arn_" + value), nil
}

// NewAutomationSkipID mints the durable identity of one recorded Skip.
func NewAutomationSkipID() (AutomationSkipID, error) {
	value, err := newAutomationUUID("automation skip")
	if err != nil {
		return "", err
	}
	return AutomationSkipID("ask_" + value), nil
}

// ParseAutomationID validates one canonical aut_-prefixed UUIDv7.
func ParseAutomationID(value string) (AutomationID, error) {
	if err := validateAutomationUUID(value, "aut"); err != nil {
		return "", fmt.Errorf("%w: automation ID: %w", ErrInvalidAutomation, err)
	}
	return AutomationID(value), nil
}

// ParseAutomationRunID validates one canonical arn_-prefixed UUIDv7 and rejects
// adapter Runtime identities.
func ParseAutomationRunID(value string) (AutomationRunID, error) {
	if err := validateAutomationUUID(value, "arn"); err != nil {
		return "", fmt.Errorf("%w: automation run ID: %w", ErrInvalidAutomation, err)
	}
	return AutomationRunID(value), nil
}

// ParseAutomationSkipID validates one canonical ask_-prefixed UUIDv7.
func ParseAutomationSkipID(value string) (AutomationSkipID, error) {
	if err := validateAutomationUUID(value, "ask"); err != nil {
		return "", fmt.Errorf("%w: automation skip ID: %w", ErrInvalidAutomation, err)
	}
	return AutomationSkipID(value), nil
}

// ParseTriggerID validates one Trigger identity. Trigger IDs are subject-safe
// slugs, so they are unique only within their own definition's Trigger list.
func ParseTriggerID(value string) (TriggerID, error) {
	if !subjectSlugPattern.MatchString(value) {
		return "", fmt.Errorf("%w: trigger ID is not a subject-safe slug", ErrInvalidAutomation)
	}
	return TriggerID(value), nil
}

// ParseStepID validates one Step identity. Step IDs are subject-safe slugs, so
// they are unique only within their own definition's Step list.
func ParseStepID(value string) (StepID, error) {
	if !subjectSlugPattern.MatchString(value) {
		return "", fmt.Errorf("%w: step ID is not a subject-safe slug", ErrInvalidAutomation)
	}
	return StepID(value), nil
}

func newAutomationUUID(label string) (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate %s UUID: %w", label, err)
	}
	return value.String(), nil
}

func validateAutomationUUID(value, prefix string) error {
	text, ok := strings.CutPrefix(value, prefix+"_")
	if !ok {
		return fmt.Errorf("expected %s_ prefix", prefix)
	}
	parsed, err := uuid.Parse(text)
	if err != nil {
		return fmt.Errorf("invalid UUID: %w", err)
	}
	switch {
	case parsed.String() != text:
		return errors.New("UUID must be canonical lowercase")
	case parsed.Version() != canonicalUUIDVersion:
		return errors.New("UUID must be version 7")
	case parsed.Variant() != uuid.RFC4122:
		return errors.New("UUID must use the RFC 4122 variant")
	default:
		return nil
	}
}
