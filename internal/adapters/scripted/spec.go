package scripted

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

const (
	// CommandBehaviorAcceptPublish accepts the Command and publishes one
	// Command-linked Observation of the current value.
	CommandBehaviorAcceptPublish = "accept-and-publish"
	// CommandBehaviorAcceptSilent accepts the Command but publishes no outcome
	// Observation, so an observed-outcome Command runs to its deadline.
	CommandBehaviorAcceptSilent = "accept-no-publish"
	// CommandBehaviorReject rejects the Command with the configured reason.
	CommandBehaviorReject = "reject"

	maxRegistrationEntities = 64
)

var operationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// Duration is a YAML interval parsed with [time.ParseDuration] ("5s", "1m").
type Duration time.Duration

// UnmarshalYAML accepts a duration string and rejects every other YAML shape
// so a bare number can never silently mean nanoseconds.
func (value *Duration) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err != nil {
		return fmt.Errorf("interval must be a duration string like \"5s\": %w", err)
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(text))
	if err != nil {
		return fmt.Errorf("invalid interval %q: %w", text, err)
	}
	if parsed <= 0 {
		return fmt.Errorf("interval %q must be positive", text)
	}
	*value = Duration(parsed)
	return nil
}

// DeviceSpec is one scripted Device: one Binding registration.
type DeviceSpec struct {
	BindingKey string       `yaml:"binding_key"`
	Name       string       `yaml:"name"`
	Kind       string       `yaml:"kind"`
	Health     string       `yaml:"health"`
	Entities   []EntitySpec `yaml:"entities"`
}

// EntitySpec is one scripted Entity with its emitted value series and Command
// behavior. Support, Initial, and Outputs values are YAML values converted to
// JSON and validated against the Entity type's authoritative schemas.
type EntitySpec struct {
	Key                string                     `yaml:"key"`
	Name               string                     `yaml:"name"`
	Type               string                     `yaml:"type"`
	Support            any                        `yaml:"support"`
	Initial            any                        `yaml:"initial"`
	Outputs            *OutputsSpec               `yaml:"outputs"`
	Commands           map[string]CommandBehavior `yaml:"commands"`
	Available          *bool                      `yaml:"available"`
	AvailabilityReason string                     `yaml:"availability_reason"`
}

// OutputsSpec is the looping value series for one Entity. Values are States
// for State Entities and event names for event-source Entities.
type OutputsSpec struct {
	Interval Duration `yaml:"interval"`
	Values   []any    `yaml:"values"`
}

// CommandBehavior describes how the simulator answers one Operation.
type CommandBehavior struct {
	Behavior        string `yaml:"behavior"`
	Reason          string `yaml:"reason"`
	ApplyParameters *bool  `yaml:"apply_parameters"`
}

// HealthStatus parses the device health shorthand: empty or "healthy" means
// healthy, "unhealthy:<reason-code>" means unhealthy.
func (spec DeviceSpec) HealthStatus() (bool, string, error) {
	trimmed := strings.TrimSpace(spec.Health)
	if trimmed == "" || trimmed == "healthy" {
		return true, "", nil
	}
	reason, found := strings.CutPrefix(trimmed, "unhealthy:")
	if !found {
		return false, "", fmt.Errorf("health must be \"healthy\" or \"unhealthy:<reason-code>\"")
	}
	if strings.TrimSpace(reason) == "" {
		return false, "", fmt.Errorf("unhealthy health requires a reason code")
	}
	return false, reason, nil
}

// Validate checks the device and entity configuration structurally. Value
// schemas are validated later against the type registry when the runtime is
// built, so config load fails fast with the Device, Entity, and value index.
func (spec DeviceSpec) Validate() error {
	if err := platformconfig.ValidateSlug("binding_key", spec.BindingKey); err != nil {
		return err
	}
	if err := platformconfig.ValidateName("name", spec.Name); err != nil {
		return err
	}
	switch spec.Kind {
	case "light", "relay", "sensor":
	default:
		return fmt.Errorf("kind must be light, relay, or sensor")
	}
	if _, _, err := spec.HealthStatus(); err != nil {
		return err
	}
	if len(spec.Entities) == 0 || len(spec.Entities) > maxRegistrationEntities {
		return fmt.Errorf("entities must contain 1-%d entries", maxRegistrationEntities)
	}
	seen := make(map[string]struct{}, len(spec.Entities))
	for index := range spec.Entities {
		entity := &spec.Entities[index]
		if err := entity.validate(); err != nil {
			return fmt.Errorf("entities[%d]: %w", index, err)
		}
		if _, duplicate := seen[entity.Key]; duplicate {
			return fmt.Errorf("entities[%d]: duplicate Entity key %q", index, entity.Key)
		}
		seen[entity.Key] = struct{}{}
	}
	return nil
}

func (spec EntitySpec) validate() error {
	if err := platformconfig.ValidateSlug("key", spec.Key); err != nil {
		return err
	}
	if err := platformconfig.ValidateName("name", spec.Name); err != nil {
		return err
	}
	if strings.TrimSpace(spec.Type) == "" {
		return fmt.Errorf("type is required")
	}
	if _, err := Lookup(spec.Type); err != nil {
		return err
	}
	if spec.Support == nil {
		return fmt.Errorf("support is required")
	}
	if spec.Available != nil && !*spec.Available && strings.TrimSpace(spec.AvailabilityReason) == "" {
		return fmt.Errorf("availability_reason is required when available is false")
	}
	for operation, behavior := range spec.Commands {
		if !operationNamePattern.MatchString(operation) {
			return fmt.Errorf("commands: operation name %q is not subject-safe", operation)
		}
		switch behavior.Behavior {
		case "", CommandBehaviorAcceptPublish, CommandBehaviorAcceptSilent:
			if strings.TrimSpace(behavior.Reason) != "" {
				return fmt.Errorf("commands[%q]: reason requires the reject behavior", operation)
			}
		case CommandBehaviorReject:
		default:
			return fmt.Errorf(
				"commands[%q]: behavior must be %q, %q, or %q",
				operation,
				CommandBehaviorAcceptPublish, CommandBehaviorAcceptSilent, CommandBehaviorReject,
			)
		}
	}
	if spec.Outputs != nil {
		if time.Duration(spec.Outputs.Interval) <= 0 {
			return fmt.Errorf("outputs: interval is required and must be positive")
		}
	}
	return nil
}

// toJSON converts one YAML-decoded value to JSON for schema validation.
func toJSON(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, fmt.Errorf("value is required")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode value as JSON: %w", err)
	}
	return json.RawMessage(raw), nil
}
