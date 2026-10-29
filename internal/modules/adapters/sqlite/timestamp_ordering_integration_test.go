//go:build integration

package sqlite_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestLeaseExpiryOrdersWholeFractionalAndTiedInstants(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	// Claim in reverse chronological/identity order so insertion order cannot
	// satisfy the expiry query's (lease_expires_at, runtime_id) contract.
	for _, test := range []struct {
		adapter string
		runtime adapters.RuntimeID
		delay   time.Duration
	}{
		{"fractional-b", replacementRuntime, 100 * time.Millisecond},
		{"fractional-a", testRuntime, 100 * time.Millisecond},
		{"whole", "run_01890f47-7a6b-7c4d-8e9f-0123456789ad", 0},
		{"future", "run_01890f47-7a6b-7c4d-8e9f-0123456789ae", time.Second},
	} {
		write := claimWrite(at.Add(test.delay))
		write.AdapterID = test.adapter
		write.RuntimeID = test.runtime
		if err := repository.ClaimAdapterRuntime(t.Context(), write); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.ExpireAdapterLeases(t.Context(), adapters.ExpireLeasesWrite{
		ExpiresAt: at.Add(15*time.Second + 100*time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := database.QueryContext(t.Context(), `SELECT adapter_id FROM health_transitions
		WHERE reason_code = 'hearth.heartbeat_expired' ORDER BY receive_order`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for _, want := range []string{"whole", "fractional-a", "fractional-b"} {
		var got string
		if !rows.Next() {
			t.Fatalf("missing expiry for %s: %v", want, rows.Err())
		}
		if err = rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("expiry order: got %s, want %s", got, want)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra expiry or query error: %v", rows.Err())
	}
}
