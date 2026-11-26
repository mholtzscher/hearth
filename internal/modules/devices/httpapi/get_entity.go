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

func registerGetEntity(api huma.API, handler *handler) {
	registry := api.OpenAPI().Components.Schemas
	registerPairedStateSchemas(registry)
	registry.Schema(reflect.TypeFor[StateBody](), true, "")
	registry.Schema(reflect.TypeFor[StateBody](), false, "").AdditionalProperties = false
	huma.Register(api, huma.Operation{
		OperationID: "get-entity", Method: http.MethodGet, Path: "/entities/{entity_id}",
		Summary: "Get an Entity and its current State", Tags: []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.getEntity)
}

type GetEntityInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
}

type GetEntityOutput struct {
	Body PairedEntityBody
}

func (handler *handler) getEntity(ctx context.Context, input *GetEntityInput) (*GetEntityOutput, error) {
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
	body, err := handler.pairedEntityBody(view)
	if err != nil {
		return nil, handler.internalAPIError(ctx, "get-entity", "mapping", err)
	}
	return &GetEntityOutput{Body: body}, nil
}

// PairedEntityBody contains both readings without legacy quantities or state.value.
type PairedEntityBody struct {
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
	State        *PairedStateBody `json:"state"`
	Area         *EntityAreaBody  `json:"area"`
	Tags         []LabelRefBody   `json:"tags"          nullable:"false"`
}

type PairedStateBody struct {
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

func (PairedStateBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["canonical"] = &huma.Schema{Ref: "#/components/schemas/CanonicalStateTree"}
	s.Properties["display"] = &huma.Schema{Ref: "#/components/schemas/DisplayStateTree"}
	s.AdditionalProperties = false
	return s
}

func (PairedEntityBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.AdditionalProperties = false
	return s
}

func (handler *handler) pairedEntityBody(view devices.EntityWithState) (PairedEntityBody, error) {
	// Both projections use the exact same service result and never mutate it.
	canonical, canonicalMeta, err := handler.presenter.Entity(view, devices.UnitsCanonical)
	if err != nil {
		return PairedEntityBody{}, err
	}
	base, err := handler.entityBody(view, "")
	if err != nil {
		return PairedEntityBody{}, err
	}
	body := PairedEntityBody{
		AdapterName: base.AdapterName, NameOverride: base.NameOverride, ID: base.ID, DeviceID: base.DeviceID,
		AdapterID: base.AdapterID, Name: base.Name, Type: base.Type, Support: base.Support,
		Enabled: base.Enabled, Availability: base.Availability, Area: base.Area, Tags: base.Tags,
	}
	if view.State == nil {
		return body, nil
	}
	var c any
	if err = decodeJSON(canonical.State.Value, &c); err != nil {
		return PairedEntityBody{}, err
	}
	c = wrapStateQuantities(c, "", canonicalMeta, false)
	d := wrapStateQuantities(base.State.Value, "", base.Quantities, true)
	body.State = &PairedStateBody{
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
