package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sdkschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// toolCall runs a typed handler and renders its result or modeled failure.
type toolCall[I any] func(context.Context, *mcp.CallToolRequest, I) (*mcp.CallToolResult, error)

// registerOutput retains SDK input validation and structured failure handling.
// Out is any with a nil success value so the SDK leaves our raw result untouched.
func registerOutput[I, O any](server *Server, tool ToolWithRequest[I, O]) {
	input, preparationErr := prepareInputSchema[I](tool.InputSchema)
	if preparationErr != nil {
		panic(preparationErr)
	}
	validator, preparationErr := compileSuccessSchema[O](tool.SuccessSchema)
	if preparationErr != nil {
		panic(preparationErr)
	}
	call := outputCall(tool, validator)
	if len(tool.HandlerValidated) > 0 {
		addHandlerValidatedTool(server, tool, input, call)
		return
	}
	mcp.AddTool[I, any](server.server, &mcp.Tool{
		Name: tool.Name, Description: tool.Description,
		InputSchema: input,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input I) (*mcp.CallToolResult, any, error) {
		result, err := call(ctx, request, input)
		return result, nil, err
	})
}

func outputCall[I, O any](tool ToolWithRequest[I, O], validator *jsonschema.Schema) toolCall[I] {
	return func(ctx context.Context, request *mcp.CallToolRequest, input I) (*mcp.CallToolResult, error) {
		output, handlerErr := tool.Handler(ctx, request, input)
		if handlerErr != nil {
			if failure, ok := errors.AsType[*ToolError](handlerErr); ok && tool.ExactFailureText {
				encoded, err := json.Marshal(failure.structuredContent())
				if err != nil {
					return nil, toolFailure(InternalToolError(err))
				}
				// Keep opaque failure details exact in the text half too. MCP
				// clients may decode structured content through float64.
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
				result.SetError(failure)
				return result, nil
			}
			return nil, toolFailure(handlerErr)
		}
		encoded, validationErr := encodeValidatedOutput(output, validator)
		if validationErr != nil {
			return nil, toolFailure(InternalToolError(validationErr))
		}
		return &mcp.CallToolResult{
			StructuredContent: encoded,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}, nil
	}
}

// addHandlerValidatedTool advertises the complete input schema while
// validating handler-validated properties only as objects, then follows the
// SDK's typed pipeline for argument validation, decoding, and failures.
func addHandlerValidatedTool[I, O any](
	server *Server,
	tool ToolWithRequest[I, O],
	advertised json.RawMessage,
	call toolCall[I],
) {
	arguments, preparationErr := handlerValidatedArgumentSchema(advertised, tool.HandlerValidated)
	if preparationErr != nil {
		panic(fmt.Errorf("tool %s: %w", tool.Name, preparationErr))
	}
	server.server.AddTool(&mcp.Tool{
		Name: tool.Name, Description: tool.Description,
		InputSchema: advertised,
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		validated, err := validateArguments(request.Params.Arguments, arguments)
		if err != nil {
			return failedResult(fmt.Errorf("validating \"arguments\": %w", err)), nil
		}
		var decoded I
		if err = json.Unmarshal(validated, &decoded); err != nil {
			// Typed scalar decoders own their failure text, as in the SDK pipeline.
			return failedResult(err), nil
		}
		result, err := call(ctx, request, decoded)
		if err == nil {
			return result, nil
		}
		// Match the SDK's typed pipeline: protocol errors cross unchanged and
		// every other failure becomes an isError result carrying the error.
		if protocolError, ok := errors.AsType[*jsonrpc.Error](err); ok {
			return nil, protocolError
		}
		return failedResult(err), nil
	})
}

func failedResult(err error) *mcp.CallToolResult {
	result := &mcp.CallToolResult{}
	result.SetError(err)
	return result
}

// handlerValidatedArgumentSchema resolves the advertised argument schema with
// each handler-validated property reduced to an object, for SDK-equivalent
// validation of the remaining arguments.
func handlerValidatedArgumentSchema(advertised json.RawMessage, properties []string) (*sdkschema.Resolved, error) {
	var document map[string]any
	if err := json.Unmarshal(advertised, &document); err != nil {
		return nil, fmt.Errorf("decode input schema: %w", err)
	}
	declared, _ := document["properties"].(map[string]any)
	for _, property := range properties {
		if _, ok := declared[property]; !ok {
			return nil, fmt.Errorf("handler-validated property %q is not declared", property)
		}
		declared[property] = map[string]any{"type": schemaTypeObject}
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode argument schema: %w", err)
	}
	var schema sdkschema.Schema
	if err = json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("decode argument schema: %w", err)
	}
	resolved, err := schema.Resolve(&sdkschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("resolve argument schema: %w", err)
	}
	return resolved, nil
}

// validateArguments applies defaults and validates tool arguments the way the
// SDK's typed pipeline does for an ordinary tool, returning the arguments to decode.
func validateArguments(raw json.RawMessage, schema *sdkschema.Resolved) (json.RawMessage, error) {
	arguments := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, fmt.Errorf("unmarshaling arguments: %w", err)
		}
	}
	if err := schema.ApplyDefaults(&arguments); err != nil {
		return nil, fmt.Errorf("applying schema defaults: %w", err)
	}
	if err := schema.Validate(&arguments); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("marshalling with defaults: %w", err)
	}
	return encoded, nil
}
