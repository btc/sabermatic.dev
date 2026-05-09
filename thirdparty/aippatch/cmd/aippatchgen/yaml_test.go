package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadYaml_Basic(t *testing.T) {
	cfg, err := loadYaml(filepath.Join("testdata", "yaml_basic", "aippatch.yaml"))
	require.NoError(t, err)
	require.Len(t, cfg.Codecs, 1)
	require.Len(t, cfg.Resources, 1)
	r := cfg.Resources[0]
	require.Equal(t, "aippatch.fixture.v1.Widget", r.Message)
	require.Equal(t, "widgets", r.Table)
	require.Equal(t, "id", r.PK)
	require.Equal(t, []string{"name"}, r.Writable)
	require.Equal(t, "NOW()", r.AutoSet["updated_at"])
	require.Equal(t, "enum_color", r.Overrides["color"].Codec)
	require.Equal(t, "github.com/btc/drill/thirdparty/aippatch/internal/fixturepb", r.GoPackagePath)
}
