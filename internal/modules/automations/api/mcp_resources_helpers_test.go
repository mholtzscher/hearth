package api_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func readAutomationResource(t *testing.T, session *mcp.ClientSession, uri string) string {
	t.Helper()
	result, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("read resource %s: %v", uri, err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text == "" {
		t.Fatalf("resource %s contents = %#v", uri, result.Contents)
	}
	return result.Contents[0].Text
}

func decodeResourceInto[T any](t *testing.T, text string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("decode resource body %s: %v", text, err)
	}
	return value
}

func resourceErrorCode(t *testing.T, err error) int64 {
	t.Helper()
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error %v is not a JSON-RPC error", err)
	}
	return rpcErr.Code
}
