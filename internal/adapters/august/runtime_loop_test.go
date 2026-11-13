package august //nolint:testpackage // Exercises the runtime's generation ownership and recovery boundary.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestRunAcquiresImmediatelyAndJoinsSubscriptionBeforeReturn(t *testing.T) {
	t.Parallel()
	started, returned := make(chan struct{}), make(chan struct{})
	transport := syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "ps.pndsn.com" {
			close(started)
			<-r.Context().Done()
			close(returned)
			return nil, r.Context().Err()
		}
		if r.URL.Path == inventoryPath {
			return response(200, syntheticInventory("Front")), nil
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			return response(200, `{"status":"locked","doorState":"closed"}`), nil
		}
		return response(
			200,
			strings.TrimSuffix(syntheticDetail("Front", onlineBridge), "}")+`,"pubsubChannel":"alpha"}`,
		), nil
	})
	session := newRecordingSession()
	a := newTestAdapter(t, session, transport)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-returned:
	default:
		t.Fatal("Run returned before subscription transport joined")
	}
	assertBindingAvailability(t, session, a.devices["front"], adapter.AvailabilityAvailable)
}

func TestOverflowEndsGenerationAndResynchronizesBeforeNextSubscription(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var visits []string
	subscriptions := 0
	restarted := make(chan struct{})
	transport := syntheticTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		visits = append(visits, r.URL.Host+r.URL.Path)
		if r.URL.Host == "ps.pndsn.com" {
			subscriptions++
			count := subscriptions
			mu.Unlock()
			if count == 1 {
				message := `{"c":"alpha","d":{"status":"locked"}}`
				return response(
					200,
					`{"t":{"t":"1","r":1},"m":[`+strings.Repeat(message+",", pushIngressCapacity)+message+`]}`,
				), nil
			}
			close(restarted)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		mu.Unlock()
		if r.URL.Path == inventoryPath {
			return response(200, syntheticInventory("Front")), nil
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			return response(200, `{"status":"locked","doorState":"closed"}`), nil
		}
		return response(
			200,
			strings.TrimSuffix(syntheticDetail("Front", onlineBridge), "}")+`,"pubsubChannel":"alpha"}`,
		), nil
	})
	a := newTestAdapter(t, newRecordingSession(), transport)
	a.devices["front"].gate <- struct{}{}
	generation := a.startPush(t.Context(), []pushTarget{{DeviceID: testLockID, Channel: "alpha"}})
	if err := generation.worker.Wait(
		t.Context(),
	); !errors.Is(err, errPushOverflow) {
		t.Fatal("overflow did not terminate its bounded generation")
	}
	<-a.devices["front"].gate
	loop := monitorLoop{
		a:          a,
		generation: generation,
		poll:       time.NewTimer(time.Hour),
		retry:      time.NewTimer(time.Hour),
		backoff:    time.Second,
	}
	defer func() { loop.generation.stop(t.Context()); loop.poll.Stop(); loop.retry.Stop() }()
	if err := loop.subscriptionEnded(t.Context()); err != nil {
		t.Fatal(err)
	}
	loop.retryC = nil
	loop.startAcquisition(t.Context(), true)
	<-loop.acquisition.Closed()
	if err := loop.finishAcquisition(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-restarted
	loop.generation.stop(t.Context())
	mu.Lock()
	defer mu.Unlock()
	if len(visits) != 5 || visits[1] != "api-production.august.com"+inventoryPath ||
		visits[2] != "api-production.august.com/locks/"+string(testLockID) ||
		!strings.HasPrefix(visits[4], "ps.pndsn.com/") {
		t.Fatalf("subscription resumed before inventory/detail reconciliation: %v", visits)
	}
}

func TestMissingBatteryDoesNotSuppressHealthyBoltPush(t *testing.T) {
	t.Parallel()
	details := 0
	session := newRecordingSession()
	a := newTestAdapter(t, session, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == inventoryPath {
			return response(200, syntheticInventory("Front")), nil
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			return response(200, `{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"}`), nil
		}
		details++
		return response(
			200,
			strings.ReplaceAll(syntheticDetail("Front", onlineBridge), `"battery":0.7157`, `"battery":null`),
		), nil
	}))
	runCycle(t, a, true)
	for _, stamp := range []string{"2020-01-02T00:00:00Z", "2020-01-03T00:00:00Z"} {
		r, err := decodeStatus([]byte(`{"status":"unlocked","dateTime":"`+stamp+`"}`), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = a.applyDevicePush(
			t.Context(),
			a.devices["front"],
			pushReport{DeviceID: testLockID, Report: r},
		); err != nil {
			t.Fatal(err)
		}
		if a.devices["front"].fields["lock"].value != `"unlocked"` {
			t.Fatal("missing battery suppressed eligible bolt push")
		}
	}
	if a.devices["front"].fields["battery"].available || details != 2 {
		t.Fatal("battery recovered without evidence or recovery reads were not coalesced")
	}
}

