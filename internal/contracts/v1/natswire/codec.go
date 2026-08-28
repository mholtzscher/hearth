package natswire

import (
	"encoding/json"
	"fmt"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

func Decode[T any](validator *contractsv1.Validator, schemaID string, payload []byte) (Envelope[T], error) {
	var envelope Envelope[T]
	if err := validator.Validate(schemaID, payload); err != nil {
		return envelope, err
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return envelope, fmt.Errorf("decode validated envelope: %w", err)
	}
	return envelope, nil
}

func Encode[T any](validator *contractsv1.Validator, schemaID string, envelope Envelope[T]) ([]byte, error) {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode envelope: %w", err)
	}
	if validationErr := validator.Validate(schemaID, payload); validationErr != nil {
		return nil, validationErr
	}
	return payload, nil
}
