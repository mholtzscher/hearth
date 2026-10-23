package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

const knownFingerprint = adapters.ContractFingerprint(
	"sha256-v1:f527a6cf069eea12f79d697c3895935a3e605ef3d07e244524a5472ab34058c3",
)

func contractDescriptor() adapters.Descriptor {
	return adapters.Descriptor{Key: "inspect", Description: "Inspect", TimeoutMS: 1000,
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
}

func TestContractFingerprintKnownDigestAndDeclaredChanges(t *testing.T) {
	t.Parallel()
	got, err := adapters.FingerprintDescriptor(contractDescriptor())
	if err != nil || got != knownFingerprint {
		t.Fatalf("fingerprint=%s err=%v", got, err)
	}
	for _, test := range []struct {
		name   string
		change func(*adapters.Descriptor)
		same   bool
	}{
		{"outer description", func(d *adapters.Descriptor) { d.Description = "New prose" }, true},
		{"whitespace", func(d *adapters.Descriptor) { d.InputSchema = json.RawMessage(` { "type" : "object" } `) }, true},
		{"input", func(d *adapters.Descriptor) {
			d.InputSchema = json.RawMessage(`{"type":"object","additionalProperties":false}`)
		}, false},
		{"output", func(d *adapters.Descriptor) { d.OutputSchema = json.RawMessage(`{"type":"object","title":"Output"}`) }, false},
		{"timeout", func(d *adapters.Descriptor) { d.TimeoutMS++ }, false},
		{"key", func(d *adapters.Descriptor) { d.Key = "status" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			descriptor := contractDescriptor()
			test.change(&descriptor)
			fingerprint, fingerprintErr := adapters.FingerprintDescriptor(descriptor)
			if fingerprintErr != nil || (fingerprint == knownFingerprint) != test.same {
				t.Fatalf("fingerprint=%s err=%v same=%v", fingerprint, fingerprintErr, test.same)
			}
		})
	}
}

func TestContractFingerprintNormalizedDocumentsNotSchemaEquivalence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, first, second string
		same                bool
	}{
		{"object order", `{"type":"object","properties":{"z":{"type":"number"},"a":{"type":"string"}}}`, `{"properties":{"a":{"type":"string"},"z":{"type":"number"}},"type":"object"}`, true},
		{"array order", `{"type":"object","required":["a","b"]}`, `{"type":"object","required":["b","a"]}`, false},
		{"number spelling", `{"type":"object","properties":{"n":{"const":1}}}`, `{"type":"object","properties":{"n":{"const":1.0}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, second := contractDescriptor(), contractDescriptor()
			first.InputSchema, second.InputSchema = json.RawMessage(test.first), json.RawMessage(test.second)
			left, err := adapters.FingerprintDescriptor(first)
			if err != nil {
				t.Fatal(err)
			}
			right, err := adapters.FingerprintDescriptor(second)
			if err != nil || (left == right) != test.same {
				t.Fatalf("left=%s right=%s err=%v", left, right, err)
			}
		})
	}
}

type contractRepository struct {
	adapters.FunctionRepository

	function adapters.Function
	err      error
}

func (*contractRepository) Recover(context.Context, time.Time) error { return nil }
func (r *contractRepository) GetFunction(context.Context, string, adapters.FunctionKey) (adapters.Function, error) {
	return r.function, r.err
}
func (r *contractRepository) ListFunctions(context.Context, string, adapters.PageInput) (adapters.FunctionPage, error) {
	return adapters.FunctionPage{Items: []adapters.Function{r.function}}, r.err
}

type contractInvoker struct{}

func (contractInvoker) Invoke(context.Context, adapters.Invocation) adapters.Evidence {
	panic("validation must not dispatch")
}

func TestValidateContractInputOfflineAndDiscovery(t *testing.T) {
	t.Parallel()
	repo := &contractRepository{
		function: adapters.Function{AdapterID: "bridge", Descriptor: contractDescriptor(), Callable: false},
	}
	service, err := adapters.NewFunctionService(t.Context(), repo, contractInvoker{})
	if err != nil {
		t.Fatal(err)
	}
	input := adapters.ContractInput{AdapterID: "bridge", FunctionKey: "inspect", ContractFingerprint: knownFingerprint,
		Arguments: json.RawMessage(` {"z":9007199254740993,"a":1.0000000000000001} `)}
	normalized, err := service.ValidateContractInput(t.Context(), input)
	if err != nil || string(normalized) != `{"a":1.0000000000000001,"z":9007199254740993}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
	input.Arguments[0] = '['
	if string(normalized) != `{"a":1.0000000000000001,"z":9007199254740993}` {
		t.Fatal("arguments aliased")
	}
	function, err := service.GetFunction(t.Context(), "bridge", "inspect")
	if err != nil || function.Callable || function.ContractFingerprint != knownFingerprint {
		t.Fatalf("function=%+v err=%v", function, err)
	}
	page, err := service.ListFunctions(t.Context(), "bridge", adapters.PageInput{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ContractFingerprint != knownFingerprint {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestValidateContractInputErrorsRemainDistinct(t *testing.T) {
	t.Parallel()
	readFailure := errors.New("read failure")
	for _, test := range []struct {
		name   string
		change func(*adapters.ContractInput, *contractRepository)
		want   error
	}{
		{"adapter shape", func(i *adapters.ContractInput, _ *contractRepository) { i.AdapterID = "BAD" }, adapters.ErrInvalidFunctionInput},
		{"key shape", func(i *adapters.ContractInput, _ *contractRepository) { i.FunctionKey = "BAD" }, adapters.ErrInvalidFunctionInput},
		{"fingerprint shape", func(i *adapters.ContractInput, _ *contractRepository) { i.ContractFingerprint = "sha256-v1:AB" }, adapters.ErrInvalidFunctionInput},
		{"changed", func(i *adapters.ContractInput, _ *contractRepository) {
			i.ContractFingerprint = "sha256-v1:0000000000000000000000000000000000000000000000000000000000000000"
		}, adapters.ErrFunctionContractChanged},
		{"missing adapter", func(_ *adapters.ContractInput, r *contractRepository) { r.err = adapters.ErrAdapterNotFound }, adapters.ErrAdapterNotFound},
		{"missing function", func(_ *adapters.ContractInput, r *contractRepository) { r.err = adapters.ErrFunctionNotFound }, adapters.ErrFunctionNotFound},
		{"read failure", func(_ *adapters.ContractInput, r *contractRepository) { r.err = readFailure }, readFailure},
		{"corrupt descriptor", func(_ *adapters.ContractInput, r *contractRepository) {
			r.function.Descriptor.InputSchema = json.RawMessage(`{}`)
		}, adapters.ErrFunctionPersistence},
		{"null args", func(i *adapters.ContractInput, _ *contractRepository) { i.Arguments = json.RawMessage(`null`) }, adapters.ErrInvalidFunctionInput},
		{"array args", func(i *adapters.ContractInput, _ *contractRepository) { i.Arguments = json.RawMessage(`[]`) }, adapters.ErrInvalidFunctionInput},
		{"duplicate args", func(i *adapters.ContractInput, _ *contractRepository) { i.Arguments = json.RawMessage(`{"x":1,"x":2}`) }, adapters.ErrInvalidFunctionInput},
		{"schema args", func(i *adapters.ContractInput, r *contractRepository) {
			r.function.Descriptor.InputSchema = json.RawMessage(`{"type":"object","additionalProperties":false}`)
			i.ContractFingerprint, _ = adapters.FingerprintDescriptor(r.function.Descriptor)
			i.Arguments = json.RawMessage(`{"x":1}`)
		}, adapters.ErrInvalidFunctionInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := &contractRepository{
				function: adapters.Function{AdapterID: "bridge", Descriptor: contractDescriptor()},
			}
			input := adapters.ContractInput{
				AdapterID:           "bridge",
				FunctionKey:         "inspect",
				ContractFingerprint: knownFingerprint,
				Arguments:           json.RawMessage(`{}`),
			}
			test.change(&input, repo)
			service, err := adapters.NewFunctionService(t.Context(), repo, contractInvoker{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.ValidateContractInput(t.Context(), input)
			if !errors.Is(err, test.want) ||
				errors.Is(
					err,
					adapters.ErrFunctionContractChanged,
				) != errors.Is(test.want, adapters.ErrFunctionContractChanged) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
			if errors.Is(test.want, adapters.ErrFunctionPersistence) &&
				errors.Is(err, adapters.ErrInvalidFunctionInput) {
				t.Fatalf("corrupt descriptor mislabeled as caller input: %v", err)
			}
		})
	}
}
