package httpapi

import (
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// LabelBody is a household Label.
type LabelBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"       enum:"area,tag"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// LabelRefBody names a Label assigned to a Device or Entity.
type LabelRefBody struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// EntityAreaBody is an Entity's effective Area and where it came from.
type EntityAreaBody struct {
	// Huma uses this ignored marker to emit EntityAreaBody as an object-or-null schema.
	_      struct{} `json:"-"      nullable:"true"`
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Source string   `json:"source"                 enum:"device,entity" doc:"device when inherited from the Device; entity when set on the Entity"`
}

// DeviceAreaBody is the Area assigned directly to a Device.
type DeviceAreaBody struct {
	// Huma uses this ignored marker to emit DeviceAreaBody as an object-or-null schema.
	_    struct{} `json:"-"    nullable:"true"`
	ID   string   `json:"id"`
	Name string   `json:"name"`
}

const problemJSONContentType = "application/problem+json"

type labelConflictError struct {
	huma.ErrorModel

	Code string `json:"code" enum:"label_name_conflict,label_limit_reached"`
}

// Schema retains the required domain fields, not the native optional base shape.
func (labelConflictError) Schema(registry huma.Registry) *huma.Schema {
	type fields labelConflictError
	schema := huma.SchemaFromType(registry, reflect.TypeFor[fields]())
	schema.Required = append(schema.Required, "type", "title", "status", "detail")
	return schema
}

func labelConflictProblem(detail, code string) error {
	problem := &labelConflictError{Code: code}
	problem.Type = "about:blank"
	problem.Title = http.StatusText(http.StatusConflict)
	problem.Status = http.StatusConflict
	problem.Detail = detail
	return problem
}

func labelConflictResponses(api huma.API) map[string]*huma.Response {
	schema := huma.SchemaFromType(api.OpenAPI().Components.Schemas, reflect.TypeFor[labelConflictError]())
	return map[string]*huma.Response{
		"409": {
			Description: http.StatusText(http.StatusConflict),
			Content:     map[string]*huma.MediaType{problemJSONContentType: {Schema: schema}},
		},
	}
}
