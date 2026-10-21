package httpapi //nolint:testpackage // Tests use private transport helpers and domain doubles.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestReadInternalFailureRendersGenericProblem pins the safe HTTP problem,
// content type, and schema link without exposing the private service cause.
func TestReadInternalFailureRendersGenericProblem(t *testing.T) {
	t.Parallel()
	const cause = "SQLite unavailable: /var/lib/hearth/hearth.db"
	router, _ := testAPI(t, &stubDevices{getEntity: func(
		context.Context,
		devices.EntityID,
	) (devices.EntityWithState, error) {
		return devices.EntityWithState{}, errors.New(cause)
	}})

	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Fatalf("content type = %q, want application/problem+json", contentType)
	}
	if link := response.Header().Get("Link"); !strings.Contains(link, "ErrorModel.json") {
		t.Fatalf("Link = %q, want the generic problem schema link", link)
	}
	body := response.Body.String()
	for _, want := range []string{
		`"$schema"`, `"title":"Internal Server Error"`, `"status":500`, `"detail":"internal error"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "hearth.db") || strings.Contains(body, "SQLite") {
		t.Fatalf("body leaked the cause: %s", body)
	}
}
