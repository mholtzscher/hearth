package mcpapi_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestSendRejectsInvalidArgumentsBeforeService(t *testing.T) {
	t.Parallel()
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"destination_id": destination, "message": "ok", "title": nil},
		{"destination_id": destination, "message": "ok", "attempt_id": "ntf_supplied"},
		{"destination_id": destination, "message": nil},
		{"destination_id": destination},
		{"destination_id": destination, "message": strings.Repeat("x", 2001)},
		{"destination_id": destination, "message": "ok", "title": strings.Repeat("x", 257)},
	} {
		recorder := &inputRecorder{}
		client := inputMCP(t, recorder)
		result, callErr := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "send_notification", Arguments: args})
		if callErr == nil && !result.IsError {
			t.Fatalf("invalid args accepted: %+v", args)
		}
		recorder.assertCalls(t, 0, 0, 0)
	}
}

func TestSendPreservesSafeDomainErrorsAndSingleAttempt(t *testing.T) {
	t.Parallel()
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, message, code string
		err                 error
	}{
		{"blank message", "  ", "invalid_input", notifications.ErrInvalidInput},
		{"closed admission", "ok", "admission_closed", notifications.ErrAdmissionClosed},
		{"unknown destination", "ok", "not_found", notifications.ErrDestinationNotFound},
		{"storage failure", "ok", "internal_error", notifications.ErrPersistence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &inputRecorder{sendErr: errors.Join(errors.New("private storage secret"), tc.err)}
			result, fields := call(
				t,
				inputMCP(t, r),
				"send_notification",
				map[string]any{"destination_id": destination, "message": tc.message},
			)
			if !result.IsError || fields["failure_code"] != tc.code {
				t.Fatalf("failure = %+v", result)
			}
			for _, content := range result.Content {
				if text, ok := content.(*mcp.TextContent); ok && strings.Contains(text.Text, "private") {
					t.Fatalf("leaked cause: %s", text.Text)
				}
			}
			r.assertCalls(t, 1, 0, 0)
			r.assertSend(t, destination, tc.message)
		})
	}
}

func TestSendReturnsActualCompactOutcomeReceipt(t *testing.T) {
	t.Parallel()
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status          notifications.Outcome
		reason, receipt string
	}{
		{notifications.Accepted, "", "provider-receipt"},
		{notifications.Rejected, "provider_rejected", ""},
		{notifications.NotSent, "adapter_busy", ""},
		{notifications.Unknown, "provider_error", ""},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			t.Parallel()
			r := &inputRecorder{
				attempt: notifications.Attempt{Outcome: tc.status, ReasonCode: tc.reason, Receipt: tc.receipt},
			}
			result, fields := call(
				t,
				inputMCP(t, r),
				"send_notification",
				map[string]any{"destination_id": destination, "message": "immutable"},
			)
			if result.IsError || fields["status"] != string(tc.status) ||
				fields["destination_id"] != string(destination) {
				t.Fatalf("receipt = %+v", fields)
			}
			r.assertCalls(t, 1, 0, 0)
			r.assertSend(t, destination, "immutable")
			if fields["notification_id"] != string(r.sends[0].AttemptID) {
				t.Fatalf("identity = %+v", fields)
			}
			want := map[string]any{
				"notification_id": string(r.sends[0].AttemptID),
				"destination_id":  string(destination),
				"status":          string(tc.status),
			}
			if tc.reason != "" {
				want["reason_code"] = tc.reason
			}
			if tc.receipt != "" {
				want["receipt"] = tc.receipt
			}
			if !reflect.DeepEqual(fields, want) {
				t.Fatalf("receipt is not compact: %+v", fields)
			}
		})
	}
}
