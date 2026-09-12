package nats

import (
	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// deviceFactTraceFromHeaders captures the inbound W3C trace context one accepted
// report carried, so the Device Fact it produces continues the originating trace
// instead of starting a new one. It reads exactly the two propagation headers
// and nothing else, so an arbitrary inbound header can never reach SQLite.
//
// Capture is bounded and sanitizing rather than rejecting: devices bounds each
// field by size and printable ASCII, and a field outside that bound is dropped
// instead of persisted or failed. A defect in a caller's header therefore costs
// trace continuity for that report; it never costs the report itself, and it
// never lets unbounded input past the devices contract. The domain's own
// validation is the single rule, so transport and storage can never disagree
// about what a persistable trace is.
func deviceFactTraceFromHeaders(headers natsgo.Header) devices.DeviceFactTraceContext {
	trace := devices.DeviceFactTraceContext{
		Traceparent: headers.Get(traceparentHeaderKey),
		Tracestate:  headers.Get(tracestateHeaderKey),
	}
	if (devices.DeviceFactTraceContext{Traceparent: trace.Traceparent}).Validate() != nil {
		trace.Traceparent = ""
	}
	if (devices.DeviceFactTraceContext{Tracestate: trace.Tracestate}).Validate() != nil {
		trace.Tracestate = ""
	}
	return trace
}
