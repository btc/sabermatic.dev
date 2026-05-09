package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadProto_Basic(t *testing.T) {
	files, err := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	require.NoError(t, err)
	desc, err := lookupMessage(files, "aippatch.fixture.v1.Widget")
	require.NoError(t, err)
	require.Equal(t, "Widget", string(desc.Name()))
	require.NotNil(t, desc.Fields().ByName("name"))
}
