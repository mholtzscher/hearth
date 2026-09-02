package homeassistant //nolint:testpackage // Tests exercise package-private protocol and reconnect behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	testExternalEntityID = "light.office"
	testEntityID         = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testCommandID        = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

type recordingSession struct {
	mutex               sync.Mutex
	observations        []adapter.Observation
	healthReports       []adapter.HealthReport
	availabilityReports []adapter.EntityAvailabilityReport
	healthHook          func(adapter.HealthReport)
	published           chan struct{}
}

func newRecordingSession() *recordingSession {
	return &recordingSession{published: make(chan struct{}, 16)}
}

func (session *recordingSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.mutex.Lock()
	session.observations = append(session.observations, observation)
	session.mutex.Unlock()
	session.published <- struct{}{}
	return "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

func (session *recordingSession) SetHealth(_ context.Context, report adapter.HealthReport) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.healthHook != nil {
		session.healthHook(report)
	}
	session.healthReports = append(session.healthReports, report)
	return nil
}

func (session *recordingSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.availabilityReports = append(session.availabilityReports, reports...)
	return nil
}

func (session *recordingSession) values() []adapter.Observation {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.Observation(nil), session.observations...)
}

func (session *recordingSession) health() []adapter.HealthReport {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.HealthReport(nil), session.healthReports...)
}

func (session *recordingSession) availability() []adapter.EntityAvailabilityReport {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.EntityAvailabilityReport(nil), session.availabilityReports...)
}

type recordingResponder struct {
	mutex       sync.Mutex
	accepted    bool
	rejected    bool
	unavailable bool
}

func (responder *recordingResponder) Accept() error {
	responder.mutex.Lock()
	responder.accepted = true
	responder.mutex.Unlock()
	return nil
}

func (responder *recordingResponder) Reject(string) error {
	responder.mutex.Lock()
	responder.rejected = true
	responder.mutex.Unlock()
	return nil
}

func (responder *recordingResponder) RejectUnavailable(string) error {
	responder.mutex.Lock()
	responder.rejected = true
	responder.unavailable = true
	responder.mutex.Unlock()
	return nil
}

func (responder *recordingResponder) result() (bool, bool, bool) {
	responder.mutex.Lock()
	defer responder.mutex.Unlock()
	return responder.accepted, responder.rejected, responder.unavailable
}

//nolint:gocognit // The event-ordering scenario is clearer as one end-to-end test.
func TestSubscribeFirstReconcilesBufferedTransitionAfterSnapshot(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	snapshotTime := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	eventTime := snapshotTime.Add(time.Second)
	server, serverErrors := newScriptedServer(t, func(ctx context.Context, connection *websocket.Conn) error {
		subscribe, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if subscribe.Type != "subscribe_events" || subscribe.EventType != "state_changed" {
			return errors.New("first request was not the state_changed subscription")
		}
		if err := writeResult(ctx, connection, subscribe.ID, nil); err != nil {
			return err
		}
		snapshot, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if snapshot.Type != "get_states" {
			return errors.New("snapshot was not requested after subscription acknowledgement")
		}
		if err := writeStateEvent(ctx, connection, "on", snapshotTime.Add(-time.Second)); err != nil {
			return err
		}
		if err := writeStateEvent(ctx, connection, "on", eventTime); err != nil {
			return err
		}
		if err := writeResult(ctx, connection, snapshot.ID, []upstreamState{{
			EntityID: testExternalEntityID, State: "off", LastUpdated: snapshotTime.Format(time.RFC3339Nano),
		}}); err != nil {
			return err
		}
		_, _, _ = connection.Read(ctx)
		return nil
	})
	defer server.Close()

	migrationAdapter := newTestAdapter(t, session, server.URL)
	healthyWithClient := make(chan bool, 1)
	session.healthHook = func(report adapter.HealthReport) {
		if report.Status == adapter.HealthHealthy {
			healthyWithClient <- migrationAdapter.currentClient() != nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- migrationAdapter.Run(ctx) }()
	waitForPublications(t, session, 2)
	cancel()
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}
	assertNoServerError(t, serverErrors)

	observations := session.values()
	if got := string(observations[0].Value); got != "false" {
		t.Fatalf("snapshot value = %s, want false", got)
	}
	if got := string(observations[1].Value); got != "true" {
		t.Fatalf("reconciled event value = %s, want true", got)
	}
	if observations[1].SourceUpdatedAt == nil ||
		*observations[1].SourceUpdatedAt != eventTime.Format(time.RFC3339Nano) {
		t.Fatalf("reconciled source_updated_at = %v", observations[1].SourceUpdatedAt)
	}
	health := session.health()
	availability := session.availability()
	if len(health) != 1 || health[0].Status != adapter.HealthHealthy {
		t.Fatalf("health reports = %#v", health)
	}
	if installed := <-healthyWithClient; !installed {
		t.Fatal("healthy status was reported before the Home Assistant client was installed")
	}
	if len(availability) != 2 || availability[0].Status != adapter.AvailabilityAvailable ||
		availability[1].Status != adapter.AvailabilityAvailable {
		t.Fatalf("availability reports = %#v", availability)
	}
}

