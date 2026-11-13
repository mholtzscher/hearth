package august //nolint:testpackage // Tests assembly and the private credential boundary.

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(path string) Config {
	return Config{
		AdapterID:       "august-home",
		NATSURL:         "nats://127.0.0.1:4222",
		CredentialsFile: path,
		InstallationID:  "00000000-0000-4000-8000-000000000001",
		Locks:           []LockConfig{{BindingKey: "front", LockID: strings.Repeat("a", 32)}},
	}
}

func TestConfigDefaultsAndGate(t *testing.T) {
	t.Parallel()
	c := testConfig("missing-private-file")
	runtime, err := c.runtimeConfig()
	if err != nil || runtime.PollInterval != 5*time.Minute || runtime.InventoryInterval != 15*time.Minute ||
		runtime.Bindings[0].LockID != strings.Repeat("A", 32) {
		t.Fatalf("invalid defaults: %+v %v", runtime, err)
	}
	c.Locks[0].CommandsEnabled = true
	if err = c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), c, nil, nil); err == nil || FailureCode(err) != failureCommandsBlocked {
		t.Fatalf("command gate not enforced before credentials: %v", err)
	}
	interval := 30 * time.Second
	c.PollInterval = &interval
	if c.Validate() == nil {
		t.Fatal("invalid poll accepted")
	}
}

func TestCredentialsFileBoundary(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secret.json")
	write := func(data string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"email":"private@example.com","password":" secret "}`, 0o600)
	credentials, err := readCredentials(path)
	if err != nil || credentials.Password != " secret " {
		t.Fatal("password was not preserved")
	}
	write(`{"email":"private@example.com","password":"secret"}`, 0o644)
	if _, err = readCredentials(path); err == nil {
		t.Fatal("nonprivate credentials accepted")
	}
	for _, data := range []string{`{"password":"private`, `null`, strings.Repeat("private", 16385)} {
		write(data, 0o600)
		if _, err = readCredentials(path); err == nil || strings.Contains(err.Error(), "private@") {
			t.Fatal("invalid credentials or unsafe error")
		}
	}
	if _, err = readCredentials(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthorizeAssemblyDoesNotConnectCore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path, []byte(`{"email":"synthetic@example.com","password":" spaced "}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var paths []string
	transport := transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api-production.august.com" {
			t.Fatal("unexpected origin")
		}
		paths = append(paths, r.URL.Path)
		body := `{}`
		if r.URL.Path == "/session" {
			request, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(request), `"password":" spaced "`) ||
				!strings.Contains(string(request), testConfig(path).InstallationID) {
				t.Fatal("credentials or installation changed")
			}
			body = `{"vPassword":true,"vInstallId":true,"expiresAt":"2099-01-01T00:00:00Z"}`
		}
		header := make(http.Header)
		header.Set("X-August-Access-Token", "synthetic")
		return &http.Response{
			StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	err := authorize(
		context.Background(),
		testConfig(path),
		func() (string, error) { t.Fatal("unexpected code request"); return "", nil },
		transport,
	)
	if err != nil || len(paths) != 1 || paths[0] != "/session" {
		t.Fatalf("authorization: %v %v", paths, err)
	}
}

func TestAuthorizationFailuresExposeOnlySafeActionableCodes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, code    string
		passwordValid bool
		input         string
		verifyStatus  int
	}{
		{"password", "auth_rejected", false, "123456", 200},
		{"input", "verification_input_invalid", true, "private-code", 200},
		{"verification", "verification_rejected", true, "123456", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "credentials.json")
			if err := os.WriteFile(
				path,
				[]byte(`{"email":"private@example.com","password":"private-password"}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			transport := authorizationFailurePeer(t, test.passwordValid, test.verifyStatus)
			err := authorize(
				t.Context(),
				testConfig(path),
				func() (string, error) { return test.input, nil },
				transport,
			)
			if err == nil || FailureCode(err) != test.code {
				t.Fatalf("safe classification = %s, want %s", FailureCode(err), test.code)
			}
			for _, secret := range []string{path, "private@example.com", "private-password", "private-code"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("authorization failure leaked private input")
				}
			}
		})
	}
}

func authorizationFailurePeer(t *testing.T, passwordValid bool, verifyStatus int) transportFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		body, status := `{}`, http.StatusOK
		switch r.URL.Path {
		case "/session":
			body = `{"vPassword":true,"vInstallId":false,"expiresAt":"2099-01-01T00:00:00Z"}`
			if !passwordValid {
				body = `{"vPassword":false,"vInstallId":true}`
			}
		case "/validate/email":
			body, status = `{"error":"private-code"}`, verifyStatus
		case "/validation/email":
		default:
			t.Fatal("unexpected authorization route")
		}
		header := make(http.Header)
		header.Set("X-August-Access-Token", "synthetic")
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}
