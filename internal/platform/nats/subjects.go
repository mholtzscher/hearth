package nats

import (
	"fmt"
	"regexp"
	"strings"
)

const subjectPrefix = "hearth.v1.adapter"

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	entityIDPattern = regexp.MustCompile(`^ent_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type ObservationRoute struct {
	AdapterID string
	EntityID  string
}

type CommandRoute struct {
	AdapterID string
	EntityID  string
	Operation string
}

func RegistrationWildcard() string {
	return subjectPrefix + ".*.register"
}

func RegistrationSubject(adapterID string) (string, error) {
	if err := validateSlug("adapter ID", adapterID); err != nil {
		return "", err
	}
	return subjectPrefix + "." + adapterID + ".register", nil
}

func ObservationWildcard() string {
	return subjectPrefix + ".*.observation.>"
}

func ObservationSubject(adapterID, entityID string) (string, error) {
	if err := validateSlug("adapter ID", adapterID); err != nil {
		return "", err
	}
	if !entityIDPattern.MatchString(entityID) {
		return "", fmt.Errorf("invalid entity ID %q", entityID)
	}
	return subjectPrefix + "." + adapterID + ".observation." + entityID, nil
}

func CommandSubject(adapterID, entityID, operation string) (string, error) {
	if err := validateSlug("adapter ID", adapterID); err != nil {
		return "", err
	}
	if !entityIDPattern.MatchString(entityID) {
		return "", fmt.Errorf("invalid entity ID %q", entityID)
	}
	if err := validateSlug("operation", operation); err != nil {
		return "", err
	}
	return subjectPrefix + "." + adapterID + ".command." + entityID + "." + operation, nil
}

func CommandWildcard(adapterID string) (string, error) {
	if err := validateSlug("adapter ID", adapterID); err != nil {
		return "", err
	}
	return subjectPrefix + "." + adapterID + ".command.*.*", nil
}

func ParseObservationSubject(subject string) (ObservationRoute, error) {
	parts := strings.Split(subject, ".")
	if len(parts) != 6 || strings.Join(parts[:3], ".") != subjectPrefix || parts[4] != "observation" {
		return ObservationRoute{}, fmt.Errorf("invalid observation subject %q", subject)
	}
	if _, err := ObservationSubject(parts[3], parts[5]); err != nil {
		return ObservationRoute{}, fmt.Errorf("invalid observation subject %q: %w", subject, err)
	}
	return ObservationRoute{AdapterID: parts[3], EntityID: parts[5]}, nil
}

func ParseCommandSubject(subject string) (CommandRoute, error) {
	parts := strings.Split(subject, ".")
	if len(parts) != 7 || strings.Join(parts[:3], ".") != subjectPrefix || parts[4] != "command" {
		return CommandRoute{}, fmt.Errorf("invalid command subject %q", subject)
	}
	if _, err := CommandSubject(parts[3], parts[5], parts[6]); err != nil {
		return CommandRoute{}, fmt.Errorf("invalid command subject %q: %w", subject, err)
	}
	return CommandRoute{AdapterID: parts[3], EntityID: parts[5], Operation: parts[6]}, nil
}

func validateSlug(name, value string) error {
	if !slugPattern.MatchString(value) {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	return nil
}
