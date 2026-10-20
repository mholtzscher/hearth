//go:build integration || e2e

package hearthd //nolint:testpackage // Exercises real assembly and shutdown with the existing Run harness.

import (
	"encoding/json"
	"testing"
)

func decodeNotification[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