func TestSupersededClientFailureDoesNotOverwriteHealth(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	migrationAdapter := newTestAdapter(t, session, "http://unused.example")
	oldClient := &client{}
	currentClient := &client{}
	migrationAdapter.setClient(currentClient)
	if err := migrationAdapter.reportClientUnhealthy(
		context.Background(), oldClient, errors.New("old connection failed"),
	); err != nil {
		t.Fatal(err)
	}
	if reports := session.health(); len(reports) != 0 {
		t.Fatalf("superseded client health reports = %#v", reports)
	}
}

//nolint:gocognit // The event-ordering scenario is clearer as one end-to-end test.
func TestSetRetainsMatchingRefreshWhenImmediatelySuperseded(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	initialTime := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	refreshTime := initialTime.Add(time.Second)
	server, serverErrors := newScriptedServer(t, func(ctx context.Context, connection *websocket.Conn) error {
		subscribe, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if err := writeResult(ctx, connection, subscribe.ID, nil); err != nil {
			return err
		}
		snapshot, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if err := writeResult(ctx, connection, snapshot.ID, []upstreamState{{
			EntityID: testExternalEntityID, State: "off", LastUpdated: initialTime.Format(time.RFC3339Nano),
		}}); err != nil {
			return err
		}
		service, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if service.Type != "call_service" || service.Domain != "light" || service.Service != "turn_on" ||
			service.Target == nil ||
			service.Target.EntityID != testExternalEntityID {
			return errors.New("set did not call light.turn_on for the configured Entity")
		}
		if err := writeResult(ctx, connection, service.ID, nil); err != nil {
			return err
		}
		firstRefresh, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if firstRefresh.Type != "get_states" {
			return errors.New("accepted command did not request a fresh State")
		}
		if err := writeResult(ctx, connection, firstRefresh.ID, []upstreamState{{
			EntityID: testExternalEntityID, State: "off", LastUpdated: initialTime.Format(time.RFC3339Nano),
		}}); err != nil {
			return err
		}
		if err := writeStateEvent(ctx, connection, "on", refreshTime); err != nil {
			return err
		}
		if err := writeStateEvent(ctx, connection, "off", refreshTime.Add(time.Second)); err != nil {
			return err
		}
		_, _, _ = connection.Read(ctx)
		return nil
	})
	defer server.Close()

	migrationAdapter := newTestAdapter(t, session, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- migrationAdapter.Run(ctx) }()
	waitForPublications(t, session, 1)

	handler, handlerErr := migrationAdapter.CommandHandler()
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	responder := &recordingResponder{}
	if err := handler(ctx, adapter.Command{
		ID:            testCommandID,
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID:      testEntityID,
		OperationName: "set",
		Parameters:    json.RawMessage(`{"value":true}`),
		Deadline:      time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); err != nil {
		t.Fatal(err)
	}
	accepted, rejected, unavailable := responder.result()
	if !accepted || rejected || unavailable {
		t.Fatalf(
			"responder accepted = %t, rejected = %t, unavailable = %t",
			accepted, rejected, unavailable,
		)
	}
	observations := session.values()
	var staleLinked, matchingLinked bool
	for _, observation := range observations {
		if observation.RefreshForCommand == nil || *observation.RefreshForCommand != testCommandID {
			continue
		}
		staleLinked = staleLinked || string(observation.Value) == "false"
		matchingLinked = matchingLinked || string(observation.Value) == "true"
	}
	if !staleLinked || !matchingLinked {
		t.Fatalf("linked refreshes did not preserve stale evidence and reacquire the target: %#v", observations)
	}

	cancel()
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}
	assertNoServerError(t, serverErrors)
}

