# Baseball Mark Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate the existing Sabermatic baseball mark across web (inline as the `.` in `[.DEV]`), email (PNG adjacent to wordmark), and OG social-share images.

**Architecture:** One new `<BallMark/>` primitive (wrapper span owning `inline-block` + `translate-y-[0.05em]`, inner `<svg>` owning em-relative sizing + caller className) feeds every web surface through an updated `<BrandName/>` plus a direct embed in the hero. Backend adds `LogoURL` to the email template context so the wrapper shows a PNG mark when a URL is supplied.

**Tech Stack:** React 19, TypeScript, Vitest + @testing-library/react, Tailwind (arbitrary-value utilities), Go, `html/template`, `rsvg-convert` for PNG regeneration.

**Spec reference:** `docs/superpowers/specs/2026-04-22-baseball-mark-integration-design.md`

---

## File Structure

**New files:**
- `web/src/components/ball-mark.tsx` — the inline SVG primitive
- `web/src/__tests__/ball-mark.test.tsx` — primitive tests
- `web/src/__tests__/brand-name.test.tsx` — BrandName tests (currently untested)
- `web/src/pages/landing/__tests__/hero.test.tsx` — hero tests (currently untested)

**Modified files:**
- `web/src/components/brand-name.tsx` — embed `<BallMark/>`, add `role="img"` + `aria-label`
- `web/src/pages/landing/hero.tsx` — inline `<BallMark/>` directly; add `group` + `aria-label`
- `internal/email/template.go` — extend `TemplateData` with `LogoURL`; add third arg to `RenderEmail`
- `internal/email/templates/wrapper.html` — conditional `<img>` + table-in-table header
- `internal/email/template_test.go` — 3-arg signature + logo-specific assertions
- `internal/backend/auth.go` — pass logoURL to both `RenderEmail` calls (lines 168, 323)
- `internal/jobs/evaluate.go` — derive logoURL inside `renderEvaluationEmail` from existing `baseURL` param
- `internal/jobs/render_email_test.go` — assert rendered HTML contains `"https://example.com/mark-256.png"`
- `internal/handler/spa.go` — extend Sprintf with Twitter Card tags
- `internal/handler/spa_test.go` — assert Twitter Card tags injected before `</head>`
- `web/public/og-landing.svg` — rewrite with B-treatment layout
- `web/public/og-landing.png` — regenerate via rsvg-convert
- `web/public/og-sample.svg` — same layout, tagline differs
- `web/public/og-sample.png` — regenerate via rsvg-convert
- `Makefile` — add `regen-og` target

---

## Task 1: Add `<BallMark/>` primitive

**Files:**
- Create: `web/src/components/ball-mark.tsx`
- Create: `web/src/__tests__/ball-mark.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/__tests__/ball-mark.test.tsx`:

```tsx
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { BallMark } from "@/components/ball-mark";

describe("BallMark", () => {
  it("renders inline SVG with aria-hidden and viewBox", () => {
    const { container } = render(<BallMark />);
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 32 32");
  });

  it("renders ball geometry (circle + two seam paths + 14 tick lines)", () => {
    const { container } = render(<BallMark />);
    const svg = container.querySelector("svg")!;
    expect(svg.querySelectorAll("circle")).toHaveLength(1);
    expect(svg.querySelectorAll("path")).toHaveLength(2);
    expect(svg.querySelectorAll("line")).toHaveLength(14);
  });

  it("wraps the svg in an inline-block span with baseline translateY", () => {
    const { container } = render(<BallMark />);
    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.tagName).toBe("SPAN");
    expect(wrapper.className).toContain("inline-block");
    expect(wrapper.className).toContain("translate-y-[0.05em]");
  });

  it("applies caller className to the inner svg", () => {
    const { container } = render(<BallMark className="group-hover:animate-spin" />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("class")).toContain("group-hover:animate-spin");
  });

  it("keeps em-relative sizing on the inner svg", () => {
    const { container } = render(<BallMark />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("class")).toContain("h-[0.55em]");
    expect(svg.getAttribute("class")).toContain("w-[0.55em]");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/__tests__/ball-mark.test.tsx`
Expected: FAIL with "Cannot find module '@/components/ball-mark'".

- [ ] **Step 3: Write the primitive**

Create `web/src/components/ball-mark.tsx`:

