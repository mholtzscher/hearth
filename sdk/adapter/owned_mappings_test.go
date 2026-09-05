package adapter //nolint:testpackage // Tests exercise package-private request and Session state behavior.

import (
	"bytes"
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const testDeviceID = "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"

type testOwnedMappingsRequest struct {
	Limit  *int    `json:"limit,omitempty"`
	Cursor *string `json:"cursor,omitempty"`
}

func TestListOwnedMappingsReturnsOneExplicitPageAndEncodesLimit(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	requests := make(chan natswire.Envelope[testOwnedMappingsRequest], 3)
	subjects := make(chan string, 3)
	wantPage := OwnedMappingPage{
		Items: []OwnedMapping{{
			BindingKey: "office-light", DeviceID: testDeviceID,
			EntityKey: "power", EntityID: testEntityID,
		}},
		NextCursor: "next-page",
	}
	if _, err := core.Subscribe(natswire.OwnedMappingsWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[testOwnedMappingsRequest](
			validator, contractsv1.OwnedMappingsRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode owned mappings request: %v", decodeErr)
			return
		}
		requests <- request
		subjects <- message.Subject
		respondOwnedMappings(
			t, validator, message, request.ID, request.CorrelationID,
			acceptedOwnedMappings(wantPage.Items, wantPage.NextCursor),
		)
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session := connectSession(t, server.ClientURL())
	wantSubject, err := natswire.OwnedMappingsSubject(session.adapterID, session.runtimeID)
	if err != nil {
		t.Fatal(err)
	}

	for _, limit := range []int{0, 1, 200} {
		page, listErr := session.ListOwnedMappings(testContext(t), OwnedMappingPageRequest{Limit: limit})
		if listErr != nil {
			t.Fatalf("Limit %d: %v", limit, listErr)
		}
		if !reflect.DeepEqual(page, wantPage) {
			t.Fatalf("Limit %d page = %#v, want %#v", limit, page, wantPage)
		}
		assertOwnedMappingsLimit(t, <-requests, limit)
		if subject := <-subjects; subject != wantSubject {
			t.Fatalf("subject = %q, want %q", subject, wantSubject)
		}
	}
}

func TestListOwnedMappingsRejectsInvalidLimitsLocally(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	var requests atomic.Int32
	if _, err := core.Subscribe(natswire.OwnedMappingsWildcard(), func(*natsgo.Msg) {
		requests.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session := connectSession(t, server.ClientURL())

	for _, limit := range []int{-1, 201} {
		_, err := session.ListOwnedMappings(testContext(t), OwnedMappingPageRequest{Limit: limit})
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("Limit %d error = %v, want ValidationError", limit, err)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("Core received %d invalid requests, want 0", got)
	}
}

func TestListOwnedMappingsRetriesByteIdenticalEnvelope(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	payloads := make(chan []byte, 2)
	requests := make(chan natswire.Envelope[testOwnedMappingsRequest], 2)
	var attempts atomic.Int32
	if _, err := core.Subscribe(natswire.OwnedMappingsWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[testOwnedMappingsRequest](
			validator, contractsv1.OwnedMappingsRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode owned mappings request: %v", decodeErr)
			return
		}
		payloads <- append([]byte(nil), message.Data...)
		requests <- request
		if attempts.Add(1) == 1 {
			return
		}
		respondOwnedMappings(
			t, validator, message, request.ID, request.CorrelationID,
			acceptedOwnedMappings([]OwnedMapping{}, ""),
		)
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session := connectSession(t, server.ClientURL())

	page, err := session.ListOwnedMappings(testContext(t), OwnedMappingPageRequest{
		Limit: 73, Cursor: "opaque-position",
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("terminal empty page = %#v, want explicit empty items and no cursor", page)
	}
	firstPayload, secondPayload := <-payloads, <-payloads
	if !bytes.Equal(firstPayload, secondPayload) {
		t.Fatalf("retry payload changed:\nfirst:  %s\nsecond: %s", firstPayload, secondPayload)
	}
	firstRequest, secondRequest := <-requests, <-requests
	if !strings.HasPrefix(firstRequest.ID, "map_") || firstRequest.ID != secondRequest.ID ||
		firstRequest.CorrelationID != secondRequest.CorrelationID {
		t.Fatalf("retry identities changed: first=%#v second=%#v", firstRequest, secondRequest)
	}
	if firstRequest.Data.Limit == nil || *firstRequest.Data.Limit != 73 ||
		firstRequest.Data.Cursor == nil || *firstRequest.Data.Cursor != "opaque-position" {
		t.Fatalf("retry request data = %#v", firstRequest.Data)
	}
}

func TestListOwnedMappingsRetriesAfterReconnect(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	server := startServer(t, -1, storeDir)
	session := connectSession(t, server.ClientURL())
	port := server.Addr().(*net.TCPAddr).Port

	server.Shutdown()
	server.WaitForShutdown()
	waitForConnectionStatus(t, session.connection, natsgo.RECONNECTING)

	type listResult struct {
		page OwnedMappingPage
		err  error
	}
	result := make(chan listResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		page, err := session.ListOwnedMappings(ctx, OwnedMappingPageRequest{
			Limit: 17, Cursor: "resume-position",
		})
		result <- listResult{page: page, err: err}
	}()

	restarted := startServer(t, port, storeDir)
	// connectSession's lifecycle responder reconnects with the original Core
	// connection; a second responder would race to reply during cleanup.
	core := connectNATS(t, restarted.ClientURL())
	validator := compileValidator(t)
	if _, err := core.Subscribe(natswire.OwnedMappingsWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[testOwnedMappingsRequest](
			validator, contractsv1.OwnedMappingsRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode owned mappings request: %v", decodeErr)
			return
		}
		if request.Data.Limit == nil || *request.Data.Limit != 17 ||
			request.Data.Cursor == nil || *request.Data.Cursor != "resume-position" {
			t.Errorf("reconnected request = %#v", request.Data)
			return
		}
		respondOwnedMappings(
			t, validator, message, request.ID, request.CorrelationID,
			acceptedOwnedMappings([]OwnedMapping{}, ""),
		)
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}

	// Release before cleanup removes the restarted lifecycle responders/server.
	t.Cleanup(func() { _ = session.Close() })
	listed := <-result
	if listed.err != nil {
		t.Fatal(listed.err)
	}
	if listed.page.Items == nil || len(listed.page.Items) != 0 || listed.page.NextCursor != "" {
		t.Fatalf("reconnected page = %#v", listed.page)
	}
}

func TestListOwnedMappingsInvalidCursorIsPermanentAndDoesNotFence(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	var attempts atomic.Int32
	if _, err := core.Subscribe(natswire.OwnedMappingsWildcard(), func(message *natsgo.Msg) {
		attempts.Add(1)
		request, decodeErr := natswire.Decode[testOwnedMappingsRequest](
			validator, contractsv1.OwnedMappingsRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode owned mappings request: %v", decodeErr)
			return
		}
		respondOwnedMappings(t, validator, message, request.ID, request.CorrelationID, ownedMappingsResponse{
			Status: statusRejected,
			Error: &ownedMappingsError{
				Code: OwnedMappingsInvalidCursor, Message: "cursor belongs to another adapter",
			},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session := connectSession(t, server.ClientURL())

	_, err := session.ListOwnedMappings(testContext(t), OwnedMappingPageRequest{Cursor: "bad-cursor"})
	var rejected *OwnedMappingsRejectedError
	if !errors.As(err, &rejected) || rejected.Code != OwnedMappingsInvalidCursor ||
		rejected.Message != "cursor belongs to another adapter" {
		t.Fatalf("rejection = %#v, error = %v", rejected, err)
	}
	time.Sleep(2 * requestRetryWait)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("invalid cursor attempts = %d, want 1", got)
	}
	if sessionErr := session.sessionError(); sessionErr != nil {
		t.Fatalf("invalid cursor fenced Session: %v", sessionErr)
	}
}

func TestListOwnedMappingsRuntimeFencingTerminatesSession(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	var attempts atomic.Int32
	if _, err := core.Subscribe(natswire.OwnedMappingsWildcard(), func(message *natsgo.Msg) {
		attempts.Add(1)
		request, decodeErr := natswire.Decode[testOwnedMappingsRequest](
			validator, contractsv1.OwnedMappingsRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode owned mappings request: %v", decodeErr)
			return
		}
		respondOwnedMappings(t, validator, message, request.ID, request.CorrelationID, ownedMappingsResponse{
			Status: statusRejected,
			Error: &ownedMappingsError{
				Code: ownedMappingsRuntimeFenced, Message: "runtime replaced",
			},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := core.Flush(); err != nil {
		t.Fatal(err)
	}
	session := connectSession(t, server.ClientURL())

	if _, err := session.ListOwnedMappings(
		testContext(t), OwnedMappingPageRequest{},
	); !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("fencing error = %v", err)
	}
	if _, err := session.ListOwnedMappings(
		context.Background(), OwnedMappingPageRequest{},
	); !errors.Is(err, ErrRuntimeFenced) {
		t.Fatalf("future call error = %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("fenced Session sent %d requests, want 1", got)
	}
}

func TestListOwnedMappingsCancellationEndsRetry(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	session := connectSession(t, server.ClientURL())
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	_, err := session.ListOwnedMappings(ctx, OwnedMappingPageRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListOwnedMappings error = %v, want context deadline", err)
	}
}

func TestListOwnedMappingsValidatesResponseIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		causation   func(*testing.T, string) string
		correlation func(*testing.T, string) string
		want        string
	}{
		{
			name:        "causation",
			causation:   func(t *testing.T, _ string) string { return mustID(t, "map") },
			correlation: func(_ *testing.T, correlationID string) string { return correlationID },
			want:        "response causation ID does not match request",
		},
		{
			name:        "correlation",
			causation:   func(_ *testing.T, requestID string) string { return requestID },
			correlation: func(t *testing.T, _ string) string { return mustID(t, "cor") },
			want:        "response correlation ID does not match request",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := startServer(t, -1, t.TempDir())
			core := connectNATS(t, server.ClientURL())
			validator := compileValidator(t)
			var attempts atomic.Int32
			if _, err := core.Subscribe(natswire.OwnedMappingsWildcard(), func(message *natsgo.Msg) {
				attempts.Add(1)
				request, decodeErr := natswire.Decode[testOwnedMappingsRequest](
					validator, contractsv1.OwnedMappingsRequestSchemaID, message.Data,
				)
				if decodeErr != nil {
					t.Errorf("decode owned mappings request: %v", decodeErr)
					return
				}
				respondOwnedMappingsWithIdentity(
					t, validator, message,
					test.causation(t, request.ID), test.correlation(t, request.CorrelationID),
					acceptedOwnedMappings([]OwnedMapping{}, ""),
				)
			}); err != nil {
				t.Fatal(err)
			}
			if err := core.Flush(); err != nil {
				t.Fatal(err)
			}
			session := connectSession(t, server.ClientURL())

			_, err := session.ListOwnedMappings(testContext(t), OwnedMappingPageRequest{})
			if err == nil || err.Error() != test.want {
				t.Fatalf("identity error = %v, want %q", err, test.want)
			}
			time.Sleep(2 * requestRetryWait)
			if got := attempts.Load(); got != 1 {
				t.Fatalf("identity mismatch attempts = %d, want 1", got)
			}
		})
	}
}

func assertOwnedMappingsLimit(
	t *testing.T,
	request natswire.Envelope[testOwnedMappingsRequest],
	want int,
) {
	t.Helper()
	if !strings.HasPrefix(request.ID, "map_") {
		t.Fatalf("Limit %d request ID = %q, want map_ prefix", want, request.ID)
	}
	if want == 0 {
		if request.Data.Limit != nil {
			t.Fatalf("Limit 0 encoded as %d, want omitted", *request.Data.Limit)
		}
		return
	}
	if request.Data.Limit == nil || *request.Data.Limit != want {
		t.Fatalf("Limit %d encoded as %v", want, request.Data.Limit)
	}
}

func acceptedOwnedMappings(items []OwnedMapping, nextCursor string) ownedMappingsResponse {
	return ownedMappingsResponse{
		Status: statusAccepted, Items: &items, NextCursor: nextCursor,
	}
}

func respondOwnedMappings(
	t *testing.T,
	validator *contractsv1.Validator,
	message *natsgo.Msg,
	requestID string,
	correlationID string,
	response ownedMappingsResponse,
) {
	t.Helper()
	respondOwnedMappingsWithIdentity(t, validator, message, requestID, correlationID, response)
}

func respondOwnedMappingsWithIdentity(
	t *testing.T,
	validator *contractsv1.Validator,
	message *natsgo.Msg,
	causationID string,
	correlationID string,
	response ownedMappingsResponse,
) {
	t.Helper()
	envelope := natswire.Envelope[ownedMappingsResponse]{
		ID: mustID(t, "rep"), Schema: contractsv1.OwnedMappingsResponseSchemaID,
		EmittedAt: nowString(), CorrelationID: correlationID, CausationID: &causationID,
		Data: response,
	}
	payload, err := natswire.Encode(validator, contractsv1.OwnedMappingsResponseSchemaID, envelope)
	if err != nil {
		t.Errorf("encode owned mappings response: %v", err)
		return
	}
	if respondErr := message.Respond(payload); respondErr != nil {
		t.Errorf("respond to owned mappings request: %v", respondErr)
	}
}
