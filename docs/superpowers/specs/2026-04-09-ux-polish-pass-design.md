# UX Polish Pass — Design Spec

**Date:** 2026-04-09  
**Scope:** User-facing UX refinements across login, session config, waiting page, theme system, empty states, emails, and branding rename.

---

## 1. Branding Rename: Drill → Sabermatic[.DEV]

### Frontend

- A `<BrandName />` React component renders the brand name with consistent typographic treatment. The `[.DEV]` portion may have distinct weight or opacity from `Sabermatic`, controlled via CSS within the component. All user-facing instances of the app name use this component rather than hardcoded strings.
- A single string constant (e.g., `APP_NAME = "Sabermatic[.DEV]"`) for contexts where plain text is needed (page titles, `<title>` tags, alt text).

### Backend

- A single Go constant (e.g., `const AppName = "Sabermatic[.DEV]"`) in a shared package, used in email subjects, email body text, and any other user-facing backend output.

### Scope

- User-facing text only. No renaming of Go packages, module paths, proto package names (`pb.drill.v1`), observability spans/metrics, database names, internal variable names, or repository name.

### Locations to update

| Location | Current | New |
|---|---|---|
| App header (`app-layout.tsx:44`) | `DRILL` | `<BrandName />` |
| Auth layout (`auth-layout.tsx:8`) | `DRILL` | `<BrandName />` |
| App loading screen (`app-layout.tsx:33`) | `Loading...` | `<BrandName />` |
| Email subject: verify (`auth.go:162`) | `"Verify your Drill account"` | `"Verify your Sabermatic[.DEV] account"` |
| Email subject: reset (`auth.go:305`) | `"Reset your Drill password"` | `"Reset your Sabermatic[.DEV] password"` |
| Email body text | `"Drill"` references | Use `AppName` constant |

---

## 2. Session Config Page

**File:** `web/src/pages/session-config.tsx`

### Image beside question text

The first card combines the question image and text side-by-side, following the `HeroQuestionCard` pattern from the home page:

- Image (4:3 aspect ratio) on the left, ~45% width, rounded left corners.
- Title and prompt text on the right.
- Falls back to the gradient placeholder if `question.imageUrl` is absent.

### Configuration card reorganization

The microphone permission section moves from a standalone box into the configuration card. Items are organized into labeled groups:

**Session**
- Duration selector (15/30/45/60 preset buttons + custom input, plan-aware max)

**Audio**
- Microphone — a toggle that triggers the browser permission prompt (`getUserMedia`) when switched on. If the user denies permission, the toggle reverts to off and a note appears: "You can still use text input." On mount, check existing permission state via `navigator.permissions.query({ name: 'microphone' })` — if already `granted`, initialize the toggle as on without re-prompting.
- Interviewer voice responses — TTS toggle (default: on)

**Ungrouped (bottom)**
- Coach briefing toggle — conditional on coach data existing. Label: "Brief interviewer on your weak areas."

Group labels are subtle (small uppercase text or similar), not full card sub-headers.

### Element order (top to bottom)

1. Question card (image beside title + prompt)
2. Configuration card (Session group → Audio group → coach briefing)
3. "Before you begin" tips card (unchanged)
4. Begin session button (unchanged)

The standalone microphone box is removed.

---

## 3. Login Page

**File:** `web/src/pages/auth/login.tsx`, `web/src/pages/auth/auth-layout.tsx`

### Brand heading and tagline

The auth layout renders `<BrandName />` as the heading, with the tagline **"system design, measured."** as a subtitle below it.

### OAuth buttons first

The login form is reordered:

1. **Google button** — outline style with inline Google "G" SVG icon, text: "Sign in with Google"
2. **GitHub button** — outline style with inline GitHub octomark SVG icon, text: "Sign in with GitHub"
3. **"or" separator** (unchanged)
4. **Email field**
5. **Password field** + **"Forgot password?"** link positioned as a small inline link directly below/right of the password field
6. **"Sign in" submit button**

### Footer

Only "Don't have an account? Sign up" remains in the footer. "Forgot password?" has moved to inline position near the password field.

### Spacing fix

The bottom padding of the card content area matches the top. Currently the gap between the GitHub button and the card footer is smaller than the gap between the "or" separator and the Google button.

---

## 4. Signup Page

**File:** `web/src/pages/auth/signup.tsx`

Same treatment as login:

- `<BrandName />` heading with "system design, measured." tagline via the shared auth layout.
- OAuth buttons (Google, GitHub) with SVG icons above the email/password form.
- Consistent spacing.

---

## 5. Post-Session Waiting Page

**File:** `web/src/pages/session/layout.tsx`

### Composition (top to bottom, vertically centered but biased toward upper viewport)

1. **Question image** — the cubist illustration for the session's question. Subdued (opacity ~0.7), moderate size (~180-200px wide), rounded corners, subtle shadow. Positioned with less space above than below to sit higher in the viewport.
2. **WebGL shader orb** — the centerpiece loading indicator.
3. **Rotating evaluation messages** — the existing 5 messages, fading in/out on a cycle.

No question title text — the image already provides context.

### WebGL shader orb

A Three.js scene with a single sphere:

