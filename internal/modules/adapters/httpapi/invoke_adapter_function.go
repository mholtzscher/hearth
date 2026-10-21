//nolint:goconst // Literal field names keep the invocation outer contract readable.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerInvokeAdapterFunction(api huma.API, h *functionHandler) {
	invoke := functionOperation(api,
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
}

type invokeFunctionInput struct {
	FunctionInput

	RawBody []byte
}
type invokeFunctionOutput struct {
	Location string `header:"Location"`
	Body     InvocationBody
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

const maximumFunctionBodyBytes = 128 * 1024

type InvokeBody struct {
	CatalogRevision string          `json:"catalog_revision"`
	Arguments       json.RawMessage `json:"arguments"`
}

// UnmarshalJSON validates only the outer contract. The service owns argument
// normalization, bounds, and dynamic schema validation.
func (body *InvokeBody) UnmarshalJSON(data []byte) error {
	if !jsontext.Value(data).IsValid() {
		return errors.New("invalid function input")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 2 {
		return errors.New("invalid function input")
	}
	var decoded InvokeBody
	revision, present := fields["catalog_revision"]
	if !present || bytes.Equal(revision, []byte("null")) || json.Unmarshal(revision, &decoded.CatalogRevision) != nil {
		return errors.New("invalid function input")
	}
	arguments, present := fields["arguments"]
	if !present || len(bytes.TrimSpace(arguments)) == 0 || bytes.TrimSpace(arguments)[0] != '{' {
		return errors.New("invalid function input")
	}
	decoded.Arguments = arguments
	*body = decoded
	return nil
}

func (InvokeBody) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "object", AdditionalProperties: false,
		Required:   []string{"catalog_revision", "arguments"},
		Properties: map[string]*huma.Schema{"catalog_revision": textSchema(), "arguments": objectSchema()}}
}
