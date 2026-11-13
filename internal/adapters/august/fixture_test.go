package august //nolint:testpackage // Fixture support for private provider boundaries.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	testLockID    = lockID("11111111111111111111111111111111")
	testOtherID   = lockID("22222222222222222222222222222222")
	testInstallID = "00000000-0000-4000-8000-000000000001"
	testSession   = `{"vPassword":true,"vInstallId":true,"expiresAt":"2099-01-01T00:00:00Z"}`
)

type syntheticTransport func(*http.Request) (*http.Response, error)

func (f syntheticTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func authorizationPeer(t *testing.T, validated bool, paths *[]string) syntheticTransport {
	t.Helper()
	return func(request *http.Request) (*http.Response, error) {
		*paths = append(*paths, request.URL.Path)
		if request.URL.Host != "api-production.august.com" || request.Method != http.MethodPost {
			t.Fatal("authorize left fixed origin/method")
		}
		if request.URL.Path == sessionPath {
			if !validated && len(*paths) == 1 {
				return sessionResponse(`{"vPassword":true,"vInstallId":false,"expiresAt":"2099-01-01T00:00:00Z"}`), nil
			}
			return sessionResponse(testSession), nil
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"/validation/email": `{"value":"synthetic@example.com"}`,
			"/validate/email":   `{"email":"synthetic@example.com","code":"123456"}`,
		}[request.URL.Path]
		if want == "" || !json.Valid(body) || string(body) != want {
			t.Fatal("incorrect provisioning route/body")
		}
		return response(200, `{}`), nil
	}
}

func readRefreshPeer(t *testing.T, expiredAgain bool, paths *[]string) syntheticTransport {
	t.Helper()
	return func(request *http.Request) (*http.Response, error) {
		*paths = append(*paths, request.URL.Path)
		if request.URL.Path == sessionPath {
			return sessionResponse(testSession), nil
		}
		if len(*paths) == 1 || expiredAgain {
			return response(401, ""), nil
		}
		if request.Header.Get("X-August-Access-Token") != "synthetic-token" {
			t.Error("read retry did not use the new Session")
		}
		result := response(200, `{}`)
		result.Header.Set("X-August-Access-Token", "rotated-token")
		return result, nil
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func sessionResponse(body string) *http.Response {
	result := response(http.StatusOK, body)
	result.Header.Set("X-August-Access-Token", "synthetic-token")
	return result
}

func testConfig() Config {
	return Config{
		InstallationID:    testInstallID,
		PollInterval:      time.Minute,
		InventoryInterval: 5 * time.Minute,
		Bindings:          []BindingConfig{{BindingKey: "front", LockID: string(testLockID)}},
	}
}

func testCredentials() Credentials {
	return Credentials{Email: "synthetic@example.com", Password: " password with spaces "}
}

func testClient(t *testing.T, transport http.RoundTripper) *client {
	t.Helper()
	result, err := newClient(testConfig(), testCredentials(), transport)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func authenticatedClient(t *testing.T, transport http.RoundTripper) *client {
	t.Helper()
	result := testClient(t, transport)
	result.session = authSession{token: "original-token", expiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
	return result
}