- **Geometry:** `SphereGeometry` with sufficient segments for smooth displacement.
- **Material:** `ShaderMaterial` with custom vertex and fragment shaders.
- **Vertex shader:** Simplex noise-based displacement for organic surface rippling. The noise offset animates with time for continuous fluid motion.
- **Fragment shader:** Base color in amber/terracotta tones with procedural color variation — cerulean blue and sage green accents drift through the surface, matching the brand palette (cream, amber, terracotta, burnt orange with accents of cerulean blue and sage green). Fresnel-based rim glow for the outer luminous edge.
- **Post-processing or outer mesh:** A larger, semi-transparent sphere or sprite for the ambient glow halo around the orb.
- **Size:** ~100-120px diameter on screen. Substantially larger than the current 12px dot.
- **Performance:** Single sphere, no shadows, no complex scene graph. Negligible GPU cost. Renders in a contained `<canvas>` element.

The orb should feel alive — breathing, shifting, organic. Reference: Apple Siri orb aesthetic, but in the Sabermatic warm palette.

### Message transitions

Messages fade in (translate up + opacity) → hold → fade out (translate up + opacity). Same 5 evaluation-themed messages, ~4s per message.

---

## 6. Theme System

### Sync fix

**Current bug:** `useTheme()` hook uses `useState` initialized from `localStorage`. Each component that calls `useTheme()` gets an independent state copy. Changing theme in the app-layout dropdown does not reactively update the settings page (and vice versa) because there is no shared state — only `localStorage` is shared, which React does not observe.

**Fix:** Create a `ThemeContext` with a `ThemeProvider` wrapping the app. The provider owns the single `useState` for theme, exposes `{ theme, setTheme }` via context. The `useTheme()` hook becomes `useContext(ThemeContext)`. All consumers share one reactive state instance.

**Files:**
- New: `web/src/contexts/theme-context.tsx` (or similar)
- Modified: `web/src/hooks/use-theme.ts` — becomes a thin wrapper around `useContext`
- Modified: app entry point to wrap with `<ThemeProvider>`

### Ternary theme switcher

Replace the cycling `Theme: {theme}` dropdown menu item with an inline segmented control containing three icon buttons:

| Icon | Theme | Tooltip |
|---|---|---|
| Sun | `light` | Light |
| Moon | `dark` | Dark |
| Monitor | `system` | System |

The selected icon is visually highlighted (background fill or similar). This control is used in both:

- The avatar dropdown menu in `app-layout.tsx`
- The settings page in `settings.tsx`

Both render the same `<ThemeSwitch />` component backed by the shared `ThemeContext`.

---

## 7. Empty States

### Sessions page (no sessions)

**File:** `web/src/pages/history.tsx`

Replace the current "No sessions yet" text with a visual onboarding section:

**Visual "how it works" steps** — three items in a horizontal (or responsive vertical) layout:

1. **Pick a question** — with a simple icon or illustration
2. **Practice live** — conversation icon
3. **Get scored** — chart/score icon

Each step is minimal — icon + short label. Connected by a subtle line or arrow.

**Two CTAs below the steps:**

- **Primary:** "Start a recommended question" — picks a random question from the easiest available difficulty (easy > medium > hard fallback). Links directly to `/sessions/new?question={id}`.
- **Secondary:** "Browse questions" — links to `/` (home page).

### Home page (new user)

**File:** `web/src/pages/home.tsx`

Replace the small welcome text ("Pick a question to start practicing...") with a hero-promoted first question card. Use the `HeroQuestionCard` pattern that already exists for coach-recommended questions, but with different label text (e.g., "Suggested first question" or "Get started" instead of "Recommended by Coach"). Selection: random question from the easiest available difficulty (same logic as the sessions page empty state CTA).

---

## 8. Emails

**Files:** `internal/backend/auth.go`, potentially a new `internal/email/template.go`

### Styled HTML template

A shared HTML email wrapper used by both transactional emails. Design reference: Notion's transactional emails (card-style body on a colored background).

Structure:
- **Outer background:** subtle warm tone (cream or light amber, e.g., `#fffbf5`)
- **Inner card:** white background, rounded corners (border-radius via padding hack for email clients), centered, max-width ~480px
- **Header:** `Sabermatic[.DEV]` brand name, text-based (no image dependency), styled to match brand typography
- **Body:** the email-specific content (verification link, reset link)
- **Footer:** muted small text — e.g., "You received this email because you signed up for Sabermatic[.DEV]." Subtle, non-intrusive.

### Email content

**Verification email:**
- Subject: `Verify your Sabermatic[.DEV] account`
- Body: "Click the button below to verify your email address."
- CTA button: amber background (`#f59e0b`), white text, "Verify email"
- Fallback text link below button for email clients that don't render buttons.

**Password reset email:**
- Subject: `Reset your Sabermatic[.DEV] password`
- Body: "Click the button below to reset your password. This link expires in 1 hour."
- CTA button: amber background, white text, "Reset password"
- Fallback text link below button.

### Implementation

- Go template (html/template) for the shared wrapper, with slots for subject-specific body content.
- The template is embedded in the binary via `embed.FS`, not loaded from disk.
- Plain text fallback versions continue to exist alongside HTML for email clients that prefer plain text.
- Uses the `AppName` Go constant for brand references.

---

## 9. App Loading State

**File:** `web/src/layouts/app-layout.tsx`

Replace the current `Loading...` text in the auth-loading screen with `<BrandName />`, centered, matching the auth layout's typographic treatment. Provides visual consistency — the brand name is the first thing users see whether they're logging in or loading the app.
