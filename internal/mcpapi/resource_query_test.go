package mcpapi_test

import (
	"errors"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// TestResourceQueryOnlyRejectsUnknownParameter proves a resource rejects a query
// parameter it does not accept instead of returning a broader page than a client
// asked for.
func TestResourceQueryOnlyRejectsUnknownParameter(t *testing.T) {
	t.Parallel()
	query := mcpapi.NewResourceQuery(url.Values{"status": {"satisfied"}, "verbose": {"true"}})

	err := query.Only("status", "limit", "cursor")
	if err == nil {
		t.Fatal("Only accepted an unsupported parameter")
	}
	if want := `unsupported query parameter "verbose"`; err.Error() != want {
		t.Fatalf("Only error = %q, want %q", err.Error(), want)
	}
	if _, ok := errors.AsType[*mcpapi.ResourceInputError](err); !ok {
		t.Fatalf("Only error = %T, want *mcpapi.ResourceInputError", err)
	}
	if allowedErr := query.Only("cursor", "limit", "status", "verbose"); allowedErr != nil {
		t.Fatalf("Only rejected allowed parameters: %v", allowedErr)
	}
	if emptyErr := mcpapi.NewResourceQuery(url.Values{}).Only("limit"); emptyErr != nil {
		t.Fatalf("Only rejected an empty query: %v", emptyErr)
	}
}

// TestResourceQuerySingleRejectsRepeatedParameter proves one parameter resolves
// to one value: an explicit value is returned, an absent one reports absence,
// and a repeated one is rejected rather than resolved arbitrarily.
func TestResourceQuerySingleRejectsRepeatedParameter(t *testing.T) {
	t.Parallel()
	query := mcpapi.NewResourceQuery(url.Values{"limit": {"1", "2"}, "cursor": {"abc"}})

	value, present, err := query.Single("limit")
	if err == nil {
		t.Fatal("Single accepted a repeated parameter")
	}
	if want := `query parameter "limit" must appear once`; err.Error() != want {
		t.Fatalf("Single error = %q, want %q", err.Error(), want)
	}
	if value != "" || present {
		t.Fatalf("Single on a repeated parameter = (%q, %t), want no value", value, present)
	}
	if _, ok := errors.AsType[*mcpapi.ResourceInputError](err); !ok {
		t.Fatalf("Single error = %T, want *mcpapi.ResourceInputError", err)
	}

	value, present, err = query.Single("cursor")
	if err != nil || !present || value != "abc" {
		t.Fatalf("Single on one value = (%q, %t, %v), want (\"abc\", true, nil)", value, present, err)
	}

	value, present, err = query.Single("filter")
	if err != nil || present || value != "" {
		t.Fatalf("Single on an absent value = (%q, %t, %v), want (\"\", false, nil)", value, present, err)
	}

	if _, valueErr := query.Value("limit"); valueErr == nil {
		t.Fatal("Value accepted a repeated parameter")
	}
	if cursor, cursorErr := query.Value("cursor"); cursorErr != nil || cursor != "abc" {
		t.Fatalf("Value on one value = (%q, %v), want (\"abc\", nil)", cursor, cursorErr)
	}
	if absent, absentErr := query.Value("filter"); absentErr != nil || absent != "" {
		t.Fatalf("Value on an absent value = (%q, %v), want (\"\", nil)", absent, absentErr)
	}
	if got := query.Get("limit"); got != "1" {
		t.Fatalf("Get on a repeated parameter = %q, want the first value \"1\"", got)
	}
}

// TestResourceQueryLimitAppliesPageBounds proves the bounded page size every
// paginated resource shares: the default for an omitted limit, the inclusive
// range for an explicit one, and one rejection message per broken shape.
func TestResourceQueryLimitAppliesPageBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		query   url.Values
		limit   int
		wantErr string
	}{
		{"omitted limit", url.Values{}, 50, ""},
		{"smallest limit", url.Values{"limit": {"1"}}, 1, ""},
		{"largest limit", url.Values{"limit": {"200"}}, 200, ""},
		{"explicit limit", url.Values{"limit": {"37"}}, 37, ""},
		{"non-numeric limit", url.Values{"limit": {"lots"}}, 0, "limit must be an integer"},
		{"limit below the range", url.Values{"limit": {"0"}}, 0, "limit must be between 1 and 200"},
		{"negative limit", url.Values{"limit": {"-4"}}, 0, "limit must be between 1 and 200"},
		{"limit above the range", url.Values{"limit": {"201"}}, 0, "limit must be between 1 and 200"},
		{"repeated limit", url.Values{"limit": {"1", "2"}}, 0, `query parameter "limit" must appear once`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertResourceLimit(t, test.query, test.limit, test.wantErr)
		})
	}
}

