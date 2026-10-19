package zigbee2mqtt //nolint:testpackage // Exercises vendor function through its exposed handler and runtime.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// The refresh function must include read-only and disabled Entities without
// granting them Commands or fabricating reports; all gets are single QoS1 requests.
func TestRefreshReadableEntitiesAndRejectInvalidTargets(t *testing.T) {
	t.Parallel()
	fixture := newRefreshFixture(t)
	for _, entityID := range []string{fixture.temperature, fixture.power} {
		result := invokeRefresh(fixture.z2m, entityID, time.Now().Add(time.Second))
		success, ok := result.(adapter.FunctionSucceeded)
		if !ok || string(success.Result) != `{"entity_id":"`+entityID+`","request_status":"published"}` {
			t.Fatalf("refresh result = %#v", result)
		}
	}
	cases := []struct{ name, id, reason string }{
		{"malformed", "ent_00000000-0000-4000-8000-000000000000", "invalid_entity_id"},
		{"foreign", "ent_01900000-0000-7000-8000-000000000000", "entity_unavailable"},
		{"not-gettable", fixture.unsupported, "refresh_unsupported"},
	}
	for _, test := range cases {
		result := invokeRefresh(fixture.z2m, test.id, time.Now().Add(time.Second))
		if result != (adapter.FunctionNotExecuted{ReasonCode: test.reason}) {
			t.Fatalf("%s result = %#v", test.name, result)
		}
	}
	responder := newFakeResponder(fixture.session.recorder, fixture.session)
	if err := fixture.z2m.HandleCommand(
		context.Background(),
		testCommand(fixture.temperature, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	if responder.unavailable != 1 || responder.accepted != 0 {
		t.Fatal("read-only refresh target became commandable")
	}
	fixture.connection.mutex.Lock()
	defer fixture.connection.mutex.Unlock()
	if len(fixture.connection.published) != 2 {
		t.Fatalf("publications = %#v", fixture.connection.published)
	}
	for index, payload := range []string{`{"temperature":""}`, `{"state":""}`} {
		publication := fixture.connection.published[index]
		if publication.topic != "zigbee2mqtt/test-light/get" || publication.payload != payload ||
			publication.qos != 1 ||
			publication.retained {
			t.Fatalf("publication = %#v", publication)
		}
	}
	fixture.session.mutex.Lock()
	defer fixture.session.mutex.Unlock()
	if len(fixture.session.observations) != 0 || len(fixture.session.linked) != 0 {
		t.Fatal("refresh fabricated observations")
	}
}

// A blocked MQTT publication cannot block route invalidation or be retargeted to
// a replacement connection. A late successful PUBACK cannot revise unknown.
func TestRefreshInvalidationDoesNotWaitForPublishOrReplay(t *testing.T) {
	t.Parallel()
	fixture := newRefreshFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	fixture.connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error { close(started); <-release; return nil }
	results := make(chan adapter.FunctionResult, 1)
	go func() { results <- invokeRefresh(fixture.z2m, fixture.temperature, time.Now().Add(time.Minute)) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fixture.z2m.invalidateRoutes(ctx, 1, errors.New("connection lost")); err != nil {
		close(release)
		t.Fatal(err)
	}
	if result := awaitRefresh(t, results); result != (adapter.FunctionUnknown{ReasonCode: "refresh_outcome_unknown"}) {
		t.Errorf("invalidated result = %#v", result)
	}
	if result := invokeRefresh(
		fixture.z2m,
		fixture.power,
		time.Now().Add(time.Second),
	); result != (adapter.FunctionNotExecuted{ReasonCode: "adapter_unavailable"}) {
		t.Errorf("offline result = %#v", result)
	}
	replacement := newFakeConnection(&runtimeRecorder{})
	if _, err := fixture.z2m.activateRoutes(ctx, 2, replacement, func(error) {}, fixture.snapshot); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	// Fresh work is a barrier proving the replacement serves new requests, not
	// a queue of the lost invocation or the offline request.
	if _, ok := invokeRefresh(fixture.z2m, fixture.power, time.Now().Add(time.Second)).(adapter.FunctionSucceeded); !ok {
		t.Fatal("replacement did not serve fresh refresh")
	}
	replacement.mutex.Lock()
	defer replacement.mutex.Unlock()
	if len(replacement.published) != 1 || replacement.published[0].payload != `{"state":""}` {
		t.Fatalf("replacement publications = %#v", replacement.published)
	}
}

// A deadline bounds the reply even when MQTT ignores cancellation.
func TestRefreshDeadlineBoundsReplyForBlockedPublish(t *testing.T) {
	t.Parallel()
	fixture := newRefreshFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	fixture.connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error { close(started); <-release; return nil }
	results := make(chan adapter.FunctionResult, 1)
	go func() {
		results <- invokeRefresh(fixture.z2m, fixture.temperature, time.Now().Add(50*time.Millisecond))
	}()
	<-started
	if result := awaitRefresh(t, results); result != (adapter.FunctionUnknown{ReasonCode: "refresh_outcome_unknown"}) {
		t.Errorf("deadline result = %#v", result)
	}
	close(release)
}

func TestRefreshPublishFailureIsUnknownAndExpiredWorkNeverPublishes(t *testing.T) {
	t.Parallel()
	fixture := newRefreshFixture(t)
	fixture.connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error { return errors.New("PUBACK lost") }
	if result := invokeRefresh(
		fixture.z2m,
		fixture.temperature,
		time.Now().Add(time.Second),
	); result != (adapter.FunctionUnknown{ReasonCode: "refresh_outcome_unknown"}) {
		t.Fatalf("publish failure = %#v", result)
	}
	if result := invokeRefresh(
		fixture.z2m,
		fixture.power,
		time.Now().Add(-time.Second),
	); result != (adapter.FunctionNotExecuted{ReasonCode: "adapter_unavailable"}) {
		t.Fatalf("expired result = %#v", result)
	}
	fixture.connection.mutex.Lock()
	defer fixture.connection.mutex.Unlock()
	if len(fixture.connection.published) != 1 {
		t.Fatalf("expired request published: %#v", fixture.connection.published)
	}
}

// A revision change within one MQTT generation fences the old friendly-name
// snapshot just as a reconnect does; a late PUBACK must not report success.
func TestRefreshRouteReplacementCancelsPublication(t *testing.T) {
	t.Parallel()
	fixture := newRefreshFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	fixture.connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error { close(started); <-release; return nil }
	results := make(chan adapter.FunctionResult, 1)
	go func() { results <- invokeRefresh(fixture.z2m, fixture.temperature, time.Now().Add(time.Minute)) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fixture.z2m.activateRoutes(ctx, 1, fixture.connection, func(error) {}, fixture.snapshot); err != nil {
		close(release)
		t.Fatal(err)
	}
	if result := awaitRefresh(t, results); result != (adapter.FunctionUnknown{ReasonCode: "refresh_outcome_unknown"}) {
		t.Errorf("route replacement result = %#v", result)
	}
	close(release)
}

// The coordinator's shutdown path cannot rely on completion events, which are
// dropped after cancellation. It must resolve handlers itself and join effects.
func TestRuntimeFailureResolvesRefreshBeforeJoiningBlockedPublish(t *testing.T) {
	t.Parallel()
	fixture := newRefreshFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	fixture.connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error { close(started); <-release; return nil }
	results := make(chan adapter.FunctionResult, 1)
	go func() { results <- invokeRefresh(fixture.z2m, fixture.temperature, time.Now().Add(time.Minute)) }()
	<-started
	fixture.z2m.runtimeEvents <- ordinaryPublishFinished{result: make(chan stateDisposition, 1), err: errors.New("Session failed")}
	if result := awaitRefresh(t, results); result != (adapter.FunctionUnknown{ReasonCode: "refresh_outcome_unknown"}) {
		t.Errorf("runtime failure = %#v", result)
	}
	select {
	case <-fixture.z2m.runtimeDone:
		t.Error("runtime finished while publication was still running")
	default:
	}
	close(release)
	select {
	case <-fixture.z2m.runtimeDone:
	case <-time.After(time.Second):
		t.Fatal("runtime did not join effect")
	}
}
