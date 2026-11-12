package august //nolint:testpackage // Exercises private Session flags and request policy.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentLoginEnvelopeAndSerializedReuse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := testClient(t, syntheticTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		var body map[string]string
		if json.NewDecoder(request.Body).Decode(&body) != nil || !reflect.DeepEqual(body, map[string]string{
			"installId":  testInstallID,
			"identifier": "email:synthetic@example.com",
			"password":   " password with spaces ",
		}) {
			t.Error("login did not preserve the independent credentials and installation")
		}
		if request.URL.String() != apiOrigin+sessionPath || request.Method != http.MethodPost ||
			request.Header.Get("X-August-Access-Token") != "" || request.Header.Get("X-August-Country") != "US" ||
			request.Header.Get(
				"X-August-Branding",
			) != "august" || request.Header.Get("X-August-Api-Key") != applicationID ||
			request.Header.Get("X-Kease-Api-Key") != applicationID || request.Header.Get("Accept-Version") != "0.0.1" {
			t.Error("incorrect login origin, method or headers")
		}
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Error("authentication attempt is not bounded")
		}
		return sessionResponse(testSession), nil
	}))
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			if err := c.authenticate(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if calls.Load() != 1 || c.session.token != "synthetic-token" {
		t.Fatalf("login calls = %d; Session was not reused", calls.Load())
	}
}

func TestLoginFlagsExpiryAndRetryPolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, want string
		status           int
		blocked          bool
	}{
		{"password-false", `{"vPassword":false,"vInstallId":true}`, codeAuthRejected, 200, true},
		{"install-false", `{"vPassword":true,"vInstallId":false,"expiresAt":"2099-01-01T00:00:00Z"}`, codeValidation, 200, true},
		{"missing-flags", `{"expiresAt":"2099-01-01T00:00:00Z"}`, codeInvalid, 200, false},
		{"null-flag", `{"vPassword":null,"vInstallId":true}`, codeInvalid, 200, false},
		{"invalid-flag", `{"vPassword":"true","vInstallId":true}`, codeInvalid, 200, false},
		{"missing-expiry", `{"vPassword":true,"vInstallId":true}`, codeInvalid, 200, false},
		{"expired", `{"vPassword":true,"vInstallId":true,"expiresAt":"2000-01-01T00:00:00Z"}`, codeInvalid, 200, false},
		{"unauthorized", `{"message":"private"}`, codeAuthRejected, 401, true},
		{"forbidden", `{"message":"private"}`, codeAuthRejected, 403, true},
		{"transient", `{"message":"private"}`, codeUnavailable, 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			c := testClient(t, syntheticTransport(func(_ *http.Request) (*http.Response, error) {
				calls++
				result := sessionResponse(test.body)
				result.StatusCode = test.status
				return result, nil
			}))
			now := time.Now()
			c.now = func() time.Time { return now }
			for attempt := range 2 {
				err := c.authenticate(t.Context())
				if err == nil || (attempt == 0 && failureCode(err) != test.want) ||
					strings.Contains(err.Error(), "private") {
					t.Fatalf("unsafe/incorrect login failure: %v", err)
				}
			}
			if calls != 1 {
				t.Fatalf("repeated sign-in without cooldown: %d", calls)
			}
			now = now.Add(time.Minute)
			_ = c.authenticate(t.Context())
			wantCalls := 2
			if test.blocked {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("calls after 60s = %d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestScheduledReadRefreshIsBoundedAndUsesRenewedToken(t *testing.T) {
	t.Parallel()
	for _, expiredAgain := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered", true: "second-expiry"}[expiredAgain], func(t *testing.T) {
			t.Parallel()
			var paths []string
			c := authenticatedClient(t, readRefreshPeer(t, expiredAgain, &paths))
			_, err := c.inventory(t.Context())
			if !reflect.DeepEqual(paths, []string{inventoryPath, sessionPath, inventoryPath}) {
				t.Fatalf("bounded read recovery failed: paths=%v err=%v", paths, err)
			}
			wantToken, wantCode := "rotated-token", ""
			if expiredAgain {
				wantToken, wantCode = "", codeExpired
			}
			gotCode := ""
			if err != nil {
				gotCode = failureCode(err)
			}
			if c.session.token != wantToken || gotCode != wantCode {
				t.Fatalf("token recovery = %q, %v", c.session.token, err)
			}
		})
	}
}

func TestAuthorizeSendsOnlyOneCodeAndRechecksFlags(t *testing.T) {
	t.Parallel()
	for _, validated := range []bool{false, true} {
		t.Run(map[bool]string{false: "validation-needed", true: "already-validated"}[validated], func(t *testing.T) {
			t.Parallel()
			var paths []string
			reads := 0
			transport := authorizationPeer(t, validated, &paths)
			err := Authorize(t.Context(), testConfig(), testCredentials(), func() (string, error) {
				reads++
				return "123456\n", nil
			}, transport)
			want := []string{sessionPath}
			wantReads := 0
			if !validated {
				want = []string{sessionPath, "/validation/email", "/validate/email", sessionPath}
				wantReads = 1
			}
			if err != nil || reads != wantReads || !reflect.DeepEqual(paths, want) {
				t.Fatalf("authorize=%v code reads=%d routes=%v", err, reads, paths)
			}
		})
	}
}

func TestCancellationAndFailedCodeInputRemainSafe(t *testing.T) {
	t.Parallel()
	calls := 0
	transport := syntheticTransport(func(_ *http.Request) (*http.Response, error) {
		calls++
		return sessionResponse(`{"vPassword":true,"vInstallId":false,"expiresAt":"2099-01-01T00:00:00Z"}`), nil
	})
	c := testClient(t, transport)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.authenticate(ctx); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled login dispatched")
	}
	err := Authorize(t.Context(), testConfig(), testCredentials(), func() (string, error) {
		return "", errors.New("private code input")
	}, transport)
	if err == nil || strings.Contains(err.Error(), "private") || calls != 2 {
		t.Fatal("code input error leaked or provisioning continued")
	}
}
