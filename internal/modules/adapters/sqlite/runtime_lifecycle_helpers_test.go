//go:build integration

package sqlite //nolint:testpackage // Fixtures assemble independent migrated Adapter repositories.
import (
	"database/sql"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

const testAdapterLeaseDuration = 15 * time.Second
const (
	testRuntimeID     = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	testSecondRuntime = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	testThirdRuntime  = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ad")
)

func openMigratedDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	return dbtest.OpenMigrated(t, path)
}
func testClaimWrite(runtimeID adapters.RuntimeID, claimedAt time.Time) adapters.ClaimRuntimeWrite {
	return adapters.ClaimRuntimeWrite{
		RuntimeID: runtimeID, AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(15 * time.Second),
	}
}

func assertTableCount(t *testing.T, database *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}
