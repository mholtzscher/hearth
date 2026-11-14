package httpapi

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:goconst // This operation advertises the standard JSON media type.
func registerGetEntity(api huma.API, handler *handler) {
	registry := api.OpenAPI().Components.Schemas
	registerPairedStateSchemas(registry)
	legacy := registry.Schema(reflect.TypeFor[EntityBody](), false, "")
	legacy.AdditionalProperties = false
	legacyState := registry.Schema(reflect.TypeFor[StateBody](), true, "")
	registry.Schema(reflect.TypeFor[StateBody](), false, "").AdditionalProperties = false
	legacy.Properties["state"] = legacyState
	huma.Register(api, huma.Operation{
		OperationID: "get-entity", Method: http.MethodGet, Path: "/entities/{entity_id}",
		Summary: "Get an Entity and its current State", Tags: []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
		Responses: map[string]*huma.Response{"200": {
			Description: "Entity and current State",
			Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{OneOf: []*huma.Schema{
				legacy,
				registry.Schema(reflect.TypeFor[pairedEntityBody](), true, ""),
			}}}},
		}},
	}, handler.getEntity)
}

type GetEntityInput struct {
	EntityID       string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Units          string `                 doc:"Omit for household units; canonical returns canonical values" query:"units"`
	Include        string `                 doc:"canonical includes canonical and household readings together" query:"include"`
	invalidInclude bool
}

func (input *GetEntityInput) Resolve(ctx huma.Context) []error {
	input.invalidInclude = invalidIncludeSelection(ctx)
	return nil
}

type GetEntityOutput struct {
	Body any
}

func (handler *handler) getEntity(ctx context.Context, input *GetEntityInput) (*GetEntityOutput, error) {
	if input.invalidInclude || !validPairedInclude(input.Include, input.Units) {
		return nil, huma.NewError(http.StatusBadRequest, "invalid include")
	}
	units, err := devices.ParseReadUnits(input.Units)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid units")
	}
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	view, err := handler.devices.GetEntity(ctx, entityID)
	if errors.Is(err, devices.ErrEntityNotFound) {
		return nil, huma.NewError(http.StatusNotFound, "entity not found")
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "get-entity", "service", err)
	}
	if input.Include == string(devices.UnitsCanonical) {
		body, mapErr := handler.pairedEntityBody(view)
		if mapErr != nil {
			return nil, handler.internalAPIError(ctx, "get-entity", "mapping", mapErr)
		}
		return &GetEntityOutput{Body: body}, nil
	}
	body, err := handler.entityBody(view, units)
	if err != nil {
		return nil, handler.internalAPIError(ctx, "get-entity", "mapping", err)
	}
	return &GetEntityOutput{Body: body}, nil
}

// Paired reads intentionally have neither legacy quantities nor state.value.
type pairedEntityBody struct {
	AdapterName  string           `json:"adapter_name"`
	NameOverride *string          `json:"name_override" nullable:"true"`
	ID           string           `json:"id"`
	DeviceID     string           `json:"device_id"`
	AdapterID    string           `json:"adapter_id"`
	Name         string           `json:"name"`
	Type         string           `json:"type"`
	Support      map[string]any   `json:"support"`
	Enabled      bool             `json:"enabled"`
	Availability AvailabilityBody `json:"availability"`
	State        *pairedStateBody `json:"state"`
}

type pairedStateBody struct {
	_                 struct{} `json:"-"                           nullable:"true"`
	ObservationID     string   `json:"observation_id"`
	AdapterReceivedAt string   `json:"adapter_received_at"`
	SourceUpdatedAt   *string  `json:"source_updated_at,omitempty"`
	ObservedAt        string   `json:"observed_at"`
	Canonical         any      `json:"canonical"`
	Display           any      `json:"display"`
}

type canonicalReading struct {
	Value any    `json:"value"`
	Unit  string `json:"unit"`
}

