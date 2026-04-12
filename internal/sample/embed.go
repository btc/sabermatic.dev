package sample

import "embed"

// Fixture data extracted from v0 prototype, session 27 ("Chat System").
// Text-only — no audio files (all audio_url fields are null).
// To regenerate: run a session in v0, export via extract-sample script.
//
//go:embed fixtures/*.json
var fixtureFS embed.FS

// FixtureFS returns the embedded fixture filesystem.
func FixtureFS() embed.FS {
	return fixtureFS
}
