package esphome //nolint:testpackage // Regressions drive the owning worker and Command boundaries.

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestLightFrameRefreshesSiblingCacheBeforeFIFOAdvances(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		if err := peer.Push(&apipb.LightStateResponse{Key: 3, State: true, Brightness: 0.5}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		power := entityIDFor(t, session, "02:00:00:00:00:01/0/light/Lamp#power")
		brightness := entityIDFor(t, session, "02:00:00:00:00:01/0/light/Lamp#brightness")
		_, off := command(t, instance, session, power, "off", `{"value":false}`)
		synctest.Wait()
		<-off
		_, fifty := command(t, instance, session, brightness, "fifty", `{"value":50}`)
		synctest.Wait()
		if err := peer.Push(&apipb.LightStateResponse{Key: 3, State: false, Brightness: 0.5}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		<-fifty
		linked := callsOfKind(session.snapshot(), "linked")
		if len(linked) != 1 || linked[0].linkedTo != "off" {
			t.Fatalf("off frame must not satisfy brightness from old cache: %+v", linked)
		}
	})
}

func TestMissingSwitchInvalidatesCachedEvidence(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		if err := peer.Push(&apipb.SwitchStateResponse{Key: 2, MissingState: true}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		relay := entityIDFor(t, session, "02:00:00:00:00:01/0/switch/Relay#power")
		_, done := command(t, instance, session, relay, "missing", `{"value":false}`)
		synctest.Wait()
		<-done
		if got := len(callsOfKind(session.snapshot(), "linked")); got != 0 {
			t.Fatalf("missing State reused cache: %d", got)
		}
		if err := peer.Push(&apipb.SwitchStateResponse{Key: 2, State: false}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := len(callsOfKind(session.snapshot(), "linked")); got != 1 {
			t.Fatalf("fresh State did not provide evidence: %d", got)
		}
	})
}

func TestCachedEvidencePreservesFrameReceiptTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		relay := entityIDFor(t, session, "02:00:00:00:00:01/0/switch/Relay#power")
		var received string
		for _, entry := range callsOfKind(session.snapshot(), "observation") {
			if entry.observation.EntityID == relay {
				received = entry.observation.AdapterReceivedAt
			}
		}
		time.Sleep(time.Second)
		_, done := command(t, instance, session, relay, "cached", `{"value":false}`)
		synctest.Wait()
		<-done
		linked := callsOfKind(session.snapshot(), "linked")
		if len(linked) != 1 || linked[0].observation.AdapterReceivedAt != received {
			t.Fatalf("cached timestamp changed: %s => %+v", received, linked)
		}
	})
}

func TestInvalidBrightnessPublishesNoBrightness(t *testing.T) {
	t.Parallel()
	for _, brightness := range []float64{math.NaN(), math.Inf(1), -0.1, 1.1} {
		_, ok, err := project(
			entityRoute{Kind: routeLightBrightness},
			"ent_light",
			nativeapi.StateUpdate{Type: nativeapi.EntityLight, Bool: true, Brightness: brightness},
			projectionTime(),
		)
		if ok || !errors.Is(err, errUnprojectable) {
			t.Errorf("invalid %v became a reading: %v, %v", brightness, ok, err)
		}
	}
}

type failingEvidence struct{ err error }

func (e failingEvidence) PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error) {
	return "", e.err
}

func TestLinkedSDKFailureIsTerminalAndLateStatePublishesUnlinked(t *testing.T) {
	t.Parallel()
	for _, expired := range []bool{false, true} {
		session := &fakeSession{}
		w := &worker{adapter: &Adapter{session: session}, logger: slog.New(slog.DiscardHandler)}
		failure := errors.New("non-transient broker failure")
		deadline := time.Now().Add(time.Hour)
		if expired {
			deadline = time.Now().Add(-time.Second)
		}
		queue := &commandQueue{active: &commandAttempt{
			request: &commandRequest{
				deadline: deadline,
			},
			route:    boundRoute{entityRoute: entityRoute{Kind: routeSwitchPower}, EntityID: "ent_1"},
			target:   []byte("true"),
			evidence: failingEvidence{failure},
		}}
		err := w.publishLinked(
			context.Background(),
			queue,
			adapter.Observation{
				EntityID:          "ent_1",
				Value:             []byte("true"),
				AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
		)
		if expired {
			if err != nil || len(callsOfKind(session.snapshot(), "observation")) != 1 {
				t.Fatalf("late State lost: %v", err)
			}
		} else if !errors.Is(err, failure) || !isSessionFailure(err) {
			t.Fatalf("SDK failure suppressed: %v", err)
		}
	}
}

type deadlineConn struct {
	nativeapi.Conn

	deadline time.Time
}

func (connection *deadlineConn) Send(ctx context.Context, _ nativeapi.Command) error {
	connection.deadline, _ = ctx.Deadline()
	return errors.New("write interrupted")
}

func TestCommandWriteUsesCommandDeadline(t *testing.T) {
	t.Parallel()
	deadline := time.Now().Add(100 * time.Millisecond)
	connection := &deadlineConn{}
	session := &fakeSession{}
	responder := &fakeResponder{session: session}
	attempt := &commandAttempt{
		request: &commandRequest{deadline: deadline, responder: responder, result: make(chan error, 1)},
	}
	w := &worker{}
	if err := w.startNext(
		context.Background(),
		&generation{conn: connection},
		&commandQueue{queued: []*commandAttempt{attempt}},
	); err == nil {
		t.Fatal("write failure lost")
	}
	if !connection.deadline.Equal(deadline) {
		t.Fatalf("write deadline = %v, want %v", connection.deadline, deadline)
	}
}

type registerFailureSession struct {
	*fakeSession

	err    error
	budget chan time.Duration
}

func (session registerFailureSession) Register(ctx context.Context, _ adapter.Registration) (adapter.Binding, error) {
	if session.budget != nil {
		deadline, _ := ctx.Deadline()
		session.budget <- time.Until(deadline)
	}
	return adapter.Binding{}, session.err
}

func TestRegistrationValidationIsPerDeviceButDeadlineFailureIsTerminal(t *testing.T) {
	t.Parallel()
	for _, validation := range []bool{true, false} {
		failure := context.DeadlineExceeded
		if validation {
			failure = &adapter.ValidationError{Err: errors.New("external ID too long")}
		}
		session := registerFailureSession{
			fakeSession: &fakeSession{},
			err:         failure,
			budget:      make(chan time.Duration, 1),
		}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		device := harnessDevice(t, "02:00:00:00:00:01", "one")
		connection, err := dialer.Dial(t.Context(), nativeapi.DialConfig{Address: "one", PSK: device.Transport.key})
		if err != nil {
			t.Fatal(err)
		}
		w := &worker{adapter: &Adapter{session: session}, device: device}
		w.adapter.timing = harnessTiming()
		// Simulate device discovery nearing its own deadline. Core must still get a fresh ten-second budget.
		deviceContext, cancel := context.WithTimeout(t.Context(), time.Second)
		result := w.discover(deviceContext, t.Context(), connection)
		cancel()
		_ = connection.Close()
		if budget := <-session.budget; budget < 9*time.Second {
			t.Fatalf("Core inherited the depleted device deadline: %v", budget)
		}
		if validation {
			if connectionReason(result.err) != reasonPlanningFailed || isSessionFailure(result.err) {
				t.Fatalf("validation classification = %v", result.err)
			}
		} else if !isSessionFailure(result.err) || !errors.Is(result.err, context.DeadlineExceeded) {
			t.Fatalf("registration deadline mislabeled: %v", result.err)
		}
	}
}