//nolint:gocognit // The scripted exchange verifies readiness, service attempt, refresh, report, and typed rejection.
func TestCommandConfirmedUnavailableUsesTypedRejection(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	server, serverErrors := newScriptedServer(t, func(ctx context.Context, connection *websocket.Conn) error {
		subscribe, err := readRequest(ctx, connection)
		if err != nil {
			return err
		}
		if err = writeResult(ctx, connection, subscribe.ID, nil); err != nil {
			return err
		}
		snapshot, err := readRequest(ctx, connection)
		if err != nil {
			return err
		}
		if err = writeResult(ctx, connection, snapshot.ID, []upstreamState{{
			EntityID: testExternalEntityID, State: "on", LastUpdated: now.Format(time.RFC3339Nano),
		}}); err != nil {
			return err
		}
		service, err := readRequest(ctx, connection)
		if err != nil {
			return err
		}
		if service.Type != "call_service" {
			return errors.New("command did not call Home Assistant service")
		}
		if err = writeResult(ctx, connection, service.ID, nil); err != nil {
			return err
		}
		refresh, err := readRequest(ctx, connection)
		if err != nil {
			return err
		}
		unavailable := upstreamState{
			EntityID: testExternalEntityID,
			State:    stateUnavailable,
			LastUpdated: now.Add(time.Second).
				Format(time.RFC3339Nano),
		}
		if err = writeResult(ctx, connection, refresh.ID, []upstreamState{unavailable}); err != nil {
			return err
		}
		_, _, _ = connection.Read(ctx)
		return nil
	})
	defer server.Close()

	migrationAdapter := newTestAdapter(t, session, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- migrationAdapter.Run(ctx) }()
	waitForPublications(t, session, 1)
	handler, err := migrationAdapter.CommandHandler()
	if err != nil {
		t.Fatal(err)
	}
	responder := &recordingResponder{}
	if err = handler(ctx, adapter.Command{
		ID: testCommandID, CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID: testEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":false}`),
		Deadline: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); err != nil {
		t.Fatal(err)
	}
	accepted, rejected, unavailable := responder.result()
	if accepted || !rejected || !unavailable {
		t.Fatalf("responder accepted = %t, rejected = %t, unavailable = %t", accepted, rejected, unavailable)
	}
	reports := session.availability()
	if len(reports) != 2 || reports[1].Status != adapter.AvailabilityUnavailable ||
		reports[1].ReasonCode != entityUnavailableReason || len(session.values()) != 1 {
		t.Fatalf("availability = %#v, observations = %#v", reports, session.values())
	}
	cancel()
	if err = <-runResult; err != nil {
		t.Fatal(err)
	}
	assertNoServerError(t, serverErrors)
}

func TestAuthenticationFailureReportsUnhealthy(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		ctx := request.Context()
		if err = wsjson.Write(ctx, connection, map[string]any{"type": "auth_required"}); err != nil {
			return
		}
		var authentication map[string]any
		if err = wsjson.Read(ctx, connection, &authentication); err != nil {
			return
		}
		_ = wsjson.Write(ctx, connection, map[string]any{
			"type": "auth_invalid", "message": "invalid access token",
		})
	}))
	defer server.Close()
	migrationAdapter := newTestAdapter(t, session, server.URL)
	if err := migrationAdapter.Run(context.Background()); err == nil {
		t.Fatal("authentication failure returned nil")
	}
	health := session.health()
	if len(health) != 1 || health[0].Status != adapter.HealthUnhealthy ||
		health[0].ReasonCode != authenticationFailedReason {
		t.Fatalf("health reports = %#v", health)
	}
}

func TestUnavailableStateReportsAvailabilityWithoutReplacingState(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	migrationAdapter := newTestAdapter(t, session, "http://127.0.0.1:1")
	if err := migrationAdapter.processState(context.Background(), upstreamState{
		EntityID: testExternalEntityID, State: stateUnavailable,
	}, time.Now().UTC(), nil); err != nil {
		t.Fatal(err)
	}
	if err := migrationAdapter.processState(context.Background(), upstreamState{
		EntityID: testExternalEntityID, State: "unsupported",
	}, time.Now().UTC(), nil); !errors.Is(err, errUnsupportedState) {
		t.Fatalf("process unsupported State error = %v", err)
	}
	availability := session.availability()
	if len(availability) != 1 || availability[0].Status != adapter.AvailabilityUnavailable ||
		availability[0].ReasonCode != entityUnavailableReason || len(session.values()) != 0 {
		t.Fatalf("availability = %#v, observations = %#v", availability, session.values())
	}
	handler, handlerErr := migrationAdapter.CommandHandler()
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	responder := &recordingResponder{}
	if err := handler(context.Background(), adapter.Command{
		ID: testCommandID, EntityID: testEntityID, OperationName: "set",
		Parameters: json.RawMessage(`{"value":false}`),
		Deadline:   time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano),
	}, responder); err != nil {
		t.Fatal(err)
	}
	accepted, rejected, unavailable := responder.result()
	if accepted || !rejected || unavailable || len(session.values()) != 0 {
		t.Fatalf(
			"accepted = %t, rejected = %t, unavailable = %t, publications = %d",
			accepted, rejected, unavailable, len(session.values()),
		)
	}
}