```tsx
import { cn } from "@/lib/utils";

interface BallMarkProps {
  /** Additional Tailwind classes applied to the inner <svg>.
   *  Use for hover animation (e.g. "group-hover:animate-spin") or
   *  per-surface color overrides. Do not pass transform utilities —
   *  they will conflict with animate-spin on the same element (both
   *  write to `transform`, and CSS animation takes precedence).
   *  Wrapper positioning is not externally overridable by design;
   *  if a surface needs a different baseline, prefer a new variant. */
  className?: string;
}

export function BallMark({ className }: BallMarkProps) {
  return (
    <span className="inline-block translate-y-[0.05em]">
      <svg
        aria-hidden="true"
        viewBox="0 0 32 32"
        className={cn("h-[0.55em] w-[0.55em]", className)}
      >
        <g transform="rotate(-28 16 16)" strokeLinejoin="round" fill="none">
          <circle cx="16" cy="16" r="12" fill="#fef3c7" stroke="#92400e" strokeWidth="1.3" />
          <g stroke="#b45309" strokeWidth="1" strokeLinecap="butt">
            <path d="M 5.61 10 Q 14 17 5.61 22" />
            <path d="M 26.39 10 Q 14 17 26.39 22" />
          </g>
          <g stroke="#92400e" strokeWidth="0.38" strokeLinecap="round">
            <line x1="8.97" y1="12.20" x2="7.61" y2="13.24" />
            <line x1="9.88" y1="13.63" x2="8.38" y2="14.42" />
            <line x1="10.46" y1="15.06" x2="8.82" y2="15.50" />
            <line x1="10.65" y1="16.50" x2="8.95" y2="16.50" />
            <line x1="10.46" y1="17.92" x2="8.82" y2="17.44" />
            <line x1="9.86" y1="19.26" x2="8.40" y2="18.38" />
            <line x1="8.91" y1="20.50" x2="7.67" y2="19.34" />
            <line x1="22.99" y1="13.36" x2="21.86" y2="12.08" />
            <line x1="21.86" y1="14.54" x2="20.52" y2="13.50" />
            <line x1="21.23" y1="15.60" x2="19.65" y2="14.96" />
            <line x1="21.05" y1="16.50" x2="19.35" y2="16.50" />
            <line x1="21.22" y1="17.35" x2="19.66" y2="18.01" />
            <line x1="21.83" y1="18.26" x2="20.55" y2="19.38" />
            <line x1="22.93" y1="19.23" x2="21.93" y2="20.61" />
          </g>
        </g>
      </svg>
    </span>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/__tests__/ball-mark.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 5: Run typecheck**

Run: `cd web && npx tsc -b`
Expected: No errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/ball-mark.tsx web/src/__tests__/ball-mark.test.tsx
git commit -m "feat(web): add BallMark inline SVG primitive"
```

---

## Task 2: Update `<BrandName/>` to use `<BallMark/>`

**Files:**
- Modify: `web/src/components/brand-name.tsx`
- Create: `web/src/__tests__/brand-name.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/__tests__/brand-name.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { BrandName } from "@/components/brand-name";

describe("BrandName", () => {
  it("renders an accessible img with the canonical label", () => {
    render(<BrandName />);
    const name = screen.getByRole("img", { name: "Sabermatic dot DEV" });
    expect(name).toBeInTheDocument();
  });

  it("renders the visible wordmark text", () => {
    const { container } = render(<BrandName />);
    const text = container.textContent ?? "";
    expect(text).toContain("Sabermatic");
    expect(text).toContain("[DEV]");
  });

  it("marks the styled inner span aria-hidden to avoid double-reading", () => {
    const { container } = render(<BrandName />);
    const outer = container.firstChild as HTMLElement;
    const inner = outer.querySelector("span");
    expect(inner).not.toBeNull();
    expect(inner?.getAttribute("aria-hidden")).toBe("true");
  });

  it("embeds the BallMark primitive inside the inner styled span", () => {
    const { container } = render(<BrandName />);
    const inner = container.querySelector("span span");
    const svg = inner?.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
  });

  it("forwards caller className to the outer span", () => {
    const { container } = render(<BrandName className="text-red-500" />);
    const outer = container.firstChild as HTMLElement;
    expect(outer.className).toContain("text-red-500");
    expect(outer.className).toContain("whitespace-nowrap");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/__tests__/brand-name.test.tsx`
Expected: FAIL — no `role="img"` on current markup.

- [ ] **Step 3: Update the component**

Replace the entire contents of `web/src/components/brand-name.tsx` with:

