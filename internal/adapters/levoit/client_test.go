package levoit //nolint:testpackage // Exercises private protocol decoding and credential lifecycle.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type syntheticTransport func(*http.Request) (*http.Response, error)

func (f syntheticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testClient(t *testing.T, transport http.RoundTripper) *client {
	t.Helper()
	c, err := newClient(Config{PollInterval: time.Minute, TimeZone: "UTC"},
		Credentials{Email: "synthetic@example.com", Password: "password"}, transport)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func authenticatedClient(t *testing.T, transport http.RoundTripper) *client {
	t.Helper()
	c := testClient(t, transport)
	c.session = authSession{token: "synthetic-token", accountID: "synthetic-account"}
	return c
}

func requestBody(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestLoginPacketAndConcurrentSessionReuse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := testClient(t, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		body := requestBody(t, r)
		if r.URL.Host != "smartapi.vesync.com" || r.Method != http.MethodPost || string(body["accountID"]) != `""` ||
			string(body["clientVersion"]) != `"VeSync 5.6.60"` || string(body["userCountryCode"]) != `"US"` {
			t.Fatal("incorrect auth envelope")
		}
		switch r.URL.Path {
		case "/globalPlatform/api/accountAuth/v1/authByPWDOrOTM":
			if string(body["password"]) != `"5f4dcc3b5aa765d61d8327deb882cf99"` ||
				string(body["appID"]) != `"eldodkfj"` {
				t.Fatal("incorrect password transform or app ID")
			}
			return response(
				`{"code":0,"result":{"accountID":"stage-one","authorizeCode":"synthetic-authorization"}}`,
			), nil
		case "/user/api/accountManage/v1/loginByAuthorizeCode4Vesync":
			if string(body["authorizeCode"]) != `"synthetic-authorization"` || body["bizToken"] != nil ||
				body["appID"] != nil {
				t.Fatal("incorrect token exchange")
			}
			return response(
				`{"code":0,"result":{"accountID":"final-account","token":"final-token","countryCode":"US"}}`,
			), nil
		default:
			t.Fatalf("unexpected auth path %s", r.URL.Path)
			return nil, errors.New("unexpected path")
		}
	}))
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			session, err := c.authentication(t.Context(), true)
			if err != nil || session.accountID != "final-account" || session.token != "final-token" {
				t.Error("invalid final session")
			}
		})
	}
	workers.Wait()
	if calls.Load() != 2 {
		t.Fatalf("login HTTP calls = %d, want 2", calls.Load())
	}
}

func TestBypassMutationsAndNestedExpiryAreNotReplayed(t *testing.T) {
	t.Parallel()
	for _, control := range []struct {
		name, method, data string
		invoke             func(*client, device) error
	}{
		{"power", "setSwitch", `{"enabled":true,"id":0}`, func(c *client, d device) error { return c.setPower(t.Context(), d, true) }},
		{"auto", "setPurifierMode", `{"mode":"auto"}`, func(c *client, d device) error { return c.setMode(t.Context(), d, "auto") }},
		{"sleep", "setPurifierMode", `{"mode":"sleep"}`, func(c *client, d device) error { return c.setMode(t.Context(), d, "sleep") }},
		{"manual", "setLevel", `{"id":0,"level":1,"type":"wind"}`, func(c *client, d device) error { return c.setMode(t.Context(), d, "manual") }},
		{"level", "setLevel", `{"id":0,"level":4,"type":"wind"}`, func(c *client, d device) error { return c.setLevel(t.Context(), d, 4) }},
		{"display", "setDisplay", `{"state":false}`, func(c *client, d device) error { return c.setDisplay(t.Context(), d, false) }},
		{"lock", "setChildLock", `{"child_lock":true}`, func(c *client, d device) error { return c.setChildLock(t.Context(), d, true) }},
	} {
		t.Run(control.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			c := authenticatedClient(t, syntheticTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := requestBody(t, r)
				if r.URL.Path != bypassPath || string(body["deviceId"]) != `"cid"` || string(body["cid"]) != `"cid"` ||
					string(body["configModel"]) != `"module"` || r.Header.Get("User-Agent") != "okhttp/3.12.1" {
					t.Fatal("incorrect bypass envelope")
				}
				var payload struct {
					Method string          `json:"method"`
					Source string          `json:"source"`
					Data   json.RawMessage `json:"data"`
				}
				if json.Unmarshal(body["payload"], &payload) != nil || payload.Method != control.method ||
					payload.Source != "APP" ||
					string(payload.Data) != control.data {
					t.Fatalf("payload = %+v", payload)
				}
				return response(`{"code":0,"result":{"code":-11001000,"result":null}}`), nil
			}))
			err := control.invoke(c, device{CID: "cid", ConfigModule: "module"})
			if failureCode(err) != "session_expired" || calls != 1 || c.session.token != "" {
				t.Fatalf("error = %v, calls = %d", err, calls)
			}
		})
	}
}

