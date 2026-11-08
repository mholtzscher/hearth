package dbload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func schemaIdentity(ctx context.Context, db *sql.DB) (string, int64, error) {
	rows, err := db.QueryContext(
		ctx,
		"SELECT type,name,COALESCE(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name",
	)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	var canonical []byte
	for rows.Next() {
		var kind, name, text string
		if err = rows.Scan(&kind, &name, &text); err != nil {
			break
		}
		canonical = append(canonical, kind+"\x00"+name+"\x00"+text+"\x00"...)
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return "", 0, err
	}
	var version int64
	if err = db.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied=1").
		Scan(&version); err != nil {
		return "", 0, err
	}
	return hashBytes(canonical), version, nil
}

// PrepareVariant migrates and checks one image once, outside all measured blocks.
// A strict baseline must exactly match the binary's empty migrated schema.
func PrepareVariant(ctx context.Context, cfg Config, strictBaseline bool) (Manifest, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return Manifest{}, err
	}
	m, err := ReadManifest(cfg.Fixture)
	if err != nil {
		return m, err
	}
	if err = createOwnedDirectory(cfg.OutputDir); err != nil {
		return m, err
	}
	if strictBaseline {
		if err = verifyBaselineSchema(ctx, cfg, m); err != nil {
			return m, err
		}
	}
	path := filepath.Join(cfg.OutputDir, fixtureDatabaseName)
	if err = cloneFile(m.Database, path); err != nil {
		return m, err
	}
	database, err := platformdb.Open(ctx, path)
	if err != nil {
		return m, err
	}
	defer database.Close()
	if err = platformdb.Migrate(ctx, database); err != nil {
		return m, err
	}
	logical, err := logicalFixtureHash(ctx, database)
	if err != nil {
		return m, err
	}
	if logical != m.FixtureHash {
		return m, errors.New("variant migration changed logical fixture data; comparison refused")
	}
	m.RowCounts, err = fixtureRowCounts(ctx, database)
	if err != nil {
		return m, err
	}
	m.SchemaHash, m.MigrationVersion, err = schemaIdentity(ctx, database)
	if err != nil {
		return m, err
	}
	if err = closeFixtureDatabase(ctx, database); err != nil {
		return m, err
	}
	m.Database = fixtureDatabaseName
	m.PhysicalHash, err = fileHash(path)
	if err != nil {
		return m, err
	}
	if err = WriteJSON(filepath.Join(cfg.OutputDir, fixtureManifestName), m); err != nil {
		return m, err
	}
	return m, nil
}

func verifyBaselineSchema(ctx context.Context, cfg Config, m Manifest) error {
	path := filepath.Join(cfg.OutputDir, "expected-baseline-schema.db")
	db, err := platformdb.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(); removeRunDatabase(path) }()
	if err = platformdb.Migrate(ctx, db); err != nil {
		return err
	}
	expected, version, err := schemaIdentity(ctx, db)
	if err != nil {
		return err
	}
	if expected != m.SchemaHash || version != m.MigrationVersion {
		return fmt.Errorf(
			"canonical fixture is not the frozen baseline schema (fixture migration %d; baseline %d)",
			m.MigrationVersion,
			version,
		)
	}
	return nil
}

func sourceWithin(root, path string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !startsOutside(rel)
}

func startsOutside(path string) bool {
	return len(path) > 3 && path[:3] == ".."+string(os.PathSeparator)
}
