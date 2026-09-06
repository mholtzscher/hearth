package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"testing"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// This test protects shared request/reply discard diagnostics for requests
// without a reply subject and fails if the discard is missing, loses the
// extracted operation context, or reports without a fixed error code and kind.
func TestHandleRequestWithoutReplySubjectDiscardsWithOperationContext(t *testing.T) {
	t.Parallel()
	logs, observer, logger := newContextObservingSink(observedSpanContext)
	headers := make(natsgo.Header)
	natswire.InjectTrace(testTraceContext(t), headers)
	subject, err := natswire.AdapterClaimSubject("simulator")
	if err != nil {
		t.Fatal(err)
	}
	handleRequest(
		nil,
		&natsgo.Msg{Subject: subject, Header: headers, Data: []byte(`{}`)},
		nil,
		"Adapter claim", "claim_id",
		"", "",
		logger,
		func(
			context.Context,
			string,
			natswire.Envelope[adapterClaimRequest],
		) (adapterClaimResponse, bool) {
			t.Error("request without reply subject reached the handler")
			return adapterClaimResponse{}, false
		},
	)

	discarded := logEvents(logs.records(t), "transport.request_discarded")
	if len(discarded) != 1 {
		t.Fatalf("transport.request_discarded events = %d, want 1:\n%s", len(discarded), logs.output())
	}
	if discarded[0]["level"] != "WARN" {
		t.Fatalf("discarded level = %#v, want WARN", discarded[0]["level"])
	}
	if discarded[0]["error_code"] != "missing_reply_subject" || discarded[0]["kind"] != "Adapter claim" {
		t.Fatalf("discarded record = %#v", discarded[0])
	}
	if !observer.allObserved() {
		t.Fatal("discard log emission lost the extracted operation context")
	}
}
