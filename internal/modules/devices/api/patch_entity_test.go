package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestPatchEntitySetsEnablementAndReturnsCompleteEntity(t *testing.T) {
	t.Parallel()
	var gotID devices.EntityID
	var gotEnabled bool
	stub := &stubDevices{setEntityEnabled: func(
		_ context.Context,
		entityID devices.EntityID,
		enabled bool,
	) (devices.EntityWithState, error) {
		gotID = entityID
		gotEnabled = enabled
		view := apiEntityWithState(nil)
		view.Entity.Enabled = enabled
		return view, nil
	}}
	router, openapi := testAPI(t, stub)
	response := patchEntityRequest(router, string(apiEntityID), `{"enabled":false}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body EntityBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if gotID != apiEntityID || gotEnabled || body.ID != string(apiEntityID) || body.Enabled {
		t.Fatalf("mutation = (%q, %t), body = %#v", gotID, gotEnabled, body)
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}"].Patch
	if operation == nil || operation.OperationID != "update-entity" || operation.Summary != "Update an Entity" {
		t.Fatalf("PATCH operation = %#v", operation)
	}
}

func TestPatchEntityUsesHumaStructuralValidation(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":false,"extra":true}`} {
		response := patchEntityRequest(router, string(apiEntityID), body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s: status = %d, response = %s", body, response.Code, response.Body.String())
		}
		var problem huma.ErrorModel
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if problem.Status != http.StatusUnprocessableEntity {
			t.Fatalf("body %s: problem = %#v", body, problem)
		}
	}
}

func TestPatchEntityMapsDomainErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		id     string
		err    error
		status int
		detail string
	}{
		{"invalid ID", "bad", nil, http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID"},
		{"unknown Entity", string(apiEntityID), devices.ErrEntityNotFound, http.StatusNotFound, "entity not found"},
		{"internal", string(apiEntityID), errors.New("SQLite unavailable"), http.StatusInternalServerError, "internal error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubDevices{}
			if test.id == string(apiEntityID) {
				stub.setEntityEnabled = func(context.Context, devices.EntityID, bool) (devices.EntityWithState, error) {
					return devices.EntityWithState{}, test.err
				}
			}
			router, _ := testAPI(t, stub)
			response := patchEntityRequest(router, test.id, `{"enabled":true}`)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var problem huma.ErrorModel
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Detail != test.detail {
				t.Fatalf("problem = %#v", problem)
			}
		})
	}
}

func patchEntityRequest(handler http.Handler, entityID, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPatch, "/v1/entities/"+entityID, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
