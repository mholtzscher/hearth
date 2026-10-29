package agent //nolint:testpackage // Transport fixtures exercise the module's published error boundary.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestAgentFrameworkProblemsMatchPublishedSchemas(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, media, body string
		status            int
	}{
		{"size", "application/json", strings.Repeat(" ", 1024*1024+1), 413},
		{"media", "text/plain", "{}", 415},
		{"JSON", "application/json", "{", 400},
		{"validation", "application/json", `{"text":""}`, 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Agent", "test"))
			// No turn can run on invalid input; unconfigured stub calls panic.
			RegisterHTTP(api, &stubOperations{}, nil)
			request := httptest.NewRequest(
				http.MethodPost,
				"/agent/conversations/aconv_synthetic/messages",
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.media)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			body, err := jsonschema.UnmarshalJSON(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			schema := agentMessageErrorSchema(t, api, test.status)
			if err = schema.Validate(body); err != nil {
				t.Fatalf("problem violates publication: %v", err)
			}
		})
	}
}

func agentMessageErrorSchema(t *testing.T, api huma.API, status int) *jsonschema.Schema {
	t.Helper()
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://hearth.invalid/agent"
	if err = compiler.AddResource(location, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(
		fmt.Sprintf(
			"%s#/paths/~1agent~1conversations~1{id}~1messages/post/responses/%d/content/application~1problem+json/schema",
			location,
			status,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestAgentOperationErrorCatalogs(t *testing.T) {
	t.Parallel()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Agent", "test"))
	RegisterHTTP(api, &stubOperations{}, nil)
	paths := api.OpenAPI().Paths
	for _, test := range []struct {
		operation *huma.Operation
		statuses  []int
	}{
		{paths["/agent/conversations"].Post, []int{500}},
		{paths["/agent/conversations"].Get, []int{500}},
		{paths["/agent/conversations/{id}/messages"].Get, []int{404, 422, 500}},
		{paths["/agent/conversations/{id}/messages"].Post, []int{400, 404, 413, 415, 422, 500, 502, 503}},
	} {
		var statuses []int
		for code := range test.operation.Responses {
			status, err := strconv.Atoi(code)
			if err != nil {
				t.Fatal(err)
			}
			if status >= http.StatusBadRequest {
				statuses = append(statuses, status)
			}
		}
		slices.Sort(statuses)
		if !slices.Equal(statuses, test.statuses) {
			t.Errorf("%s errors=%v, want=%v", test.operation.OperationID, statuses, test.statuses)
		}
	}
}
