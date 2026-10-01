package api

import (
	"mime"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

const mergePatchContentType = "application/merge-patch+json"

// mergePatchAPI decodes these operations as JSON after their middleware checks
// Content-Type. This avoids Huma's suffix parser interpreting valid MIME
// parameter values or whitespace as part of the format name.
type mergePatchAPI struct{ huma.API }

func (api mergePatchAPI) Unmarshal(_ string, data []byte, value any) error {
	return api.API.Unmarshal("application/json", data, value)
}

// DocumentOperation preserves the wrapped group's prefixes and modifiers in
// OpenAPI, just as its Adapter preserves them for runtime routes.
func (api mergePatchAPI) DocumentOperation(op *huma.Operation) {
	if documenter, ok := api.API.(huma.OperationDocumenter); ok {
		documenter.DocumentOperation(op)
	} else if !op.Hidden {
		api.OpenAPI().AddOperation(op)
	}
}

// requireMergePatch runs before Huma's body decoding, which otherwise accepts
// JSON regardless of the request media type documented by the operation.
func requireMergePatch(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		ctx.SetHeader("Accept-Patch", mergePatchContentType)
		mediaType, _, err := mime.ParseMediaType(ctx.Header("Content-Type"))
		if err != nil || mediaType != mergePatchContentType {
			_ = huma.WriteErr(api, ctx, http.StatusUnsupportedMediaType, "Content-Type must be "+mergePatchContentType)
			return
		}
		next(ctx)
	}
}
