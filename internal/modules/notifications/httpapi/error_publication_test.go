package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	notificationsapi "github.com/mholtzscher/hearth/internal/modules/notifications/httpapi"
)

func TestNotificationFrameworkProblemsMatchPublishedDefault(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, method, path, schemaPath, media, body string
		status                                      int
	}{
		{"destination query", "GET", "/notification-destinations?limit=0", "/notification-destinations", "", "", 422},
		{"attempt query", "GET", "/notifications?limit=0", "/notifications", "", "", 422},
		{"send size", "POST", "/notification-destinations/ndst_synthetic/notifications", "/notification-destinations/{id}/notifications", "application/json", strings.Repeat(" ", 1024*1024+1), 413},
		{"send media", "POST", "/notification-destinations/ndst_synthetic/notifications", "/notification-destinations/{id}/notifications", "text/plain", "{}", 415},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Notifications", "test"))
			notificationsapi.Register(api, nil, nil)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
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
			schema := notificationDefaultErrorSchema(t, api, test.schemaPath, test.method)
			if err = schema.Validate(body); err != nil {
				t.Fatalf("problem violates default publication: %v", err)
			}
		})
	}
}

func notificationDefaultErrorSchema(t *testing.T, api huma.API, path, method string) *jsonschema.Schema {
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
	const location = "https://hearth.invalid/notifications"
	if err = compiler.AddResource(location, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(
		location + "#/paths/" + strings.ReplaceAll(
			path,
			"/",
			"~1",
		) + "/" + strings.ToLower(
			method,
		) + "/responses/default/content/application~1problem+json/schema",
	)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
