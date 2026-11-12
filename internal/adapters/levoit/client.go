package levoit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	apiOrigin                 = "https://smartapi.vesync.com"
	inventoryPath             = "/cloud/v1/deviceManaged/devices"
	bypassPath                = "/cloud/v2/deviceManaged/bypassV2" //nolint:gosec // Protocol path, not a credential.
	appVersion                = "5.6.60"
	maximumResponseBytes      = 1024 * 1024
	requestTimeout            = 5 * time.Second
	reasonInvalidResponse     = "invalid_response"
	reasonProviderUnavailable = "provider_unavailable"
	reasonProviderThrottled   = "provider_throttled"
	reasonSessionExpired      = "session_expired"
	reasonUnsupportedAuth     = "unsupported_authentication"
)

type providerError struct {
	code      string
	uncertain bool // A mutation may have reached the provider without usable outcome evidence.
	cause     error
}

func (e *providerError) Error() string { return "Levoit: " + e.code }

func (e *providerError) Unwrap() error { return e.cause }

func failureCode(err error) string {
	if failure, ok := errors.AsType[*providerError](err); ok {
		return failure.code
	}
	return reasonProviderUnavailable
}

type authSession struct{ token, accountID string }

type client struct {
	config       Config
	credentials  Credentials
	http         *http.Client
	now          func() time.Time
	loginGate    chan struct{}
	mu           sync.Mutex
	session      authSession
	blocked      string
	cooldown     time.Time
	nextLogin    time.Time
	terminalID   string
	traceCounter uint64 // Owned by loginGate.
}

func newClient(config Config, credentials Credentials, transport http.RoundTripper) (*client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := credentials.Validate(); err != nil {
		return nil, err
	}
	return &client{config: config, credentials: credentials, now: time.Now,
		loginGate: make(chan struct{}, 1), terminalID: terminalID(credentials.Email),
		http: &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("levoit redirects are not supported")
		}}}, nil
}

// request returns only the validated outer result. Raw provider messages are never errors.
func (c *client) request(ctx context.Context, path string, body map[string]any,
	decodeResult func([]byte) (json.RawMessage, error)) (json.RawMessage, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	data, err := json.Marshal(body)
	if err != nil {
		return nil, &providerError{code: reasonInvalidResponse}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, apiOrigin+path, bytes.NewReader(data))
	if err != nil {
		return nil, &providerError{code: reasonProviderUnavailable}
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if path == inventoryPath || path == bypassPath {
		request.Header.Set("User-Agent", "okhttp/3.12.1")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &providerError{code: reasonProviderUnavailable, uncertain: true, cause: ctx.Err()}
		}
		return nil, &providerError{code: reasonProviderUnavailable, uncertain: true, cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		c.throttle(response.Header.Get("Retry-After"))
		return nil, &providerError{code: reasonProviderThrottled}
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, &providerError{code: reasonSessionExpired}
	}
	if response.StatusCode != http.StatusOK {
		return nil, &providerError{code: reasonProviderUnavailable, uncertain: true}
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return nil, &providerError{code: reasonProviderUnavailable, uncertain: true, cause: err}
	}
	if len(data) > maximumResponseBytes {
		return nil, &providerError{code: reasonInvalidResponse, uncertain: true}
	}
	result, err := decodeEnvelope(data)
	if err == nil && decodeResult != nil {
		result, err = decodeResult(result)
	}
	if failure, ok := errors.AsType[*providerError](err); ok && failure.code == reasonInvalidResponse {
		failure.uncertain = true
	}
	if failureCode(err) == reasonProviderThrottled {
		c.throttle(response.Header.Get("Retry-After"))
	}
	return result, err
}

func (c *client) ready() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked != "" {
		return &providerError{code: c.blocked}
	}
	if c.now().Before(c.cooldown) {
		return &providerError{code: reasonProviderThrottled}
	}
	return nil
}

