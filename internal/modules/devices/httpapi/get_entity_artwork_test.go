package httpapi //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const artworkJPEG = "\xff\xd8\xff\xe0\x00\x10JFIF\x00image"

func artworkRequest(router http.Handler, ifNoneMatch string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/v1/entities/"+string(apiEntityID)+"/artwork?v=3f2a", nil)
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestGetEntityArtworkServesTheImageWithLockedDownHeaders(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{fetchArtwork: func(_ context.Context, id devices.EntityID) (devices.Artwork, error) {
		if id != apiEntityID {
			t.Errorf("FetchArtwork ID = %q", id)
		}
		return devices.Artwork{ContentType: "image/jpeg", Data: []byte(artworkJPEG), ETag: "0123456789abcdef"}, nil
	}}
	router, openapi := testAPI(t, stub)
	response := artworkRequest(router, "")
	if response.Code != http.StatusOK || response.Body.String() != artworkJPEG {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/jpeg",
		"Cache-Control":           "private, max-age=3600",
		"ETag":                    `"0123456789abcdef"`,
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'",
	} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}/artwork"].Get
	if operation == nil || operation.OperationID != "get-entity-artwork" ||
		operation.Responses["200"].Content["image/webp"] == nil || operation.Responses["304"] == nil ||
		operation.Responses["502"] == nil || operation.Responses["504"] == nil {
		t.Fatalf("GET operation = %#v", operation)
	}
}

func TestGetEntityArtworkRevalidatesWithoutFetching(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{artworkETag: func(context.Context, devices.EntityID) (string, error) {
		return "0123456789abcdef", nil
	}}
	router, _ := testAPI(t, stub)
	for _, header := range []string{`"0123456789abcdef"`, `W/"0123456789abcdef"`, `"stale", "0123456789abcdef"`, `*`} {
		response := artworkRequest(router, header)
		if response.Code != http.StatusNotModified || response.Body.Len() != 0 ||
			response.Header().Get("ETag") != `"0123456789abcdef"` ||
			response.Header().Get("Cache-Control") != "private, max-age=3600" ||
			response.Header().Get("Content-Type") != "" {
			t.Fatalf("If-None-Match %s: status = %d, headers %v, body %q", header, response.Code,
				response.Header(), response.Body.String())
		}
	}
}

func TestGetEntityArtworkWildcardStillNeedsArtwork(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{artworkETag: func(context.Context, devices.EntityID) (string, error) {
		return "", devices.ErrArtworkNotFound
	}}
	router, _ := testAPI(t, stub)
	if response := artworkRequest(router, "*"); response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no artwork exists", response.Code)
	}
}

func TestEtagMatchesOnlyAWholeWildcard(t *testing.T) {
	t.Parallel()
	if etagMatches(`"stale", *`, `"0123456789abcdef"`) {
		t.Fatal("a wildcard inside a list matched")
	}
}

func TestGetEntityArtworkFetchesWhenTheETagChanged(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{
		artworkETag: func(context.Context, devices.EntityID) (string, error) { return "fedcba9876543210", nil },
		fetchArtwork: func(context.Context, devices.EntityID) (devices.Artwork, error) {
			return devices.Artwork{ContentType: "image/jpeg", Data: []byte(artworkJPEG), ETag: "fedcba9876543210"}, nil
		},
	}
	router, _ := testAPI(t, stub)
	if response := artworkRequest(router, `"0123456789abcdef"`); response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a stale ETag", response.Code)
	}
}

func TestGetEntityArtworkMapsErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: devices.ErrArtworkNotFound, want: http.StatusNotFound},
		{name: "unavailable", err: devices.ErrArtworkUnavailable, want: http.StatusBadGateway},
		{name: "timeout", err: devices.ErrArtworkTimeout, want: http.StatusGatewayTimeout},
		{name: "internal", err: errors.New("database closed"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubDevices{
				fetchArtwork: func(context.Context, devices.EntityID) (devices.Artwork, error) { return devices.Artwork{}, tc.err },
				artworkETag:  func(context.Context, devices.EntityID) (string, error) { return "", tc.err },
			}
			router, _ := testAPI(t, stub)
			for _, ifNoneMatch := range []string{"", `"0123456789abcdef"`} {
				response := artworkRequest(router, ifNoneMatch)
				if response.Code != tc.want || response.Header().Get("Content-Type") != "application/problem+json" {
					t.Fatalf("If-None-Match %q: status = %d, type %q", ifNoneMatch, response.Code,
						response.Header().Get("Content-Type"))
				}
			}
		})
	}
}

func TestGetEntityArtworkRejectsMalformedEntityIDs(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	if response := performRequest(router, "/v1/entities/not-an-id/artwork"); response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}
