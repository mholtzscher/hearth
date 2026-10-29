package sqlite

import "database/sql"

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
