# Image Prompt: Devotional Undercurrent

**Date:** 2026-04-09
**Status:** Approved

## Problem

Generated images depict figures performing mechanically shallow tasks — people using computers, sorting boxes, standing in queues. The scenes lack a sense of purpose or meaning beyond the literal. Designing a news feed should feel like a sacred act of creation, not a mundane office task.

## Goal

Add a subtle devotional quality. Figures attend to their work with reverence. The act of building a system carries weight and significance. No overtly religious imagery — no halos, altars, or worship poses. The shift is tonal: gesture, posture, and warmth of light.

## Changes

### A) Suffix — devotional tone

**File:** `internal/imagegen/prompt.go` — `promptSuffix` constant.

Add two sentences after the objects refraction sentence ("...yet immediately legible.") and before "Bold dark outlines...":

```
Figures attend to their work with quiet reverence, as if performing a sacred act of creation.
Warmth radiates from the work itself.
```

### B) Meta-system — consecrating

**File:** `internal/imagegen/prompt.go` — `metaSystem` constant.

Replace:
```
abstract figures representing a system design concept
```

With:
```
abstract figures consecrating a system design concept
```

Single word change. Shifts the tone of Sonnet-generated topic fragments toward intentionality and purpose.

## What Does Not Change

- `promptPrefix`
- `metaExamples` (three few-shot examples)
- Cubist dislocation language (already in suffix)
- Palette, brushwork, composition, aspect ratio

## Verification

Regenerate 2–3 question images and inspect for:

- Figures that lean into their work with visible attentiveness and care
- Gestures that feel intentional, almost ceremonial
- A quality of warmth or light emanating from the central activity
- No overt religious symbols (halos, crosses, altars, prayer poses)
- Topic fragments from Sonnet that carry more purposeful, elevated language

## Out of Scope

- Few-shot example rewrites
- Palette or composition changes
- Regenerating all existing images
