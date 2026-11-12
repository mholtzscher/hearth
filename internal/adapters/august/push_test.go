package august //nolint:testpackage // Exercises the private subscription boundary.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPushSubscriptionRoutesAndCursor(t *testing.T) {
	t.Parallel()
	stop := errors.New("stop delivery")
	calls, deliveries := 0, 0
	client := newPushClient(syntheticTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "ps.pndsn.com" || req.Method != http.MethodGet ||
			req.URL.Path != "/v2/subscribe/"+pushSubscribeKey+"/alpha,beta/0" || len(req.Header) != 0 {
			t.Fatal("subscription left its fixed, credential-free route")
		}
		if calls == 1 {
			if req.URL.Query().Get("tt") != "0" || req.URL.Query().Has("tr") {
				t.Fatal("invalid handshake cursor")
			}
			return response(200, `{"t":{"t":"123456789","r":4},"m":[]}`), nil
		}
		if req.URL.Query().Get("tt") != "123456789" || req.URL.Query().Get("tr") != "4" {
			t.Fatal("cursor/region were not carried forward")
		}
		return response(200, `{"t":{"t":"123456790","r":5},"m":[`+
			`{"c":"other","d":{"status":"locked"}},`+
			`{"c":"alpha","e":1,"d":{"status":"locked"}},`+
			`{"c":"alpha","d":{"lockID":"22222222222222222222222222222222","status":"locked"}},`+
			`{"c":"beta","d":{"info":{"lockID":"22222222222222222222222222222222","startTime":"2025-01-01T00:00:00Z"},"lockAction":"unlock","doorState":"closed"}}]}`), nil
	}))
	err := client.Run(
		context.Background(),
		[]pushTarget{{testLockID, "alpha"}, {testOtherID, "beta"}},
		func(_ context.Context, got pushReport) error {
			deliveries++
			if got.DeviceID != testOtherID || got.Report.Lock == nil || got.Report.Lock.Value != "unlocked" ||
				got.Report.Lock.SourceUpdatedAt != nil || got.Report.DoorOpen == nil || got.Report.DoorOpen.Value || got.Report.ReceivedAt.IsZero() {
				t.Fatal("incorrect ordinary report")
			}
			return stop
		},
	)
	if !errors.Is(err, stop) || calls != 2 || deliveries != 1 {
		t.Fatalf("Run returned %v after %d requests and %d deliveries", err, calls, deliveries)
	}
}

func TestPushTargetValidation(t *testing.T) {
	t.Parallel()
	good := pushTarget{testLockID, "alpha"}
	cases := [][]pushTarget{
		nil, {{testLockID, ""}}, {{testLockID, "a,b"}}, {{testLockID, "../other"}},
		{{testLockID, "a?x=y"}}, {{testLockID, "%2F"}}, {{testLockID, ".."}},
		{{testLockID, strings.Repeat("x", 93)}}, {{"bad", "alpha"}},
		{good, good}, {good, {testOtherID, "alpha"}}, {good, {testLockID, "beta"}},
	}
	for _, targets := range cases {
		client := newPushClient(syntheticTransport(func(_ *http.Request) (*http.Response, error) {
			t.Fatal("invalid selection reached transport")
			return nil, errors.New("unexpected request")
		}))
		if err := client.Run(
			context.Background(),
			targets,
			func(context.Context, pushReport) error { return nil },
		); failureCode(
			err,
		) != codeInvalid {
			t.Fatalf("invalid targets accepted: %v", err)
		}
	}
}

