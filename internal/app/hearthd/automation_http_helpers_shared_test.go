//go:build integration || e2e

package hearthd //nolint:testpackage // Public HTTP fixtures are shared by journey and lifecycle tests.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func sliceRequest(
	ctx context.Context,
	t *testing.T,
	method, httpAddress, path string,
	body io.Reader,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, "http://"+httpAddress+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := newNonPoolingHTTPClient(t).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readSliceBody(t *testing.T, response *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func conditionsManualRun(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, body string,
) *http.Response {
	t.Helper()
	return sliceRequest(ctx, t, http.MethodPost, httpAddress,
		"/v1/automations/"+automationID+"/runs", strings.NewReader(body))
}

func conditionsLocationRunID(t *testing.T, response *http.Response) string {
	t.Helper()
	location := response.Header.Get("Location")
	position := strings.LastIndexByte(location, '/')
	if position < 0 || position == len(location)-1 {
		t.Fatalf("manual admission Location = %q", location)
	}
	return location[position+1:]
}
