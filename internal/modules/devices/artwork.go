package devices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// MaxArtworkBytes bounds one artwork image read from upstream.
	MaxArtworkBytes = 512 << 10
	// ArtworkFetchTimeout bounds one upstream artwork request, headers through body.
	ArtworkFetchTimeout = 3 * time.Second
)

var (
	// ErrArtworkNotFound covers an unknown Entity, a non-media-player Entity,
	// and a media player whose current State names no artwork.
	ErrArtworkNotFound = errors.New("artwork not found")
	// ErrArtworkUnavailable covers an upstream that failed, redirected, sent too
	// many bytes, or sent something other than an allowed image.
	ErrArtworkUnavailable = errors.New("artwork unavailable")
	// ErrArtworkTimeout reports an upstream slower than ArtworkFetchTimeout.
	ErrArtworkTimeout = errors.New("artwork fetch timed out")
)

// allowedArtworkType names the only image types Core serves; SVG can carry script.
func allowedArtworkType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

// Artwork is a validated image to serve from Core's own origin.
type Artwork struct {
	ContentType string
	Data        []byte
	// ETag identifies the artwork_url the image came from.
	ETag string
}

func newArtworkClient(transport http.RoundTripper) *http.Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{
		Transport: transport,
		Timeout:   ArtworkFetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ArtworkETag returns the current artwork's ETag without fetching it, so an
// unchanged image revalidates without an upstream request.
func (service *Service) ArtworkETag(ctx context.Context, entityID EntityID) (string, error) {
	_, etag, err := service.artworkURL(ctx, entityID)
	return etag, err
}

// FetchArtwork returns the image at the artwork_url in the Entity's current
// State. It never accepts a URL from the caller, sends no credentials, follows
// no redirects, and serves only JPEG, PNG, or WebP bytes up to MaxArtworkBytes.
func (service *Service) FetchArtwork(ctx context.Context, entityID EntityID) (Artwork, error) {
	source, etag, err := service.artworkURL(ctx, entityID)
	if err != nil {
		return Artwork{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, http.NoBody)
	if err != nil {
		return Artwork{}, fmt.Errorf("%w: build request: %w", ErrArtworkUnavailable, err)
	}
	request.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	response, err := service.artwork.Do(request)
	if err != nil {
		return Artwork{}, artworkFetchError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Artwork{}, fmt.Errorf("%w: upstream status %d", ErrArtworkUnavailable, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxArtworkBytes+1))
	if err != nil {
		return Artwork{}, artworkFetchError(ctx, err)
	}
	if len(data) > MaxArtworkBytes {
		return Artwork{}, fmt.Errorf("%w: image exceeds %d bytes", ErrArtworkUnavailable, MaxArtworkBytes)
	}
	// Sniff the bytes; an upstream Content-Type header is not trusted.
	contentType := http.DetectContentType(data)
	if !allowedArtworkType(contentType) {
		return Artwork{}, fmt.Errorf("%w: unsupported content type %s", ErrArtworkUnavailable, contentType)
	}
	return Artwork{ContentType: contentType, Data: data, ETag: etag}, nil
}

// artworkFetchError keeps the caller's cancellation, names the client
// timeout, and treats every other transport failure as unavailable.
func artworkFetchError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return ErrArtworkTimeout
	}
	return fmt.Errorf("%w: %w", ErrArtworkUnavailable, err)
}

func (service *Service) artworkURL(ctx context.Context, entityID EntityID) (string, string, error) {
	view, err := service.GetEntity(ctx, entityID)
	if errors.Is(err, ErrEntityNotFound) {
		return "", "", ErrArtworkNotFound
	}
	if err != nil {
		return "", "", err
	}
	if view.Entity.TypeID != EntityTypeMediaplayerV1 || view.State == nil {
		return "", "", ErrArtworkNotFound
	}
	var state struct {
		ArtworkURL string `json:"artwork_url"`
	}
	if err = json.Unmarshal(view.State.Value, &state); err != nil {
		return "", "", fmt.Errorf("decode media player State: %w", err)
	}
	if state.ArtworkURL == "" {
		return "", "", ErrArtworkNotFound
	}
	digest := sha256.Sum256([]byte(state.ArtworkURL))
	return state.ArtworkURL, hex.EncodeToString(digest[:8]), nil
}
