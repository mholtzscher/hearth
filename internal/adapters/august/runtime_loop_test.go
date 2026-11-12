package august //nolint:testpackage // Exercises the runtime's generation ownership and recovery boundary.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
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
	generation := a.startPush(t.Context(), []pushTarget{{DeviceID: testLockID, Channel: "alpha"}})
	if err := generation.worker.Wait(
		t.Context(),
	); !errors.Is(err, errPushOverflow) ||
		len(generation.ingress) != pushIngressCapacity {
		t.Fatal("overflow did not terminate its bounded generation")
	}
	loop := monitorLoop{
		a:          a,
		generation: generation,
		poll:       time.NewTimer(time.Hour),
		retry:      time.NewTimer(time.Hour),
		backoff:    time.Second,
	}
	defer func() { loop.generation.stop(t.Context()); loop.poll.Stop(); loop.retry.Stop() }()
	loop.subscriptionEnded(t.Context())
	if err := loop.reconnect(t.Context()); err != nil {
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
		if err = a.applyPush(t.Context(), pushReport{DeviceID: testLockID, Report: r}); err != nil {
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
	r, err := decodeStatus([]byte(`{"status":"locked"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err = a.applyPush(t.Context(), pushReport{DeviceID: testLockID, Report: r}); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 || a.devices["front"].online {
		t.Fatal("push bypassed online detail gate or created a recovery-read storm")
	}
}
