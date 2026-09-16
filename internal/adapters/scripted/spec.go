package scripted

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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
	// maxReasonCodeRunes matches the sdk/adapter limit, which accepts at most
	// 128 runes for a health or availability reason code.
	maxReasonCodeRunes = 128
)

var (
	operationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	// reasonCodePattern mirrors the sdk/adapter rule for reason codes: a
	// lowercase dotted identifier, never empty, at most 128 runes, such as
	// "hearth.external_system_unavailable" or "adapter.node.measurement_stale".
	reasonCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)+$`)
)

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
// healthy, "unhealthy:<reason-code>" means unhealthy with a reason code that
// must satisfy the sdk/adapter rule enforced on Session health reports.
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
	if err := validateReasonCode("health reason code", reason); err != nil {
		return false, "", err
	}
	return false, reason, nil
}

// validateReasonCode mirrors the sdk/adapter rule for reason codes so an
// invalid scripted reason fails at config load instead of the first health
// report: valid UTF-8, at most 128 runes, a lowercase dotted identifier, and
// inside the shared "hearth." or "adapter." namespace.
func validateReasonCode(field, code string) error {
	if !utf8.ValidString(code) ||
		utf8.RuneCountInString(code) > maxReasonCodeRunes ||
		!reasonCodePattern.MatchString(code) {
		return fmt.Errorf(
			"%s must be a lowercase dotted identifier of at most 128 characters, got %q",
			field, code,
		)
	}
	if strings.HasPrefix(code, "hearth.") || strings.HasPrefix(code, "adapter.") {
		return nil
	}
	return fmt.Errorf("%s must use the hearth or adapter namespace, got %q", field, code)
}

// validateAvailabilityReason requires a reason code when an Entity is marked
// unavailable and checks any configured reason code against the same rule the
// Session enforces, so a bad availability_reason fails at config load.
func validateAvailabilityReason(spec EntitySpec) error {
	if strings.TrimSpace(spec.AvailabilityReason) == "" {
		if spec.Available != nil && !*spec.Available {
			return fmt.Errorf("availability_reason is required when available is false")
		}
		return nil
	}
	return validateReasonCode("availability_reason", spec.AvailabilityReason)
}

// Validate checks the device and entity configuration structurally. Value
// schemas are validated separately by ValidateValues, which config load calls
// before startup, and again when the runtime is built.
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
	if err := validateAvailabilityReason(spec); err != nil {
		return err
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
	// Only a multi-value script is stepped by a ticker, so an interval is
	// required exactly then; zero or one value publishes once and stays silent.
	if spec.Outputs != nil && len(spec.Outputs.Values) > 1 {
		if time.Duration(spec.Outputs.Interval) <= 0 {
			return fmt.Errorf("outputs: interval is required and must be positive for a multi-value script")
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
