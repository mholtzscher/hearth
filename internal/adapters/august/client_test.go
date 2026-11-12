package august //nolint:testpackage // Exercises the private HTTP boundary without sockets.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClientRejectsInvalidConfigurationBeforeTransport(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*Config)
	}{
		{"nil-installation", func(c *Config) { c.InstallationID = "00000000-0000-0000-0000-000000000000" }},
		{"invalid-installation", func(c *Config) { c.InstallationID = "invalid" }},
		{"short-poll", func(c *Config) { c.PollInterval = time.Second }},
		{"long-poll", func(c *Config) { c.PollInterval = 2 * time.Hour }},
		{"short-inventory", func(c *Config) { c.InventoryInterval = time.Second }},
		{"long-inventory", func(c *Config) { c.InventoryInterval = 2 * time.Hour }},
		{"no-bindings", func(c *Config) { c.Bindings = nil }},
		{"invalid-key", func(c *Config) { c.Bindings[0].BindingKey = "bad.key" }},
		{"invalid-id", func(c *Config) { c.Bindings[0].LockID = "../other" }},
		{"long-name", func(c *Config) { c.Bindings[0].Name = strings.Repeat("a", 129) }},
		{"duplicate-key", func(c *Config) {
			c.Bindings = append(c.Bindings, BindingConfig{BindingKey: "front", LockID: string(testOtherID)})
		}},
		{"duplicate-normalized-id", func(c *Config) {
			c.Bindings[0].LockID = strings.Repeat("a", 32)
			c.Bindings = append(c.Bindings, BindingConfig{BindingKey: "deck", LockID: strings.Repeat("A", 32)})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := testConfig()
			test.change(&config)
			if _, err := newClient(config, testCredentials(), nil); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if _, err := newClient(testConfig(), Credentials{Email: "synthetic@example.com"}, nil); err == nil {
		t.Fatal("empty password accepted")
	}
}

func TestCooldownIsAccountWideAndBounded(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"120", 2 * time.Minute}, {"7200", time.Hour}, {"invalid", time.Minute},
		{"Thu, 12 Nov 2026 00:03:00 GMT", 3 * time.Minute},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			calls := 0
			c := authenticatedClient(t, syntheticTransport(func(_ *http.Request) (*http.Response, error) {
				calls++
				result := response(429, `{"error":"private"}`)
				result.Header.Set("Retry-After", test.value)
				return result, nil
			}))
			now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
			c.now = func() time.Time { return now }
			if _, err := c.inventory(t.Context()); failureCode(err) != codeThrottled {
				t.Fatal(err)
			}
			if _, err := c.status(t.Context(), testLockID); failureCode(err) != codeThrottled {
				t.Fatal(err)
			}
			if calls != 1 || c.cooldown.Sub(now) != test.want {
				t.Fatal("account cooldown not honored")
			}
			now = now.Add(test.want)
			_, _ = c.inventory(t.Context())
			if calls != 2 {
				t.Fatal("scheduled recovery did not resume after cooldown")
			}
		})
	}
}

func TestHTTPBoundariesAndSafeErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"too-large", 200, strings.Repeat("x", maximumResponseBytes+1), codeInvalid},
		{"forbidden", 403, "private", codeAuthRejected},
		{"rejected", 400, "private", codeRejected},
		{"service", 503, "private", codeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c := authenticatedClient(
				t,
				syntheticTransport(
					func(_ *http.Request) (*http.Response, error) { return response(test.status, test.body), nil },
				),
			)
			_, err := c.inventory(t.Context())
			if failureCode(err) != test.want || strings.Contains(err.Error(), "private") {
				t.Fatalf("failure=%v", err)
			}
		})
	}
	c := authenticatedClient(t, syntheticTransport(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.inventory(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost caller cancellation: %v", err)
	}
}

func TestExactMembershipBlocksExtraRecordsAndRetainsValidEmptyInventory(t *testing.T) {
	t.Parallel()
	calls := 0
	c := authenticatedClient(t, syntheticTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path != inventoryPath {
			t.Fatal("extra Device requested")
		}
		return response(200, `{}`), nil
	}))
	for _, entry := range []inventoryEntry{{ID: testOtherID, UserType: "superuser"}, {ID: testLockID, UserType: "user"}} {
		if _, err := c.detail(t.Context(), entry); failureCode(err) != codeRejected {
			t.Fatal("ineligible detail request allowed")
		}
	}
	if _, err := c.status(t.Context(), testOtherID); failureCode(err) != codeRejected {
		t.Fatal("unconfigured status request allowed")
	}
	entries, err := c.inventory(t.Context())
	if err != nil || len(entries) != 0 || calls != 1 {
		t.Fatal("complete empty inventory was not distinguished from failure")
	}
}

func TestSelectedStatusPublishesOnlyTheExactLockReading(t *testing.T) {
	t.Parallel()
	c := authenticatedClient(t, syntheticTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != apiOrigin+"/locks/"+string(testLockID)+"/status" ||
			request.Method != http.MethodGet {
			t.Fatal("incorrect selected status route")
		}
		return response(200, `{"status":"locked","doorState":"open"}`), nil
	}))
	got, err := c.status(t.Context(), testLockID)
	if err != nil || got.Lock == nil || got.Lock.Value != "locked" || got.DoorOpen == nil || !got.DoorOpen.Value {
		t.Fatalf("status = %+v, %v", got, err)
	}
}
