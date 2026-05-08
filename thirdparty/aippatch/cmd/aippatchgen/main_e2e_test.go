package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun_FixtureRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		ConfigPath:    filepath.Join("testdata", "yaml_basic", "aippatch.yaml"),
		OutDir:        dir,
		ProtoPath:     filepath.Join("testdata", "proto_basic", "buf.binpb"),
		MigrationsDir: filepath.Join("..", "..", "internal", "fixturepb", "migrations"),
	}
	require.NoError(t, run(cfg))
	// Files exist and parse as Go.
	for _, fn := range []string{"widget.gen.go", "init.gen.go"} {
		body, err := os.ReadFile(filepath.Join(dir, fn))
		require.NoError(t, err)
		require.NoError(t, parseGoFile(string(body)))
	}

	// --check on identical inputs passes.
	cfg.Check = true
	require.NoError(t, run(cfg))

	// Mutate one file: --check fails.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "widget.gen.go"), []byte("// stale\n"), 0o644))
	require.Error(t, run(cfg))
}
