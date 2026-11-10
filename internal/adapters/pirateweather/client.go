package pirateweather

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

const (
	requestTimeout            = 10 * time.Second
	maxProviderBody           = 1 << 20
	maxCooldown               = 31 * 24 * time.Hour
	reasonInvalidResponse     = "invalid_response"
	reasonProviderUnavailable = "provider_unavailable"
)

// providerError exposes a safe reason code and tracks whether HTTP started so
// Functions can distinguish failed attempts from requests that never executed.
type providerError struct {
	reason  string
	started bool
}

func (e *providerError) Error() string { return e.reason }

type client struct {
	config                                             Config
	apiKey                                             string
	http                                               *http.Client
	now                                                func() time.Time
	mu                                                 sync.Mutex
	throttledUntil                                     time.Time
	hourlyInput, dailyInput, hourlyOutput, dailyOutput *functionschema.Schema
}

func newClient(config Config, apiKey string, transport http.RoundTripper) (*client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if apiKey == "" || strings.TrimSpace(apiKey) != apiKey || strings.ContainsAny(apiKey, "\r\n") ||
		!utf8.ValidString(apiKey) {
		return nil, errors.New("invalid API key")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	c := &client{config: config, apiKey: apiKey, now: time.Now, http: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	var err error
	c.hourlyInput, c.dailyInput, c.hourlyOutput, c.dailyOutput, err = compileFunctionSchemas()
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *client) current(ctx context.Context) (currentResult, error) {
	body, received, err := c.request(ctx, "currently")
	if err != nil {
		return currentResult{}, err
	}
	result, err := decodeCurrent(body, received, c.config.PollInterval)
	if err != nil {
		return currentResult{}, &providerError{reason: reasonInvalidResponse, started: true}
	}
	return result, nil
}

func (c *client) request(ctx context.Context, block string) ([]byte, time.Time, error) {
	if ctx.Err() != nil {
		return nil, time.Time{}, &providerError{reason: "canceled"}
	}
	c.mu.Lock()
	cooling := c.now().Before(c.throttledUntil)
	c.mu.Unlock()
	if cooling {
		return nil, time.Time{}, &providerError{reason: "provider_throttled"}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	query := url.Values{"units": {"si"}, "version": {"2"}, "lang": {"en"}}
	var excluded []string
	for _, candidate := range []string{"currently", "minutely", "hourly", "daily", "alerts"} {
		if candidate != block {
			excluded = append(excluded, candidate)
		}
	}
	query.Set("exclude", strings.Join(excluded, ","))
	endpoint := fmt.Sprintf(
		"https://api.pirateweather.net/forecast/KeyInHeader/%s,%s?%s",
		strconv.FormatFloat(c.config.Latitude, 'f', -1, 64),
		strconv.FormatFloat(c.config.Longitude, 'f', -1, 64),
		query.Encode(),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, time.Time{}, &providerError{reason: reasonProviderUnavailable}
	}
	req.Header.Set("Apikey", c.apiKey)
	if ctx.Err() != nil {
		return nil, time.Time{}, &providerError{reason: "canceled"}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, time.Time{}, &providerError{reason: reasonProviderUnavailable, started: true}
	}
	defer resp.Body.Close()
	now := c.now().UTC()
	if resp.StatusCode == http.StatusTooManyRequests {
		c.cooldown(resp.Header, now)
		return nil, time.Time{}, &providerError{reason: "provider_throttled", started: true}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, time.Time{}, &providerError{reason: "provider_authentication", started: true}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, &providerError{reason: reasonProviderUnavailable, started: true}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody+1))
	if err != nil {
		return nil, time.Time{}, &providerError{reason: reasonProviderUnavailable, started: true}
	}
	if len(body) > maxProviderBody {
		return nil, time.Time{}, &providerError{reason: reasonInvalidResponse, started: true}
	}
	return body, c.now().UTC(), nil
}

func (c *client) cooldown(headers http.Header, now time.Time) {
	var until time.Time
	if value := headers.Get("Retry-After"); value != "" {
		if seconds, err := strconv.ParseInt(
			value,
			10,
			64,
		); err == nil && seconds >= 0 &&
			seconds <= int64(maxCooldown/time.Second) {
			until = now.Add(time.Duration(seconds) * time.Second)
		} else if parsed, parseErr := http.ParseTime(
			value,
		); parseErr == nil && !parsed.Before(now) &&
			parsed.Sub(now) <= maxCooldown {
			until = parsed
		}
	}
	if headers.Get("Ratelimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(
			headers.Get("Ratelimit-Reset"),
			10,
			64,
		); err == nil && seconds >= 0 &&
			seconds <= int64(maxCooldown/time.Second) {
			reset := now.Add(time.Duration(seconds) * time.Second)
			if reset.After(until) {
				until = reset
			}
		}
	}
	if until.IsZero() {
		until = now.Add(c.config.PollInterval)
	}
	c.mu.Lock()
	if until.After(c.throttledUntil) {
		c.throttledUntil = until
	}
	c.mu.Unlock()
}