func TestPushRecoveryIsCoalescedAndCannotAssertReachability(t *testing.T) {
	t.Parallel()
	reads := 0
	a := newTestAdapter(t, newRecordingSession(), syntheticTransport(func(_ *http.Request) (*http.Response, error) {
		reads++
		return response(200, syntheticDetail("Front", `{"operative":true}`)), nil
	}))
	a.devices["front"].entry = &inventoryEntry{ID: testLockID, Name: "Front", UserType: roleSuperuser}
	a.needsAssessment.Store(0) // This isolated push starts after the account assessment.
	r, err := decodeStatus([]byte(`{"status":"locked"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err = a.applyDevicePush(
			t.Context(),
			a.devices["front"],
			pushReport{DeviceID: testLockID, Report: r},
		); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 || a.devices["front"].online {
		t.Fatal("push bypassed online detail gate or created a recovery-read storm")
	}
}

type blockingPushSession struct {
	*recordingSession

	blocked  chan struct{}
	release  chan struct{}
	deck     chan struct{}
	canceled chan struct{}
}

func (s *blockingPushSession) PublishObservation(
	ctx context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	if string(observation.Value) == `"unlocked"` {
		s.mu.Lock()
		front := s.bindings["front"].Entities[0].EntityID
		deck := s.bindings["deck"].Entities[0].EntityID
		s.mu.Unlock()
		switch observation.EntityID {
		case front:
			close(s.blocked)
			select {
			case <-s.release:
			case <-ctx.Done():
				if s.canceled != nil {
					close(s.canceled)
					<-s.release
				}
				return "", ctx.Err()
			}
		case deck:
			id, err := s.recordingSession.PublishObservation(ctx, observation)
			if err == nil {
				close(s.deck)
			}
			return id, err
		}
	}
	return s.recordingSession.PublishObservation(ctx, observation)
}

func TestPushPublicationOnOneLockDoesNotBlockAnother(t *testing.T) {
	t.Parallel()
	session := &blockingPushSession{recordingSession: newRecordingSession(), blocked: make(chan struct{}),
		release: make(chan struct{}), deck: make(chan struct{}), canceled: make(chan struct{})}
	transport := syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "ps.pndsn.com" {
			if r.URL.Query().Get("tt") == "0" {
				return response(
					200,
					`{"t":{"t":"1","r":1},"m":[{"c":"alpha","d":{"status":"unlocked","dateTime":"2020-01-02T00:00:00Z"}},{"c":"beta","d":{"status":"unlocked","dateTime":"2020-01-02T00:00:00Z"}}]}`,
				), nil
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if r.URL.Path == inventoryPath {
			return response(200, strings.ReplaceAll(syntheticInventory("Front"), `"user"`, `"superuser"`)), nil
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			return response(200, `{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"}`), nil
		}
		body := syntheticDetail("Front", onlineBridge)
		channel := "alpha"
		if strings.Contains(r.URL.Path, string(testOtherID)) {
			body = strings.ReplaceAll(body, string(testLockID), string(testOtherID))
			channel = "beta"
		}
		return response(200, strings.TrimSuffix(body, "}")+`,"pubsubChannel":"`+channel+`"}`), nil
	})
	config := testConfig()
	config.Bindings = append(config.Bindings, BindingConfig{BindingKey: "deck", LockID: string(testOtherID)})
	a, err := New(session, config, testCredentials(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.client.session = authenticatedClient(t, nil).session
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	var releaseOnce sync.Once
	finished := false
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(session.release) })
		cancel()
		if !finished {
			<-done
		}
	})
	select {
	case <-session.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("first lock's push publication did not start")
	}
	select {
	case <-session.deck:
	case <-time.After(time.Second):
		t.Fatal("second lock's push publication blocked behind first lock")
	}
	cancel()
	<-session.canceled
	select {
	case <-done:
		finished = true
		t.Fatal("Run returned before its push application joined")
	default:
	}
	releaseOnce.Do(func() { close(session.release) })
	err = <-done
	if !errors.Is(err, context.Canceled) {
		finished = true
		t.Fatalf("Run returned %v after cancellation", err)
	}
	finished = true
}

//nolint:gocognit // Synthetic peer includes both barriers and cancellation.
func TestScheduledReadOnOneLockDoesNotBlockAnotherLocksPush(
	t *testing.T,
) {
	t.Parallel()
	session := &blockingPushSession{recordingSession: newRecordingSession(), blocked: make(chan struct{}),
		release: make(chan struct{}), deck: make(chan struct{})}
	statusBlocked := make(chan struct{})
	var frontReads atomic.Int32
	transport := syntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "ps.pndsn.com" {
			if r.URL.Query().Get("tt") == "0" {
				select {
				case <-statusBlocked:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				return response(
					200,
					`{"t":{"t":"1","r":1},"m":[{"c":"beta","d":{"status":"unlocked","dateTime":"2020-01-02T00:00:00Z"}}]}`,
				), nil
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if r.URL.Path == inventoryPath {
			return response(200, strings.ReplaceAll(syntheticInventory("Front"), `"user"`, `"superuser"`)), nil
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			if strings.Contains(r.URL.Path, string(testLockID)) && frontReads.Add(1) == 2 {
				close(statusBlocked)
				select {
				case <-session.release:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}
			return response(200, `{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"}`), nil
		}
		body, channel := syntheticDetail("Front", onlineBridge), "alpha"
		if strings.Contains(r.URL.Path, string(testOtherID)) {
			body = strings.ReplaceAll(body, string(testLockID), string(testOtherID))
			channel = "beta"
		}
		return response(200, strings.TrimSuffix(body, "}")+`,"pubsubChannel":"`+channel+`"}`), nil
	})
	config := testConfig()
	config.Bindings = append(config.Bindings, BindingConfig{BindingKey: "deck", LockID: string(testOtherID)})
	a, err := New(session, config, testCredentials(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.config.PollInterval = 20 * time.Millisecond
	a.client.session = authenticatedClient(t, nil).session
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { close(session.release); cancel(); <-done })
	select {
	case <-statusBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled status request did not start")
	}
	select {
	case <-session.deck:
	case <-time.After(time.Second):
		t.Fatal("second lock's push blocked behind first lock's status request")
	}
}
