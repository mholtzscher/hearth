// Package viewsv1 contains the generated, portable Hearth view contract.
package viewsv1

import _ "embed"

// Schema is generated from the frontend component catalog.
//
//go:embed view.schema.json
var Schema []byte

// Examples are shared frontend and backend conformance fixtures.
//
//go:embed examples.json
var Examples []byte
