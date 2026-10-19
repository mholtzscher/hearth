package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type Functions interface {
	ListFunctions(context.Context, string, adapters.PageInput) (adapters.FunctionPage, error)
	GetFunction(context.Context, string, adapters.FunctionKey) (adapters.Function, error)
	Invoke(context.Context, adapters.InvokeInput) (adapters.Invocation, error)
	ListInvocations(context.Context, adapters.InvocationFilter) (adapters.InvocationPage, error)
	GetInvocation(context.Context, adapters.InvocationID) (adapters.Invocation, error)
}

type functionHandler struct{ service Functions }
type FunctionPageInput struct {
	AdapterID string `path:"adapter_id"`
	Limit     int    `                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor    string `                  query:"cursor"`
}
type FunctionInput struct {
	AdapterID   string `path:"adapter_id"`
	FunctionKey string `path:"function_key"`
}
type invokeFunctionInput struct {
	FunctionInput

	RawBody []byte
}
type invocationListInput struct {
	FunctionPageInput

	FunctionKey string `query:"function_key"`
}
type invocationInput struct {
	InvocationID string `path:"invocation_id"`
}
type functionOutput struct{ Body FunctionBody }
type functionListOutput struct{ Body FunctionCollectionBody }
type invocationOutput struct{ Body InvocationBody }
type invocationListOutput struct{ Body InvocationCollectionBody }
type invokeFunctionOutput struct {
	Location string `header:"Location"`
	Body     InvocationBody
}

func RegisterFunctions(api huma.API, service Functions) {
	h := &functionHandler{service: service}
	errors := []int{
		http.StatusUnprocessableEntity,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusServiceUnavailable,
		http.StatusInternalServerError,
	}
	operation := func(id, method, path, summary string) huma.Operation {
		responses := make(map[string]*huma.Response, len(errors))
		for _, status := range errors {
			responses[strconv.Itoa(status)] = &huma.Response{Description: http.StatusText(status),
				Content: map[string]*huma.MediaType{"application/problem+json": {
					Schema: huma.SchemaFromType(
						api.OpenAPI().Components.Schemas,
						reflect.TypeFor[functionProblemError](),
					),
				}}}
		}
		return huma.Operation{
			OperationID: id,
			Method:      method,
			Path:        path,
			Summary:     summary,
			Tags:        []string{"Adapters"},
			Errors:      errors,
			Responses:   responses,
		}
	}
	huma.Register(
		api,
		operation(
			"list-adapter-functions",
			http.MethodGet,
			"/adapters/{adapter_id}/functions",
			"Discover Adapter functions",
		),
		h.listFunctions,
	)
	huma.Register(
		api,
		operation(
			"get-adapter-function",
			http.MethodGet,
			"/adapters/{adapter_id}/functions/{function_key}",
			"Describe an Adapter function before invocation",
		),
		h.getFunction,
	)
	invoke := operation(
		"invoke-adapter-function",
		http.MethodPost,
		"/adapters/{adapter_id}/functions/{function_key}/invocations",
		"Invoke once without retry",
	)
	// Huma rejects count == MaxBodyBytes, so allow its one-byte overflow probe.
	invoke.MaxBodyBytes = maximumFunctionBodyBytes + 1
	invoke.SkipValidateBody = true
	invoke.Middlewares = huma.Middlewares{requireFunctionBody(api)}
	invoke.Errors = append(invoke.Errors, http.StatusRequestEntityTooLarge)
	invoke.RequestBody = &huma.RequestBody{
		Required: true,
		Content:  map[string]*huma.MediaType{"application/json": {Schema: InvokeBody{}.Schema(nil)}},
	}
	huma.Register(api, invoke, h.invoke)
	// RawBody owns bounded decoding; replace Huma's binary-body advertisement.
	invoke.RequestBody.Content = map[string]*huma.MediaType{"application/json": {Schema: InvokeBody{}.Schema(nil)}}
	huma.Register(
		api,
		operation(
			"list-adapter-function-invocations",
			http.MethodGet,
			"/adapters/{adapter_id}/function-invocations",
			"List retained function invocation evidence",
		),
		h.listInvocations,
	)
	huma.Register(
		api,
		operation(
			"get-adapter-function-invocation",
			http.MethodGet,
			"/adapter-function-invocations/{invocation_id}",
			"Read retained function invocation evidence",
		),
		h.getInvocation,
	)
}

func (h *functionHandler) listFunctions(ctx context.Context, in *FunctionPageInput) (*functionListOutput, error) {
	page, err := h.service.ListFunctions(ctx, in.AdapterID, adapters.PageInput{Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, functionHTTPError(err)
	}
	body := FunctionCollectionBody{
		Items:      make([]FunctionBody, len(page.Items)),
		NextCursor: functionNextCursor(page.NextCursor),
	}
	for index, item := range page.Items {
		body.Items[index] = functionBody(item)
	}
	return &functionListOutput{Body: body}, nil
}

func (h *functionHandler) getFunction(ctx context.Context, in *FunctionInput) (*functionOutput, error) {
	function, err := h.service.GetFunction(ctx, in.AdapterID, adapters.FunctionKey(in.FunctionKey))
	if err != nil {
		return nil, functionHTTPError(err)
	}
	return &functionOutput{Body: functionBody(function)}, nil
}

type functionRequestContext = huma.Context

type functionBodyContext struct {
	functionRequestContext

	reader io.Reader
}

func (ctx functionBodyContext) BodyReader() io.Reader { return ctx.reader }

// Huma otherwise categorizes an empty required body as 400. Probe only one
// byte here so the function contract returns 422 without parsing unbounded input.
func requireFunctionBody(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		reader := ctx.BodyReader()
		first := make([]byte, 1)
		if reader == nil {
			_ = huma.WriteErr(api, ctx, http.StatusUnprocessableEntity, "invalid function input")
			return
		}
		if _, err := io.ReadFull(reader, first); err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusUnprocessableEntity, "invalid function input")
			return
		}
		next(functionBodyContext{functionRequestContext: ctx, reader: io.MultiReader(bytes.NewReader(first), reader)})
	}
}

func (h *functionHandler) invoke(ctx context.Context, in *invokeFunctionInput) (*invokeFunctionOutput, error) {
	var body InvokeBody
	if err := json.Unmarshal(in.RawBody, &body); err != nil {
		return nil, functionHTTPError(adapters.ErrInvalidFunctionInput)
	}
	invocation, err := h.service.Invoke(ctx, adapters.InvokeInput{
		AdapterID: in.AdapterID,
		FunctionKey: adapters.FunctionKey(
			in.FunctionKey,
		),
		CatalogRevision: adapters.CatalogID(body.CatalogRevision),
		Arguments:       body.Arguments,
	})
	if err != nil {
		return nil, functionHTTPError(err)
	}
	return &invokeFunctionOutput{
		Location: "/v1/adapter-function-invocations/" + string(invocation.ID),
		Body:     invocationBody(invocation),
	}, nil
}

func (h *functionHandler) listInvocations(ctx context.Context, in *invocationListInput) (*invocationListOutput, error) {
	pageInput := adapters.PageInput{Limit: in.Limit, Cursor: in.Cursor}
	page, err := h.service.ListInvocations(ctx, adapters.InvocationFilter{
		AdapterID: in.AdapterID,
		FunctionKey: adapters.FunctionKey(
			in.FunctionKey,
		),
		PageInput: pageInput,
	})
	if err != nil {
		return nil, functionHTTPError(err)
	}
	body := InvocationCollectionBody{
		Items:      make([]InvocationBody, len(page.Items)),
		NextCursor: functionNextCursor(page.NextCursor),
	}
	for index, item := range page.Items {
		body.Items[index] = invocationBody(item)
	}
	return &invocationListOutput{Body: body}, nil
}

func (h *functionHandler) getInvocation(ctx context.Context, in *invocationInput) (*invocationOutput, error) {
	invocation, err := h.service.GetInvocation(ctx, adapters.InvocationID(in.InvocationID))
	if err != nil {
		return nil, functionHTTPError(err)
	}
	return &invocationOutput{Body: invocationBody(invocation)}, nil
}
