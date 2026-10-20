package nats //nolint:testpackage // Tests exercise package-private transport trace capture.

import (
	"strings"
	"testing"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestDeviceFactTraceFromHeadersCapturesBoundedPrintableASCIIOnly pins the
// capture rule: exactly the two W3C propagation headers are read, a bounded
// printable value is carried unchanged, and any field devices could not persist
// is dropped instead of being stored, truncated or failed.
func TestDeviceFactTraceFromHeadersCapturesBoundedPrintableASCIIOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		traceparent string
		tracestate  string
		want        devices.DeviceFactTraceContext
	}{
		{
			name:        "canonical context is carried unchanged",
			traceparent: testFactTraceparent,
			tracestate:  testFactTracestate,
			want: devices.DeviceFactTraceContext{
				Traceparent: testFactTraceparent,
				Tracestate:  testFactTracestate,
			},
		},
		{
			name:        "absent headers capture nothing",
			traceparent: "",
			tracestate:  "",
			want:        devices.DeviceFactTraceContext{},
		},
		{
			name:        "oversized traceparent is dropped and tracestate kept",
			traceparent: strings.Repeat("a", 129),
			tracestate:  testFactTracestate,
			want:        devices.DeviceFactTraceContext{Tracestate: testFactTracestate},
		},
		{
			name:        "oversized tracestate is dropped and traceparent kept",
			traceparent: testFactTraceparent,
			tracestate:  strings.Repeat("b", 513),
			want:        devices.DeviceFactTraceContext{Traceparent: testFactTraceparent},
		},
		{
			name:        "non-printable traceparent is dropped",
			traceparent: "00-\x00-trace",
			tracestate:  testFactTracestate,
			want:        devices.DeviceFactTraceContext{Tracestate: testFactTracestate},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			headers := natsgo.Header{}
			if test.traceparent != "" {
				headers.Set(traceparentHeaderKey, test.traceparent)
			}
			if test.tracestate != "" {
				headers.Set(tracestateHeaderKey, test.tracestate)
			}
			// An unrelated header must never be captured.
			headers.Set("x-arbitrary-header", "must-not-be-persisted")
			if captured := deviceFactTraceFromHeaders(headers); captured != test.want {
				t.Fatalf("captured trace = %#v, want %#v", captured, test.want)
			}
		})
	}
}
