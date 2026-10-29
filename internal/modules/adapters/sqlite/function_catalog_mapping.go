package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

func functionCatalogFromRow(row dbsqlc.AdapterFunctionCatalog) (adapters.Catalog, error) {
	catalog := adapters.Catalog{
		ID:        adapters.CatalogID(row.CatalogRevision),
		AdapterID: row.AdapterID,
		RuntimeID: adapters.RuntimeID(row.RuntimeID),
	}
	if decodeFunctionJSON(row.FunctionsJson, &catalog.Functions) != nil {
		return adapters.Catalog{}, adapters.ErrFunctionPersistence
	}
	registered, err := parseTime(row.RegisteredAt)
	if err != nil || !adapters.ValidFunctionTime(registered) {
		return adapters.Catalog{}, adapters.ErrFunctionPersistence
	}
	prepared, err := adapters.PrepareCatalog(catalog)
	if err != nil {
		return adapters.Catalog{}, adapters.ErrFunctionPersistence
	}
	return prepared, nil
}
