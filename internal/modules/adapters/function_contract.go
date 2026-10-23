package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

var contractFingerprintPattern = regexp.MustCompile(`^sha256-v1:[0-9a-f]{64}$`)

// FingerprintDescriptor identifies the normalized declared contract, not the
// runtime, outer description, or behavior of an Adapter.
func FingerprintDescriptor(descriptor Descriptor) (ContractFingerprint, error) {
	prepared, _, _, err := PrepareDescriptor(descriptor)
	if err != nil {
		return "", err
	}
	document, err := json.Marshal(struct {
		Key          FunctionKey     `json:"key"`
		InputSchema  json.RawMessage `json:"input_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
		TimeoutMS    int             `json:"timeout_ms"`
	}{prepared.Key, prepared.InputSchema, prepared.OutputSchema, prepared.TimeoutMS})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("hearth.adapter-function-contract.v1\n"), document...))
	return ContractFingerprint("sha256-v1:" + hex.EncodeToString(digest[:])), nil
}

// ValidateContractInput validates against retained discovery without requiring a
// callable runtime. The returned arguments are owned normalized JSON.
func (service *FunctionService) ValidateContractInput(
	ctx context.Context,
	input ContractInput,
) (json.RawMessage, error) {
	_, arguments, err := service.resolveContractInput(ctx, input)
	return arguments, err
}

func (service *FunctionService) resolveContractInput(
	ctx context.Context,
	input ContractInput,
) (Function, json.RawMessage, error) {
	if ValidateAdapterID(input.AdapterID) != nil || !ValidFunctionKey(input.FunctionKey) ||
		!contractFingerprintPattern.MatchString(string(input.ContractFingerprint)) || len(input.Arguments) == 0 {
		return Function{}, nil, ErrInvalidFunctionInput
	}
	function, err := service.GetFunction(ctx, input.AdapterID, input.FunctionKey)
	if err != nil {
		return Function{}, nil, err
	}
	if function.ContractFingerprint != input.ContractFingerprint {
		return Function{}, nil, ErrFunctionContractChanged
	}
	_, schema, _, err := PrepareDescriptor(function.Descriptor)
	if err != nil {
		return Function{}, nil, ErrFunctionPersistence
	}
	arguments, err := schema.Normalize(input.Arguments)
	if err != nil {
		return Function{}, nil, ErrInvalidFunctionInput
	}
	return function, arguments, nil
}
