# UX Polish Pass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply a comprehensive UX polish pass covering branding rename, page redesigns, theme system fix, empty states, email templates, and a WebGL shader orb.

**Architecture:** The work spans frontend (React/TypeScript) and backend (Go). Tasks are ordered to build foundational pieces first (branding constants, theme context) that later tasks depend on. The WebGL orb is an isolated frontend component. Email templates are a backend-only change.

**Tech Stack:** React, TypeScript, Tailwind CSS, Three.js (new), Go, html/template, Protocol Buffers, sqlc

**Spec:** `docs/superpowers/specs/2026-04-09-ux-polish-pass-design.md`

**Boy Scout Rule:** When your awareness shines on tech debt while working on a task — unused imports, dead code, inconsistent patterns, missing types, stale comments — clean it up. Either integrate the cleanup into the current commit if it's related, or make a separate small commit. Don't leave code worse than you found it.

---

### Task 1: Branding Constants and BrandName Component

**Files:**
- Create: `web/src/components/brand-name.tsx`
- Modify: `web/src/lib/constants.ts`
- Create: `internal/branding/branding.go`

- [ ] **Step 1: Add frontend APP_NAME constant**

In `web/src/lib/constants.ts`, add:

```typescript
export const APP_NAME = "Sabermatic[.DEV]";
```

- [ ] **Step 2: Create BrandName component**

Create `web/src/components/brand-name.tsx`:

```tsx
import { cn } from "@/lib/utils";

interface BrandNameProps {
  className?: string;
}

export function BrandName({ className }: BrandNameProps) {
  return (
    <span className={cn("whitespace-nowrap", className)}>
      Sabermatic
      <span className="opacity-60">[.DEV]</span>
    </span>
  );
}
```

The `[.DEV]` portion is rendered at reduced opacity for a subtle typographic distinction. The `className` prop allows callers to control size, weight, tracking, etc.

- [ ] **Step 3: Create backend AppName constant**

Create `internal/branding/branding.go`:

```go
package branding

// AppName is the user-facing application name. Use this constant in all
// user-facing strings (email subjects, email body text, etc.).
// Do NOT use it for internal identifiers, observability, or package names.
const AppName = "Sabermatic[.DEV]"
```

- [ ] **Step 4: Verify frontend build compiles**

Run: `cd web && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 5: Verify backend compiles**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add web/src/components/brand-name.tsx web/src/lib/constants.ts internal/branding/branding.go
git commit -m "feat: add branding constants and BrandName component

Frontend APP_NAME const and <BrandName /> component with [.DEV]
at reduced opacity. Backend branding.AppName const for emails."
```

---

### Task 2: Apply Branding Across Frontend

**Files:**
- Modify: `web/src/layouts/app-layout.tsx`
- Modify: `web/src/pages/auth/auth-layout.tsx`
- Modify: `web/src/components/error-fallback.tsx`
- Modify: `web/index.html`

- [ ] **Step 1: Update app-layout header**

In `web/src/layouts/app-layout.tsx`, add import at top:

```typescript
import { BrandName } from "@/components/brand-name";
```

Replace line 44 (the `DRILL` text inside the `<Link>`):

```tsx
<Link to="/" className="hover:text-foreground transition-colors">
  <BrandName className="text-sm font-semibold tracking-wider text-muted-foreground" />
</Link>
```

- [ ] **Step 2: Update app-layout loading state**

In the same file, replace line 34 (`Loading...`):

```tsx
<div className="flex h-screen items-center justify-center bg-background">
  <BrandName className="text-sm font-semibold tracking-wider text-muted-foreground" />
</div>
```

- [ ] **Step 3: Update auth layout**

In `web/src/pages/auth/auth-layout.tsx`, add import:

```typescript
import { BrandName } from "@/components/brand-name";
```

Replace the `DRILL` text div (line 7-9):

```tsx
<div className="mb-6 text-center">
  <BrandName className="text-sm font-semibold tracking-wider text-muted-foreground" />
  <p className="mt-2 text-xs text-muted-foreground/70">system design, measured.</p>
</div>
```

- [ ] **Step 4: Update error fallback**

In `web/src/components/error-fallback.tsx`, add import:

```typescript
import { BrandName } from "@/components/brand-name";
```

Add `<BrandName />` above the "Something went wrong" heading:

```tsx
<div className="flex h-screen flex-col items-center justify-center gap-4 text-center">
  <BrandName className="text-sm font-semibold tracking-wider text-muted-foreground mb-4" />
  <h1 className="text-2xl font-semibold">Something went wrong</h1>
```

- [ ] **Step 5: Update HTML page title**

In `web/index.html`, change line 7:

```html
<title>Sabermatic[.DEV]</title>
```

- [ ] **Step 6: Verify build**

Run: `cd web && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 7: Load the app in browser and verify**

Run: `scripts/deploy.sh` (or whatever local dev command), then open the app.
Verify:
- Header shows `Sabermatic[.DEV]` (with [.DEV] slightly faded)
- Login page shows brand name + tagline
- Browser tab title says `Sabermatic[.DEV]`

- [ ] **Step 8: Commit**

```bash
git add web/src/layouts/app-layout.tsx web/src/pages/auth/auth-layout.tsx web/src/components/error-fallback.tsx web/index.html
git commit -m "feat: apply Sabermatic[.DEV] branding across frontend

Replace DRILL with BrandName component in header, auth layout,
error fallback. Add tagline to auth layout. Update HTML title."
```

---

### Task 3: Theme System — Context and Ternary Switcher

**Files:**
- Create: `web/src/contexts/theme-context.tsx`
- Modify: `web/src/hooks/use-theme.ts`
- Create: `web/src/components/theme-switch.tsx`
- Modify: `web/src/main.tsx`
- Modify: `web/src/layouts/app-layout.tsx`
- Modify: `web/src/pages/settings.tsx`

- [ ] **Step 1: Create ThemeContext provider**

Create `web/src/contexts/theme-context.tsx`:

```tsx
import { createContext, useState, useEffect, useCallback, type ReactNode } from "react";

export type Theme = "light" | "dark" | "system";

