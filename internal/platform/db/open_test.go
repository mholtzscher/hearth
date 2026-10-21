package db_test

import (
	"context"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/db"
)

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := db.Open(context.Background(), " "); err == nil {
		t.Fatal("Open accepted an empty path")
	}
}
