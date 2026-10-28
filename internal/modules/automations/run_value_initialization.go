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
		if b.Failure.Field == "" || b.Failure.ConditionID != "" || b.Failure.State != nil {
			return invalid("initialization failure requires a field")
		}
		return validateDataFailure(b.Failure)
	default:
		return invalid("unsupported initialization")
	}
}

// DecodeRunValueInitialization returns validated initialization and its canonical
// encoding, shared by history reconstruction and accounting.
func DecodeRunValueInitialization(raw json.RawMessage) (RunValueInitialization, json.RawMessage, error) {
	fields, err := decodeEvidenceObject(raw)
	if err != nil {
		return RunValueInitialization{}, nil, err
	}
	var kind string
	var at time.Time
	if json.Unmarshal(fields["kind"], &kind) != nil || json.Unmarshal(fields["recorded_at"], &at) != nil ||
		len(fields) != 3 {
		return RunValueInitialization{}, nil, invalid("invalid initialization shape")
	}
	v := RunValueInitialization{RecordedAt: at}
	switch kind {
	case "initialized":
		if _, ok := fields["values"]; !ok {
			return v, nil, invalid("missing initialization values")
		}
		v.Body = ValuesInitialized{Values: fields["values"]}
	case "failed":
		var failure ValueFailure
		if _, ok := fields["failure"]; !ok {
			return v, nil, invalid("missing initialization failure")
		}
		if err = decodeWire(fields["failure"], &failure); err != nil {
			return v, nil, err
		}
		v.Body = InitializationFailed{Failure: failure}
	default:
		return v, nil, invalid("invalid initialization kind")
	}
	canonical, err := EncodeRunValueInitialization(v)
	return v, canonical, err
}
