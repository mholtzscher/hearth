package db

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// This is the immutable v2 schema from the pre-expression migration base.
//
//go:embed automation-definition-v2.schema.json
var automationDefinitionV2Schema []byte

const automationExpressionsMigrationVersion = 23
const automationDefinitionV2MaxBytes = 66080

func migrateAutomationExpressions(ctx context.Context, tx *sql.Tx) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(automationDefinitionV2Schema))
	if err != nil {
		return errors.New("invalid embedded automation v2 schema")
	}
	compiler := jsonschema.NewCompiler()
	const schemaID = "urn:hearth:schema:automation-definition:v2"
	if err = compiler.AddResource(schemaID, document); err != nil {
		return errors.New("cannot load automation v2 schema")
	}
	schema, err := compiler.Compile(schemaID)
	if err != nil {
		return errors.New("cannot compile automation v2 schema")
	}
	rows, err := tx.QueryContext(ctx, `SELECT definition_json FROM automation_definition_revisions`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if len(raw) > automationDefinitionV2MaxBytes {
			return errors.New("invalid retained automation v2 definition")
		}
		value, decodeErr := jsonschema.UnmarshalJSON(strings.NewReader(raw))
		if decodeErr != nil || schema.Validate(value) != nil {
			return errors.New("invalid retained automation v2 definition")
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	script, err := migrationFiles.ReadFile("migrations/00023_automation_expressions.sql")
	if err != nil {
		return err
	}
	up, _, found := strings.Cut(string(script), "-- +goose Down")
	if !found {
		return errors.New("invalid automation expressions migration script")
	}
	_, err = tx.ExecContext(ctx, up)
	return err
}

func refuseAutomationExpressionsDown(context.Context, *sql.Tx) error {
	return errors.New(
		"restore the pre-upgrade backup with its matching binary; expressions have no lossless Down migration",
	)
}
