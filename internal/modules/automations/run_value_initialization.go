package automations

import (
	"encoding/json"
	"time"
)

type RunValueInitialization struct {
	RecordedAt time.Time
	Body       RunValueInitializationBody
}
type RunValueInitializationBody interface{ isRunValueInitializationBody() }
type ValuesInitialized struct{ Values json.RawMessage }
type InitializationFailed struct{ Failure ValueFailure }

func (ValuesInitialized) isRunValueInitializationBody()    {}
func (InitializationFailed) isRunValueInitializationBody() {}

func EncodeRunValueInitialization(v RunValueInitialization) (json.RawMessage, error) {
	if err := validateRunValueInitialization(v); err != nil {
		return nil, err
	}
	switch b := v.Body.(type) {
	case ValuesInitialized:
		return json.Marshal(struct {
			Kind   string          `json:"kind"`
			At     time.Time       `json:"recorded_at"`
			Values json.RawMessage `json:"values"`
		}{"initialized", v.RecordedAt, b.Values})
	case InitializationFailed:
		return json.Marshal(struct {
			Kind    string       `json:"kind"`
			At      time.Time    `json:"recorded_at"`
			Failure ValueFailure `json:"failure"`
		}{"failed", v.RecordedAt, b.Failure})
	default:
		return nil, invalid("unsupported initialization")
	}
}

func validateRunValueInitialization(v RunValueInitialization) error {
	if !isValidExecutionTimestamp(v.RecordedAt) {
		return invalid("invalid initialization time")
	}
	switch b := v.Body.(type) {
	case ValuesInitialized:
		return validateDataVariables(b.Values, false)
	case InitializationFailed:
		return validateDataFailure(b.Failure)
	default:
		return invalid("unsupported initialization")
	}
}
func DecodeRunValueInitialization(raw json.RawMessage) (RunValueInitialization, error) {
	var fields map[string]json.RawMessage
	if err := strictSingleObject(raw); err != nil {
		return RunValueInitialization{}, err
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return RunValueInitialization{}, invalid("invalid initialization")
	}
	var kind string
	var at time.Time
	if json.Unmarshal(fields["kind"], &kind) != nil || json.Unmarshal(fields["recorded_at"], &at) != nil ||
		len(fields) != 3 {
		return RunValueInitialization{}, invalid("invalid initialization shape")
	}
	v := RunValueInitialization{RecordedAt: at}
	switch kind {
	case "initialized":
		if _, ok := fields["values"]; !ok {
			return v, invalid("missing initialization values")
		}
		v.Body = ValuesInitialized{Values: fields["values"]}
	case "failed":
		var failure ValueFailure
		if _, ok := fields["failure"]; !ok {
			return v, invalid("missing initialization failure")
		}
		if err := decodeWire(fields["failure"], &failure); err != nil {
			return v, err
		}
		v.Body = InitializationFailed{Failure: failure}
	default:
		return v, invalid("invalid initialization kind")
	}
	return v, validateRunValueInitialization(v)
}
