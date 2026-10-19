package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

var _ adapters.FunctionRepository = (*AdapterRepository)(nil)

func (repository *AdapterRepository) functionTransaction(ctx context.Context, work func(*dbsqlc.Queries) error) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = work(repository.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func (repository *AdapterRepository) RegisterCatalog(ctx context.Context, input adapters.Catalog) error {
	catalog, err := adapters.PrepareCatalog(input)
	if err != nil {
		return err
	}
	body, err := json.Marshal(catalog.Functions)
	if err != nil {
		return errors.Join(adapters.ErrFunctionPersistence, err)
	}
	return repository.functionTransaction(ctx, func(queries *dbsqlc.Queries) error {
		_, err = queries.FunctionRuntimeActive(
			ctx,
			dbsqlc.FunctionRuntimeActiveParams{AdapterID: catalog.AdapterID, RuntimeID: string(catalog.RuntimeID)},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return adapters.ErrRuntimeFenced
		}
		if err != nil {
			return err
		}
		owner, ownerErr := queries.GetFunctionCatalogOwner(
			ctx,
			dbsqlc.GetFunctionCatalogOwnerParams{CatalogRevision: string(catalog.ID)},
		)
		if ownerErr == nil && (owner.AdapterID != catalog.AdapterID || owner.RuntimeID != string(catalog.RuntimeID)) {
			return adapters.ErrCatalogConflict
		}
		if ownerErr != nil && !errors.Is(ownerErr, sql.ErrNoRows) {
			return ownerErr
		}
		unchanged, readErr := sameRuntimeFunctionCatalog(ctx, queries, catalog, body)
		if readErr != nil || unchanged {
			return readErr
		}
		return queries.UpsertFunctionCatalog(ctx, dbsqlc.UpsertFunctionCatalogParams{
			AdapterID: catalog.AdapterID,
			CatalogRevision: string(
				catalog.ID,
			),
			RuntimeID:     string(catalog.RuntimeID),
			RegisteredAt:  functionTimestamp(time.Now().UTC()),
			FunctionsJson: string(body),
		})
	})
}

func sameRuntimeFunctionCatalog(
	ctx context.Context,
	queries *dbsqlc.Queries,
	catalog adapters.Catalog,
	body []byte,
) (bool, error) {
	stored, err := queries.GetFunctionCatalog(ctx, dbsqlc.GetFunctionCatalogParams{AdapterID: catalog.AdapterID})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stored.RuntimeID != string(catalog.RuntimeID) {
		return false, nil
	}
	retained, err := decodeFunctionCatalog(stored)
	if err != nil {
		return false, err
	}
	encoded, _ := json.Marshal(retained.Functions)
	if retained.ID != catalog.ID || !bytes.Equal(encoded, body) {
		return false, adapters.ErrCatalogConflict
	}
	return true, nil
}

func (repository *AdapterRepository) ListFunctions(
	ctx context.Context,
	adapterID string,
	page adapters.PageInput,
) (adapters.FunctionPage, error) {
	if adapters.ValidateAdapterID(adapterID) != nil || adapters.ValidateFunctionPage(page) != nil {
		return adapters.FunctionPage{}, adapters.ErrInvalidFunctionInput
	}
	revision, after, err := adapters.DecodeFunctionCursor(adapterID, page.Cursor)
	if err != nil {
		return adapters.FunctionPage{}, err
	}
	limit, _ := adapters.FunctionPageLimit(page.Limit)
	result := adapters.FunctionPage{Items: make([]adapters.Function, 0)}
	err = repository.functionTransaction(ctx, func(queries *dbsqlc.Queries) error {
		catalog, present, readErr := loadFunctionCatalog(ctx, queries, adapterID)
		if readErr != nil {
			return readErr
		}
		if revision != "" && (!present || revision != catalog.ID) {
			return adapters.ErrCatalogChanged
		}
		if !present {
			return nil
		}
		callable, readErr := functionRuntimeActive(ctx, queries, catalog)
		if readErr != nil {
			return readErr
		}
		result = mapFunctionPage(catalog, callable, after, limit)
		return nil
	})
	return result, err
}

func mapFunctionPage(
	catalog adapters.Catalog,
	callable bool,
	after adapters.FunctionKey,
	limit int,
) adapters.FunctionPage {
	result := adapters.FunctionPage{Items: make([]adapters.Function, 0)}
	for _, descriptor := range catalog.Functions {
		if descriptor.Key <= after {
			continue
		}
		if len(result.Items) == limit {
			result.NextCursor = adapters.EncodeFunctionCursor(
				catalog.AdapterID,
				catalog.ID,
				result.Items[len(result.Items)-1].Descriptor.Key,
			)
			break
		}
		result.Items = append(
			result.Items,
			adapters.Function{
				AdapterID:       catalog.AdapterID,
				CatalogRevision: catalog.ID,
				Descriptor:      descriptor,
				Callable:        callable,
			},
		)
	}
	return result
}

func (repository *AdapterRepository) GetFunction(
	ctx context.Context,
	adapterID string,
	key adapters.FunctionKey,
) (adapters.Function, error) {
	if adapters.ValidateAdapterID(adapterID) != nil || !adapters.ValidFunctionKey(key) {
		return adapters.Function{}, adapters.ErrInvalidFunctionInput
	}
	var result adapters.Function
	err := repository.functionTransaction(ctx, func(queries *dbsqlc.Queries) error {
		catalog, present, readErr := loadFunctionCatalog(ctx, queries, adapterID)
		if readErr != nil {
			return readErr
		}
		if !present {
			return adapters.ErrFunctionNotFound
		}
		callable, readErr := functionRuntimeActive(ctx, queries, catalog)
		if readErr != nil {
			return readErr
		}
		for _, descriptor := range catalog.Functions {
			if descriptor.Key == key {
				result = adapters.Function{
					AdapterID:       adapterID,
					CatalogRevision: catalog.ID,
					Descriptor:      descriptor,
					Callable:        callable,
				}
				return nil
			}
		}
		return adapters.ErrFunctionNotFound
	})
	return result, err
}

func loadFunctionCatalog(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
) (adapters.Catalog, bool, error) {
	if err := functionAdapterExists(ctx, queries, adapterID); err != nil {
		return adapters.Catalog{}, false, err
	}
	row, err := queries.GetFunctionCatalog(ctx, dbsqlc.GetFunctionCatalogParams{AdapterID: adapterID})
	if errors.Is(err, sql.ErrNoRows) {
		return adapters.Catalog{}, false, nil
	}
	if err != nil {
		return adapters.Catalog{}, false, err
	}
	catalog, err := decodeFunctionCatalog(row)
	return catalog, true, err
}

func decodeFunctionCatalog(row dbsqlc.AdapterFunctionCatalog) (adapters.Catalog, error) {
	catalog := adapters.Catalog{
		ID:        adapters.CatalogID(row.CatalogRevision),
		AdapterID: row.AdapterID,
		RuntimeID: adapters.RuntimeID(row.RuntimeID),
	}
	if decodeFunctionJSON(row.FunctionsJson, &catalog.Functions) != nil {
		return adapters.Catalog{}, adapters.ErrFunctionPersistence
	}
	registered, err := time.Parse(time.RFC3339Nano, row.RegisteredAt)
	if err != nil || !adapters.ValidFunctionTime(registered) {
		return adapters.Catalog{}, adapters.ErrFunctionPersistence
	}
	prepared, err := adapters.PrepareCatalog(catalog)
	if err != nil {
		return adapters.Catalog{}, adapters.ErrFunctionPersistence
	}
	return prepared, nil
}

func functionAdapterExists(ctx context.Context, queries *dbsqlc.Queries, adapterID string) error {
	_, err := queries.GetAdapterInstance(ctx, dbsqlc.GetAdapterInstanceParams{AdapterID: adapterID})
	if errors.Is(err, sql.ErrNoRows) {
		return adapters.ErrAdapterNotFound
	}
	return err
}

func functionRuntimeActive(ctx context.Context, queries *dbsqlc.Queries, catalog adapters.Catalog) (bool, error) {
	_, err := queries.FunctionRuntimeActive(
		ctx,
		dbsqlc.FunctionRuntimeActiveParams{AdapterID: catalog.AdapterID, RuntimeID: string(catalog.RuntimeID)},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func decodeFunctionJSON(raw string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return adapters.ErrFunctionPersistence
	}
	return nil
}

func functionTimestamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000000Z") }