```tsx
import { BallMark } from "@/components/ball-mark";
import { cn } from "@/lib/utils";

interface BrandNameProps {
  className?: string;
}

export function BrandName({ className }: BrandNameProps) {
  return (
    <span
      role="img"
      aria-label="Sabermatic dot DEV"
      className={cn("whitespace-nowrap", className)}
    >
      Sabermatic
      <span className="opacity-60" aria-hidden="true">
        [<BallMark />DEV]
      </span>
    </span>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/__tests__/brand-name.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 5: Run full frontend test suite (BrandName is used in many places — catches regressions)**

Run: `cd web && npx vitest run`
Expected: PASS across all suites.

- [ ] **Step 6: Typecheck**

Run: `cd web && npx tsc -b`
Expected: No errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/brand-name.tsx web/src/__tests__/brand-name.test.tsx
git commit -m "feat(web): BrandName embeds BallMark with role=img + aria-label"
```

---

## Task 3: Hero inlines `<BallMark/>` directly

**Files:**
- Modify: `web/src/pages/landing/hero.tsx:50-55`
- Create: `web/src/pages/landing/__tests__/hero.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/landing/__tests__/hero.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Hero } from "@/pages/landing/hero";

describe("Hero", () => {
  it("gives the <h1> a canonical aria-label", () => {
    render(<Hero />);
    const heading = screen.getByRole("heading", { level: 1 });
    expect(heading.getAttribute("aria-label")).toBe("Sabermatic dot DEV");
  });

  it("embeds the BallMark SVG as a descendant of the <h1>", () => {
    render(<Hero />);
    const heading = screen.getByRole("heading", { level: 1 });
    const svg = heading.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 32 32");
  });

  it("does NOT wrap the wordmark in <BrandName/> (guards against accidental refactor)", () => {
    render(<Hero />);
    const heading = screen.getByRole("heading", { level: 1 });
    // BrandName introduces a role="img" span inside its wordmark region.
    expect(heading.querySelector('[role="img"]')).toBeNull();
  });

  it("keeps the hero's lowercase casing and bracketed opacity treatment", () => {
    render(<Hero />);
    const heading = screen.getByRole("heading", { level: 1 });
    const text = heading.textContent ?? "";
    expect(text).toContain("sabermatic"); // lowercase, deliberately
    const dimSpan = heading.querySelector('span[aria-hidden="true"]');
    expect(dimSpan?.className).toContain("opacity-40");
  });

  it("adds the group class to <h1> to enable group-hover animation on the ball", () => {
    render(<Hero />);
    const heading = screen.getByRole("heading", { level: 1 });
    expect(heading.className.split(/\s+/)).toContain("group");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/pages/landing/__tests__/hero.test.tsx`
Expected: FAIL — current `<h1>` has no `aria-label`, no embedded SVG, no `group` class.

- [ ] **Step 3: Update the hero**

In `web/src/pages/landing/hero.tsx`, update the import block to add:

```tsx
import { BallMark } from "@/components/ball-mark";
```

Then replace the `<h1>...</h1>` block (lines 50-55) with:

```tsx
<h1
  id="hero-heading"
  className="group mb-9 text-[clamp(56px,10vw,140px)] font-extrabold leading-[0.9] tracking-[-0.055em]"
  aria-label="Sabermatic dot DEV"
>
  sabermatic<span className="font-bold tracking-[-0.03em] opacity-40" aria-hidden="true">[<BallMark className="group-hover:animate-[spin_2s_linear_infinite] motion-reduce:animate-none" />DEV]</span>
</h1>
```

- [ ] **Step 4: Run hero test to verify it passes**

Run: `cd web && npx vitest run src/pages/landing/__tests__/hero.test.tsx`
Expected: PASS (5 tests).

- [ ] **Step 5: Run full frontend suite**

Run: `cd web && npx vitest run`
Expected: PASS.

- [ ] **Step 6: Typecheck**

Run: `cd web && npx tsc -b`
Expected: No errors.

- [ ] **Step 7: Manual visual check (golden path)**

Run: `make dev` in one terminal. Open `http://localhost:8080/` in a browser. Confirm:
- Nav bar (top): `Sabermatic` wordmark has a small amber baseball between `[` and `DEV]`
- Hero: giant `sabermatic[⚾DEV]` wordmark — ball sits between the brackets, scales with the type
- Hover over the hero `<h1>`: ball slowly rotates (2s spin). Release hover: rotation stops cleanly.
- Zoom browser to 75% / 100% / 150%: ball scales correctly with font-size at every zoom.

