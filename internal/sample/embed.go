package sample

import "embed"

//go:embed fixtures/*.json
var fixtureFS embed.FS
