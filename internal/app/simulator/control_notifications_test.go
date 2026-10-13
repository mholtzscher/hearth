//nolint:testpackage // Exercises app-owned control routing with concurrent scripted runtimes.
package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func notificationControlRuntime(t *testing.T, id string) (*scripted.NotificationRuntime, string) {
	t.Helper()
	runtime, err := scripted.NewNotificationRuntime(id, []scripted.NotificationSpec{{Key: "home", Name: "Home"}})
	if err != nil {
		t.Fatal(err)
	}
	destinationID, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Attach("home", string(destinationID)); err != nil {
		t.Fatal(err)
	}
	return runtime, string(destinationID)
}

//nolint:gocognit // One HTTP scenario checks strict replacement, exact capture and retained release semantics.
func TestNotificationControlsReplacementAndRelease(t *testing.T) {
	t.Parallel()
	runtime, id := notificationControlRuntime(t, "alerts")
	mux := http.NewServeMux()
	registerNotificationControls(mux, map[string]*scripted.NotificationRuntime{"alerts": runtime})
	for _, body := range []string{
		`{}`, `null`, `{"outcome":"accepted"}`, `{"hold":false}`,
		`{"outcome":null,"hold":false}`, `{"outcome":"accepted","hold":null}`,
		`{"outcome":"accepted","hold":false,"reason_code":null}`,
		`{"outcome":"accepted","hold":false,"extra":1}`,
		`{"outcome":"accepted","hold":false} {}`,
		`{"outcome":"accepted","hold":false,"reason_code":"provider_error"}`,
		`{"outcome":"accepted","hold":"false"}`,
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(
			w,
			httptest.NewRequest(
				http.MethodPut,
				"/v1/sim/notifications/destinations/"+id+"/behavior",
				strings.NewReader(body),
			),
		)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", body, w.Code)
		}
	}
	put := httptest.NewRecorder()
	mux.ServeHTTP(
		put,
		httptest.NewRequest(
			http.MethodPut,
			"/v1/sim/notifications/destinations/"+id+"/behavior",
			strings.NewReader(`{"outcome":"accepted","hold":true}`),
		),
	)
	if put.Code != http.StatusOK {
		t.Fatal(put.Body.String())
	}
	attempt := "exact-attempt"
	done := make(chan adapter.NotificationOutcome, 1)
	ctx, cancel := context.WithTimeout(t.Context(), sessionFailureWait)
	defer cancel()
	go func() {
		done <- runtime.Handler()(ctx, adapter.NotificationRequest{AttemptID: attempt, LocalKey: "home", Message: adapter.NotificationMessage{Title: "Title", Text: "Exact text"}, Deadline: time.Now().Add(time.Minute)})
	}()
	for {
		page, err := runtime.Requests(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("held request never captured")
		case <-time.After(time.Millisecond):
		}
	}
	for range 2 {
		w := httptest.NewRecorder()
		mux.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPost, "/v1/sim/notifications/requests/"+attempt+"/release", nil),
		)
		if w.Code != http.StatusNoContent {
			t.Fatal(w.Code)
		}
	}
	select {
	case outcome := <-done:
		if outcome.Outcome != "accepted" {
			t.Fatal(outcome)
		}
	case <-ctx.Done():
		t.Fatal("release did not unblock handler")
	}
	finishedRelease := httptest.NewRecorder()
	mux.ServeHTTP(
		finishedRelease,
		httptest.NewRequest(http.MethodPost, "/v1/sim/notifications/requests/"+attempt+"/release", nil),
	)
	if finishedRelease.Code != http.StatusNoContent {
		t.Fatal("retained finished release was not a no-op")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(
		w,
		httptest.NewRequest(http.MethodGet, "/v1/sim/notifications/requests?adapter_id=alerts&after_sequence=0", nil),
	)
	var page notificationRequestsResponse
	//nolint:musttag // Capture embeds the SDK's untagged NotificationMessage API.
	if err := json.Unmarshal(
		w.Body.Bytes(),
		&page,
	); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].AttemptID != attempt || page.Items[0].DestinationID != id ||
		page.Items[0].Message.Text != "Exact text" ||
		page.Items[0].FinishedAt == nil {
		t.Fatalf("capture: %+v", page)
	}
	for _, path := range []string{"/v1/sim/notifications/destinations/missing/behavior", "/v1/sim/notifications/requests/missing/release"} {
		method, body := http.MethodPost, ""
		if strings.Contains(path, "behavior") {
			method, body = http.MethodPut, `{"outcome":"accepted","hold":false}`
		}
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestNotificationControlsGlobalBoundAndRetentionMetadata(t *testing.T) {
	t.Parallel()
	a, _ := notificationControlRuntime(t, "alpha")
	b, _ := notificationControlRuntime(t, "beta")
	runtimes := map[string]*scripted.NotificationRuntime{"alpha": a, "beta": b}
	for index := range scripted.NotificationCaptureLimit + 1 {
		a.Handler()(t.Context(), adapter.NotificationRequest{AttemptID: fmt.Sprintf("a-%d", index), LocalKey: "home"})
		if index == 2 {
			b.Handler()(t.Context(), adapter.NotificationRequest{AttemptID: "b-1", LocalKey: "home"})
		}
	}
	for _, query := range []string{"after_sequence=0", "adapter_id=alpha&after_sequence=-1", "limit=0", "limit=1001", "adapter_id=missing", "adapter_id=alpha&after_sequence="} {
		if _, err := notificationRequests(httptest.NewRequest(http.MethodGet, "/?"+query, nil), runtimes); err == nil {
			t.Errorf("accepted %s", query)
		}
	}
	page, err := notificationRequests(httptest.NewRequest(http.MethodGet, "/?limit=3", nil), runtimes)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || !page.HasMore || len(page.Metadata) != 2 || !page.Metadata[0].Truncated ||
		page.Metadata[0].HighestEvictedSequence != 1 ||
		page.Metadata[0].DroppedCount != 1 {
		t.Fatalf("page: %+v", page)
	}
	if page.Items[0].AttemptID != "a-1" || page.Items[1].AttemptID != "a-2" || page.Items[2].AttemptID != "b-1" {
		t.Fatalf("global receive order: %+v", page.Items)
	}
	page, err = notificationRequests(
		httptest.NewRequest(http.MethodGet, "/?adapter_id=beta&after_sequence=1", nil),
		runtimes,
	)
	if err != nil || len(page.Items) != 0 || len(page.Metadata) != 1 || page.Metadata[0].LatestSequence != 1 {
		t.Fatalf("cursor: %+v %v", page, err)
	}
}
