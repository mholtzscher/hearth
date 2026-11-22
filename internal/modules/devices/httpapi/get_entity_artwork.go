package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// artworkMaxAge lets a browser reuse an image for an hour; the card's `v` key
// changes when the artwork does, and ETag revalidation avoids refetching.
const artworkMaxAge = "private, max-age=3600"

func registerGetEntityArtwork(api huma.API, handler *handler) {
	// OpenAPI 3.1 describes binary media without a schema; Huma would label a
	// binary-format schema application/octet-stream.
	huma.Register(api, huma.Operation{
		OperationID: "get-entity-artwork", Method: http.MethodGet, Path: "/entities/{entity_id}/artwork",
		Summary: "Get the artwork a media player's current State names",
		Description: "Core fetches exactly the artwork_url in the Entity's current State, without credentials " +
			"or redirects, and serves it only when its bytes are a JPEG, PNG, or WebP image of at most 512 KiB.",
		Tags: []string{entitiesTag},
		Responses: map[string]*huma.Response{
			"200": {Description: "Artwork image", Content: map[string]*huma.MediaType{
				"image/jpeg": {}, "image/png": {}, "image/webp": {},
			}},
			"304": {Description: "The cached artwork is still current"},
		},
		Errors: []int{
			http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError,
			http.StatusBadGateway, http.StatusGatewayTimeout,
		},
	}, handler.getEntityArtwork)
}

type GetEntityArtworkInput struct {
	EntityID    string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Version     string `                 doc:"Client cache key; ignored for lookup" query:"v"`
	IfNoneMatch string `                                                                      header:"If-None-Match"`
}

type GetEntityArtworkOutput struct {
	Status                int
	ContentType           string `header:"Content-Type"`
	CacheControl          string `header:"Cache-Control"`
	ETag                  string `header:"ETag"`
	ContentTypeOptions    string `header:"X-Content-Type-Options"`
	ContentSecurityPolicy string `header:"Content-Security-Policy"`
	Body                  []byte
}

func (handler *handler) getEntityArtwork(
	ctx context.Context, input *GetEntityArtworkInput,
) (*GetEntityArtworkOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	if input.IfNoneMatch != "" {
		etag, etagErr := handler.devices.ArtworkETag(ctx, entityID)
		if etagErr != nil {
			return nil, handler.artworkError(ctx, etagErr)
		}
		if etagMatches(input.IfNoneMatch, quoteETag(etag)) {
			return &GetEntityArtworkOutput{
				Status: http.StatusNotModified, CacheControl: artworkMaxAge, ETag: quoteETag(etag),
			}, nil
		}
	}
	artwork, err := handler.devices.FetchArtwork(ctx, entityID)
	if err != nil {
		return nil, handler.artworkError(ctx, err)
	}
	return &GetEntityArtworkOutput{
		Status: http.StatusOK, ContentType: artwork.ContentType, CacheControl: artworkMaxAge,
		ETag: quoteETag(artwork.ETag), ContentTypeOptions: "nosniff",
		ContentSecurityPolicy: "default-src 'none'", Body: artwork.Data,
	}, nil
}

func (handler *handler) artworkError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, devices.ErrArtworkNotFound):
		return huma.NewError(http.StatusNotFound, "artwork not found")
	case errors.Is(err, devices.ErrArtworkUnavailable):
		return huma.NewError(http.StatusBadGateway, "artwork unavailable")
	case errors.Is(err, devices.ErrArtworkTimeout):
		return huma.NewError(http.StatusGatewayTimeout, "artwork fetch timed out")
	default:
		return handler.internalAPIError(ctx, "get-entity-artwork", "service", err)
	}
}

func quoteETag(etag string) string { return `"` + etag + `"` }

// etagMatches applies If-None-Match's weak comparison. Core's tags are hex, so
// splitting the list on commas cannot cut one of them.
func etagMatches(header, etag string) bool {
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == etag {
			return true
		}
	}
	return false
}