type displayReading struct {
	Value          any                     `json:"value"`
	Unit           string                  `json:"unit"`
	Label          string                  `json:"label"`
	FractionDigits int                     `json:"fraction_digits"`
	Range          *devices.QuantityExtent `json:"range,omitempty"`
}

func (canonicalReading) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["value"] = &huma.Schema{Type: huma.TypeNumber}
	return s
}

func (displayReading) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["value"] = &huma.Schema{Type: huma.TypeNumber}
	return s
}

// Each tree permits the plain non-quantity fields and arrays of a State alongside
// typed reading leaves; references make forecast period arrays recursively typed.
func registerPairedStateSchemas(r huma.Registry) {
	for _, entry := range []struct {
		name string
		leaf *huma.Schema
	}{
		{"CanonicalStateTree", r.Schema(reflect.TypeFor[canonicalReading](), false, "")},
		{"DisplayStateTree", r.Schema(reflect.TypeFor[displayReading](), false, "")},
	} {
		entry.leaf.AdditionalProperties = false
		ref := &huma.Schema{Ref: "#/components/schemas/" + entry.name}
		r.Map()[entry.name] = &huma.Schema{AnyOf: []*huma.Schema{
			entry.leaf,
			{
				Type: huma.TypeObject, AdditionalProperties: ref,
				Not: &huma.Schema{Required: []string{"value", "unit"}},
			},
			{Type: huma.TypeArray, Items: ref},
			{Type: huma.TypeString}, {Type: huma.TypeBoolean}, {Type: huma.TypeNumber}, {Type: "null"},
		}}
	}
}

func (pairedStateBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["canonical"] = &huma.Schema{Ref: "#/components/schemas/CanonicalStateTree"}
	s.Properties["display"] = &huma.Schema{Ref: "#/components/schemas/DisplayStateTree"}
	s.AdditionalProperties = false
	return s
}

func (pairedEntityBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.AdditionalProperties = false
	return s
}

func (handler *handler) pairedEntityBody(view devices.EntityWithState) (pairedEntityBody, error) {
	// Both projections use the exact same service result and never mutate it.
	canonical, canonicalMeta, err := handler.presenter.Entity(view, devices.UnitsCanonical)
	if err != nil {
		return pairedEntityBody{}, err
	}
	base, err := handler.entityBody(view, "")
	if err != nil {
		return pairedEntityBody{}, err
	}
	body := pairedEntityBody{
		AdapterName: base.AdapterName, NameOverride: base.NameOverride, ID: base.ID, DeviceID: base.DeviceID,
		AdapterID: base.AdapterID, Name: base.Name, Type: base.Type, Support: base.Support,
		Enabled: base.Enabled, Availability: base.Availability,
	}
	if view.State == nil {
		return body, nil
	}
	var c any
	if err = decodeJSON(canonical.State.Value, &c); err != nil {
		return pairedEntityBody{}, err
	}
	c = wrapStateQuantities(c, "", canonicalMeta, false)
	d := wrapStateQuantities(base.State.Value, "", base.Quantities, true)
	body.State = &pairedStateBody{
		ObservationID: base.State.ObservationID, AdapterReceivedAt: base.State.AdapterReceivedAt,
		SourceUpdatedAt: base.State.SourceUpdatedAt, ObservedAt: base.State.ObservedAt,
		Canonical: c, Display: d,
	}
	return body, nil
}

func wrapStateQuantities(value any, path string, metadata map[string]devices.QuantityMetadata, display bool) any {
	if meta, ok := metadata[path]; ok && value != nil {
		if display {
			return displayReading{
				Value:          value,
				Unit:           meta.Unit,
				Label:          meta.Label,
				FractionDigits: meta.FractionDigits,
				Range:          meta.Range,
			}
		}
		return canonicalReading{Value: value, Unit: meta.Unit}
	}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			value[key] = wrapStateQuantities(child, path+"/"+key, metadata, display)
		}
	case []any:
		for i, child := range value {
			value[i] = wrapStateQuantities(child, path+"/"+strconv.Itoa(i), metadata, display)
		}
	}
	return value
}
