package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationsapi "github.com/mholtzscher/hearth/internal/modules/notifications/httpapi"
)

// inputRecorder records the existing service seam; it never creates durable evidence.
// Errors are scripted, not a second implementation of service validation/storage.
type inputRecorder struct {
	notificationsapi.Notifications

	mutex                    sync.Mutex
	sends                    []notifications.SendInput
	gets                     []notifications.AttemptID
	lists                    []notifications.AttemptFilter
	sendErr, getErr, listErr error
}

func (r *inputRecorder) SendPublic(_ context.Context, in notifications.SendInput) (notifications.Attempt, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.sends = append(r.sends, in)
	return notifications.Attempt{
		ID:            in.AttemptID,
		DestinationID: in.DestinationID,
		Message:       in.Message,
		Outcome:       notifications.Accepted,
	}, r.sendErr
}

func (r *inputRecorder) GetAttempt(_ context.Context, id notifications.AttemptID) (notifications.Attempt, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.gets = append(r.gets, id)
	return notifications.Attempt{}, r.getErr
}

func (r *inputRecorder) ListAttempts(
	_ context.Context,
	filter notifications.AttemptFilter,
) (notifications.AttemptPage, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.lists = append(r.lists, filter)
	return notifications.AttemptPage{}, r.listErr
}

func (r *inputRecorder) assertCalls(t *testing.T, sends, gets, lists int) {
	t.Helper()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.sends) != sends || len(r.gets) != gets || len(r.lists) != lists {
		t.Fatalf(
			"service calls: sends=%d gets=%d lists=%d, want %d/%d/%d",
			len(r.sends),
			len(r.gets),
			len(r.lists),
			sends,
			gets,
			lists,
		)
	}
}

func (r *inputRecorder) assertSend(t *testing.T, destination notifications.DestinationID, message string) {
	t.Helper()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	in := r.sends[0]
	if in.DestinationID != destination || notifications.ValidateID(string(in.AttemptID), "ntf_") != nil ||
		in.Message.Text != message || in.Message.Title != "" {
		t.Fatalf("send conversion: %+v", in)
	}
}

func inputRouter(service notificationsapi.Notifications) http.Handler {
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Notifications", "1"))
	notificationsapi.Register(huma.NewGroup(api, "/v1"), service, nil)
	return router
}

func inputRequest(t *testing.T, handler http.Handler, method, path, body string, status int) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s: %d, want %d: %s", method, path, response.Code, status, response.Body.String())
	}
}

// Decoder rejection is distinguished from domain errors returned by the service.
func TestNotificationStrictInputsAndStatusMapping(t *testing.T) {
	t.Parallel()
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/notification-destinations/" + string(destination) + "/notifications"
	t.Run("HTTP decoder", func(t *testing.T) {
		t.Parallel()
		testInputHTTPDecoder(t, path)
	})
	t.Run(
		"HTTP service errors and query validation",
		func(t *testing.T) {
			t.Parallel()
			testInputHTTPServiceErrors(t, destination, path)
		},
	)
}

func testInputHTTPDecoder(t *testing.T, path string) {
	t.Helper()
	for _, tc := range []struct{ name, body string }{
		{"null title", `{"message":"ok","title":null}`},
		{"supplied attempt identity", `{"message":"ok","attempt_id":"ntf_supplied"}`},
		{"unknown field", `{"message":"ok","extra":true}`},
		{"null message", `{"message":null}`},
		{"missing message", `{}`},
		{"null body", `null`},
		{"numeric title", `{"message":"ok","title":17}`},
		{"invalid UTF8", `{"message":"` + string([]byte{0xff}) + `"}`},
		{"long message", `{"message":"` + strings.Repeat("x", 2001) + `"}`},
		{"long title", `{"message":"ok","title":"` + strings.Repeat("x", 257) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := &inputRecorder{}
			inputRequest(t, inputRouter(recorder), http.MethodPost, path, tc.body, 422)
			recorder.assertCalls(t, 0, 0, 0)
		})
	}
	for _, escaped := range []string{`\ud800`, `\udfff`, `\ud800x`, `\ud800\ud800`, `\udfff\ud800`} {
		for _, field := range []string{"message", "title"} {
			t.Run(escaped+"/"+field, func(t *testing.T) {
				t.Parallel()
				body := `{"message":"` + escaped + `"}`
				if field == "title" {
					body = `{"message":"ok","title":"` + escaped + `"}`
				}
				recorder := &inputRecorder{}
				inputRequest(t, inputRouter(recorder), http.MethodPost, path, body, 422)
				recorder.assertCalls(t, 0, 0, 0)
			})
		}
	}
}

func testInputHTTPServiceErrors(t *testing.T, destination notifications.DestinationID, path string) {
	t.Helper()
	missing, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, requestPath, body string
		serviceErr                      error
		status, sends, gets, lists      int
	}{
		{"blank message", http.MethodPost, path, `{"message":"  "}`, notifications.ErrInvalidInput, 422, 1, 0, 0},
		{"unknown destination", http.MethodPost, path, `{"message":"ok"}`, notifications.ErrDestinationNotFound, 404, 1, 0, 0},
		{"missing attempt", http.MethodGet, "/v1/notifications/" + string(missing), "", notifications.ErrAttemptNotFound, 404, 0, 1, 0},
		{"zero limit", http.MethodGet, "/v1/notifications?limit=0", "", nil, 422, 0, 0, 0},
		{"excessive limit", http.MethodGet, "/v1/notifications?limit=201", "", nil, 422, 0, 0, 0},
		{"invalid cursor", http.MethodGet, "/v1/notifications?cursor=invalid", "", notifications.ErrInvalidInput, 422, 0, 0, 1},
		{"closed admission", http.MethodPost, path, `{"message":"ok"}`, notifications.ErrAdmissionClosed, 503, 1, 0, 0},
		{"storage failure", http.MethodPost, path, `{"message":"ok"}`, notifications.ErrPersistence, 500, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := &inputRecorder{sendErr: tc.serviceErr, getErr: tc.serviceErr, listErr: tc.serviceErr}
			inputRequest(t, inputRouter(recorder), tc.method, tc.requestPath, tc.body, tc.status)
			recorder.assertCalls(t, tc.sends, tc.gets, tc.lists)
			if tc.sends == 1 {
				message := "ok"
				if tc.name == "blank message" {
					message = "  "
				}
				recorder.assertSend(t, destination, message)
			}
			if tc.gets == 1 && recorder.gets[0] != missing {
				t.Fatalf("attempt identity not forwarded: %+v", recorder.gets)
			}
			if tc.lists == 1 && recorder.lists[0].Cursor != "invalid" {
				t.Fatalf("cursor not forwarded: %+v", recorder.lists)
			}
		})
	}
}