func assertResourceLimit(t *testing.T, query url.Values, want int, wantErr string) {
	t.Helper()
	limit, err := mcpapi.NewResourceQuery(query).Limit()
	if wantErr == "" {
		if err != nil {
			t.Fatalf("Limit = (%d, %v), want (%d, nil)", limit, err, want)
		}
		if limit != want {
			t.Fatalf("Limit = %d, want %d", limit, want)
		}
		return
	}
	if err == nil {
		t.Fatalf("Limit = %d, want error %q", limit, wantErr)
	}
	if err.Error() != wantErr {
		t.Fatalf("Limit error = %q, want %q", err.Error(), wantErr)
	}
	if _, ok := errors.AsType[*mcpapi.ResourceInputError](err); !ok {
		t.Fatalf("Limit error = %T, want *mcpapi.ResourceInputError", err)
	}
}

// TestResourceQueryPaginatedParametersParseTogether proves the composed page
// read resolves the limit and the cursor, defaults both when absent, and reports
// a broken limit before a broken cursor.
func TestResourceQueryPaginatedParametersParseTogether(t *testing.T) {
	t.Parallel()
	page, err := mcpapi.NewResourceQuery(url.Values{"cursor": {"abc"}, "limit": {"7"}}).Page()
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if page.Limit != 7 || page.Cursor != "abc" {
		t.Fatalf("Page = %#v, want limit 7 and cursor \"abc\"", page)
	}

	defaulted, err := mcpapi.NewResourceQuery(url.Values{}).Page()
	if err != nil {
		t.Fatalf("Page on an empty query: %v", err)
	}
	if defaulted.Limit != 50 || defaulted.Cursor != "" {
		t.Fatalf("Page = %#v, want limit 50 and an empty cursor", defaulted)
	}

	if cursor, cursorErr := mcpapi.NewResourceQuery(url.Values{"cursor": {"abc"}}).Cursor(); cursorErr != nil ||
		cursor != "abc" {
		t.Fatalf("Cursor = (%q, %v), want (\"abc\", nil)", cursor, cursorErr)
	}
	if cursor, cursorErr := mcpapi.NewResourceQuery(url.Values{}).Cursor(); cursorErr != nil || cursor != "" {
		t.Fatalf("Cursor on an omitted cursor = (%q, %v), want (\"\", nil)", cursor, cursorErr)
	}

	_, repeatedErr := mcpapi.NewResourceQuery(url.Values{"cursor": {"one", "two"}}).Page()
	if repeatedErr == nil {
		t.Fatal("Page accepted a repeated cursor")
	}
	if want := `query parameter "cursor" must appear once`; repeatedErr.Error() != want {
		t.Fatalf("Page error = %q, want %q", repeatedErr.Error(), want)
	}

	_, bothErr := mcpapi.NewResourceQuery(url.Values{"cursor": {"one", "two"}, "limit": {"201"}}).Page()
	if bothErr == nil {
		t.Fatal("Page accepted an out-of-range limit")
	}
	if want := "limit must be between 1 and 200"; bothErr.Error() != want {
		t.Fatalf("Page error = %q, want the limit rejection %q", bothErr.Error(), want)
	}
}

// TestSharedResourceConstantsMatchTheServedContract pins the values one
// hearth:// resource client sees across modules: the scheme, the JSON body MIME
// type, and the page parameters and bounds.
func TestSharedResourceConstantsMatchTheServedContract(t *testing.T) {
	t.Parallel()
	assertResourceStringConstant(t, "ResourceScheme", mcpapi.ResourceScheme, "hearth")
	assertResourceStringConstant(t, "ResourceMIMEType", mcpapi.ResourceMIMEType, "application/json")
	assertResourceStringConstant(t, "ResourceQueryLimit", mcpapi.ResourceQueryLimit, "limit")
	assertResourceStringConstant(t, "ResourceQueryCursor", mcpapi.ResourceQueryCursor, "cursor")
	assertResourceIntConstant(t, "ResourcePageDefaultLimit", mcpapi.ResourcePageDefaultLimit, 50)
	assertResourceIntConstant(t, "ResourcePageMinimumLimit", mcpapi.ResourcePageMinimumLimit, 1)
	assertResourceIntConstant(t, "ResourcePageMaximumLimit", mcpapi.ResourcePageMaximumLimit, 200)
}

func assertResourceStringConstant(t *testing.T, name string, got string, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func assertResourceIntConstant(t *testing.T, name string, got int, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d", name, got, want)
	}
}

// TestResourceInputErrorBecomesInvalidParams proves the shared conversion a
// resource's failure mapper applies: unreadable input is a JSON-RPC
// invalid-params error carrying the rejection message.
func TestResourceInputErrorBecomesInvalidParams(t *testing.T) {
	t.Parallel()
	inputError := mcpapi.NewResourceInputError("limit must be an integer")
	if inputError.Error() != "limit must be an integer" {
		t.Fatalf("ResourceInputError = %q, want the rejection message", inputError.Error())
	}

	converted := mcpapi.InvalidParamsError(inputError.Message)
	rpcError, ok := errors.AsType[*jsonrpc.Error](converted)
	if !ok {
		t.Fatalf("InvalidParamsError = %T, want *jsonrpc.Error", converted)
	}
	if rpcError.Code != -32602 {
		t.Fatalf("invalid params code = %d, want -32602", rpcError.Code)
	}
	if rpcError.Message != "limit must be an integer" {
		t.Fatalf("invalid params message = %q, want the rejection message", rpcError.Message)
	}
}