func TestWaitForStateAfterRetainsImmediatelySupersededMatch(t *testing.T) {
	t.Parallel()
	client := &client{
		latestStates: make(map[string]stateChange),
		stateSignal:  make(chan struct{}),
		done:         make(chan struct{}),
	}
	matching := stateChange{State: upstreamState{State: "on"}, Sequence: 1}
	client.recordState(matching)
	client.recordState(stateChange{State: upstreamState{State: "off"}, Sequence: 2})

	result, err := client.WaitForStateAfter(context.Background(), 0, "on")
	if err != nil {
		t.Fatal(err)
	}
	if result.Sequence != matching.Sequence || result.State.State != matching.State.State {
		t.Fatalf("matching State = %#v, want %#v", result, matching)
	}
}

func TestPublishOmitsMalformedLastUpdated(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	migrationAdapter := newTestAdapter(t, session, "http://127.0.0.1:1")
	if err := migrationAdapter.publish(context.Background(), upstreamState{
		EntityID: testExternalEntityID, State: "on", LastUpdated: "not-a-timestamp",
	}, time.Now().UTC(), nil); err != nil {
		t.Fatal(err)
	}
	observations := session.values()
	if len(observations) != 1 || observations[0].SourceUpdatedAt != nil || string(observations[0].Value) != "true" {
		t.Fatalf("Observation = %#v", observations)
	}
}

func TestDeliveredResponseWinsDisconnect(t *testing.T) {
	t.Parallel()
	connectionError := errors.New("connection closed")
	for range 100 {
		client := &client{done: make(chan struct{}), err: connectionError}
		response := make(chan resultMessage, 1)
		response <- resultMessage{ID: 42, Success: true}
		close(client.done)

		result, err := client.waitForResult(context.Background(), response)
		if err != nil {
			t.Fatal(err)
		}
		if result.ID != 42 {
			t.Fatalf("result ID = %d, want 42", result.ID)
		}
	}
}

