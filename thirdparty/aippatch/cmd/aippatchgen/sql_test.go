package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadSchema_CreateTable(t *testing.T) {
	schema, err := loadSchema(filepath.Join("testdata", "sql_basic"))
	require.NoError(t, err)
	tbl, ok := schema.Table("widgets")
	require.True(t, ok)
	col, ok := tbl.Column("name")
	require.True(t, ok)
	require.Equal(t, "text", col.Type)
	require.True(t, col.NotNull)

	idCol, _ := tbl.Column("id")
	require.True(t, idCol.NotNull, "PRIMARY KEY implies NOT NULL")
}

func TestLoadSchema_AlterTableAddColumn(t *testing.T) {
	schema, err := loadSchema(filepath.Join("testdata", "sql_alter"))
	require.NoError(t, err)
	tbl, _ := schema.Table("widgets")
	col, ok := tbl.Column("notes")
	require.True(t, ok)
	require.Equal(t, "text", col.Type)
}

func TestLoadSchema_RenameColumn(t *testing.T) {
	schema, err := loadSchema(filepath.Join("testdata", "sql_rename"))
	require.NoError(t, err)
	tbl, _ := schema.Table("widgets")
	_, oldStillThere := tbl.Column("name")
	require.False(t, oldStillThere, "old column name should be gone after RENAME")
	col, ok := tbl.Column("display_name")
	require.True(t, ok, "renamed column should be findable under new name")
	require.Equal(t, "text", col.Type)
	require.True(t, col.NotNull, "constraints survive rename")
}
