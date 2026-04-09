# Image Prompt: Cubist Dislocation Suffix Tweak Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add analytical-cubism dislocation language to the image generation prompt suffix so Gemini produces figures with genuine multi-viewpoint fragmentation rather than geometry-textured naturalism.

**Architecture:** Single constant edit in `internal/imagegen/prompt.go`. The new sentences lead the suffix so they are weighted early in the prompt. Test file updated to assert the new language is present.

**Tech Stack:** Go, testify/assert.

**Spec:** `docs/superpowers/specs/2026-04-09-image-prompt-cubist-dislocation-design.md`

---

### Task 1: Update suffix constant and test

**Files:**
- Modify: `internal/imagegen/prompt.go` — `promptSuffix` constant
- Modify: `internal/imagegen/prompt_test.go` — add assertion for new language

- [ ] **Step 1: Add assertion for the new dislocation language to the test first**

Open `internal/imagegen/prompt_test.go` and add one assertion to `TestAssemblePrompt`:

```go
func TestAssemblePrompt(t *testing.T) {
	fragment := "an abstract figure as a calm gatekeeper"
	got := AssemblePrompt(fragment)

	assert.Contains(t, got, "Cubist editorial illustration of")
	assert.Contains(t, got, fragment)
	assert.Contains(t, got, "true cubist refraction")
	assert.Contains(t, got, "Objects and forms are subject to the same refraction")
	assert.Contains(t, got, "Edge-to-edge composition")
	assert.Contains(t, got, "No text")
}
```

- [ ] **Step 2: Run the test — verify it fails**

```bash
go test ./internal/imagegen/... -run TestAssemblePrompt -v
```

Expected: FAIL — `"true cubist refraction" not found in string`.

- [ ] **Step 3: Update `promptSuffix` in `prompt.go`**

Replace the existing `promptSuffix` constant:

```go
const promptSuffix = ". Figures seen through a true cubist refraction — faces reveal front and profile " +
	"at the same moment, an eye where the cheekbone sits, noses split across two viewpoints. " +
	"Bodies dismantled and reassembled across the picture plane: anatomically wrong in the " +
	"parts, compositionally right in the whole. " +
	"Objects and forms are subject to the same refraction — edges split, volumes seen " +
	"from contradictory angles simultaneously, shapes that are geometrically impossible " +
	"yet immediately legible. " +
	"Bold dark outlines on flat color planes. Warm palette: cream, amber, " +
	"terracotta, burnt orange with accents of cerulean blue and sage green. " +
	"Painterly brushwork. Playful and warm, Apple corporate illustration energy. " +
	"Edge-to-edge composition filling the entire canvas, no border, no margin, no frame. " +
	"No text. Horizontal 4:3 aspect ratio."
```

- [ ] **Step 4: Run the test — verify it passes**

```bash
go test ./internal/imagegen/... -v
```

Expected: PASS — both `TestAssemblePrompt` and `TestMetaPrompt` green.

- [ ] **Step 5: Commit**

```bash
git add internal/imagegen/prompt.go internal/imagegen/prompt_test.go
git commit -m "feat: add cubist dislocation language to image prompt suffix"
```
