package scripted_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const notificationDestinationID = "ndst_01900000-0000-7000-8000-000000000001"

func notificationRuntime(t *testing.T, behavior scripted.NotificationBehavior) *scripted.NotificationRuntime {
	t.Helper()
	runtime, err := scripted.NewNotificationRuntime("sim", []scripted.NotificationSpec{
		{
			Key:        "alerts",
			Name:       " Alerts ",
			Outcome:    behavior.Outcome,
			ReasonCode: behavior.ReasonCode,
			Hold:       behavior.Hold,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attachErr := runtime.Attach("alerts", notificationDestinationID); attachErr != nil {
		t.Fatal(attachErr)
	}
	return runtime
}

func notificationPage(t *testing.T, runtime *scripted.NotificationRuntime) scripted.NotificationCapturePage {
	t.Helper()
	page, err := runtime.Requests(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func requireNotificationMetadata(t *testing.T, got, want scripted.NotificationCaptureMetadata) {
	t.Helper()
	if got != want {
		t.Fatalf("capture metadata = %+v, want %+v", got, want)
	}
}

// The config contract defines descriptor bounds and default accepted evidence.
// Constructor checks matter independently because configs are freely constructed.
func TestNotificationRuntimeValidatesConfiguration(t *testing.T) {
	t.Parallel()
	for name, spec := range map[string]scripted.NotificationSpec{
		"default":      {Key: "alerts", Name: "Alerts"},
		"name maximum": {Key: strings.Repeat("a", 63), Name: strings.Repeat("界", 200)},
		"not sent":     {Key: "alerts", Name: "Alerts", Outcome: "not_sent", ReasonCode: "adapter_busy"},
		"unknown":      {Key: "alerts", Name: "Alerts", Outcome: "unknown", ReasonCode: "provider_error"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := scripted.NewNotificationRuntime("sim", []scripted.NotificationSpec{spec}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, spec := range map[string]scripted.NotificationSpec{
		"key too long":            {Key: strings.Repeat("a", 64), Name: "Alerts"},
		"name too long":           {Key: "alerts", Name: strings.Repeat("界", 201)},
		"invalid UTF8":            {Key: "alerts", Name: "\xff"},
		"accepted reason":         {Key: "alerts", Name: "Alerts", ReasonCode: "provider_error"},
		"missing reason":          {Key: "alerts", Name: "Alerts", Outcome: "rejected"},
		"invalid saturation pair": {Key: "alerts", Name: "Alerts", Outcome: "not_sent", ReasonCode: "provider_error"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := scripted.NewNotificationRuntime("sim", []scripted.NotificationSpec{spec}); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	runtime := notificationRuntime(t, scripted.NotificationBehavior{})
	if runtime.Registrations()[0].Name != "Alerts" || runtime.Destinations()[0].Behavior.Outcome != "accepted" {
		t.Fatal("registration name or default behavior not normalized")
	}
	before := runtime.Destinations()
	if err := runtime.SetBehavior(notificationDestinationID, scripted.NotificationBehavior{}); err == nil {
		t.Fatal("empty update accepted")
	}
	if !reflect.DeepEqual(before, runtime.Destinations()) {
		t.Fatal("failed update changed behavior")
	}
	if err := runtime.SetBehavior(
		"missing",
		scripted.NotificationBehavior{Outcome: "accepted"},
	); !errors.Is(
		err,
		scripted.ErrUnknownNotificationDestination,
	) {
		t.Fatal(err)
	}
	if err := runtime.Release("missing"); !errors.Is(err, scripted.ErrUnknownNotificationAttempt) {
		t.Fatal(err)
	}
}

// Snapshots must preserve exact SDK bytes/IDs and isolate both behavior updates
// and callers mutating returned snapshots from an in-flight held handler.
//
//nolint:gocognit // One held-send scenario checks snapshot isolation across update and repeated release.
func TestNotificationHoldSnapshotsAndRelease(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime := notificationRuntime(t, scripted.NotificationBehavior{Hold: true})
		request := adapter.NotificationRequest{
			AttemptID: "ntf_exact",
			LocalKey:  "alerts",
			Message: adapter.NotificationMessage{
				Title: "  Title\n",
				Text:  "exact\n消息  ",
			},
			Deadline: time.Now().Add(time.Hour),
		}
		result := make(chan adapter.NotificationOutcome, 1)
		go func() { result <- runtime.Handler()(context.Background(), request) }()
		synctest.Wait()
		page := notificationPage(t, runtime)
		if len(page.Items) != 1 {
			t.Fatalf("captures = %v", page)
		}
		capture := page.Items[0]
		if capture.AttemptID != request.AttemptID || capture.Message != request.Message ||
			capture.Deadline != request.Deadline || capture.DestinationID != notificationDestinationID ||
			capture.FinishedAt != nil || !capture.Behavior.Hold || capture.Sequence != 1 {
			t.Fatalf("inexact capture = %+v", capture)
		}
		changed := scripted.NotificationBehavior{Outcome: "rejected", ReasonCode: "provider_rejected"}
		if err := runtime.SetBehavior(notificationDestinationID, changed); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Release(request.AttemptID); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Release(request.AttemptID); err != nil {
			t.Fatal(err)
		}
		if got := <-result; got.Outcome != "accepted" || got.ReasonCode != "" {
			t.Fatalf("held outcome = %+v", got)
		}
		if err := runtime.Release(request.AttemptID); err != nil {
			t.Fatal(err)
		}
		finished := notificationPage(t, runtime).Items[0].FinishedAt
		if finished == nil {
			t.Fatal("finished handler still pending")
		}
		*finished = time.Time{}
		if notificationPage(t, runtime).Items[0].FinishedAt.IsZero() {
			t.Fatal("snapshot mutated retained capture")
		}
		request.AttemptID = "ntf_next"
		if got := runtime.Handler()(
			context.Background(),
			request,
		); got.Outcome != "rejected" ||
			got.ReasonCode != "provider_rejected" {
			t.Fatalf("new outcome = %+v", got)
		}
		page, err := runtime.Requests(1, 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].AttemptID != "ntf_next" || page.HasMore {
			t.Fatalf("exclusive page = %+v, %v", page, err)
		}
	})
}

//nolint:gocognit // Both cancellation sources must join held sends and retain finished evidence.
func TestNotificationHoldCancellation(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		t.Run(strconv.FormatBool(deadline), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				runtime := notificationRuntime(t, scripted.NotificationBehavior{Hold: true})
				ctx, cancel := context.WithCancel(context.Background())
				if deadline {
					ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				}
				defer cancel()
				result := make(chan adapter.NotificationOutcome, 1)
				go func() {
					result <- runtime.Handler()(ctx, adapter.NotificationRequest{AttemptID: "held", LocalKey: "alerts"})
				}()
				synctest.Wait()
				if deadline {
					time.Sleep(time.Second)
				} else {
					cancel()
				}
				got := <-result
				want := "destination_unavailable"
				if deadline {
					want = "deadline_expired"
				}
				if got.Outcome != "not_sent" || got.ReasonCode != want {
					t.Fatalf("cancel outcome = %+v", got)
				}
				if notificationPage(t, runtime).Items[0].FinishedAt == nil {
					t.Fatal("canceled handler still pending")
				}
				if err := runtime.Release("held"); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

// Concurrent admission protects the hard per-Adapter bound. Keeping request 1
// pending while evicting request 2 proves metadata exposes interior holes.
//
//nolint:gocognit // Saturation, selective release, and interior eviction form one retention scenario.
func TestNotificationCaptureSaturationAndInteriorEviction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime := notificationRuntime(t, scripted.NotificationBehavior{Hold: true})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var workers sync.WaitGroup
		workers.Go(
			func() { runtime.Handler()(ctx, adapter.NotificationRequest{AttemptID: "oldest", LocalKey: "alerts"}) },
		)
		synctest.Wait()
		results := make(chan adapter.NotificationOutcome, 1004)
		for index := range 1004 {
			workers.Go(func() {
				results <- runtime.Handler()(ctx, adapter.NotificationRequest{AttemptID: strconv.Itoa(index), LocalKey: "alerts"})
			})
		}
		synctest.Wait()
		page := notificationPage(t, runtime)
		if len(page.Items) != 1000 {
			t.Fatalf("saturated entries = %d", len(page.Items))
		}
		requireNotificationMetadata(t, page.Metadata, scripted.NotificationCaptureMetadata{
			AdapterID: "sim", OldestSequence: 1, LatestSequence: 1005,
			HighestDroppedSequence: 1005, DroppedCount: 5, Truncated: true,
		})
		for range 5 {
			if got := <-results; got.Outcome != "not_sent" || got.ReasonCode != "adapter_busy" {
				t.Fatalf("overflow = %+v", got)
			}
		}
		finishedID := page.Items[1].AttemptID
		// Finish sequence 3 before sequence 2: eviction must follow receive
		// order, not completion order, while retaining pending sequence 1.
		if err := runtime.Release(page.Items[2].AttemptID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := <-results; got.Outcome != "accepted" {
			t.Fatal(got)
		}
		if err := runtime.Release(finishedID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := <-results; got.Outcome != "accepted" {
			t.Fatal(got)
		}
		if err := runtime.SetBehavior(
			notificationDestinationID,
			scripted.NotificationBehavior{Outcome: "accepted"},
		); err != nil {
			t.Fatal(err)
		}
		if got := runtime.Handler()(
			ctx,
			adapter.NotificationRequest{AttemptID: "new", LocalKey: "alerts"},
		); got.Outcome != "accepted" {
			t.Fatal(got)
		}
		page = notificationPage(t, runtime)
		if len(page.Items) != 1000 || page.Items[0].AttemptID != "oldest" ||
			page.Items[1].Sequence != 3 || page.Items[999].Sequence != 1006 {
			t.Fatalf("interior eviction = %+v", page.Metadata)
		}
		requireNotificationMetadata(t, page.Metadata, scripted.NotificationCaptureMetadata{
			AdapterID: "sim", OldestSequence: 1, LatestSequence: 1006, HighestEvictedSequence: 2,
			HighestDroppedSequence: 1005, DroppedCount: 6, Truncated: true,
		})
		if err := runtime.Release(finishedID); !errors.Is(err, scripted.ErrUnknownNotificationAttempt) {
			t.Fatalf("evicted release = %v", err)
		}
		if got := runtime.Handler()(
			ctx,
			adapter.NotificationRequest{AttemptID: "newer", LocalKey: "alerts"},
		); got.Outcome != "accepted" {
			t.Fatal(got)
		}
		page = notificationPage(t, runtime)
		if page.Items[0].Sequence != 1 || page.Items[1].Sequence != 4 {
			t.Fatalf("finished eviction did not follow receive order: %+v", page.Metadata)
		}
		requireNotificationMetadata(t, page.Metadata, scripted.NotificationCaptureMetadata{
			AdapterID: "sim", OldestSequence: 1, LatestSequence: 1007, HighestEvictedSequence: 3,
			HighestDroppedSequence: 1005, DroppedCount: 7, Truncated: true,
		})
		firstPage, pageErr := runtime.Requests(0, 1)
		if pageErr != nil || len(firstPage.Items) != 1 || !firstPage.HasMore ||
			firstPage.Metadata.HighestEvictedSequence != 3 {
			t.Fatalf("bounded page hid loss metadata: %+v, %v", firstPage, pageErr)
		}
		other := notificationRuntime(t, scripted.NotificationBehavior{})
		other.Handler()(ctx, adapter.NotificationRequest{AttemptID: "independent", LocalKey: "alerts"})
		if otherPage := notificationPage(
			t,
			other,
		); len(otherPage.Items) != 1 || otherPage.Metadata.LatestSequence != 1 ||
			otherPage.Metadata.Truncated {
			t.Fatalf("other Adapter = %+v", otherPage)
		}
		cancel()
		workers.Wait()
		for _, capture := range notificationPage(t, runtime).Items {
			if capture.FinishedAt == nil {
				t.Fatal("pending handler survived cancellation")
			}
		}
	})
}
