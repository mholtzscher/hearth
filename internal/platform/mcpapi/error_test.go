package mcpapi //nolint:testpackage // Exercises error precedence at the shared renderer.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestModeledFailurePrecedesJoinedProtocolError(t *testing.T) {
	t.Parallel()
	modeled := &ToolError{Code: "refused", Message: "request refused"}
	protocol := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "PRIVATE-protocol-message"}
	for _, err := range []error{errors.Join(modeled, protocol), errors.Join(protocol, modeled)} {
		result, renderErr := renderToolFailure(err)
		if renderErr != nil || result == nil || !result.IsError || !errors.Is(result.GetError(), modeled) {
			t.Fatalf("modeled failure precedence changed: result=%+v error=%v", result, renderErr)
		}
		const want = `{"failure_code":"refused","message":"request refused"}`
		if len(result.Content) != 1 || string(result.StructuredContent.(json.RawMessage)) != want {
			t.Fatalf("modeled failure changed: %+v", result)
		}
	}
}
