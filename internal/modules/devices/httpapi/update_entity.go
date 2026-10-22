package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerUpdateEntity(api huma.API, handler *handler) {
	huma.Register(mergePatchAPI{api}, huma.Operation{
		OperationID: "update-entity",
		Method:      http.MethodPatch,
		Path:        "/entities/{entity_id}",
		Summary:     "Update an Entity",
		Tags:        []string{entitiesTag},
		Description: "Merge writable metadata fields. Omitted fields are unchanged; null name_override clears the override. An empty object returns the current Entity without mutation.",
		Middlewares: huma.Middlewares{requireMergePatch(api)},
		Errors: []int{
			http.StatusBadRequest,
			http.StatusNotFound,
			http.StatusUnsupportedMediaType,
			http.StatusUnprocessableEntity,
			http.StatusInternalServerError,
		},
	}, handler.patchEntity)
}

type PatchEntityInput struct {
	EntityID string          `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Body     PatchEntityBody `                 doc:"Mutable Entity fields"      contentType:"application/merge-patch+json"`
}

type PatchEntityOutput struct {
	Body EntityBody
}

func (handler *handler) patchEntity(ctx context.Context, input *PatchEntityInput) (*PatchEntityOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	var view devices.EntityWithState
	if input.Body.Enabled == nil && !input.Body.nameOverridePresent {
		view, err = handler.devices.GetEntity(ctx, entityID)
	} else {
		view, err = handler.devices.PatchEntity(ctx, entityID, devices.EntityPatch{
			Enabled:  input.Body.Enabled,
			NameEdit: domainNameEdit(input.Body.nameOverridePresent, input.Body.NameOverride),
		})
	}
	switch {
	case errors.Is(err, devices.ErrInvalidMetadataPatch):
		return nil, huma.NewError(http.StatusBadRequest, "invalid metadata patch")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, huma.NewError(http.StatusNotFound, "entity not found")
	case err != nil:
		return nil, internalAPIError(err)
	}
	body, err := entityBody(view)
	if err != nil {
		return nil, internalAPIError(err)
	}
	return &PatchEntityOutput{Body: body}, nil
}

type PatchEntityBody struct {
	Enabled             *bool   `json:"enabled,omitempty"`
	NameOverride        *string `json:"name_override,omitempty" nullable:"true"`
	nameOverridePresent bool
}

func (body *PatchEntityBody) UnmarshalJSON(data []byte) error {
	properties, err := metadataPatchProperties(data, true)
	if err != nil {
		return err
	}
	type decodedBody PatchEntityBody
	var decoded decodedBody
	if err = json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	_, decoded.nameOverridePresent = properties["name_override"]
	*body = PatchEntityBody(decoded)
	return nil
}
func domainNameEdit(present bool, override *string) *devices.NameEdit {
	if !present {
		return nil
	}
	return &devices.NameEdit{Override: override}
}
