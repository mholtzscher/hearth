package august

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	apiOrigin            = "https://api-production.august.com"
	sessionPath          = "/session"
	inventoryPath        = "/users/locks/mine"
	maximumResponseBytes = 1024 * 1024
	readTimeout          = 10 * time.Second
	mutationTimeout      = 20 * time.Second
	applicationID        = "79fd0eb6-381d-4adf-95a0-47721289d1d9" // Public US client key; see pinned provenance in the spec.
	codeAuthRejected     = "auth_rejected"
	codeValidation       = "validation_required"
	codeExpired          = "session_expired"
	codeThrottled        = "throttled"
	codeOffline          = "device_offline"
	codeRejected         = "upstream_rejected"
	codeInvalid          = "invalid_response"
	codeIndeterminate    = "dispatch_indeterminate"
	codeUnavailable      = "provider_unavailable"
)

type providerError struct {
	code       string
	httpStatus int
	cause      error
}

func (e *providerError) Error() string { return "August: " + e.code }

func (e *providerError) Unwrap() error { return e.cause }

func failureCode(err error) string {
	if failure, ok := errors.AsType[*providerError](err); ok {
		return failure.code
	}
	return codeUnavailable
}

type authSession struct {
	token     string
	expiresAt time.Time
}

type providerResponse struct {
	status     int
	header     http.Header
	body       []byte
	receivedAt time.Time
	attempted  bool
}

type client struct {
	config      Config
	credentials Credentials
	http        *http.Client
	now         func() time.Time
	loginGate   chan struct{}
	mu          sync.Mutex
	session     authSession
	blocked     string
	cooldown    time.Time
	nextLogin   time.Time
}

func newClient(config Config, credentials Credentials, transport http.RoundTripper) (*client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := credentials.Validate(); err != nil {
		return nil, err
	}
	config.Bindings = slices.Clone(config.Bindings)
	for i := range config.Bindings {
		id, _ := parseLockID(config.Bindings[i].LockID) // Config validation established the ID shape.
		config.Bindings[i].LockID = string(id)
	}
	return &client{config: config, credentials: credentials, now: time.Now, loginGate: make(chan struct{}, 1),
		http: &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("august redirects are not supported")
		}}}, nil
}

func (c *client) ready() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked != "" {
		return &providerError{code: c.blocked}
	}
	if c.now().Before(c.cooldown) {
		return &providerError{code: codeThrottled}
	}
	return nil
}

func (c *client) request(ctx context.Context, method, path string, body any,
	token string) (providerResponse, error) {
	var result providerResponse
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := c.ready(); err != nil {
		return result, err
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return result, &providerError{code: codeInvalid}
		}
	}
	attempt, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attempt, method, apiOrigin+path, bytes.NewReader(data))
	if err != nil {
		return result, &providerError{code: codeInvalid}
	}
	req.Header.Set("X-August-Api-Key", applicationID)
	req.Header.Set("X-Kease-Api-Key", applicationID)
	req.Header.Set("X-August-Branding", "august")
	req.Header.Set("X-August-Country", "US")
	req.Header.Set("X-August-Access-Token", token)
	req.Header.Set("Accept-Version", "0.0.1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "August/Luna-22.17.0 (Android; SDK 31; gphone64_arm64)")
	if body == nil {
		req.Header.Set("Content-Length", "0")
	}
	result.attempted = true
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, &providerError{code: codeUnavailable, cause: err}
	}
	defer response.Body.Close()
	result.status, result.header, result.receivedAt = response.StatusCode, response.Header, c.now().UTC()
	responseErr := c.responseError(response)
	result.body, err = io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, &providerError{code: codeUnavailable, cause: err}
	}
	if len(result.body) > maximumResponseBytes {
		return result, &providerError{code: codeInvalid}
	}
	return result, responseErr
}

func (c *client) responseError(response *http.Response) error {
	var code string
	switch {
	case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
		return nil
	case response.StatusCode == http.StatusTooManyRequests:
		c.throttle(response.Header.Get("Retry-After"))
		code = codeThrottled
	case response.StatusCode == http.StatusUnauthorized:
		code = codeExpired
	case response.StatusCode == http.StatusForbidden:
		code = codeAuthRejected
	case response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError:
		code = codeRejected
	default:
		code = codeUnavailable
	}
	return &providerError{code: code, httpStatus: response.StatusCode}
}

func (c *client) throttle(value string) {
	duration := c.config.PollInterval
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		if seconds > int64(time.Hour/time.Second) {
			duration = time.Hour
		} else {
			duration = time.Duration(seconds) * time.Second
		}
	} else if at, parseErr := http.ParseTime(value); parseErr == nil && at.After(c.now()) {
		duration = min(at.Sub(c.now()), time.Hour)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cooldown = maxTime(c.cooldown, c.now().Add(duration))
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (c *client) authorizedRead(ctx context.Context, path string) (providerResponse, error) {
	session, err := c.authentication(ctx)
	if err != nil {
		return providerResponse{}, err
	}
	response, err := c.request(ctx, http.MethodGet, path, nil, session.token)
	if failureCode(err) != codeExpired {
		c.renewToken(session, response)
		return response, err
	}
	c.invalidate(session)
	session, err = c.authentication(ctx)
	if err != nil {
		return providerResponse{}, err
	}
	response, err = c.request(ctx, http.MethodGet, path, nil, session.token)
	if failureCode(err) == codeExpired {
		c.invalidate(session)
	} else {
		c.renewToken(session, response)
	}
	return response, err
}

func (c *client) invalidate(session authSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == session {
		c.session = authSession{}
	}
}

func (c *client) renewToken(session authSession, response providerResponse) {
	if response.status < http.StatusOK || response.status >= http.StatusMultipleChoices {
		return
	}
	token := response.header.Get("X-August-Access-Token")
	if token == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == session {
		// Retain the known expiry conservatively; a later explicit login supplies its replacement.
		c.session.token = token
	}
}

func (c *client) inventory(ctx context.Context) ([]inventoryEntry, error) {
	response, err := c.authorizedRead(ctx, inventoryPath)
	if err != nil {
		return nil, err
	}
	return decodeInventory(response.body)
}

func (c *client) selectedID(id lockID) bool {
	return slices.ContainsFunc(c.config.Bindings, func(binding BindingConfig) bool {
		return binding.LockID == string(id)
	})
}

func (c *client) detail(ctx context.Context, entry inventoryEntry) (deviceDetail, error) {
	if !c.selectedID(entry.ID) || entry.UserType != roleSuperuser {
		return deviceDetail{}, &providerError{code: codeRejected}
	}
	response, err := c.authorizedRead(ctx, "/locks/"+string(entry.ID))
	if err != nil {
		return deviceDetail{}, err
	}
	return decodeDetail(response.body, entry, response.receivedAt)
}

func (c *client) status(ctx context.Context, id lockID) (report, error) {
	if !c.selectedID(id) {
		return report{}, &providerError{code: codeRejected}
	}
	response, err := c.authorizedRead(ctx, "/locks/"+string(id)+"/status")
	if err != nil {
		return report{}, err
	}
	return decodeStatus(response.body, response.receivedAt)
}
