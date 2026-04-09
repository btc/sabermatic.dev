# Image Prompt: Devotional Undercurrent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a subtle devotional quality to image prompts — figures attend to their work with reverence, the act of creation carries weight. No overt religious imagery.

**Architecture:** Two edits in `internal/imagegen/prompt.go`: extend `promptSuffix` with two devotional sentences, change one word in `metaSystem`. Tests updated to assert both.

**Tech Stack:** Go, testify/assert.

**Spec:** `docs/superpowers/specs/2026-04-09-image-prompt-devotional-tone-design.md`

---

### Task 1: Update suffix, meta-system, and tests

**Files:**
- Modify: `internal/imagegen/prompt.go` — `promptSuffix` and `metaSystem` constants
- Modify: `internal/imagegen/prompt_test.go` — add assertions for both changes

- [ ] **Step 1: Add failing test assertions**

In `internal/imagegen/prompt_test.go`, update both test functions:

```go
func TestAssemblePrompt(t *testing.T) {
	fragment := "an abstract figure as a calm gatekeeper"
	got := AssemblePrompt(fragment)

	assert.Contains(t, got, "Cubist editorial illustration of")
	assert.Contains(t, got, fragment)
	assert.Contains(t, got, "true cubist refraction")
	assert.Contains(t, got, "Objects and forms are subject to the same refraction")
	assert.Contains(t, got, "sacred act of creation")
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
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
go test ./internal/imagegen/... -v
```

Expected: FAIL — `"sacred act of creation"` not found, `"consecrating a system design concept"` not found.

- [ ] **Step 3: Update `promptSuffix` in `prompt.go`**

Replace the existing `promptSuffix` constant. The only change is two new sentences inserted after `"...yet immediately legible. "` and before `"Bold dark outlines..."`:

```go
const promptSuffix = ". Figures seen through a true cubist refraction — faces reveal front and profile " +
	"at the same moment, an eye where the cheekbone sits, noses split across two viewpoints. " +
	"Bodies dismantled and reassembled across the picture plane: anatomically wrong in the " +
	"parts, compositionally right in the whole. " +
	"Objects and forms are subject to the same refraction — edges split, volumes seen " +
	"from contradictory angles simultaneously, shapes that are geometrically impossible " +
	"yet immediately legible. " +
	"Figures attend to their work with quiet reverence, as if performing a sacred act of creation. " +
	"Warmth radiates from the work itself. " +
	"Bold dark outlines on flat color planes. Warm palette: cream, amber, " +
	"terracotta, burnt orange with accents of cerulean blue and sage green. " +
	"Painterly brushwork. Playful and warm, Apple corporate illustration energy. " +
	"Edge-to-edge composition filling the entire canvas, no border, no margin, no frame. " +
	"No text. Horizontal 4:3 aspect ratio."
```

- [ ] **Step 4: Update `metaSystem` in `prompt.go`**

Replace `representing` with `consecrating`:

```go
const metaSystem = `You generate image prompt fragments for a cubist editorial illustration series. Each prompt describes a metaphorical scene with abstract figures consecrating a system design concept. Study the examples carefully — match their style, specificity, and structure. Output ONLY the prompt fragment, nothing else.`
```

- [ ] **Step 5: Run tests — verify they pass**

```bash
go test ./internal/imagegen/... -v
```

Expected: both `TestAssemblePrompt` and `TestMetaPrompt` PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/imagegen/prompt.go internal/imagegen/prompt_test.go
git commit -m "feat: add devotional undercurrent to image prompts"
```
