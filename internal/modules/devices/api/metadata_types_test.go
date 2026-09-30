package api //nolint:testpackage // Tests exercise request models with package-local HTTP fixtures.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
)

// These decoder-only operations use the production request models without a
// database or mutation handler. D3 owns semantic validation and mutation safety.
func TestMetadataPatchDecoder(t *testing.T) {
	t.Parallel()
	disabled := false
	override := "Kitchen ceiling"
	reset := PatchEntityBody{NameEdit: &NameEditBody{}}
	rename := PatchEntityBody{NameEdit: &NameEditBody{Override: &override}}
	for _, kind := range []string{"entity", "device"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			cases := []metadataDecoderCase{
				{"omitted fields", `{}`, http.StatusOK, PatchEntityBody{}},
				{"null name edit", `{"name_edit":null}`, http.StatusUnprocessableEntity, PatchEntityBody{}},
				{
					"case-insensitive null name edit",
					`{"NAME_EDIT":null}`,
					http.StatusUnprocessableEntity,
					PatchEntityBody{},
				},
				{"missing override", `{"name_edit":{}}`, http.StatusUnprocessableEntity, PatchEntityBody{}},
				{"reset", `{"name_edit":{"override":null}}`, http.StatusOK, reset},
				{"rename", `{"name_edit":{"override":"Kitchen ceiling"}}`, http.StatusOK, rename},
				{
					"wrong override type",
					`{"name_edit":{"override":false}}`,
					http.StatusUnprocessableEntity,
					PatchEntityBody{},
				},
				{"unknown field", `{"unknown":true}`, http.StatusUnprocessableEntity, PatchEntityBody{}},
			}
			if kind == "entity" {
				cases = append(
					cases,
					metadataDecoderCase{
						"disabled",
						`{"enabled":false}`,
						http.StatusOK,
						PatchEntityBody{Enabled: &disabled},
					},
					metadataDecoderCase{
						"mixed reset",
						`{"enabled":false,"name_edit":{"override":null}}`,
						http.StatusOK,
						PatchEntityBody{Enabled: &disabled, NameEdit: &NameEditBody{}},
					},
					metadataDecoderCase{
						"null enabled",
						`{"enabled":null}`,
						http.StatusUnprocessableEntity,
						PatchEntityBody{},
					},
					metadataDecoderCase{
						"case-insensitive null enabled",
						`{"ENABLED":null}`,
						http.StatusUnprocessableEntity,
						PatchEntityBody{},
					},
					metadataDecoderCase{
						"null enabled with rename",
						`{"enabled":null,"name_edit":{"override":"Kitchen ceiling"}}`,
						http.StatusUnprocessableEntity,
						PatchEntityBody{},
					},
					metadataDecoderCase{
						"null name edit with enablement",
						`{"enabled":true,"name_edit":null}`,
						http.StatusUnprocessableEntity,
						PatchEntityBody{},
					},
				)
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					assertMetadataDecoding(t, kind, test)
				})
			}
		})
	}
}

type metadataDecoderCase struct {
	name   string
	body   string
	status int
	want   PatchEntityBody
}

func assertMetadataDecoding(t *testing.T, kind string, test metadataDecoderCase) {
	t.Helper()
	var decoded PatchEntityBody
	called := false
	router := metadataDecoderAPI(kind, func(body PatchEntityBody) { called = true; decoded = body })
	request := httptest.NewRequest(http.MethodPatch, "/patch", strings.NewReader(test.body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != test.status {
		t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
	}
	if called != (test.status == http.StatusOK) {
		t.Fatalf("handler called = %v", called)
	}
	if called && !reflect.DeepEqual(decoded, test.want) {
		t.Fatalf("decoded = %#v, want %#v", decoded, test.want)
	}
}

func metadataDecoderAPI(kind string, receive func(PatchEntityBody)) *echo.Echo {
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	op := huma.Operation{OperationID: "decode-" + kind, Method: http.MethodPatch, Path: "/patch"}
	if kind == "entity" {
		huma.Register(
			api,
			op,
			func(_ context.Context, input *struct{ Body PatchEntityBody }) (*struct{ Body string }, error) {
				receive(input.Body)
				return &struct{ Body string }{Body: "decoded"}, nil
			},
		)
	} else {
		huma.Register(
			api,
			op,
			func(_ context.Context, input *struct{ Body PatchDeviceBody }) (*struct{ Body string }, error) {
				receive(PatchEntityBody{NameEdit: input.Body.NameEdit})
				return &struct{ Body string }{Body: "decoded"}, nil
			},
		)
	}
	return router
}

func TestMetadataPatchExportedSchemas(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"entity", "device"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			router := metadataDecoderAPI(kind, func(PatchEntityBody) {})
			response := performRequest(router, "/openapi.json")
			if response.Code != http.StatusOK {
				t.Fatalf("OpenAPI status = %d", response.Code)
			}
			var document struct {
				Components struct {
					Schemas map[string]json.RawMessage `json:"schemas"`
				} `json:"components"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			name := "PatchEntityBody"
			if kind == "device" {
				name = "PatchDeviceBody"
			}
			patch := decodeMetadataSchema(t, document.Components.Schemas[name])
			assertMetadataSchema(
				t,
				!slices.Contains(patch.Required, "name_edit") && !slices.Contains(patch.Required, "enabled"),
				"PATCH fields must be optional",
				document.Components.Schemas[name],
			)
			edit := decodeMetadataSchema(t, patch.Properties["name_edit"])
			assertMetadataSchema(
				t,
				edit.Ref == "#/components/schemas/NameEditBody",
				"name_edit must reference NameEditBody",
				patch.Properties["name_edit"],
			)
			nested := decodeMetadataSchema(t, document.Components.Schemas["NameEditBody"])
			assertMetadataSchema(
				t,
				string(nested.Type) == `"object"` && slices.Contains(nested.Required, "override"),
				"name_edit must be non-null with required override",
				document.Components.Schemas["NameEditBody"],
			)
			override := decodeMetadataSchema(t, nested.Properties["override"])
			var types []string
			if err := json.Unmarshal(override.Type, &types); err != nil {
				t.Fatal(err)
			}
			assertMetadataSchema(
				t,
				slices.Contains(types, "string") && slices.Contains(types, "null"),
				"override must allow string and null",
				nested.Properties["override"],
			)
			if kind == "entity" {
				assertMetadataSchema(
					t,
					string(decodeMetadataSchema(t, patch.Properties["enabled"]).Type) == `"boolean"`,
					"enabled must be non-null boolean",
					patch.Properties["enabled"],
				)
			}
		})
	}
}

type metadataSchema struct {
	Type       json.RawMessage            `json:"type"`
	Ref        string                     `json:"$ref"`
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func decodeMetadataSchema(t *testing.T, raw json.RawMessage) metadataSchema {
	t.Helper()
	var value metadataSchema
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertMetadataSchema(t *testing.T, valid bool, message string, raw json.RawMessage) {
	t.Helper()
	if !valid {
		t.Fatalf("%s: %s", message, raw)
	}
}
