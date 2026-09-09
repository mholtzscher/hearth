package api //nolint:testpackage // Exercises cancellation through a real net/http server and the module router.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A9: cancel a socket-backed request after durable admission but before the
// response reaches the client. The server observes cancellation; work survives.
func TestAutomationHTTPDisconnectAfterAdmissionContinuesExecution(t *testing.T) {
	t.Parallel()
	h := newAutomationHTTPHarness(t)
	automation := createHTTPAutomation(t, h, readAutomationFixture(t, "valid-power"))
	admitted := make(chan *httptest.ResponseRecorder, 1)
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		// Hold the response at the actual HTTP boundary, not the execution seam.
		response := httptest.NewRecorder()
		h.router.ServeHTTP(response, request)
		admitted <- response
		<-request.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		server.URL+"/v1/automations/"+string(automation.ID)+"/runs", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Idempotency-Key", "disconnected")
	requestErrors := make(chan error, 1)
	go func() {
		response, requestErr := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		requestErrors <- requestErr
	}()
	var response *httptest.ResponseRecorder
	select {
	case response = <-admitted:
	case <-ctx.Done():
		t.Fatal("request did not reach admission")
	}
	requireAutomationStatus(t, response, http.StatusAccepted)
	select {
	case <-h.started:
	case <-ctx.Done():
		t.Fatal("admitted Command did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not observe client disconnect")
	}
	if err = <-requestErrors; !errors.Is(err, context.Canceled) {
		t.Fatalf("HTTP request error = %v, want cancellation", err)
	}
	h.unblock()
	waitHTTPAutomationWorkers(t, h)
	detail := automationRequest(h.router, http.MethodGet, response.Header().Get("Location"), "", "")
	requireAutomationStatus(t, detail, http.StatusOK)
	run := decodeAutomationResponse[AutomationRunBody](t, detail)
	if run.Status != "succeeded" || run.Steps[0].Outcome == nil || *run.Steps[0].Outcome != "observed" ||
		h.sends.Load() != 1 {
		t.Fatalf("disconnect stopped or duplicated execution: %#v", run)
	}
}
