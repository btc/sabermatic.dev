# Image Prompt: Cubist Dislocation Tweak

**Date:** 2026-04-09
**Status:** Approved

## Problem

Generated images read as "cubism as surface texture" — the geometry is applied on top of anatomically correct, readable figures. Faces are forward-facing and coherent. Limbs connect where anatomy expects. The model interprets "in the style of Picasso" and "geometric planes seen from multiple simultaneous perspectives" as an angular illustration style rather than genuine analytical cubism.

The result is warm and well-coloured but vanilla: no refracted simultaneity of viewpoints, no displaced features, no uncanny dislocation in the parts.

## Goal

Push generated images toward authentic Picasso-style analytical cubism — figures that feel "exploded and reassembled as if refracted." Right in overall impression, deliberately wrong in anatomical detail. The palette, brushwork, and compositional rules that are working stay unchanged.

## Approach

Option A: strengthen the constant suffix only. The suffix currently says nothing about multi-viewpoint dislocation. Adding explicit language there affects every generated image uniformly without touching the meta-prompt few-shot examples.

## Change

**File:** `internal/imagegen/prompt.go` — `promptSuffix` constant.

**Before:**
```
". Bold dark outlines on flat color planes. Warm palette: cream, amber, " +
    "terracotta, burnt orange with accents of cerulean blue and sage green. " +
    "Painterly brushwork. Playful and warm, Apple corporate illustration energy. " +
    "Edge-to-edge composition filling the entire canvas, no border, no margin, no frame. " +
    "No text. Horizontal 4:3 aspect ratio."
```

**After:**
```
". Figures seen through a true cubist refraction — faces reveal front and profile " +
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

The dislocation language leads the suffix so it is weighted early in the prompt. Everything else is unchanged.

## What Does Not Change

- `promptPrefix`
- `metaSystem` (meta-prompt system message)
- `metaExamples` (three few-shot Rate Limiter / News Feed / Chat System fragments)
- All other suffix language (palette, brushwork, composition, aspect ratio)

## Verification

Manually null `image_url` on 2–3 questions with varied topic fragments and let the sweep re-queue them. Inspect regen output for:

- Faces showing front and profile simultaneously
- Features (eyes, noses) displaced from anatomically expected positions
- Figures that read as recognisably human overall but are genuinely disjointed in the parts
- Objects and forms that share the same refraction — edges split, contradictory angles, impossible geometry that still reads clearly

If results are still too naturalistic, escalate to Option B: rewrite the meta-prompt few-shot examples to lead with dislocation language so Sonnet-generated topic fragments carry the same energy.

## Out of Scope

- Meta-prompt changes (Option B) — deferred until output is evaluated
- Regenerating existing question images — user will selectively null and regen manually
- Palette, composition, or aspect ratio changes
