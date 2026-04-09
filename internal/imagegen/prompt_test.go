package imagegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssemblePrompt(t *testing.T) {
	fragment := "an abstract figure as a calm gatekeeper"
	got := AssemblePrompt(fragment)

	assert.Contains(t, got, "Cubist editorial illustration of")
	assert.Contains(t, got, fragment)
	assert.Contains(t, got, "true cubist refraction")
	assert.Contains(t, got, "Objects and forms are subject to the same refraction")
	assert.Contains(t, got, "sacred act of creation")
	assert.Contains(t, got, "Warmth radiates from the work itself")
	assert.Contains(t, got, "Edge-to-edge composition")
	assert.Contains(t, got, "No text")
}

func TestMetaPrompt(t *testing.T) {
	got := MetaPrompt("Rate Limiter")

	assert.Contains(t, got.System, "cubist editorial illustration series")
	assert.Contains(t, got.System, "consecrating a system design concept")
	assert.Contains(t, got.User, "Rate Limiter")
	assert.Contains(t, got.User, "News Feed")
	assert.Contains(t, got.User, "Chat System")
	assert.Contains(t, got.User, "Now generate a prompt fragment for")
}
