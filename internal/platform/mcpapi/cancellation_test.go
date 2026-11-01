package mcpapi //nolint:testpackage // Tests exercise the diagnostic-owning middleware.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolDiagnosticsDistinguishOwnedCancellation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		owner       error
		cause       error
		wantFailure bool
	}{
		{"caller canceled", context.Canceled, context.Canceled, false},
		{"wrapped caller canceled", context.Canceled, fmt.Errorf("operation: %w", context.Canceled), false},
		{"caller deadline", context.DeadlineExceeded, context.DeadlineExceeded, false},
		{"dependency canceled", nil, context.Canceled, true},
		{"dependency deadline", nil, context.DeadlineExceeded, true},
		{"fault after cancellation", context.Canceled, errors.New("synthetic-private-storage"), true},
		{"joined persistence after cancellation", context.Canceled, errors.Join(errors.New("synthetic-private-storage"), context.Canceled), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if errors.Is(test.owner, context.Canceled) {
				owned, cancel := context.WithCancel(ctx)
				cancel()
				ctx = owned
			} else if errors.Is(test.owner, context.DeadlineExceeded) {
				owned, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				ctx = owned
			}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			result, renderErr := renderToolFailure(InternalToolError(test.cause))
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			next := func(context.Context, string, mcp.Request) (mcp.Result, error) {
				return result, nil
			}
			request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "synthetic_tool"}}
			if _, err := toolErrorMiddleware(logger)(next)(ctx, "tools/call", request); err != nil {
				t.Fatal(err)
			}
			assertCancellationDiagnostic(t, logs.String(), test.wantFailure)
			raw, marshalErr := json.Marshal(result.StructuredContent)
			var fields map[string]any
			if marshalErr != nil || json.Unmarshal(raw, &fields) != nil || fields["failure_code"] != "internal_error" {
				t.Fatalf("structured content = %#v, want unchanged internal_error contract", result.StructuredContent)
			}
			if _, retained := retainedFailure(result); !errors.Is(retained, test.cause) {
				t.Fatal("diagnostic decision discarded the retained private cause")
			}
		})
	}
}

func assertCancellationDiagnostic(t *testing.T, raw string, wantFailure bool) {
	t.Helper()
	if !wantFailure {
		if raw != "" {
			t.Fatalf("expected request cancellation emitted diagnostic: %s", raw)
		}
		return
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("want exactly one JSON diagnostic: %v", err)
	}
	if record["level"] != "ERROR" || record["event"] != "mcp.tool_internal_failure" ||
		record["error_code"] != "internal_error" {
		t.Fatalf("diagnostic = %+v, want one internal Error", record)
	}
	if strings.Contains(raw, "synthetic-private-storage") {
		t.Fatal("diagnostic leaked a private cause")
	}
}