func TestClientCorrelatesConcurrentRequestsByID(t *testing.T) {
	t.Parallel()
	server, serverErrors := newScriptedServer(t, func(ctx context.Context, connection *websocket.Conn) error {
		first, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		second, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		requests := map[string]requestMessage{first.Type: first, second.Type: second}
		service := requests["call_service"]
		states := requests["get_states"]
		if service.ID == 0 || states.ID == 0 {
			return errors.New("did not receive both concurrent requests")
		}
		if writeErr := writeResult(
			ctx,
			connection,
			states.ID,
			[]upstreamState{{EntityID: testExternalEntityID, State: "on"}},
		); writeErr != nil {
			return writeErr
		}
		if writeErr := writeResult(ctx, connection, service.ID, nil); writeErr != nil {
			return writeErr
		}
		_, _, _ = connection.Read(ctx)
		return nil
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, dialErr := dialClient(ctx, server.URL, "test-token", testExternalEntityID)
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	defer client.Close()

	errorsChannel := make(chan error, 2)
	go func() {
		states, _, _, err := client.GetStates(ctx)
		if err == nil && (len(states) != 1 || states[0].State != "on") {
			err = errors.New("get_states received the wrong correlated result")
		}
		errorsChannel <- err
	}()
	go func() { errorsChannel <- client.CallLightService(ctx, "turn_on", testExternalEntityID) }()
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	assertNoServerError(t, serverErrors)
}

func TestAdapterReconnectsAndAcquiresANewSnapshot(t *testing.T) {
	t.Parallel()
	session := newRecordingSession()
	var connections atomic.Int64
	server, serverErrors := newScriptedServer(t, func(ctx context.Context, connection *websocket.Conn) error {
		connectionNumber := connections.Add(1)
		subscribe, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if err := writeResult(ctx, connection, subscribe.ID, nil); err != nil {
			return err
		}
		snapshot, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		state := "off"
		if connectionNumber > 1 {
			state = "on"
		}
		if err := writeResult(ctx, connection, snapshot.ID, []upstreamState{{
			EntityID: testExternalEntityID, State: state,
			LastUpdated: time.Date(2026, 8, 19, 12, 0, int(connectionNumber), 0, time.UTC).Format(time.RFC3339Nano),
		}}); err != nil {
			return err
		}
		<-session.published
		if connectionNumber == 1 {
			return connection.CloseNow()
		}
		_, _, _ = connection.Read(ctx)
		return nil
	})
	defer server.Close()

	migrationAdapter := newTestAdapter(t, session, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- migrationAdapter.Run(ctx) }()
	waitForPublications(t, session, 2)
	cancel()
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}
	assertNoServerError(t, serverErrors)
	observations := session.values()
	if string(observations[0].Value) != "false" || string(observations[1].Value) != "true" {
		t.Fatalf("reconnect snapshots = %s, %s", observations[0].Value, observations[1].Value)
	}
	health := session.health()
	if len(health) != 3 || health[0].Status != adapter.HealthHealthy ||
		health[1].Status != adapter.HealthUnhealthy || health[1].ReasonCode != externalSystemUnavailableReason ||
		health[2].Status != adapter.HealthHealthy {
		t.Fatalf("reconnect health reports = %#v", health)
	}
	availability := session.availability()
	if len(availability) != 2 || availability[0].Status != adapter.AvailabilityAvailable ||
		availability[1].Status != adapter.AvailabilityAvailable {
		t.Fatalf("reconnect availability reports = %#v", availability)
	}
}

func newTestAdapter(t *testing.T, session Session, upstreamURL string) *Adapter {
	t.Helper()
	value, err := New(session, Config{
		URL: upstreamURL, Token: "test-token", ExternalEntityID: testExternalEntityID, EntityID: testEntityID,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newScriptedServer(
	t *testing.T,
	script func(context.Context, *websocket.Conn) error,
) (*httptest.Server, <-chan error) {
	t.Helper()
	errorsChannel := make(chan error, 8)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/websocket" {
			http.NotFound(response, request)
			return
		}
		connection, acceptErr := websocket.Accept(response, request, nil)
		if acceptErr != nil {
			errorsChannel <- acceptErr
			return
		}
		defer connection.CloseNow()
		ctx := request.Context()
		if err := wsjson.Write(ctx, connection, map[string]any{"type": "auth_required"}); err != nil {
			errorsChannel <- err
			return
		}
		var authentication struct {
			Type        string `json:"type"`
			AccessToken string `json:"access_token"`
		}
		if err := wsjson.Read(ctx, connection, &authentication); err != nil {
			errorsChannel <- err
			return
		}
		if authentication.Type != "auth" || authentication.AccessToken != "test-token" {
			errorsChannel <- errors.New("invalid authentication request")
			return
		}
		if err := wsjson.Write(ctx, connection, map[string]any{"type": "auth_ok", "ha_version": "2026.8"}); err != nil {
			errorsChannel <- err
			return
		}
		if err := script(ctx, connection); err != nil && websocket.CloseStatus(err) == -1 {
			errorsChannel <- err
		}
	}))
	return server, errorsChannel
}

func readRequest(ctx context.Context, connection *websocket.Conn) (requestMessage, error) {
	var request requestMessage
	if err := wsjson.Read(ctx, connection, &request); err != nil {
		return requestMessage{}, err
	}
	return request, nil
}

func writeResult(ctx context.Context, connection *websocket.Conn, id int64, result any) error {
	return wsjson.Write(ctx, connection, map[string]any{
		"id": id, "type": "result", "success": true, "result": result,
	})
}

func writeStateEvent(ctx context.Context, connection *websocket.Conn, state string, updatedAt time.Time) error {
	return wsjson.Write(ctx, connection, map[string]any{
		"id":   1,
		"type": "event",
		"event": map[string]any{
			"event_type": "state_changed",
			"data": map[string]any{
				"entity_id": testExternalEntityID,
				"new_state": upstreamState{
					EntityID: testExternalEntityID, State: state, LastUpdated: updatedAt.Format(time.RFC3339Nano),
				},
			},
		},
	})
}

func waitForPublications(t *testing.T, session *recordingSession, count int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for len(session.values()) < count {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %d publications; got %d", count, len(session.values()))
		}
	}
}

func assertNoServerError(t *testing.T, serverErrors <-chan error) {
	t.Helper()
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	default:
	}
}