If the ball's vertical position looks off at 14px (nav) or 140px (hero), fine-tune `translate-y-[0.05em]` in `ball-mark.tsx` — try `translate-y-[0.03em]`, `translate-y-[0.07em]` until both nav and hero read correctly. Commit screenshots to the PR at 14px, 40px (auth layout), and 140px (hero).

- [ ] **Step 8: Commit**

```bash
git add web/src/pages/landing/hero.tsx web/src/pages/landing/__tests__/hero.test.tsx
git commit -m "feat(web): hero embeds BallMark inline in the wordmark"
```

---

## Task 4: Extend `email.TemplateData` + `RenderEmail` signature

**Files:**
- Modify: `internal/email/template.go`
- Modify: `internal/email/template_test.go`

- [ ] **Step 1: Update the template-level test first (it will break compilation until the code is updated, which is exactly what we want)**

Replace the existing `TestRenderEmail` in `internal/email/template_test.go` with:

```go
func TestRenderEmail(t *testing.T) {
	const logoURL = "https://example.com/mark-256.png"
	html, err := RenderEmail(template.HTML("<p>Hello world</p>"), "Test footer", logoURL)
	require.NoError(t, err)
	require.Contains(t, html, "Sabermatic[.DEV]")
	require.Contains(t, html, "Hello world")
	require.Contains(t, html, "Test footer")
	require.Contains(t, html, "#fffbf5") // warm background
}
```

- [ ] **Step 2: Run test to verify it fails (compile error)**

Run: `go test ./internal/email/... -run TestRenderEmail -count=1`
Expected: FAIL with compile error "too many arguments in call to RenderEmail".

- [ ] **Step 3: Update `TemplateData` + `RenderEmail` signature**

In `internal/email/template.go`, change the `TemplateData` struct and the `RenderEmail` function:

```go
// TemplateData holds the data for the shared email wrapper template.
type TemplateData struct {
	AppName string
	// LogoURL is an absolute URL to a publicly-reachable mark PNG.
	// When empty, the wrapper renders without a logo image.
	LogoURL string
	Body    template.HTML // Pre-rendered inner HTML
	Footer  string
}

// RenderEmail renders the shared email wrapper with the given body HTML and footer text.
// logoURL should be an absolute URL to a publicly-reachable mark PNG, or "" to render
// without a logo (tests, or environments where the asset is unavailable).
func RenderEmail(bodyHTML template.HTML, footer string, logoURL string) (string, error) {
	tmpl, err := getTemplate()
	if err != nil {
		return "", fmt.Errorf("parse email template: %w", err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, TemplateData{
		AppName: branding.AppName,
		LogoURL: logoURL,
		Body:    bodyHTML,
		Footer:  footer,
	})
	if err != nil {
		return "", fmt.Errorf("render email template: %w", err)
	}
	return buf.String(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/email/... -run TestRenderEmail -count=1`
Expected: PASS.

- [ ] **Step 5: Build check (callers will now fail to compile — we fix them in Tasks 6–7)**

Run: `go build ./internal/email/...`
Expected: PASS (the email package itself compiles).

Run: `go build ./...`
Expected: FAIL with "not enough arguments in call to email.RenderEmail" at `internal/backend/auth.go` and `internal/jobs/evaluate.go`. This is expected — fixed in Tasks 6 and 7.

- [ ] **Step 6: Commit**

```bash
git add internal/email/template.go internal/email/template_test.go
git commit -m "refactor(email): add LogoURL to TemplateData; RenderEmail takes logoURL arg"
```

---

## Task 5: Update wrapper.html + logo-present/empty tests

**Files:**
- Modify: `internal/email/templates/wrapper.html`
- Modify: `internal/email/template_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/email/template_test.go` (after `TestRenderEmail`):

```go
func TestRenderEmail_WithLogo(t *testing.T) {
	const logoURL = "https://example.com/mark-256.png"
	html, err := RenderEmail(template.HTML("<p>body</p>"), "footer", logoURL)
	require.NoError(t, err)
	require.Contains(t, html, `src="`+logoURL+`"`)
	// Width/height/alt in order guards against matching an unrelated <img> with alt="".
	require.Contains(t, html, `width="28" height="28" alt=""`)
}

func TestRenderEmail_WithoutLogo(t *testing.T) {
	html, err := RenderEmail(template.HTML("<p>body</p>"), "footer", "")
	require.NoError(t, err)
	require.NotContains(t, html, "<img ")
	// App name still appears in the plain-text branch.
	require.Contains(t, html, "Sabermatic[.DEV]")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/email/... -run "TestRenderEmail_With" -count=1 -v`