//nolint:gocognit // One matrix asserts sibling presence and values together.
func TestPushDecodeEvidence(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name, data, lock string
		door             *bool
		timestamp        bool
	}{
		{
			"explicit readings",
			`{"status":"kAugLockState_Locked","doorState":"kAugDoorState_Open","dateTime":"2026-01-01T00:00:00Z"}`,
			"locked",
			new(true),
			true,
		},
		{"false door", `{"doorState":"closed"}`, "", new(false), false},
		{"explicit unknown", `{"status":"kAugLockState_Unknown"}`, "unknown", nil, false},
		{"unknown is not unknown state", `{"status":"new-vendor-value"}`, "", nil, false},
		{"unsupported action takes precedence", `{"lockAction":"unexpected","status":"locked"}`, "", nil, false},
		{"invalid time", `{"status":"locked","dateTime":"bad"}`, "", nil, false},
		{"wrong nested id", `{"info":{"lockID":"22222222222222222222222222222222"},"status":"locked"}`, "", nil, false},
		{"null id", `{"lockId":null,"status":"locked"}`, "", nil, false},
		{"unsupported event", `{"action":"doorclosed"}`, "", nil, false},
		{"not object", `"locked"`, "", nil, false},
		{
			"no inferred reachability",
			`{"Bridge":{"operative":true,"status":{"current":"online"}},"battery":0.5,"status":"locked"}`,
			"locked",
			nil,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := decodePushMessage([]byte(tc.data), testLockID, now)
			if ok != (tc.lock != "" || tc.door != nil) {
				t.Fatal("unexpected delivery decision")
			}
			if tc.lock != "" &&
				(got.Lock == nil || string(got.Lock.Value) != tc.lock || (got.Lock.SourceUpdatedAt != nil) != tc.timestamp) {
				t.Fatal("invalid lock reading")
			}
			if tc.lock == "" && got.Lock != nil ||
				tc.door != nil && (got.DoorOpen == nil || got.DoorOpen.Value != *tc.door) ||
				tc.door == nil && got.DoorOpen != nil {
				t.Fatal("invalid sibling readings")
			}
			if got.Online != nil || got.Battery != nil {
				t.Fatal("push fabricated reachability or battery evidence")
			}
		})
	}
}

func TestPushFailuresEndGeneration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"status", ``, 503, codeUnavailable},
		{"redirect", ``, 302, codeUnavailable},
		{"malformed", `{`, 200, codeInvalid},
		{"missing cursor", `{"m":[]}`, 200, codeInvalid},
		{"bad token", `{"t":{"t":"x","r":4},"m":[]}`, 200, codeInvalid},
		{"missing messages", `{"t":{"t":"1","r":4}}`, 200, codeInvalid},
		{"oversized", strings.Repeat("x", maximumResponseBytes+1), 200, codeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			client := newPushClient(syntheticTransport(func(_ *http.Request) (*http.Response, error) {
				calls++
				return response(tc.status, tc.body), nil
			}))
			err := client.Run(
				context.Background(),
				[]pushTarget{{testLockID, "alpha"}},
				func(context.Context, pushReport) error { t.Fatal("unexpected delivery"); return nil },
			)
			if failureCode(err) != tc.code || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestPushOfflineErrorDoesNotPublishCachedLockState(t *testing.T) {
	t.Parallel()
	got, ok := decodePushMessage(
		[]byte(`{"error":{"restCode":98,"name":"ERRNO_BRIDGE_OFFLINE"},"status":"locked"}`),
		testLockID,
		time.Now(),
	)
	if !ok || got.Online == nil || *got.Online || got.Lock != nil || got.DoorOpen != nil {
		t.Fatal("explicit offline evidence was discarded or cached State was published")
	}
}

func TestPushCancellationInterruptsTransport(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	client := newPushClient(syntheticTransport(func(req *http.Request) (*http.Response, error) {
		cancel()
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))
	if err := client.Run(
		ctx,
		[]pushTarget{{testLockID, "alpha"}},
		func(context.Context, pushReport) error { return nil },
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestPushRefusesRedirect(t *testing.T) {
	t.Parallel()
	calls := 0
	client := newPushClient(syntheticTransport(func(_ *http.Request) (*http.Response, error) {
		calls++
		result := response(http.StatusFound, "")
		result.Header.Set("Location", "https://example.invalid/redirect")
		return result, nil
	}))
	err := client.Run(
		context.Background(),
		[]pushTarget{{testLockID, "alpha"}},
		func(context.Context, pushReport) error { return nil },
	)
	if err == nil || calls != 1 {
		t.Fatalf("redirect was followed or accepted: error=%v calls=%d", err, calls)
	}
}

func TestPushBodyReadFailureEndsGeneration(t *testing.T) {
	t.Parallel()
	client := newPushClient(syntheticTransport(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(failingPushReader{}),
		}, nil
	}))
	if err := client.Run(
		context.Background(),
		[]pushTarget{{testLockID, "alpha"}},
		func(context.Context, pushReport) error { return nil },
	); failureCode(
		err,
	) != codeUnavailable {
		t.Fatalf("expected read failure, got %v", err)
	}
}

type failingPushReader struct{}

func (failingPushReader) Read([]byte) (int, error) { return 0, errors.New("synthetic read error") }