func TestScheduledReadRefreshesExpiredTokenOnce(t *testing.T) {
	t.Parallel()
	var paths []string
	reads := 0
	c := authenticatedClient(t, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case bypassPath:
			reads++
			if reads == 1 {
				return response(`{"code":0,"result":{"code":-11001000,"result":null}}`), nil
			}
			return response(`{"code":0,"result":{"code":0,"result":{"enabled":false,"filter_life":0}}}`), nil
		case "/globalPlatform/api/accountAuth/v1/authByPWDOrOTM":
			return response(`{"code":0,"result":{"accountID":"account","authorizeCode":"code"}}`), nil
		case "/user/api/accountManage/v1/loginByAuthorizeCode4Vesync":
			return response(`{"code":0,"result":{"accountID":"account","token":"renewed","countryCode":"US"}}`), nil
		default:
			t.Fatal("unexpected request")
			return nil, errors.New("unexpected path")
		}
	}))
	got, err := c.status(t.Context(), device{CID: "cid", ConfigModule: "module"})
	if err != nil || got.Power == nil || *got.Power || got.FilterLife == nil || *got.FilterLife != 0 ||
		len(paths) != 4 {
		t.Fatalf("read = %+v, %v; paths %v", got, err, paths)
	}
}

func TestRejectedAuthenticationAndCooldownBoundRequests(t *testing.T) {
	t.Parallel()
	for _, code := range []int{-11201022, -11202000, -11260022, -11257129} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			t.Parallel()
			calls := 0
			c := testClient(t, syntheticTransport(func(_ *http.Request) (*http.Response, error) {
				calls++
				return response(fmt.Sprintf(`{"code":%d,"msg":"secret@example.com"}`, code)), nil
			}))
			for range 2 {
				_, err := c.authentication(t.Context(), true)
				if err == nil || strings.Contains(err.Error(), "secret@example.com") {
					t.Fatal("unsafe authentication error")
				}
			}
			if calls != 1 {
				t.Fatalf("repeated rejected login %d times", calls)
			}
		})
	}
	calls := 0
	c := authenticatedClient(t, syntheticTransport(func(_ *http.Request) (*http.Response, error) {
		calls++
		r := response("")
		r.StatusCode = http.StatusTooManyRequests
		r.Header.Set("Retry-After", "120")
		return r, nil
	}))
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	d := device{CID: "cid", ConfigModule: "module"}
	for range 2 {
		if err := c.setPower(t.Context(), d, true); failureCode(err) != "provider_throttled" {
			t.Fatal(err)
		}
	}
	if calls != 1 || c.cooldown.Sub(now) != 2*time.Minute {
		t.Fatal("shared cooldown not applied")
	}
	now = now.Add(2 * time.Minute)
	_ = c.setDisplay(t.Context(), d, true)
	if calls != 2 {
		t.Fatal("cooldown did not expire")
	}
}

func TestCompleteInventoryPaginationAndExactModels(t *testing.T) {
	t.Parallel()
	pages := 0
	c := authenticatedClient(t, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		pages++
		body := requestBody(t, r)
		if string(body["pageNo"]) != strconv.Itoa(pages) || string(body["pageSize"]) != "100" {
			t.Fatal("incorrect inventory page")
		}
		if pages == 1 {
			return response(
				`{"code":0,"result":{"pageNo":1,"total":3,"list":[{"cid":"one","deviceType":"Core400S","deviceName":"  Living room  ","configModule":"module","deviceRegion":"US","connectionStatus":"online"},{"deviceType":"Core400S-unknown"}]}}`,
			), nil
		}
		return response(
			`{"code":0,"result":{"pageNo":2,"total":3,"list":[{"cid":"two","deviceType":"LAP-C601S-WUS","configModule":"module","deviceProp":{"connectionStatus":"offline"}}]}}`,
		), nil
	}))
	devices, err := c.devices(t.Context())
	if err != nil || pages != 2 || len(devices) != 2 || devices[0].Name != "Living room" || !devices[0].Online ||
		devices[1].Online ||
		devices[1].Name != "Core 600S" {
		t.Fatalf("devices = %+v, error = %v", devices, err)
	}
}

func TestInvalidInventoryNeverReturnsPartialMembership(t *testing.T) {
	t.Parallel()
	for _, listing := range []string{
		`{"total":1,"list":[{"cid":"existing","configModule":"m"}]}`,
		`{"total":2,"list":[{"cid":"same","deviceType":"Core400S","configModule":"m"},{"cid":"same","deviceType":"unsupported","configModule":"m"}]}`,
		`{"total":2,"list":[{"cid":"same","deviceType":"Core400S","configModule":"m"},{"cid":"same","deviceType":"Core600S","configModule":"m"}]}`,
		`{"total":1,"list":[{"deviceType":"Core400S","configModule":"m"}]}`,
		`{"total":1,"list":[{"cid":"cid","deviceType":"Core400S"}]}`,
		`{"total":1,"list":[]}`,
		`{"total":0,"list":null}`,
	} {
		c := authenticatedClient(t, syntheticTransport(func(_ *http.Request) (*http.Response, error) {
			return response(`{"code":0,"result":` + listing + `}`), nil
		}))
		devices, err := c.devices(t.Context())
		if err == nil || devices != nil {
			t.Fatalf("accepted partial/invalid inventory: %s", listing)
		}
	}
}

