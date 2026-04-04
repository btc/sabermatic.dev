package sample

import "embed"

//go:embed fixtures/*.json
var FixtureFS embed.FS