func (c *client) throttle(retryAfter string) {
	duration := c.config.PollInterval
	if seconds, err := strconv.ParseInt(retryAfter, 10, 64); err == nil && seconds > 0 && seconds <= 3600 {
		duration = time.Duration(seconds) * time.Second
	} else if retryAt, parseErr := http.ParseTime(retryAfter); parseErr == nil {
		if remaining := retryAt.Sub(c.now()); remaining > 0 && remaining <= time.Hour {
			duration = remaining
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	until := c.now().Add(duration)
	if until.After(c.cooldown) {
		c.cooldown = until
	}
}

func (c *client) authorized(ctx context.Context, path string, makeBody func(authSession) map[string]any,
	refresh bool, decodeResult func([]byte) (json.RawMessage, error)) (json.RawMessage, error) {
	session, err := c.authentication(ctx, refresh)
	if err != nil {
		return nil, err
	}
	result, err := c.request(ctx, path, makeBody(session), decodeResult)
	if failureCode(err) != reasonSessionExpired {
		return result, err
	}
	c.invalidate(session)
	if !refresh {
		return nil, err
	}
	session, err = c.authentication(ctx, true)
	if err != nil {
		return nil, err
	}
	result, err = c.request(ctx, path, makeBody(session), decodeResult)
	if failureCode(err) == reasonSessionExpired {
		c.invalidate(session)
	}
	return result, err
}

func (c *client) invalidate(session authSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == session {
		c.session = authSession{}
	}
}

func (c *client) common(session authSession) map[string]any {
	return map[string]any{"acceptLanguage": "en", "accountID": session.accountID, "token": session.token,
		"appVersion": appVersion, "phoneBrand": "pyvesync", "phoneOS": "Android", "timeZone": c.config.TimeZone,
		"traceId": strconv.FormatInt(c.now().Unix(), 10)}
}

func (c *client) bypass(ctx context.Context, d device, method string, data map[string]any,
	refresh bool) (json.RawMessage, error) {
	decodeResult := decodeAcknowledgement
	if method == "getPurifierStatus" {
		decodeResult = decodeEnvelope
	}
	return c.authorized(ctx, bypassPath, func(session authSession) map[string]any {
		body := c.common(session)
		body["method"], body["cid"], body["deviceId"] = "bypassV2", d.CID, d.CID
		body["configModule"], body["configModel"] = d.ConfigModule, d.ConfigModule
		body["debugMode"], body["userCountryCode"] = false, "US"
		body["payload"] = map[string]any{"method": method, "source": "APP", "data": data}
		return body
	}, refresh, decodeResult)
}

func (c *client) status(ctx context.Context, d device) (status, error) {
	result, err := c.bypass(ctx, d, "getPurifierStatus", map[string]any{}, true)
	if err != nil {
		return status{}, err
	}
	return decodeStatus(result, c.now().UTC())
}

func (c *client) setPower(ctx context.Context, d device, value bool) error {
	_, err := c.bypass(ctx, d, "setSwitch", map[string]any{"enabled": value, "id": 0}, false)
	return err
}

func (c *client) setMode(ctx context.Context, d device, value string) error {
	if value == modeManual {
		return c.setLevel(ctx, d, 1)
	}
	if value != modeAuto && value != modeSleep {
		return &providerError{code: reasonInvalidResponse}
	}
	_, err := c.bypass(ctx, d, "setPurifierMode", map[string]any{"mode": value}, false)
	return err
}

func (c *client) setLevel(ctx context.Context, d device, value int) error {
	if value < 1 || value > 4 {
		return &providerError{code: reasonInvalidResponse}
	}
	_, err := c.bypass(ctx, d, "setLevel", map[string]any{"id": 0, "level": value, "type": "wind"}, false)
	return err
}

func (c *client) setDisplay(ctx context.Context, d device, value bool) error {
	_, err := c.bypass(ctx, d, "setDisplay", map[string]any{"state": value}, false)
	return err
}

func (c *client) setChildLock(ctx context.Context, d device, value bool) error {
	_, err := c.bypass(ctx, d, "setChildLock", map[string]any{"child_lock": value}, false)
	return err
}