function getSystemTheme(): "light" | "dark" {
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyTheme(theme: Theme) {
  const resolved = theme === "system" ? getSystemTheme() : theme;
  document.documentElement.classList.toggle("dark", resolved === "dark");
}

interface ThemeContextValue {
  theme: Theme;
  setTheme: (t: Theme) => void;
}

export const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(() => {
    return (localStorage.getItem("theme") as Theme) || "system";
  });

  const setTheme = useCallback((t: Theme) => {
    localStorage.setItem("theme", t);
    setThemeState(t);
    applyTheme(t);
  }, []);

  useEffect(() => {
    applyTheme(theme);
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => { if (theme === "system") applyTheme("system"); };
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [theme]);

  return (
    <ThemeContext.Provider value={{ theme, setTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}
```

- [ ] **Step 2: Refactor useTheme hook**

Replace all content in `web/src/hooks/use-theme.ts`:

```typescript
import { useContext } from "react";
import { ThemeContext, type Theme } from "@/contexts/theme-context";

export type { Theme };

export function useTheme() {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}
```

- [ ] **Step 3: Create ThemeSwitch component**

Create `web/src/components/theme-switch.tsx`:

```tsx
import { Sun, Moon, Monitor } from "lucide-react";
import { useTheme, type Theme } from "@/hooks/use-theme";
import { cn } from "@/lib/utils";

const OPTIONS: { value: Theme; icon: typeof Sun; label: string }[] = [
  { value: "light", icon: Sun, label: "Light" },
  { value: "dark", icon: Moon, label: "Dark" },
  { value: "system", icon: Monitor, label: "System" },
];

export function ThemeSwitch() {
  const { theme, setTheme } = useTheme();

  return (
    <div className="flex items-center rounded-lg border border-border bg-muted/40 p-0.5 gap-0.5 w-fit">
      {OPTIONS.map((opt) => (
        <button
          key={opt.value}
          type="button"
          title={opt.label}
          onClick={(e) => {
            e.stopPropagation();
            setTheme(opt.value);
          }}
          className={cn(
            "rounded-md p-1.5 transition-colors cursor-pointer",
            theme === opt.value
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          <opt.icon className="h-3.5 w-3.5" />
        </button>
      ))}
    </div>
  );
}
```

`e.stopPropagation()` prevents the click from closing the dropdown menu when ThemeSwitch is rendered inside a `DropdownMenuItem`.

- [ ] **Step 4: Wrap app with ThemeProvider in main.tsx**

In `web/src/main.tsx`, add import:

```typescript
import { ThemeProvider } from "./contexts/theme-context";
```

Wrap everything with `<ThemeProvider>` as the outermost wrapper inside `<StrictMode>`:

```tsx
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <ErrorBoundary
        FallbackComponent={ErrorFallback}
        onReset={() => window.location.reload()}
      >
        <QueryClientProvider client={queryClient}>
          <TransportProvider transport={transport}>
            <BrowserRouter>
              <App />
            </BrowserRouter>
          </TransportProvider>
        </QueryClientProvider>
      </ErrorBoundary>
      <ThemedToaster />
    </ThemeProvider>
  </StrictMode>,
);
```

Add a `ThemedToaster` helper component below the imports (before `createRoot`):

```tsx
function ThemedToaster() {
  const { theme } = useTheme();
  const resolved = theme === "system"
    ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light")
    : theme;
  return <Toaster theme={resolved} position="bottom-right" richColors />;
}
```

Add import for `useTheme`:

```typescript
import { useTheme } from "./hooks/use-theme";
```

- [ ] **Step 5: Update app-layout dropdown**

In `web/src/layouts/app-layout.tsx`, replace the theme import and usage.

Remove the `useTheme` import and `const { theme, setTheme } = useTheme();` line. Add:

```typescript
import { ThemeSwitch } from "@/components/theme-switch";
```

Replace the cycling theme `DropdownMenuItem` (lines 77-82):

```tsx
<DropdownMenuItem onSelect={(e) => e.preventDefault()}>
  <ThemeSwitch />
</DropdownMenuItem>
```

`onSelect={(e) => e.preventDefault()}` prevents the dropdown from closing when the user clicks a theme option.

- [ ] **Step 6: Update settings page**

In `web/src/pages/settings.tsx`, replace the `ThemeToggle` function (lines 101-129) with:

```tsx
import { ThemeSwitch } from "@/components/theme-switch";
```

And where `<ThemeToggle />` is rendered, replace it with `<ThemeSwitch />`. Remove the local `Theme` type alias, `THEME_OPTIONS` array, and `ThemeToggle` function.

- [ ] **Step 7: Verify build**

Run: `cd web && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 8: Load app and verify**

- Open Settings → theme switcher shows sun/moon/monitor icons
- Click moon icon → app goes dark, Toaster uses dark theme
- Open avatar dropdown → theme switcher is there with same selection
- Click sun in dropdown → both dropdown and settings page show sun as selected
- Verify the two surfaces stay in sync

- [ ] **Step 9: Commit**

```bash
git add web/src/contexts/theme-context.tsx web/src/hooks/use-theme.ts web/src/components/theme-switch.tsx web/src/main.tsx web/src/layouts/app-layout.tsx web/src/pages/settings.tsx
git commit -m "fix: theme sync and ternary switcher

Replace independent useState per useTheme() call with shared
ThemeContext. Add ThemeSwitch icon component (sun/moon/monitor).
Wire Toaster to respect theme. Both dropdown and settings stay
in sync."
```

---

### Task 4: Login and Signup Pages

**Files:**
- Modify: `web/src/pages/auth/login.tsx`
- Modify: `web/src/pages/auth/signup.tsx`

- [ ] **Step 1: Rewrite login page**

Replace the full content of the `<CardContent>` in `web/src/pages/auth/login.tsx` with this order:

1. Google OAuth button with SVG icon
2. GitHub OAuth button with SVG icon
3. "or" separator
4. Email field
5. Password field with "Forgot password?" link below it
6. Submit button

```tsx
<CardContent className="pt-6 space-y-4">
  {/* OAuth buttons first */}
  <a
    href="/api/auth/oauth/google"
    className={buttonVariants({ variant: "outline", className: "w-full" })}
  >
    <svg className="mr-2 h-4 w-4" viewBox="0 0 24 24" aria-hidden="true">
      <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4"/>
      <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
      <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
      <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
    </svg>
    Sign in with Google
  </a>
  <a
    href="/api/auth/oauth/github"
    className={buttonVariants({ variant: "outline", className: "w-full" })}
  >
    <svg className="mr-2 h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0 1 12 6.844a9.59 9.59 0 0 1 2.504.337c1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.02 10.02 0 0 0 22 12.017C22 6.484 17.522 2 12 2z"/>
    </svg>
    Sign in with GitHub
  </a>

  <div className="relative flex items-center gap-2">
    <Separator className="flex-1" />
    <span className="text-xs text-muted-foreground">or</span>
    <Separator className="flex-1" />
  </div>

  <div className="space-y-1.5">
    <Label htmlFor="email">Email</Label>
    <Input
      id="email"
      type="email"
      autoComplete="email"
      value={email}
      onChange={(e) => setEmail(e.target.value)}
      required
    />
  </div>
  <div className="space-y-1.5">
    <div className="flex items-center justify-between">
      <Label htmlFor="password">Password</Label>
      <Link to="/forgot-password" className="text-xs text-muted-foreground hover:text-foreground">
        Forgot password?
      </Link>
    </div>
    <Input
      id="password"
      type="password"
      autoComplete="current-password"
      value={password}
      onChange={(e) => setPassword(e.target.value)}
      required
    />
  </div>
  {error && (
    <p className="text-sm text-orange-500">{error}</p>
  )}
  <Button type="submit" className="w-full" disabled={login.isPending}>
    {login.isPending ? "Signing in…" : "Sign in"}
  </Button>
</CardContent>
<CardFooter className="text-sm text-muted-foreground">
  <Link to="/signup" className="hover:text-foreground">
    Don't have an account? Sign up
  </Link>
</CardFooter>
```

- [ ] **Step 2: Rewrite signup page**

Apply the same reorder to `web/src/pages/auth/signup.tsx`. OAuth buttons first (with "Sign up with Google" / "Sign up with GitHub"), then separator, then name/email/password form:

```tsx
<CardContent className="pt-6 space-y-4">
  <a
    href="/api/auth/oauth/google"
    className={buttonVariants({ variant: "outline", className: "w-full" })}
  >
    <svg className="mr-2 h-4 w-4" viewBox="0 0 24 24" aria-hidden="true">
      <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4"/>
      <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
      <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
      <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
    </svg>
    Sign up with Google
  </a>
  <a
    href="/api/auth/oauth/github"
    className={buttonVariants({ variant: "outline", className: "w-full" })}
  >
    <svg className="mr-2 h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0 1 12 6.844a9.59 9.59 0 0 1 2.504.337c1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.02 10.02 0 0 0 22 12.017C22 6.484 17.522 2 12 2z"/>
    </svg>
    Sign up with GitHub
  </a>

  <div className="relative flex items-center gap-2">
    <Separator className="flex-1" />
    <span className="text-xs text-muted-foreground">or</span>
    <Separator className="flex-1" />
  </div>

  <div className="space-y-1.5">
    <Label htmlFor="display-name">Name</Label>
    <Input id="display-name" type="text" autoComplete="name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} required />
  </div>
  <div className="space-y-1.5">
    <Label htmlFor="email">Email</Label>
    <Input id="email" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
  </div>
  <div className="space-y-1.5">
    <Label htmlFor="password">Password</Label>
    <Input id="password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
  </div>
  {error && (
    <p className="text-sm text-orange-500">{error}</p>
  )}
  <Button type="submit" className="w-full" disabled={signup.isPending}>
    {signup.isPending ? "Creating account…" : "Create account"}
  </Button>
</CardContent>
<CardFooter className="text-sm text-muted-foreground">
  <Link to="/login" className="hover:text-foreground">
    Already have an account? Sign in
  </Link>
</CardFooter>
```

- [ ] **Step 3: Verify build**

Run: `cd web && npx tsc --noEmit`

- [ ] **Step 4: Load login and signup pages in browser**

Verify:
- OAuth buttons are at top with Google/GitHub SVG icons
- "or" separator between OAuth and email form
- "Forgot password?" is inline below the password label on login
- Spacing is even top and bottom
- Signup says "Sign up with" not "Sign in with"

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/auth/login.tsx web/src/pages/auth/signup.tsx
git commit -m "feat: redesign login and signup pages

OAuth buttons first with Google/GitHub SVG icons. Forgot password
link inline under password field. Signup uses 'Sign up with'
button text."
```

---

### Task 5: Session Config Page Redesign

**Files:**
- Modify: `web/src/pages/session-config.tsx`

- [ ] **Step 1: Add question image beside text**

Replace the question `<Card>` (lines 152-166) with a side-by-side layout:

```tsx
{/* Question — image beside text */}
<Card className="overflow-hidden">
  <div className="flex">
    {question?.imageUrl ? (
      <img
        src={question.imageUrl}
        alt={question.title}
        className="w-[45%] aspect-[4/3] object-cover flex-shrink-0"
      />
    ) : (
      <div
        className="w-[45%] aspect-[4/3] flex-shrink-0"
        style={{ background: "linear-gradient(135deg, hsl(32 40% 85%), hsl(24 30% 75%))" }}
      />
    )}
    <div className="flex flex-col justify-center px-6 py-5 flex-1">
      <CardTitle className="mb-2">
        {question ? question.title : <span className="text-muted-foreground">Loading question...</span>}
      </CardTitle>
      {question && (
        <p className="text-sm text-foreground leading-relaxed whitespace-pre-wrap line-clamp-6">
          {question.prompt}
        </p>
      )}
    </div>
  </div>
</Card>
```

- [ ] **Step 2: Add mic permission detection on mount**

Add a `useEffect` for mic permission detection after the existing state declarations:

```tsx
// Check mic permission on mount (non-prompting)
useEffect(() => {
  (async () => {
    try {
      const result = await navigator.permissions.query({ name: "microphone" as PermissionName });
      if (result.state === "granted") setMicState("granted");
    } catch {
      // Safari doesn't support microphone permission query — default to idle
    }
  })();
}, []);
```

- [ ] **Step 3: Reorganize Configuration card with groups**

Replace the entire Configuration `<Card>` and the standalone microphone box with a single Configuration card containing labeled groups:

```tsx
{/* Configuration */}
<Card>
  <CardHeader>
    <CardTitle>Configuration</CardTitle>
  </CardHeader>
  <CardContent className="space-y-5">
    {/* Session group */}
    <div className="space-y-2">
      <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">Session</span>
      <div>
        <Label>Duration</Label>
        <div className="flex items-center gap-2 flex-wrap mt-1.5">
          {DURATION_PRESETS.map((preset) => (
            <Button
              key={preset}
              type="button"
              variant={durationMode === "preset" && durationPreset === preset ? "default" : "outline"}
              size="sm"
              disabled={preset > planMax}
              onClick={() => { setDurationMode("preset"); setDurationPreset(preset); }}
            >
              {preset} min
            </Button>
          ))}
          <Button
            type="button"
            variant={durationMode === "custom" ? "default" : "outline"}
            size="sm"
            onClick={() => setDurationMode("custom")}
          >
            Custom
          </Button>
        </div>
        {durationMode === "custom" && (
          <div className="flex items-center gap-2 mt-2">
            <Input
              type="text"
              inputMode="numeric"
              value={customDuration}
              onChange={(e) => handleCustomDurationChange(e.target.value)}
              onBlur={handleCustomDurationBlur}
              className="w-20"
              aria-label="Custom duration in minutes"
            />
            <span className="text-sm text-muted-foreground">minutes (max {planMax})</span>
          </div>
        )}
        {me?.plan === UserPlan.FREE && (
          <p className="text-xs text-muted-foreground mt-1">Free plan: sessions capped at {FREE_PLAN_MAX} minutes.</p>
        )}
      </div>
    </div>

    {/* Audio group */}
    <div className="space-y-3">
      <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">Audio</span>

      {/* Mic toggle */}
      <div className="flex items-center justify-between">
        <div>
          <Label htmlFor="mic-toggle">Microphone</Label>
          <p className="text-xs text-muted-foreground mt-0.5">
            {micState === "denied"
              ? "Permission denied. You can still use text input."
              : "Use your voice — it's faster and more natural."}
          </p>
        </div>
        <Toggle
          id="mic-toggle"
          checked={micState === "granted"}
          onCheckedChange={(checked) => {
            if (checked) {
              handleEnableMic();
            } else {
              setMicState("idle");
            }
          }}
        />
      </div>

      {/* TTS toggle */}
      <div className="flex items-center justify-between">
        <div>
          <Label htmlFor="tts-toggle">Interviewer voice responses</Label>
          <p className="text-xs text-muted-foreground mt-0.5">Hear the interviewer speak rather than read text.</p>
        </div>
        <Toggle id="tts-toggle" checked={ttsEnabled} onCheckedChange={setTtsEnabled} />
      </div>
    </div>

    {/* Coach briefing — ungrouped, conditional */}
    {hasCoach && (
      <div className="flex items-center justify-between">
        <div>
          <Label htmlFor="coach-briefing-toggle">Brief interviewer on your weak areas</Label>
          <p className="text-xs text-muted-foreground mt-0.5">The interviewer will focus on dimensions where you need practice.</p>
        </div>
        <Toggle id="coach-briefing-toggle" checked={coachBriefing} onCheckedChange={setCoachBriefing} />
      </div>
    )}
  </CardContent>
</Card>
```

Remove the standalone microphone `<div>` (old lines 282-319). The mic is now inside the config card.

- [ ] **Step 4: Verify build**

Run: `cd web && npx tsc --noEmit`

- [ ] **Step 5: Load session config page in browser**

Navigate to `/sessions/new?question=<some-question-id>`. Verify:
- Question image shows beside title and prompt
- Configuration card has "SESSION" and "AUDIO" group labels
- Mic is a toggle inside the Audio group
- Coach briefing still appears if coach data exists
- Entitlement warning and Begin button still work

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/session-config.tsx
git commit -m "feat: redesign session config page

Question image beside text. Mic permission moved into config card
with Session/Audio group labels. Permission detection on mount
with Safari fallback."
```

---

### Task 6: Proto and SQL Plumbing for question_image_url

**Files:**
- Modify: `pb/drill/v1/session.proto`
- Modify: `sql/queries/sessions.sql`
- Modify: `internal/rpc/session/server.go`
- Regenerate: `internal/pb/`, `web/src/pb/`, `internal/db/`

- [ ] **Step 1: Add proto field**

In `pb/drill/v1/session.proto`, add to the `Session` message after field 17:

```protobuf
  optional string question_image_url = 18;
```

- [ ] **Step 2: Update SQL query**

In `sql/queries/sessions.sql`, update the `GetSession` query (line 7-15) to add `q.image_url`:

```sql
-- name: GetSession :one
SELECT s.id, s.user_id, s.question_id, s.status,
       s.config_duration_minutes, s.config_tts_enabled,
       s.config_coach_briefing, s.started_at, s.ended_at,
       s.turn_count, s.archived_at, s.created_at, s.updated_at,
       q.title AS question_title, q.prompt AS question_prompt,
       q.difficulty AS question_difficulty, q.hints AS question_hints,
       q.image_url AS question_image_url
FROM interview_sessions s
JOIN questions q ON q.id = s.question_id
WHERE s.id = $1;
```

- [ ] **Step 3: Regenerate code**

Run:

```bash
buf generate
sqlc generate
```

- [ ] **Step 4: Update getSessionRowToProto converter**

In `internal/rpc/session/server.go`, in the `getSessionRowToProto` function, add after the `QuestionHints` assignment (around line 289):

```go
if row.QuestionImageUrl.Valid {
    s.QuestionImageUrl = &row.QuestionImageUrl.String
}
```

- [ ] **Step 5: Verify build and tests**

Run:

```bash
go build ./...
go test ./internal/rpc/session/... -v -count=1
```

- [ ] **Step 6: Commit**

```bash
git add pb/ internal/pb/ web/src/pb/ sql/ internal/db/ internal/rpc/session/server.go
git commit -m "feat: add question_image_url to Session proto

Plumb image_url through GetSession SQL JOIN, sqlc codegen, and
proto converter so the waiting page can display the question image."
```

---

### Task 7: WebGL Shader Orb Component

**Files:**
- Create: `web/src/components/shader-orb.tsx`
- Create: `web/src/components/shader-orb-impl.tsx`
- Modify: `web/package.json` (add `three` dependency)

- [ ] **Step 1: Install Three.js**

Run: `cd web && npm install three && npm install -D @types/three`

- [ ] **Step 2: Create the shader orb implementation**

Create `web/src/components/shader-orb-impl.tsx`. This is the heavy file that imports Three.js (lazy-loaded):

```tsx
import { useEffect, useRef } from "react";
import * as THREE from "three";

// Simplex noise function (inline — avoids another dependency)
// Adapted from https://github.com/stegu/webgl-noise
const NOISE_GLSL = `
vec3 mod289(vec3 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 mod289(vec4 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 permute(vec4 x) { return mod289(((x*34.0)+1.0)*x); }
vec4 taylorInvSqrt(vec4 r) { return 1.79284291400159 - 0.85373472095314 * r; }
float snoise(vec3 v) {
  const vec2 C = vec2(1.0/6.0, 1.0/3.0);
  const vec4 D = vec4(0.0, 0.5, 1.0, 2.0);
  vec3 i  = floor(v + dot(v, C.yyy));
  vec3 x0 = v - i + dot(i, C.xxx);
  vec3 g = step(x0.yzx, x0.xyz);
  vec3 l = 1.0 - g;
  vec3 i1 = min(g.xyz, l.zxy);
  vec3 i2 = max(g.xyz, l.zxy);
  vec3 x1 = x0 - i1 + C.xxx;
  vec3 x2 = x0 - i2 + C.yyy;
  vec3 x3 = x0 - D.yyy;
  i = mod289(i);
  vec4 p = permute(permute(permute(
    i.z + vec4(0.0, i1.z, i2.z, 1.0))
  + i.y + vec4(0.0, i1.y, i2.y, 1.0))
  + i.x + vec4(0.0, i1.x, i2.x, 1.0));
  float n_ = 0.142857142857;
  vec3 ns = n_ * D.wyz - D.xzx;
  vec4 j = p - 49.0 * floor(p * ns.z * ns.z);
  vec4 x_ = floor(j * ns.z);
  vec4 y_ = floor(j - 7.0 * x_);
  vec4 x = x_ * ns.x + ns.yyyy;
  vec4 y = y_ * ns.x + ns.yyyy;
  vec4 h = 1.0 - abs(x) - abs(y);
  vec4 b0 = vec4(x.xy, y.xy);
  vec4 b1 = vec4(x.zw, y.zw);
  vec4 s0 = floor(b0)*2.0 + 1.0;
  vec4 s1 = floor(b1)*2.0 + 1.0;
  vec4 sh = -step(h, vec4(0.0));
  vec4 a0 = b0.xzyw + s0.xzyw*sh.xxyy;
  vec4 a1 = b1.xzyw + s1.xzyw*sh.zzww;
  vec3 p0 = vec3(a0.xy,h.x);
  vec3 p1 = vec3(a0.zw,h.y);
  vec3 p2 = vec3(a1.xy,h.z);
  vec3 p3 = vec3(a1.zw,h.w);
  vec4 norm = taylorInvSqrt(vec4(dot(p0,p0),dot(p1,p1),dot(p2,p2),dot(p3,p3)));
  p0 *= norm.x; p1 *= norm.y; p2 *= norm.z; p3 *= norm.w;
  vec4 m = max(0.6 - vec4(dot(x0,x0),dot(x1,x1),dot(x2,x2),dot(x3,x3)), 0.0);
  m = m * m;
  return 42.0 * dot(m*m, vec4(dot(p0,x0),dot(p1,x1),dot(p2,x2),dot(p3,x3)));
}
`;

const vertexShader = `
${NOISE_GLSL}
uniform float uTime;
varying vec3 vNormal;
varying vec3 vPosition;
varying float vDisplacement;

void main() {
  float displacement = snoise(position * 1.5 + uTime * 0.3) * 0.15;
  displacement += snoise(position * 3.0 + uTime * 0.5) * 0.05;
  vDisplacement = displacement;
  vec3 newPosition = position + normal * displacement;
  vNormal = normalize(normalMatrix * normal);
  vPosition = (modelViewMatrix * vec4(newPosition, 1.0)).xyz;
  gl_Position = projectionMatrix * modelViewMatrix * vec4(newPosition, 1.0);
}
`;

const fragmentShader = `
uniform float uTime;
varying vec3 vNormal;
varying vec3 vPosition;
varying float vDisplacement;

void main() {
  // Fresnel rim glow
  vec3 viewDir = normalize(-vPosition);
  float fresnel = pow(1.0 - max(dot(viewDir, vNormal), 0.0), 2.5);

  // Base amber/terracotta
  vec3 amber = vec3(0.96, 0.62, 0.04);      // #f59e0b
  vec3 terracotta = vec3(0.80, 0.45, 0.20);  // burnt orange
  vec3 cream = vec3(0.99, 0.96, 0.89);       // warm highlight

  // Accent colors that drift through
  vec3 cerulean = vec3(0.25, 0.58, 0.82);    // blue accent
  vec3 sage = vec3(0.47, 0.63, 0.45);        // green accent

  // Blend base color based on displacement
  vec3 baseColor = mix(terracotta, amber, vDisplacement * 3.0 + 0.5);

  // Mix in accents using time-varying noise
  float blueBlend = smoothstep(0.3, 0.7, sin(uTime * 0.4 + vPosition.x * 2.0) * 0.5 + 0.5) * 0.15;
  float greenBlend = smoothstep(0.3, 0.7, cos(uTime * 0.3 + vPosition.y * 2.0) * 0.5 + 0.5) * 0.10;
  baseColor = mix(baseColor, cerulean, blueBlend);
  baseColor = mix(baseColor, sage, greenBlend);

  // Core brightness
  vec3 color = mix(baseColor, cream, fresnel * 0.6);

  // Outer glow
  float glowStrength = fresnel * 0.8;
  color += amber * glowStrength;

  gl_FragColor = vec4(color, 1.0);
}
`;

interface ShaderOrbImplProps {
  size?: number;
}

export function ShaderOrbImpl({ size = 120 }: ShaderOrbImplProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(45, 1, 0.1, 100);
    camera.position.z = 3;

    const renderer = new THREE.WebGLRenderer({ alpha: true, antialias: true });
    renderer.setSize(size, size);
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    container.appendChild(renderer.domElement);

    const uniforms = { uTime: { value: 0 } };

    const geometry = new THREE.SphereGeometry(1, 64, 64);
    const material = new THREE.ShaderMaterial({
      vertexShader,
      fragmentShader,
      uniforms,
    });
    const sphere = new THREE.Mesh(geometry, material);
    scene.add(sphere);

    // Outer glow sprite
    const glowGeometry = new THREE.SphereGeometry(1.4, 32, 32);
    const glowMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(0.96, 0.62, 0.04),
      transparent: true,
      opacity: 0.08,
    });
    const glow = new THREE.Mesh(glowGeometry, glowMaterial);
    scene.add(glow);

    let animationId: number;
    const clock = new THREE.Clock();

    function animate() {
      uniforms.uTime.value = clock.getElapsedTime();
      renderer.render(scene, camera);
      animationId = requestAnimationFrame(animate);
    }
    animate();

    return () => {
      cancelAnimationFrame(animationId);
      renderer.dispose();
      geometry.dispose();
      material.dispose();
      glowGeometry.dispose();
      glowMaterial.dispose();
      container.removeChild(renderer.domElement);
    };
  }, [size]);

  return <div ref={containerRef} style={{ width: size, height: size }} />;
}
```

- [ ] **Step 3: Create lazy wrapper with CSS fallback**

Create `web/src/components/shader-orb.tsx`:

```tsx
import { lazy, Suspense } from "react";

const ShaderOrbImpl = lazy(() =>
  import("./shader-orb-impl").then((m) => ({ default: m.ShaderOrbImpl }))
);

function CssFallbackOrb({ size = 120 }: { size?: number }) {
  return (
    <div
      className="rounded-full animate-pulse"
      style={{
        width: size,
        height: size,
        background: "radial-gradient(circle at 40% 40%, #fef3c7, #f59e0b, #92400e)",
        boxShadow: "0 0 40px 10px rgba(245, 158, 11, 0.2)",
      }}
    />
  );
}

function hasWebGL(): boolean {
  try {
    const canvas = document.createElement("canvas");
    return !!(canvas.getContext("webgl") || canvas.getContext("webgl2"));
  } catch {
    return false;
  }
}

interface ShaderOrbProps {
  size?: number;
}

export function ShaderOrb({ size = 120 }: ShaderOrbProps) {
  if (!hasWebGL()) return <CssFallbackOrb size={size} />;

  return (
    <Suspense fallback={<CssFallbackOrb size={size} />}>
      <ShaderOrbImpl size={size} />
    </Suspense>
  );
}
```

- [ ] **Step 4: Verify build**

Run: `cd web && npx tsc --noEmit`

- [ ] **Step 5: Commit**

```bash
git add web/package.json web/package-lock.json web/src/components/shader-orb.tsx web/src/components/shader-orb-impl.tsx
git commit -m "feat: WebGL shader orb component

Three.js sphere with simplex noise displacement and brand palette
(amber/terracotta base, cerulean/sage accents). Lazy-loaded with
CSS fallback for devices without WebGL support."
```

---

### Task 8: Waiting Page Redesign

**Files:**
- Modify: `web/src/pages/session/layout.tsx`
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Redesign EvaluatingView in session layout**

In `web/src/pages/session/layout.tsx`, replace the `EvaluatingView` function (lines 15-33) with:

```tsx
import { ShaderOrb } from "@/components/shader-orb";

function EvaluatingView({ imageUrl }: { imageUrl?: string }) {
  const [index, setIndex] = useState(0);

  useEffect(() => {
    const id = setInterval(() => {
      setIndex((i) => (i + 1) % WAITING_MESSAGES.length);
    }, 4000);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="flex flex-col items-center justify-center pt-12 pb-24 gap-8 px-4">
      {/* Question image — subdued */}
      {imageUrl && (
        <img
          src={imageUrl}
          alt=""
          className="w-[200px] aspect-[4/3] object-cover rounded-xl opacity-70 shadow-md"
        />
      )}

      {/* WebGL shader orb */}
      <ShaderOrb size={120} />

      {/* Rotating evaluation message */}
      <p className="text-sm text-muted-foreground text-center max-w-xs animate-fade-in" key={index}>
        {WAITING_MESSAGES[index]}
      </p>
    </div>
  );
}
```

Update the render call to pass the image URL (around line 136):

```tsx
{showWaiting ? (
  <EvaluatingView imageUrl={session?.questionImageUrl} />
) : (
```

- [ ] **Step 2: Upgrade interview.tsx WaitingView**

In `web/src/pages/interview.tsx`, in the `WaitingView` function (around line 180), replace the tiny pulse dot:

```tsx
{/* Larger pulsing ring in brand colors */}
<div className="size-8 rounded-full border-2 border-amber-500/60 animate-pulse" />
```

This replaces the `size-3 rounded-full bg-primary animate-pulse` div.

- [ ] **Step 3: Add fade-in animation to Tailwind**

Check if `animate-fade-in` is already defined. If not, add to `web/tailwind.config.ts` under `extend.animation`:

```typescript
animation: {
  "fade-in": "fadeIn 0.5s ease-in-out",
},
keyframes: {
  fadeIn: {
    "0%": { opacity: "0", transform: "translateY(8px)" },
    "100%": { opacity: "1", transform: "translateY(0)" },
  },
},
```

- [ ] **Step 4: Verify build**

Run: `cd web && npx tsc --noEmit`

- [ ] **Step 5: Load a session in evaluating state and verify**

Navigate to a session that's evaluating. Verify:
- Question image appears at top (subdued)
- Shader orb is visible and animating
- Messages rotate with fade transitions
- No question title text at bottom

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/session/layout.tsx web/src/pages/interview.tsx web/tailwind.config.ts
git commit -m "feat: redesign waiting page with shader orb

EvaluatingView shows question image, WebGL orb, and fading
messages. Interview WaitingView upgraded to larger pulsing ring."
```

---

### Task 9: Empty States

**Files:**
- Modify: `web/src/pages/history.tsx`
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Add visual empty state to sessions page**

In `web/src/pages/history.tsx`, add imports at top:

```typescript
import { useQuery } from "@connectrpc/connect-query";
import { listQuestions } from "@/pb/drill/v1/question-QuestionService_connectquery";
import { Search, MessageSquare, BarChart3 } from "lucide-react";
import { Button } from "@/components/ui/button";
```

Replace the "No sessions yet" block (lines 496-502) with:

```tsx
<div className="py-16 flex flex-col items-center gap-8">
  {/* How it works steps */}
  <div className="flex items-center gap-6 text-center">
    <div className="flex flex-col items-center gap-2">
      <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center">
        <Search className="h-4 w-4 text-muted-foreground" />
      </div>
      <span className="text-xs text-muted-foreground">Pick a question</span>
    </div>
    <div className="h-px w-8 bg-border" />
    <div className="flex flex-col items-center gap-2">
      <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center">
        <MessageSquare className="h-4 w-4 text-muted-foreground" />
      </div>
      <span className="text-xs text-muted-foreground">Practice live</span>
    </div>
    <div className="h-px w-8 bg-border" />
    <div className="flex flex-col items-center gap-2">
      <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center">
        <BarChart3 className="h-4 w-4 text-muted-foreground" />
      </div>
      <span className="text-xs text-muted-foreground">Get scored</span>
    </div>
  </div>

  {/* CTAs */}
  <EmptyStateCTAs />
</div>
```

Add a new `EmptyStateCTAs` component that queries questions and picks a random medium one:

```tsx
function EmptyStateCTAs() {
  const { data: questionsResp } = useQuery(listQuestions, {});
  const questions = questionsResp?.questions ?? [];

  const recommendedId = useMemo(() => {
    const medium = questions.filter((q) => q.difficulty === "medium");
    const pool = medium.length > 0 ? medium : questions;
    if (pool.length === 0) return null;
    return pool[Math.floor(Math.random() * pool.length)].id;
  }, [questions]);

  return (
    <div className="flex items-center gap-3">
      {recommendedId && (
        <Button asChild>
          <Link to={`/sessions/new?question=${recommendedId}`}>
            Start a recommended question
          </Link>
        </Button>
      )}
      <Button variant="outline" asChild>
        <Link to="/">Browse questions</Link>
      </Button>
    </div>
  );
}
```

Add `useMemo` to imports if not already there.

- [ ] **Step 2: Update home page new user state**

In `web/src/pages/home.tsx`, replace the `isNew` welcome text (lines 564-568) with logic to hero-promote a question:

```tsx
{isNew && !heroQuestion && (
  <NewUserHero questions={questions} startDisabled={atConcurrentLimit} />
)}
```

Add a `NewUserHero` component:

```tsx
function NewUserHero({
  questions,
  startDisabled,
}: {
  questions: ProtoQuestion[];
  startDisabled: boolean;
}) {
  const suggested = useMemo(() => {
    const medium = questions.filter((q) => q.difficulty === "medium");
    const pool = medium.length > 0 ? medium : questions;
    if (pool.length === 0) return null;
    return pool[Math.floor(Math.random() * pool.length)];
  }, [questions]);

  if (!suggested) return null;

  return (
    <HeroQuestionCard
      question={suggested}
      startDisabled={startDisabled}
      labelOverride="Get started"
    />
  );
}
```

Update `HeroQuestionCard` to accept an optional `labelOverride` prop:

```tsx
function HeroQuestionCard({
  question,
  startDisabled,
  labelOverride,
}: {
  question: ProtoQuestion;
  startDisabled: boolean;
  labelOverride?: string;
}) {
```

And in the JSX, replace the "Recommended by Coach" text:

```tsx
<span className="text-xs font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400 mb-2">
  {labelOverride ?? "Recommended by Coach"}
</span>
```

- [ ] **Step 3: Verify build**

Run: `cd web && npx tsc --noEmit`

- [ ] **Step 4: Load pages and verify**

- Sessions page with no sessions → shows visual steps + CTAs
- Home page as new user → shows hero-promoted question with "Get started" label

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/history.tsx web/src/pages/home.tsx
git commit -m "feat: visual empty states for sessions and home pages

Sessions page shows how-it-works steps with recommended question
CTA. Home page hero-promotes a question for new users."
```

---

### Task 10: Backend Email Template and Branding

**Files:**
- Create: `internal/email/template.go`
- Modify: `internal/backend/auth.go`
- Modify: `internal/jobs/evaluate.go`
- Modify: `internal/jobs/workers.go`
- Modify: `internal/jobs/integration_test.go`

- [ ] **Step 1: Create formatDurationHuman helper and email template**

Create `internal/email/template.go`:

```go
package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"math"
	"time"

	"github.com/btc/drill/internal/branding"
)

//go:embed templates/*.html
var templateFS embed.FS

var emailTmpl = template.Must(template.ParseFS(templateFS, "templates/wrapper.html"))

// TemplateData holds the data for the shared email wrapper template.
type TemplateData struct {
	AppName string
	Body    template.HTML // Pre-rendered inner HTML
	Footer  string
}

// RenderEmail renders the shared email wrapper with the given body HTML and footer text.
func RenderEmail(bodyHTML template.HTML, footer string) (string, error) {
	var buf bytes.Buffer
	err := emailTmpl.Execute(&buf, TemplateData{
		AppName: branding.AppName,
		Body:    bodyHTML,
		Footer:  footer,
	})
	if err != nil {
		return "", fmt.Errorf("render email template: %w", err)
	}
	return buf.String(), nil
}

// FormatDurationHuman formats a duration as a human-readable string
// like "1 hour", "30 minutes", "2 hours".
func FormatDurationHuman(d time.Duration) string {
	hours := d.Hours()
	minutes := d.Minutes()

	if hours >= 1 && math.Mod(hours, 1) == 0 {
		h := int(hours)
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	m := int(minutes)
	if m == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}
```

- [ ] **Step 2: Create the HTML wrapper template**

Create `internal/email/templates/wrapper.html`:

```html
<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="margin:0;padding:0;background-color:#fffbf5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background-color:#fffbf5;">
    <tr>
      <td align="center" style="padding:40px 20px;">
        <table role="presentation" width="480" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:8px;overflow:hidden;max-width:480px;width:100%;">
          <!-- Header -->
          <tr>
            <td style="padding:32px 32px 0 32px;">
              <p style="margin:0;font-size:14px;font-weight:600;letter-spacing:0.05em;color:#a1a1aa;">{{.AppName}}</p>
            </td>
          </tr>
          <!-- Body -->
          <tr>
            <td style="padding:24px 32px 32px 32px;font-size:15px;line-height:1.6;color:#333;">
              {{.Body}}
            </td>
          </tr>
        </table>
        <!-- Footer -->
        <p style="margin:24px 0 0 0;font-size:12px;color:#a1a1aa;text-align:center;">{{.Footer}}</p>
      </td>
    </tr>
  </table>
</body>
</html>
```

- [ ] **Step 3: Write a test for FormatDurationHuman**

Create `internal/email/template_test.go`:

```go
package email

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormatDurationHuman(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Hour, "1 hour"},
		{2 * time.Hour, "2 hours"},
		{30 * time.Minute, "30 minutes"},
		{1 * time.Minute, "1 minute"},
		{90 * time.Minute, "90 minutes"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			require.Equal(t, tt.want, FormatDurationHuman(tt.d))
		})
	}
}

func TestRenderEmail(t *testing.T) {
	html, err := RenderEmail("<p>Hello world</p>", "Test footer")
	require.NoError(t, err)
	require.Contains(t, html, "Sabermatic[.DEV]")
	require.Contains(t, html, "Hello world")
	require.Contains(t, html, "Test footer")
	require.Contains(t, html, "#fffbf5") // warm background
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/email/... -v -count=1`
Expected: PASS

- [ ] **Step 5: Update auth.go email sending**

In `internal/backend/auth.go`, add imports:

```go
"html/template"
"github.com/btc/drill/internal/branding"
intemail "github.com/btc/drill/internal/email"
```

Replace the verification email block (around lines 160-165):

```go
verifyBody := template.HTML(fmt.Sprintf(
    `<p>Click the button below to verify your email address.</p>
    <p style="margin:24px 0;"><a href="%s" style="display:inline-block;padding:12px 24px;background:#b45309;color:#fff;text-decoration:none;border-radius:6px;font-weight:500;">Verify email</a></p>
    <p style="font-size:13px;color:#666;">Or copy this link: %s</p>`,
    verifyURL, verifyURL))
verifyHTML, _ := intemail.RenderEmail(verifyBody, fmt.Sprintf("You received this email because you signed up for %s.", branding.AppName))
_, err = b.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
    To:      user.Email,
    Subject: fmt.Sprintf("Verify your %s account", branding.AppName),
    Text:    fmt.Sprintf("Click here to verify your email: %s", verifyURL),
    HTML:    verifyHTML,
}, emailOpts)
```

Replace the reset email block (around lines 303-308):

```go
ttlStr := intemail.FormatDurationHuman(b.cfg.Auth.ResetTokenTTL)
resetBody := template.HTML(fmt.Sprintf(
    `<p>Click the button below to reset your password. This link expires in %s.</p>
    <p style="margin:24px 0;"><a href="%s" style="display:inline-block;padding:12px 24px;background:#b45309;color:#fff;text-decoration:none;border-radius:6px;font-weight:500;">Reset password</a></p>
    <p style="font-size:13px;color:#666;">Or copy this link: %s</p>`,
    ttlStr, resetURL, resetURL))
resetHTML, _ := intemail.RenderEmail(resetBody, fmt.Sprintf("You received this email because you requested a password reset for %s.", branding.AppName))
_, err = b.jobs.Insert(ctx, jobs.SendEmailArgs{
    To:      user.Email,
    Subject: fmt.Sprintf("Reset your %s password", branding.AppName),
    Text:    "Click here to reset your password: " + resetURL,
    HTML:    resetHTML,
}, resetEmailOpts)
```

- [ ] **Step 6: Add BaseURL to EvaluateSessionWorker and update evaluation email**

In `internal/jobs/evaluate.go`, add `BaseURL` field to the struct:

```go
type EvaluateSessionWorker struct {
	river.WorkerDefaults[EvaluateSessionArgs]
	Pool    *pgxpool.Pool
	LLM     *ai.Client
	Cfg     *config.LLM
	BaseURL string
	Jobs    *river.Client[pgx.Tx]
}
```

In `internal/jobs/workers.go`, wire it (around line 30):

```go
eval := &EvaluateSessionWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM, BaseURL: cfg.Auth.BaseURL}
```

Update `renderEvaluationEmail` to accept `baseURL` and use the shared template. Replace the function signature and body:

```go
func renderEvaluationEmail(baseURL, questionTitle string, result *evaluation.EvaluationResult) string {
```

Replace the hardcoded `https://drill.dev/sessions` with `baseURL + "/sessions"`.

Also wrap the evaluation email content in the shared template by importing and calling `email.RenderEmail`. The body content (scores table, strengths, gaps, advice, link) becomes the inner HTML passed to `RenderEmail`.

- [ ] **Step 7: Update integration test brand strings**

In `internal/jobs/integration_test.go`, replace `"Welcome to Drill"` on lines 62 and 76 with:

```go
branding.AppName + " test subject"
```

Add import for `"github.com/btc/drill/internal/branding"`.

- [ ] **Step 8: Run tests**

```bash
go test ./internal/... -v -count=1 -run TestEmail
go test ./internal/... -v -count=1 -run TestIntegration
go build ./...
```

- [ ] **Step 9: Commit**

```bash
git add internal/email/template.go internal/email/template_test.go internal/email/templates/ internal/backend/auth.go internal/jobs/evaluate.go internal/jobs/workers.go internal/jobs/integration_test.go internal/branding/
git commit -m "feat: styled email templates with Sabermatic branding

Shared HTML email wrapper with warm cream background, white card,
amber CTA buttons. FormatDurationHuman helper for reset TTL.
Migrate verification, reset, and evaluation emails to shared
template. Wire BaseURL into EvaluateSessionWorker."
```

---

## Self-Review Checklist

**Spec coverage:**
- [x] Section 1 (Branding): Tasks 1, 2, 10
- [x] Section 2 (Session Config): Task 5
- [x] Section 3 (Login): Tasks 2, 4
- [x] Section 4 (Signup): Task 4
- [x] Section 5 (Waiting Page): Tasks 6, 7, 8
- [x] Section 6 (Theme System): Task 3
- [x] Section 7 (Empty States): Task 9
- [x] Section 8 (Emails): Task 10
- [x] Section 9 (App Loading): Task 2

**Placeholder scan:** No TBDs, TODOs, or "implement later" found.

**Type consistency:** `BrandName`, `ThemeSwitch`, `ShaderOrb` component names are consistent across creation and usage. `branding.AppName` is used consistently in Go code. `FormatDurationHuman` matches definition and call sites.
