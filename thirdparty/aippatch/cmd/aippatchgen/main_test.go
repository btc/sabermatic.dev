package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseArgs_Defaults(t *testing.T) {
	cfg, err := parseArgs([]string{"aippatchgen"})
	require.NoError(t, err)
	require.Equal(t, "aippatch.yaml", cfg.ConfigPath)
	require.Equal(t, "internal/patches", cfg.OutDir)
	require.Equal(t, "buf.binpb", cfg.ProtoPath)
	require.Equal(t, "sql/migrations", cfg.MigrationsDir)
	require.False(t, cfg.Check)
}

func TestParseArgs_Check(t *testing.T) {
	cfg, err := parseArgs([]string{"aippatchgen", "--check", "--config", "alt.yaml"})
	require.NoError(t, err)
	require.True(t, cfg.Check)
	require.Equal(t, "alt.yaml", cfg.ConfigPath)
}

func TestParseArgs_UnknownFlag(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgsTo(&stderr, []string{"aippatchgen", "--garbage"})
	require.Error(t, err)
}
