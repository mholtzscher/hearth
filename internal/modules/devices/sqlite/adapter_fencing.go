package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// Admission reads authoritative state inside the Devices transaction; projected
// availability is never a fencing credential.
func activeAdapterInstance(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	runtimeID adapters.RuntimeID,
) (dbsqlc.AdapterInstance, error) {
	instance, err := queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{AdapterID: adapterID})
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && (!instance.ActiveRuntimeID.Valid || instance.ActiveRuntimeID.String != string(runtimeID))) {
		return dbsqlc.AdapterInstance{}, adapters.ErrRuntimeFenced
	}
	if err != nil {
		return dbsqlc.AdapterInstance{}, fmt.Errorf("get active Adapter runtime: %w", err)
	}
	return instance, nil
}
