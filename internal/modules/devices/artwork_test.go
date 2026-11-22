package devices //nolint:testpackage // Exercises the artwork fetch policy through the Service transport seam.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
)

const artworkSource = "http://10.0.0.5:8095/imageproxy/0f3c1d2e4b5a69788796a5b4c3d2e1f00f3c1d2e4b5a69788796a5b4c3d2e1f0?fmt=jpeg&size=512"

const (
	jpegMagic = "\xff\xd8\xff\xe0\x00\x10JFIF\x00"
	pngMagic  = "\x89PNG\r\n\x1a\n"
	webpMagic = "RIFF\x24\x00\x00\x00WEBPVP8 "
)

// imageBody returns a small image body that starts with the given magic bytes.
func imageBody(magic string) []byte {
	return append([]byte(magic), bytes.Repeat([]byte{0x11}, 64)...)
}

type artworkTransport struct {
	requests []*http.Request
	respond  func(*http.Request) (*http.Response, error)
}

func (transport *artworkTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests = append(transport.requests, request)
	return transport.respond(request)
}

func upstream(status int, header http.Header, body []byte) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		if header == nil {
			header = http.Header{}
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body))}, nil
	}
}

func artworkService(
	t *testing.T, view EntityWithState, entityErr error, respond func(*http.Request) (*http.Response, error),
) (*Service, *artworkTransport) {
	t.Helper()
	repository := newReadRepository()
	repository.entity, repository.entityErr = view, entityErr
	transport := &artworkTransport{respond: respond}
	return newTestService(repository, nil, nil, Dependencies{ArtworkTransport: transport}), transport
}

func mediaPlayerView(state string) EntityWithState {
	view := EntityWithState{Entity: Entity{ID: testEntityID, TypeID: EntityTypeMediaplayerV1}}
	if state != "" {
		view.State = &State{EntityID: testEntityID, Value: Value(state)}
	}
	return view
}

const (
	testEntityID   = EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	stateWithImage = `{"playback":"playing","grouping":"standalone","artwork_url":"` + artworkSource + `"}`
)

func TestFetchArtworkServesSniffedImages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		body       []byte
	}{
		{name: "jpeg", want: "image/jpeg", body: imageBody(jpegMagic)},
		{name: "png", want: "image/png", body: imageBody(pngMagic)},
		{name: "webp", want: "image/webp", body: imageBody(webpMagic)},
		{name: "exactly the size limit", want: "image/jpeg",
			body: append(append([]byte{}, imageBody(jpegMagic)...), bytes.Repeat([]byte{0}, MaxArtworkBytes-len(imageBody(jpegMagic)))...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The upstream label is wrong on purpose: Core trusts the bytes.
			header := http.Header{"Content-Type": []string{"application/octet-stream"}}
			service, transport := artworkService(t, mediaPlayerView(stateWithImage), nil,
				upstream(http.StatusOK, header, tc.body))
			artwork, err := service.FetchArtwork(t.Context(), testEntityID)
			if err != nil {
				t.Fatal(err)
			}
			if artwork.ContentType != tc.want || !bytes.Equal(artwork.Data, tc.body) || len(artwork.ETag) != 16 {
				t.Fatalf("artwork = %s, %d bytes, etag %q", artwork.ContentType, len(artwork.Data), artwork.ETag)
			}
			request := transport.requests[0]
			if request.URL.String() != artworkSource || request.Method != http.MethodGet ||
				request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatalf("upstream request = %s %s, headers %v", request.Method, request.URL, request.Header)
			}
		})
	}
}

func TestFetchArtworkRejectsUnsafeUpstreamReplies(t *testing.T) {
	t.Parallel()
	redirect := http.Header{"Location": []string{"http://169.254.169.254/latest/meta-data"}}
	for _, tc := range []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
	}{
		{name: "svg labeled as jpeg", respond: upstream(http.StatusOK,
			http.Header{"Content-Type": []string{"image/jpeg"}},
			[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))},
		{name: "html", respond: upstream(http.StatusOK, nil, []byte("<!doctype html><title>login</title>"))},
		{name: "not found", respond: upstream(http.StatusNotFound, nil, imageBody(jpegMagic))},
		{name: "redirect", respond: upstream(http.StatusFound, redirect, nil)},
		{name: "oversize", respond: upstream(http.StatusOK, nil,
			append(append([]byte{}, imageBody(jpegMagic)...), bytes.Repeat([]byte{0}, MaxArtworkBytes)...))},
		{name: "transport failure", respond: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, transport := artworkService(t, mediaPlayerView(stateWithImage), nil, tc.respond)
			if _, err := service.FetchArtwork(t.Context(), testEntityID); !errors.Is(err, ErrArtworkUnavailable) {
				t.Fatalf("err = %v, want ErrArtworkUnavailable", err)
			}
			if len(transport.requests) != 1 {
				t.Fatalf("made %d upstream requests, want 1 (no redirect followed)", len(transport.requests))
			}
		})
	}
}

func TestFetchArtworkTimesOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		service, _ := artworkService(t, mediaPlayerView(stateWithImage), nil,
			func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			})
		if _, err := service.FetchArtwork(t.Context(), testEntityID); !errors.Is(err, ErrArtworkTimeout) {
			t.Fatalf("err = %v, want ErrArtworkTimeout", err)
		}
	})
}

// stalledBody sends headers promptly and then never finishes the body.
type stalledBody struct{ done <-chan struct{} }

func (body stalledBody) Read([]byte) (int, error) {
	<-body.done
	return 0, context.DeadlineExceeded
}

func (stalledBody) Close() error { return nil }

func TestFetchArtworkTimesOutWhileReadingTheBody(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		service, _ := artworkService(t, mediaPlayerView(stateWithImage), nil,
			func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{},
					Body: stalledBody{done: request.Context().Done()},
				}, nil
			})
		if _, err := service.FetchArtwork(t.Context(), testEntityID); !errors.Is(err, ErrArtworkTimeout) {
			t.Fatalf("err = %v, want ErrArtworkTimeout", err)
		}
	})
}

func TestFetchArtworkKeepsCallerCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	service, _ := artworkService(t, mediaPlayerView(stateWithImage), nil,
		func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, context.Canceled
		})
	if _, err := service.FetchArtwork(ctx, testEntityID); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFetchArtworkNotFoundWithoutAnUpstreamRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		view      EntityWithState
		entityErr error
	}{
		{name: "unknown Entity", entityErr: ErrEntityNotFound},
		{name: "not a media player", view: EntityWithState{
			Entity: Entity{ID: testEntityID, TypeID: EntityTypeID("hearth.power/v1")},
			State:  &State{Value: Value(`{"artwork_url":"` + artworkSource + `"}`)},
		}},
		{name: "never observed", view: mediaPlayerView("")},
		{name: "no artwork in State", view: mediaPlayerView(`{"playback":"idle","grouping":"standalone"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, transport := artworkService(
				t,
				tc.view,
				tc.entityErr,
				upstream(http.StatusOK, nil, imageBody(jpegMagic)),
			)
			if _, err := service.FetchArtwork(t.Context(), testEntityID); !errors.Is(err, ErrArtworkNotFound) {
				t.Fatalf("err = %v, want ErrArtworkNotFound", err)
			}
			if _, err := service.ArtworkETag(t.Context(), testEntityID); !errors.Is(err, ErrArtworkNotFound) {
				t.Fatalf("ETag err = %v, want ErrArtworkNotFound", err)
			}
			if len(transport.requests) != 0 {
				t.Fatalf("made %d upstream requests, want none", len(transport.requests))
			}
		})
	}
}

func TestArtworkETagFollowsTheURLWithoutFetching(t *testing.T) {
	t.Parallel()
	service, transport := artworkService(
		t,
		mediaPlayerView(stateWithImage),
		nil,
		upstream(http.StatusOK, nil, imageBody(jpegMagic)),
	)
	etag, err := service.ArtworkETag(t.Context(), testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	// The first 16 hex digits of SHA-256(artworkSource).
	if etag != "5bbbb0df4c9c87bc" {
		t.Fatalf("ETag = %q, want 5bbbb0df4c9c87bc", etag)
	}
	if len(transport.requests) != 0 {
		t.Fatal("ArtworkETag contacted upstream")
	}
	artwork, err := service.FetchArtwork(t.Context(), testEntityID)
	if err != nil || artwork.ETag != etag {
		t.Fatalf("FetchArtwork ETag %q, err %v; want %q", artwork.ETag, err, etag)
	}
	other, _ := artworkService(t, mediaPlayerView(strings.Replace(stateWithImage, "size=512", "size=256", 1)), nil, nil)
	if otherETag, _ := other.ArtworkETag(t.Context(), testEntityID); otherETag == etag {
		t.Fatal("different artwork URLs share an ETag")
	}
}