func TestSecondExpiredReadClearsRenewedSession(t *testing.T) {
	t.Parallel()
	reads := 0
	c := authenticatedClient(t, syntheticTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case bypassPath:
			reads++
			return response(`{"code":0,"result":{"code":-11001000,"result":null}}`), nil
		case "/globalPlatform/api/accountAuth/v1/authByPWDOrOTM":
			return response(`{"code":0,"result":{"accountID":"account","authorizeCode":"code"}}`), nil
		case "/user/api/accountManage/v1/loginByAuthorizeCode4Vesync":
			return response(`{"code":0,"result":{"accountID":"account","token":"renewed","countryCode":"US"}}`), nil
		default:
			return nil, errors.New("unexpected path")
		}
	}))
	d := device{CID: "cid", ConfigModule: "module"}
	if _, err := c.status(t.Context(), d); failureCode(err) != reasonSessionExpired {
		t.Fatal("read did not retain expiry")
	}
	if c.session.token != "" || reads != 2 {
		t.Fatal("expired renewal remained installed or read was retried again")
	}
	if err := c.setPower(t.Context(), d, true); failureCode(err) != reasonSessionExpired || reads != 2 {
		t.Fatal("command sent a known-expired session")
	}
}

func TestStatusFieldsPreserveAbsentZeroAndConfiguredSettings(t *testing.T) {
	t.Parallel()
	received := time.Now().UTC()
	got, err := decodeStatus(
		[]byte(
			`{"enabled":false,"mode":"manual","level":4,"display":false,"configuration":{"display":true},"child_lock":false,"air_quality_value":0,"filter_life":0}`,
		),
		received,
	)
	if err != nil || got.Power == nil || *got.Power || got.Display == nil || !*got.Display ||
		got.ChildLock == nil || *got.ChildLock || got.PM25 == nil || *got.PM25 != 0 ||
		got.FilterLife == nil || *got.FilterLife != 0 || got.ManualLevel == nil || *got.ManualLevel != 4 || got.ReceivedAt != received {
		t.Fatalf("status = %+v, %v", got, err)
	}
	for _, raw := range []string{`{}`, `{"enabled":null,"mode":"unknown","air_quality_value":-1,"filter_life":101,"levelNew":0}`, `{"enabled":1,"child_lock":"false","air_quality_value":"2","filter_life":null,"mode":false}`} {
		got, err = decodeStatus([]byte(raw), received)
		if err != nil || got.Power != nil || got.ChildLock != nil || got.PM25 != nil || got.FilterLife != nil ||
			got.Mode != nil ||
			got.ManualLevel != nil ||
			got.Display != nil {
			t.Fatalf("invented invalid field from %s", raw)
		}
	}
	got, err = decodeStatus([]byte(`{"mode":"auto","level":4,"levelNew":2}`), received)
	if err != nil || got.ManualLevel == nil || *got.ManualLevel != 2 {
		t.Fatal("stored manual selection lost")
	}
	got, err = decodeStatus([]byte(`{"mode":"auto","level":4}`), received)
	if err != nil || got.ManualLevel != nil {
		t.Fatal("invented manual selection from auto speed")
	}
}

func TestTransportFailuresNeverRetryMutation(t *testing.T) {
	t.Parallel()
	for _, reply := range []string{`{"code":0,"result":{"result":{}}}`, `{"code":0,"result":{"code":11,"msg":"private","result":{}}}`, strings.Repeat("x", maximumResponseBytes+1)} {
		calls := 0
		c := authenticatedClient(
			t,
			syntheticTransport(func(_ *http.Request) (*http.Response, error) { calls++; return response(reply), nil }),
		)
		err := c.setPower(t.Context(), device{CID: "cid", ConfigModule: "module"}, true)
		if err == nil || calls != 1 || strings.Contains(err.Error(), "private") {
			t.Fatal("mutation failed unsafely")
		}
	}
	c := authenticatedClient(
		t,
		syntheticTransport(
			func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() },
		),
	)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.setPower(ctx, device{CID: "cid", ConfigModule: "module"}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestRealMutationAcknowledgementNeedsCodeNotEchoedResult(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"code":0,"result":{"code":0}}`, `{"code":0,"result":{"code":0,"result":null}}`} {
		c := authenticatedClient(
			t,
			syntheticTransport(func(_ *http.Request) (*http.Response, error) { return response(body), nil }),
		)
		if err := c.setDisplay(t.Context(), device{CID: "cid", ConfigModule: "module"}, true); err != nil {
			t.Fatal(err)
		}
		if _, err := c.status(
			t.Context(),
			device{CID: "cid", ConfigModule: "module"},
		); failureCode(
			err,
		) != reasonInvalidResponse {
			t.Fatal("missing status result was accepted")
		}
	}
	if failure, ok := errors.AsType[*providerError](codeError(-123456789)); !ok || !failure.uncertain {
		t.Fatal("unknown provider code became a definite effect rejection")
	}
}