Expected: FAIL — current wrapper emits `<p>{{.AppName}}</p>` unconditionally; no `<img>` and no conditional.

- [ ] **Step 3: Update the wrapper header**

In `internal/email/templates/wrapper.html`, replace the `<!-- Header -->` row (the `<tr>` that currently contains the `<td>` with the `<p>{{.AppName}}</p>`) with:

```html
          <!-- Header -->
          <tr>
            <td style="padding:32px 32px 0 32px;">
              {{if .LogoURL}}
              <table role="presentation" cellpadding="0" cellspacing="0"><tr>
                <td style="padding-right:10px;vertical-align:middle;">
                  <img src="{{.LogoURL}}" width="28" height="28" alt=""
                       style="display:block;border:0;outline:none;text-decoration:none;">
                </td>
                <td style="vertical-align:middle;">
                  <p style="margin:0;font-size:14px;font-weight:600;letter-spacing:0.05em;color:#a1a1aa;">
                    {{.AppName}}
                  </p>
                </td>
              </tr></table>
              {{else}}
              <p style="margin:0;font-size:14px;font-weight:600;letter-spacing:0.05em;color:#a1a1aa;">
                {{.AppName}}
              </p>
              {{end}}
            </td>
          </tr>
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/email/... -count=1 -v`
Expected: PASS — `TestRenderEmail`, `TestRenderEmail_WithLogo`, `TestRenderEmail_WithoutLogo`.

- [ ] **Step 5: Commit**

```bash
git add internal/email/templates/wrapper.html internal/email/template_test.go
git commit -m "feat(email): conditional PNG mark in wrapper header"
```

---

## Task 6: Update `RenderEmail` callers in `auth.go`

**Files:**
- Modify: `internal/backend/auth.go:168, 323`

- [ ] **Step 1: Pass logoURL to sendVerifyEmail's RenderEmail call**

In `internal/backend/auth.go`, locate the current line:

```go
verifyHTML, err := intemail.RenderEmail(verifyBody, fmt.Sprintf("You received this email because you signed up for %s.", branding.AppName))
```

Replace with:

```go
verifyHTML, err := intemail.RenderEmail(verifyBody, fmt.Sprintf("You received this email because you signed up for %s.", branding.AppName), b.cfg.Auth.BaseURL+"/mark-256.png")
```

- [ ] **Step 2: Pass logoURL to sendResetEmail's RenderEmail call**

In the same file, locate:

```go
resetHTML, err := intemail.RenderEmail(resetBody, fmt.Sprintf("You received this email because you requested a password reset for %s.", branding.AppName))
```

Replace with:

```go
resetHTML, err := intemail.RenderEmail(resetBody, fmt.Sprintf("You received this email because you requested a password reset for %s.", branding.AppName), b.cfg.Auth.BaseURL+"/mark-256.png")
```

- [ ] **Step 3: Build the package**

Run: `go build ./internal/backend/...`
Expected: PASS.

- [ ] **Step 4: Run the auth tests (ensures no rendering expectations broke)**

Run: `go test ./internal/backend/... -race -count=1 -timeout=120s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backend/auth.go
git commit -m "feat(backend): pass logo URL to verify + reset email templates"
```

---

## Task 7: Update `renderEvaluationEmail` + its test

**Files:**
- Modify: `internal/jobs/evaluate.go:286`
- Modify: `internal/jobs/render_email_test.go`

- [ ] **Step 1: Write the failing assertion**

In `internal/jobs/render_email_test.go`, add one line to the end of `TestRenderEvaluationEmail_EscapesHTML` (after the `assert.Contains(t, html, "&lt;b&gt;evil")` line):

```go
	assert.Contains(t, html, "https://example.com/mark-256.png")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/jobs/... -run TestRenderEvaluationEmail_EscapesHTML -count=1 -v`
Expected: FAIL — assertion not met (no logo URL in rendered HTML yet).

- [ ] **Step 3: Update `renderEvaluationEmail` to pass logoURL**

In `internal/jobs/evaluate.go`, locate the current line 286:

```go
	rendered, err := email.RenderEmail(template.HTML(innerHTML), fmt.Sprintf("You received this email because you use %s.", branding.AppName))
```

Replace with:

```go
	rendered, err := email.RenderEmail(template.HTML(innerHTML), fmt.Sprintf("You received this email because you use %s.", branding.AppName), baseURL+"/mark-256.png")
```

(`baseURL` is the function parameter already in scope — look at the function signature earlier in the file to confirm.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/jobs/... -run TestRenderEvaluationEmail_EscapesHTML -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Build full tree**

Run: `go build ./...`
Expected: PASS (all three `RenderEmail` callers now updated).

- [ ] **Step 6: Run full backend test suite**

Run: `go test ./internal/... ./cmd/... -race -count=1 -timeout=300s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/jobs/evaluate.go internal/jobs/render_email_test.go
git commit -m "feat(jobs): pass logo URL to evaluation email template"
```

---

## Task 8: Regenerate `og-landing.svg` + `og-landing.png`

**Files:**
- Modify: `web/public/og-landing.svg`
- Modify: `web/public/og-landing.png`

- [ ] **Step 1: Verify rsvg-convert is available**

Run: `which rsvg-convert && rsvg-convert --version`
Expected: Output a version ≥ 2.0. If not installed: `brew install librsvg`.

- [ ] **Step 2: Replace `web/public/og-landing.svg` with B-treatment layout**

The new SVG must render `Sabermatic[<ball>DEV]` on a single line, centered, with `data-driven system design prep` below. Because `rsvg-convert` doesn't interpret `em`, all positions are absolute. The ball uses the simplified seam geometry (two paths + 8 ticks) already in the current file.

Replace the full contents of `web/public/og-landing.svg` with:

```xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630">
  <!-- Regenerated on macOS; rsvg-convert falls back to Helvetica. If regenerating
       on Linux (DejaVu Sans), text widths will shift and offsets below may need
       re-tuning so the wordmark stays centered on x=600. -->
  <rect width="1200" height="630" fill="#faf5ef"/>

  <!-- Wordmark left half: "Sabermatic[" -->
  <text x="330" y="362" font-family="ui-sans-serif,system-ui,sans-serif" font-size="108" font-weight="300" fill="#57534e">Sabermatic[</text>

  <!-- Inline ball replacing the '.' -->
  <g transform="translate(795 308) scale(2.0)">
    <g transform="rotate(-28 16 16)">
      <circle cx="16" cy="16" r="12" fill="#fef3c7" stroke="#92400e" stroke-width="2.5"/>
      <path d="M 7 11 Q 14 17 7 21" fill="none" stroke="#b45309" stroke-width="2" stroke-linecap="round"/>
      <path d="M 25 11 Q 14 17 25 21" fill="none" stroke="#b45309" stroke-width="2" stroke-linecap="round"/>
      <g stroke="#92400e" stroke-width="0.55" stroke-linecap="round" opacity="0.85">
        <line x1="8.4" y1="13.3" x2="10.9" y2="14.5"/>
        <line x1="9.0" y1="15.4" x2="12.0" y2="16.2"/>
        <line x1="9.2" y1="17.2" x2="12.1" y2="16.6"/>
        <line x1="8.8" y1="18.8" x2="11.4" y2="18.2"/>
        <line x1="23.6" y1="13.3" x2="21.1" y2="14.5"/>
        <line x1="23.0" y1="15.4" x2="20.0" y2="16.2"/>
        <line x1="22.8" y1="17.2" x2="19.9" y2="16.6"/>
        <line x1="23.2" y1="18.8" x2="20.6" y2="18.2"/>
      </g>
    </g>
  </g>

  <!-- Wordmark right half: "DEV]" -->
  <text x="860" y="362" font-family="ui-sans-serif,system-ui,sans-serif" font-size="108" font-weight="300" fill="#57534e">DEV]</text>

  <!-- Tagline -->
  <text x="600" y="462" text-anchor="middle" font-family="ui-sans-serif,system-ui,sans-serif" font-size="28" fill="#a8a29e">data-driven system design prep</text>
</svg>
```

- [ ] **Step 3: Regenerate the PNG**

Run: `rsvg-convert -w 1200 -h 630 web/public/og-landing.svg -o web/public/og-landing.png`
Expected: No output on success; a 1200×630 PNG written.

- [ ] **Step 4: Visually inspect the PNG**

Run: `open web/public/og-landing.png` (macOS) or equivalent.
Check:
- Single line: `Sabermatic[⚾DEV]` with the ball centered between the brackets
- Tagline below in the same style as before
- Wordmark midpoint should land near x=600 (horizontally centered). If visibly off-center, adjust the two `<text>` `x` values proportionally in the SVG and re-run rsvg-convert.
- Ball sits as a period-dot at the baseline of `DEV]`, not floating above or dropped below.

If any of the positions look wrong, iterate on the x/y offsets in the SVG and re-run rsvg-convert until it reads cleanly. Tuning is inherently visual — budget 15 min for this.

- [ ] **Step 5: Commit**

```bash
git add web/public/og-landing.svg web/public/og-landing.png
git commit -m "assets(og): regenerate og-landing with B-treatment wordmark"
```

---

## Task 9: Regenerate `og-sample.svg` + `og-sample.png`

**Files:**
- Modify: `web/public/og-sample.svg`
- Modify: `web/public/og-sample.png`

- [ ] **Step 1: Copy landing SVG to sample, swap tagline**

Run: `cp web/public/og-landing.svg web/public/og-sample.svg`

Then edit `web/public/og-sample.svg`: change the tagline `<text>` content from:

```xml
<text ...>data-driven system design prep</text>
```

to:

```xml
<text ...>sample evaluation</text>
```

Keep all coordinates, font-sizes, and ball geometry identical to `og-landing.svg`.

- [ ] **Step 2: Regenerate the PNG**

Run: `rsvg-convert -w 1200 -h 630 web/public/og-sample.svg -o web/public/og-sample.png`
Expected: 1200×630 PNG.

- [ ] **Step 3: Visually inspect**

Run: `open web/public/og-sample.png`
Check: wordmark identical to landing; only tagline reads `sample evaluation`.

- [ ] **Step 4: Commit**

```bash
git add web/public/og-sample.svg web/public/og-sample.png
git commit -m "assets(og): regenerate og-sample with B-treatment wordmark"
```

---

## Task 10: Add `make regen-og` target

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add the target**

Open `Makefile`. Add `regen-og` to the `.PHONY` list at the top (space-separated), then append a new target section to the end of the file:

```makefile
# Regenerate OG images from their SVG sources. Requires rsvg-convert (brew install librsvg).
# Text rendering depends on the host's ui-sans-serif fallback; regenerate on macOS for
# consistency with the committed baseline.
regen-og:
	@which rsvg-convert > /dev/null || (echo "rsvg-convert not found — brew install librsvg"; exit 1)
	rsvg-convert -w 1200 -h 630 web/public/og-landing.svg -o web/public/og-landing.png
	rsvg-convert -w 1200 -h 630 web/public/og-sample.svg  -o web/public/og-sample.png
	@echo "Regenerated web/public/og-landing.png and web/public/og-sample.png"
```

- [ ] **Step 2: Verify the target runs and is idempotent**

Run: `make regen-og`
Expected: Two PNGs written; no errors.

Run: `git diff --stat web/public/og-landing.png web/public/og-sample.png`
Expected: No diff (or a minimal metadata-only diff) — running regen-og on already-regenerated assets should be idempotent on the same host.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: add regen-og target for OG PNG regeneration"
```

---

## Task 11: `spa.go` — add Twitter Card tags

**Files:**
- Modify: `internal/handler/spa.go`
- Modify: `internal/handler/spa_test.go`

- [ ] **Step 1: Write the failing test assertions**

In `internal/handler/spa_test.go`, extend the `wantTags` slice for `/` to include Twitter Card assertions. Replace:

```go
		{"/", []string{
			`og:title" content="` + branding.AppName + `"`,
			`og:description" content="data-driven system design prep"`,
			`og:url" content="https://sabermatic.dev/"`,
			`og:image" content="https://sabermatic.dev/og-landing.png"`,
		}},
```

with:

```go
		{"/", []string{
			`og:title" content="` + branding.AppName + `"`,
			`og:description" content="data-driven system design prep"`,
			`og:url" content="https://sabermatic.dev/"`,
			`og:image" content="https://sabermatic.dev/og-landing.png"`,
			`twitter:card" content="summary_large_image"`,
			`twitter:title" content="` + branding.AppName + `"`,
			`twitter:description" content="data-driven system design prep"`,
			`twitter:image" content="https://sabermatic.dev/og-landing.png"`,
		}},
```

Do the same for the `/about` and `/sample` test cases (use each route's existing `og:title`, `og:description`, `og:image` values as the corresponding `twitter:*` values).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/... -run TestSPAHandlerOGTags -count=1 -v`
Expected: FAIL — `twitter:card` tag not present.

- [ ] **Step 3: Extend the tags Sprintf in `spa.go`**

In `internal/handler/spa.go`, locate the `tags := fmt.Sprintf(...)` call inside the `for path, og := range ogRoutes` loop (around lines 64–73). Replace with:

```go
		tags := fmt.Sprintf(
			`<meta property="og:title" content="%s">`+
				`<meta property="og:description" content="%s">`+
				`<meta property="og:type" content="website">`+
				`<meta property="og:url" content="%s%s">`+
				`<meta property="og:image" content="%s%s">`+
				`<meta name="twitter:card" content="summary_large_image">`+
				`<meta name="twitter:title" content="%s">`+
				`<meta name="twitter:description" content="%s">`+
				`<meta name="twitter:image" content="%s%s">`,
			html.EscapeString(og.title), html.EscapeString(og.description),
			html.EscapeString(baseURL), html.EscapeString(path),
			html.EscapeString(baseURL), html.EscapeString(og.image),
			html.EscapeString(og.title), html.EscapeString(og.description),
			html.EscapeString(baseURL), html.EscapeString(og.image),
		)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handler/... -run TestSPAHandlerOGTags -count=1 -v`
Expected: PASS (all four sub-tests: `/`, `/about`, `/sample`, `/login`).

- [ ] **Step 5: Commit**

```bash
git add internal/handler/spa.go internal/handler/spa_test.go
git commit -m "feat(handler): inject Twitter Card meta tags alongside OG"
```

---

## Task 12: Full verification + visual screen sweep

**Files:** none (verification only)

- [ ] **Step 1: Full CI must pass**

Run: `make test`
Expected: All stages pass (protos, frontend vitest, backend race tests).

- [ ] **Step 2: Frontend build**

Run: `cd web && npm run build`
Expected: Clean build; no new chunk warnings.

- [ ] **Step 3: Start dev server and take screenshots**

Run: `make dev` (starts overmind with vite + air on :8080).

In a browser, visit each surface. Take a screenshot for each — attach them to the PR description so the reviewer doesn't have to run the app.

| URL | What to verify |
|---|---|
| `http://localhost:8080/` | Nav: ball in `[.DEV]`. Hero: giant ball between brackets. Hover over `<h1>`: ball rotates slowly. |
| `http://localhost:8080/login` | AuthLayout shows `Sabermatic[⚾DEV]` centered; ball aligned as period-dot at the `text-base` size. |
| `http://localhost:8080/signup` | Same treatment as login. |
| `http://localhost:8080/forgot-password` | Same treatment. |
| `http://localhost:8080/` (first few ms, before auth loads) | AppLayout loading splash shows the ball in the wordmark. |
| Force an error boundary (e.g. in dev-tools console: throw inside a component render) | `error-fallback` shows the ball in the wordmark. |

If the vertical alignment looks off at any surface, tune `translate-y-[0.05em]` in `ball-mark.tsx` and re-run the visual pass. No new tests needed for retuning.

- [ ] **Step 4: Email render smoke**

Run: `go test ./internal/email/... -run TestRenderEmail -count=1 -v` and confirm the logo URL + width/height/alt assertions pass.

Optional end-to-end: set `BASE_URL=http://localhost:8080` in `.env`, run `make dev`, trigger a verify-email (sign up with a fresh email), and inspect the email output via Mailhog / Stripe fixtures / whatever dev mail sink the local env uses. Confirm the PNG loads in the rendered email preview.

- [ ] **Step 5: OG social-card preview**

Paste `http://localhost:8080/` (or the deployed preview URL) into a Twitter or Slack compose box and confirm the unfurled card uses the new B-treatment OG image. (Skip if no deployment URL is available — local only; Twitter can't fetch localhost.)

- [ ] **Step 6: Final check — no stray files, clean status**

Run: `git status`
Expected: Clean working tree; all commits from prior tasks present.

Run: `git log --oneline main..HEAD`
Expected: ~11 commits, one per task.

- [ ] **Step 7: (No commit — this is verification only.)**

---

## Post-plan notes

- **Spec open questions carried forward**: brackets-not-announced-by-SR is deliberate; if UX research changes that call, revise `aria-label` (see spec Risks).
- **OG font rendering drift**: regenerate on macOS (Helvetica fallback) for consistency with the committed baseline. A contributor regenerating on Linux will get DejaVu Sans and may need to re-tune offsets.
- **`BASE_URL` mismatch in dev**: default is `:3000` but backend serves on `:8080`. Email smoke tests need `.env` override — documented in the spec's URL resolution section.
