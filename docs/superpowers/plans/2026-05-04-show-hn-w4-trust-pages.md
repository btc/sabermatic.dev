# Show HN W4 — Trust pages implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add real about / privacy / terms pages with a public footer linking to them, plus SEO basics (meta description, robots.txt, sitemap.xml, noindex on auth pages).

**Architecture:** Three new React route components rendering plain markdown-style content, plus a small `<PublicFooter />` component. Routes added to `web/src/App.tsx`. Static SEO assets land in `web/public/` (served as-is by Vite). Content drafts come verbatim from the spec's Appendix A; user has approved the language.

**Tech Stack:** React + react-router-dom, Vite, TypeScript.

**Spec:** `docs/superpowers/specs/2026-05-04-show-hn-prep-design.md` (Workstream 4 + Appendix A).

---

## File structure

| File | Action |
|---|---|
| `web/src/pages/about.tsx` | Create — replaces existing route that aliased `/about` to `<Landing />` |
| `web/src/pages/terms.tsx` | Create |
| `web/src/pages/privacy.tsx` | Create |
| `web/src/components/public-footer.tsx` | Create |
| `web/src/components/legal-page.tsx` | Create — shared layout shell for about/terms/privacy (header + footer + max-width content area) |
| `web/src/App.tsx` | Modify — replace `/about` route, add `/terms` and `/privacy` routes; mount footer alongside ConditionalHome's Landing render |
| `web/index.html` | Modify — add `<meta name="description">` |
| `web/public/robots.txt` | Create |
| `web/public/sitemap.xml` | Create |
| `web/src/pages/auth/login.tsx` | Modify — add `<meta name="robots" content="noindex">` via a small head-effect (no react-helmet dep — use a tiny useEffect that sets/cleans a meta tag, or document the choice to skip if a head-management lib isn't already in use) |
| `web/src/pages/auth/signup.tsx` | Modify — same noindex addition |

No tests required for static pages beyond the existing typecheck/lint/vitest. The `<PublicFooter />` and `<LegalPage />` get one render-smoke test each.

**Open decisions to confirm before starting (W4 review gates from spec):**
- Refund policy language matches actual Stripe billing behavior
- Mailgun is the actual prod email sender
- Stripe cancellation behavior (mid-cycle stops billing, retains access through period end)

If any of these is incorrect at the time of implementation, **STOP** and update the relevant page text before deploying.

---

## Tasks

### Task 1: Confirm open decisions

**Files:** none — verification only.

- [ ] **Step 1: Re-read the spec's "Decisions for user to confirm" block in Workstream 4**

Path: `docs/superpowers/specs/2026-05-04-show-hn-prep-design.md`, search for "Decisions for user to confirm".

- [ ] **Step 2: Verify each of the three open decisions against current production behavior**

  1. **Refund policy** — does the drafted Terms language match what you intend to honor? If not, edit the Terms draft in this plan (Task 4) BEFORE writing the page.
  2. **Mailgun** — does prod actually send mail via Mailgun? Check Cloud Run env or Terraform secrets for `MAILGUN_API_KEY` (not the default `test-key`). If a different provider is used, update the subprocessor list in Privacy (Task 5) BEFORE writing.
  3. **Stripe cancellation** — review your Stripe configuration. Mid-cycle cancel should stop future charges and retain access through period end. If your Stripe is set to immediate-cancel-and-prorate, update Terms language to match.

If any answer is "different from spec," edit the corresponding draft in this plan before writing the page.

---

### Task 2: Create shared `<LegalPage />` shell

**Files:**
- Create: `web/src/components/legal-page.tsx`

- [ ] **Step 1: Create the file**

Content:

```tsx
import type { ReactNode } from "react";

import { PublicHeader } from "@/components/public-header";
import { PublicFooter } from "@/components/public-footer";

interface LegalPageProps {
  title: string;
  children: ReactNode;
}

/**
 * LegalPage is the shell for /about, /terms, /privacy — public header,
 * max-width prose content, public footer.
 */
export function LegalPage({ title, children }: LegalPageProps) {
  return (
    <div className="min-h-screen bg-background text-foreground flex flex-col">
      <PublicHeader />
      <main className="flex-1 mx-auto max-w-2xl w-full px-4 py-12 prose prose-neutral dark:prose-invert">
        <h1>{title}</h1>
        {children}
      </main>
      <PublicFooter />
    </div>
  );
}
```

- [ ] **Step 2: Verify imports compile**

```bash
cd web && npx tsc --noEmit -p tsconfig.app.json
```

Expected: no errors related to `legal-page.tsx`. (Will error on `public-footer` until Task 3 — that's fine; we'll re-run after.)

---

### Task 3: Create `<PublicFooter />`

**Files:**
- Create: `web/src/components/public-footer.tsx`

- [ ] **Step 1: Create the file**

Content:

```tsx
import { Link } from "react-router-dom";

/**
 * PublicFooter is mounted on landing, sample, and /about, /terms, /privacy.
 * Provides trust-signal links visible to unauthenticated visitors.
 */
export function PublicFooter() {
  return (
    <footer className="border-t border-border mt-16">
      <div className="mx-auto max-w-5xl px-4 py-8 flex flex-col sm:flex-row items-center justify-between gap-4 text-sm text-muted-foreground">
        <div>© Spanda, LLC</div>
        <nav aria-label="Footer">
          <ul className="flex flex-wrap items-center gap-4">
            <li><Link to="/about" className="hover:text-foreground">About</Link></li>
            <li><Link to="/terms" className="hover:text-foreground">Terms</Link></li>
            <li><Link to="/privacy" className="hover:text-foreground">Privacy</Link></li>
            <li><a href="mailto:brian@spanda.llc" className="hover:text-foreground">Contact</a></li>
          </ul>
        </nav>
      </div>
    </footer>
  );
}
```

- [ ] **Step 2: Smoke test renders**

Create `web/src/__tests__/public-footer.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

import { PublicFooter } from "@/components/public-footer";

describe("PublicFooter", () => {
  it("renders all three legal links plus contact", () => {
    render(
      <MemoryRouter>
        <PublicFooter />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "About" })).toHaveAttribute("href", "/about");
    expect(screen.getByRole("link", { name: "Terms" })).toHaveAttribute("href", "/terms");
    expect(screen.getByRole("link", { name: "Privacy" })).toHaveAttribute("href", "/privacy");
    expect(screen.getByRole("link", { name: "Contact" })).toHaveAttribute("href", "mailto:brian@spanda.llc");
  });
});
```

- [ ] **Step 3: Run test**

```bash
cd web && npx vitest run src/__tests__/public-footer.test.tsx
```

Expected: 1 test pass.

---

### Task 4: Create About page

**Files:**
- Create: `web/src/pages/about.tsx`

- [ ] **Step 1: Create the file**

Content (copy verbatim from spec Appendix A → About):

```tsx
import { LegalPage } from "@/components/legal-page";

export default function About() {
  return (
    <LegalPage title="About Sabermatic">
      <p>
        Sabermatic helps you practice system design interviews by talking through
        real problems with an AI interviewer — and getting honest feedback on how
        you did.
      </p>
      <p>
        It was built in 2026 during a job search, as a personal practice tool.
        After it became useful enough to recommend, it became a product. Billing
        exists to keep the service running without going broke offering it.
      </p>
      <p>Hope you find it helpful.</p>
      <hr />
      <p>Sabermatic is a product of Spanda, LLC.</p>
      <p>
        Contact: <a href="mailto:brian@spanda.llc">brian@spanda.llc</a>
        <br />
        Code: <a href="https://github.com/btc">github.com/btc</a>
      </p>
    </LegalPage>
  );
}
```

- [ ] **Step 2: Verify file compiles**

```bash
cd web && npx tsc --noEmit -p tsconfig.app.json
```

Expected: no errors.

---

### Task 5: Create Privacy page

**Files:**
- Create: `web/src/pages/privacy.tsx`

- [ ] **Step 1: Create the file**

Content (verbatim from spec Appendix A → Privacy Policy):

```tsx
import { LegalPage } from "@/components/legal-page";

export default function Privacy() {
  return (
    <LegalPage title="Privacy Policy">
      <p><em>Last updated: 2026-05-04</em></p>

      <p>
        Sabermatic is operated by Spanda, LLC ("we"). This policy explains what
        data we collect, how we use it, and the choices you have.
      </p>

      <h2>What we collect</h2>
      <p>When you use Sabermatic we collect:</p>
      <ul>
        <li>
          <strong>Account information</strong> — your email, display name,
          password (hashed), and OAuth provider IDs if you sign in with Google or
          GitHub.
        </li>
        <li>
          <strong>Practice content</strong> — the conversations, transcripts,
          audio recordings, AI evaluations, and coaching analyses generated when
          you use the service. Audio recordings are stored in Google Cloud Storage
          and remain associated with your account.
        </li>
        <li>
          <strong>Usage data</strong> — events about how you use the product (page
          views, signups, session starts, session completions). These help us
          understand how the service is used.
        </li>
        <li>
          <strong>Technical data</strong> — IP address, browser/device
          information, log data from your interactions with the service.
        </li>
        <li>
          <strong>Payment data</strong> — if you purchase a subscription or minute
          pack, Stripe processes your payment. We receive transaction status and
          metadata; we do not receive or store your card number.
        </li>
      </ul>

      <h3>Sign-in with Google or GitHub</h3>
      <p>
        If you sign in using Google or GitHub, those providers receive your
        authentication request and share your name, email address, and provider
        account ID with us. Google and GitHub act as independent controllers of
        that data and handle it under their own privacy policies.
      </p>

      <h2>How we use it</h2>
      <p>
        We use your conversations, transcripts, and recordings to operate the
        service for you, investigate issues you report, and improve our prompts,
        scoring, and product based on what works.
      </p>
      <p>
        <strong>
          We do not train AI models on your data, and we do not sell or share
          your data with third parties
        </strong>{" "}
        other than the subprocessors listed below who help us operate the
        service.
      </p>

      <h2>Subprocessors</h2>
      <p>
        We use the following third-party services to operate Sabermatic. Each
        receives only the data needed to perform its function. Each operates
        under its own privacy policy and may retain the data it processes per
        its own retention rules.
      </p>
      <ul>
        <li>
          <strong>Anthropic</strong> — runs the AI interviewer and
          coaching/analysis models. Your conversation content is sent to Anthropic
          for processing. Anthropic's standard API retention is up to 30 days for
          abuse monitoring before deletion.
        </li>
        <li>
          <strong>OpenAI</strong> — converts your speech to text (Whisper) and
          generates the interviewer's voice (TTS). Audio and transcripts are sent
          to OpenAI for processing. OpenAI's standard API retention is up to 30
          days for abuse monitoring before deletion.
        </li>
        <li>
          <strong>Google Cloud Platform</strong> — hosts the service (Cloud Run,
          Cloud SQL, Cloud Storage, BigQuery) and runs Vertex AI / Gemini for
          image generation. Your account data and practice content are stored on
          Google Cloud infrastructure in the United States.
        </li>
        <li>
          <strong>Stripe</strong> — processes payments. Your payment information
          is sent directly to Stripe.
        </li>
        <li>
          <strong>Mailgun</strong> — sends transactional emails (verification,
          password reset). Your email address is sent to Mailgun for delivery.
        </li>
      </ul>

      <h3>International transfers</h3>
      <p>
        We process and store data on Google Cloud infrastructure in the United
        States. If you are in the European Economic Area, your data is
        transferred to the United States; we rely on Standard Contractual Clauses
        with our subprocessors as the legal basis for this transfer.
      </p>

      <h2>Cookies</h2>
      <p>We use the following cookies:</p>
      <ul>
        <li>
          <strong>Session cookie</strong> (essential) — keeps you signed in.
          HttpOnly, SameSite=Lax.
        </li>
        <li>
          <strong>OAuth redirect cookie</strong> (essential, transient) —
          remembers where to send you after signing in with Google or GitHub.
          Cleared after use.
        </li>
        <li>
          <strong>Visitor ID cookie</strong> (functional) — a random identifier
          that lets us count unique visitors and reconstruct usage funnels. Not
          shared with third parties.
        </li>
      </ul>
      <p>
        Your theme preference is stored in your browser's local storage, not as a
        cookie.
      </p>

      <h2>Data retention</h2>
      <p>We keep your data for as long as your account is active.</p>
      <p>
        If you delete your account (Settings → Delete Account), we revoke access
        immediately and sign you out of all devices. Your account is marked
        deleted and can no longer be used to sign in. Practice content
        (transcripts, recordings, evaluations) is retained on our servers and
        removed on request — email{" "}
        <a href="mailto:brian@spanda.llc">brian@spanda.llc</a> to request
        immediate deletion of your content.
      </p>
      <p>
        Encrypted database backups are retained for approximately 7 days before
        expiry.
      </p>

      <h2>Your rights</h2>
      <p>
        You have the right to access, correct, export, or delete your personal
        data.
      </p>
      <p>
        Account deletion is available in Settings → Delete Account; for immediate
        deletion of all associated content (rather than account-only), email{" "}
        <a href="mailto:brian@spanda.llc">brian@spanda.llc</a>.
      </p>
      <p>
        To exercise other rights — including data access or export — email{" "}
        <a href="mailto:brian@spanda.llc">brian@spanda.llc</a>. We will respond
        within 30 days.
      </p>

      <h2>Security and breach notification</h2>
      <p>
        We use industry-standard measures to protect your data, including
        encryption in transit (HTTPS), encryption at rest on Google Cloud, hashed
        password storage, and short-lived session tokens.
      </p>
      <p>
        If we become aware of a security breach that affects your personal data,
        we will notify affected users without undue delay and within 72 hours of
        confirming the breach where required by applicable law.
      </p>

      <h2>Children</h2>
      <p>
        Sabermatic is not intended for children under the applicable age of
        consent in their country (13 in the United States, and as set by
        member-state law in the European Economic Area — generally 13 to 16). If
        you are under that age, do not use the service. We do not knowingly
        collect data from children below these ages. If you believe a child has
        provided us data, please contact{" "}
        <a href="mailto:brian@spanda.llc">brian@spanda.llc</a> and we will delete
        it.
      </p>

      <h2>Changes to this policy</h2>
      <p>
        If we change this policy in a way that materially affects how we handle
        your data, we will notify you by email before the change takes effect.
      </p>

      <h2>Contact</h2>
      <p>
        Spanda, LLC (Delaware, USA)
        <br />
        <a href="mailto:brian@spanda.llc">brian@spanda.llc</a>
      </p>
    </LegalPage>
  );
}
```

- [ ] **Step 2: Verify compile**

```bash
cd web && npx tsc --noEmit -p tsconfig.app.json
```

Expected: no errors.

---

### Task 6: Create Terms page

**Files:**
- Create: `web/src/pages/terms.tsx`

- [ ] **Step 1: Create the file**

Content (verbatim from spec Appendix A → Terms of Service):

```tsx
import { LegalPage } from "@/components/legal-page";

export default function Terms() {
  return (
    <LegalPage title="Terms of Service">
      <p><em>Last updated: 2026-05-04</em></p>

      <p>
        These terms govern your use of Sabermatic, a service operated by Spanda,
        LLC ("we"). By using the service you agree to these terms.
      </p>

      <h2>The service</h2>
      <p>
        Sabermatic is a practice tool for system design interviews. It uses AI to
        conduct simulated interviews and provide feedback on your responses.
      </p>

      <h2>Your account</h2>
      <p>
        You need an account to use the service. You are responsible for keeping
        your account credentials secure. You must provide accurate information
        and notify us promptly of any unauthorized use.
      </p>

      <h2>Acceptable use</h2>
      <p>
        You agree to use the service in good faith for system design learning and
        practice. Casual or exploratory use of the chat outside the strict
        interview format is fine; the AI is tuned for system design and may not
        be helpful for unrelated topics.
      </p>
      <p>You agree not to:</p>
      <ul>
        <li>
          Use the service to cheat in a real interview, including by transmitting
          live interviewer questions or content to or from an interview in
          progress.
        </li>
        <li>
          Attempt to disrupt the service, scrape data, or reverse-engineer the AI
          prompts or models.
        </li>
        <li>
          Submit content that is illegal, infringing, or harmful, or use the
          service to harass others.
        </li>
      </ul>
      <p>
        We reserve the right to suspend or terminate accounts that violate these
        terms.
      </p>

      <h2>Pricing and subscriptions</h2>
      <p>
        Sabermatic offers paid plans (subscription and one-time minute packs).
        Current pricing is shown in the app.
      </p>
      <ul>
        <li>
          <strong>Subscriptions</strong> can be cancelled at any time.
          Cancellation stops future billing; access continues through the end of
          the billing period for which you have paid. We do not refund time
          already paid.
        </li>
        <li>
          <strong>Minute packs</strong> are non-refundable once purchased. Minutes
          do not expire while your account is active.
        </li>
      </ul>
      <p>Payments are processed by Stripe.</p>

      <h2>Your content</h2>
      <p>
        You retain ownership of the content you provide (your responses,
        recordings). By using the service, you grant us a non-exclusive license
        to process that content as needed to operate the service for you and to
        improve the product, as described in our Privacy Policy.
      </p>
      <p>
        The AI evaluations and coaching analyses generated by the service are
        made available to you for your use. The underlying prompts, models, and
        product are owned by us.
      </p>

      <h2>Disclaimers</h2>
      <p>
        The service is provided "as is." We do not guarantee that the AI's
        evaluations, coaching, or any output of the service is accurate,
        complete, or suitable for any particular purpose.{" "}
        <strong>
          Sabermatic does not provide career, hiring, or professional advice.
          Using Sabermatic does not guarantee any interview, job offer, or
          employment outcome.
        </strong>
      </p>

      <h2>Limitation of liability</h2>
      <p>
        To the maximum extent permitted by law, Spanda, LLC is not liable for any
        indirect, incidental, consequential, or punitive damages arising from
        your use of the service. Our total liability for any claim is limited to
        the greater of (a) one hundred US dollars or (b) the amount you have paid
        us in the 12 months before the claim.
      </p>

      <h2>Termination</h2>
      <p>
        We reserve the right to suspend or terminate your account for violation
        of these terms. You may terminate your account at any time via Settings →
        Delete Account.
      </p>

      <h2>Changes to these terms</h2>
      <p>
        We may update these terms from time to time. If we make material changes,
        we will notify you by email. Continued use of the service after changes
        take effect means you accept the updated terms.
      </p>

      <h2>Governing law</h2>
      <p>
        These terms are governed by the laws of the State of Delaware, USA,
        without regard to conflict-of-law rules. Any dispute will be resolved in
        the state or federal courts located in Delaware.
      </p>

      <h2>Contact</h2>
      <p>
        Spanda, LLC
        <br />
        <a href="mailto:brian@spanda.llc">brian@spanda.llc</a>
      </p>
    </LegalPage>
  );
}
```

- [ ] **Step 2: Verify compile**

```bash
cd web && npx tsc --noEmit -p tsconfig.app.json
```

Expected: no errors.

---

### Task 7: Wire routes + remove `/about`-as-Landing alias

**Files:**
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Add lazy imports**

In `web/src/App.tsx`, after the existing `const NotFound = lazy(...)` line, add:

```tsx
const About = lazy(() => import("@/pages/about"));
const Terms = lazy(() => import("@/pages/terms"));
const Privacy = lazy(() => import("@/pages/privacy"));
```

- [ ] **Step 2: Replace the `/about` route**

In the `Routes` block, replace:

```tsx
<Route path="/about" element={<><PublicHeader /><Landing /></>} />
```

with:

```tsx
<Route path="/about" element={<About />} />
<Route path="/terms" element={<Terms />} />
<Route path="/privacy" element={<Privacy />} />
```

- [ ] **Step 3: Mount footer on `ConditionalHome` (landing render path)**

In the `ConditionalHome` function, replace:

```tsx
return <><PublicHeader /><Landing /></>;
```

with:

```tsx
return (
  <div className="min-h-screen flex flex-col">
    <PublicHeader />
    <div className="flex-1">
      <Landing />
    </div>
    <PublicFooter />
  </div>
);
```

And add the import at the top:

```tsx
import { PublicFooter } from "@/components/public-footer";
```

- [ ] **Step 4: Mount footer on `/sample` layout**

In `web/src/pages/sample.tsx`, after the closing `</main>`, add `<PublicFooter />` (and import). Verify by reading the existing structure first; the sample page wraps content in a `min-h-screen` div, so add the footer inside that div, after the main.

Concretely, read `web/src/pages/sample.tsx`. Locate the `<div className="min-h-screen bg-background text-foreground">` wrapper. Add `import { PublicFooter } from "@/components/public-footer";` at the top, and place `<PublicFooter />` as the last child of that wrapper div (after `</main>`).

- [ ] **Step 5: Verify build**

```bash
cd web && npx tsc -b && npm run build
```

Expected: build succeeds with no type errors.

---

### Task 8: Add meta description to `index.html`

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Add the meta tag**

In `web/index.html`, after the `<title>` line, add:

```html
    <meta name="description" content="Practice system design interviews with an AI interviewer. Honest feedback on how you did." />
```

So the `<head>` block looks like:

```html
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <link rel="apple-touch-icon" sizes="180x180" href="/mark-180.png" />
    <link rel="manifest" href="/manifest.json" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="theme-color" content="#faf5ef" />
    <title>Sabermatic[.DEV]</title>
    <meta name="description" content="Practice system design interviews with an AI interviewer. Honest feedback on how you did." />
  </head>
```

- [ ] **Step 2: Verify in built output**

```bash
cd web && npm run build && grep description dist/index.html
```

Expected: the meta tag appears in `dist/index.html`.

---

### Task 9: Create robots.txt and sitemap.xml

**Files:**
- Create: `web/public/robots.txt`
- Create: `web/public/sitemap.xml`

- [ ] **Step 1: Create `web/public/robots.txt`**

Content:

```
User-agent: *
Allow: /

Disallow: /login
Disallow: /signup
Disallow: /forgot-password
Disallow: /reset-password
Disallow: /verify-email
Disallow: /sessions/
Disallow: /history
Disallow: /settings
Disallow: /api/

Sitemap: https://sabermatic.dev/sitemap.xml
```

- [ ] **Step 2: Create `web/public/sitemap.xml`**

Content:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://sabermatic.dev/</loc>
    <changefreq>weekly</changefreq>
    <priority>1.0</priority>
  </url>
  <url>
    <loc>https://sabermatic.dev/sample</loc>
    <changefreq>monthly</changefreq>
    <priority>0.8</priority>
  </url>
  <url>
    <loc>https://sabermatic.dev/about</loc>
    <changefreq>monthly</changefreq>
    <priority>0.6</priority>
  </url>
  <url>
    <loc>https://sabermatic.dev/terms</loc>
    <changefreq>yearly</changefreq>
    <priority>0.3</priority>
  </url>
  <url>
    <loc>https://sabermatic.dev/privacy</loc>
    <changefreq>yearly</changefreq>
    <priority>0.3</priority>
  </url>
</urlset>
```

- [ ] **Step 3: Verify served by build**

```bash
cd web && npm run build && ls dist/robots.txt dist/sitemap.xml
```

Expected: both files present in `dist/`.

---

### Task 10: Add `noindex` to login and signup

**Files:**
- Modify: `web/src/pages/auth/login.tsx`
- Modify: `web/src/pages/auth/signup.tsx`

The codebase doesn't currently use `react-helmet` or a head-management lib. Use a small `useEffect` hook in each page that sets a `<meta name="robots" content="noindex">` tag on mount and removes it on unmount, scoped per page.

- [ ] **Step 1: Create a small helper**

Create `web/src/hooks/use-noindex.ts`:

```ts
import { useEffect } from "react";

/**
 * useNoindex sets a `<meta name="robots" content="noindex">` tag on mount and
 * removes it on unmount. Use on auth-flow pages that should not appear in
 * search results.
 */
export function useNoindex() {
  useEffect(() => {
    const meta = document.createElement("meta");
    meta.name = "robots";
    meta.content = "noindex";
    document.head.appendChild(meta);
    return () => {
      document.head.removeChild(meta);
    };
  }, []);
}
```

- [ ] **Step 2: Use in login.tsx**

Read `web/src/pages/auth/login.tsx`. At the top of the default-exported component function (before any other hooks), add:

```ts
import { useNoindex } from "@/hooks/use-noindex";
// ... inside the component:
useNoindex();
```

(Place the import alongside other imports; place the hook call as the first line inside the component body.)

- [ ] **Step 3: Use in signup.tsx**

Same pattern in `web/src/pages/auth/signup.tsx`.

- [ ] **Step 4: Verify build**

```bash
cd web && npx tsc -b && npm run build
```

Expected: build succeeds.

- [ ] **Step 5: Smoke test in browser**

```bash
cd web && npm run preview
```

Visit `http://localhost:4173/login` in a browser, view source. Expected: `<meta name="robots" content="noindex">` appears in the head (added by the hook on mount).

Visit `http://localhost:4173/about`. Expected: `<meta name="robots" content="noindex">` does NOT appear (the hook is scoped to login/signup only).

---

### Task 11: Visual smoke test

**Files:** none — manual verification.

- [ ] **Step 1: Start dev server**

```bash
cd web && npm run dev
```

- [ ] **Step 2: Visit each new route**

In a browser, hit each URL and confirm:

- `http://localhost:5173/about` — renders About page with header + footer
- `http://localhost:5173/terms` — renders Terms with header + footer
- `http://localhost:5173/privacy` — renders Privacy with header + footer
- `http://localhost:5173/` — landing page now has the public footer at the bottom
- `http://localhost:5173/sample` — sample page has the public footer at the bottom
- `http://localhost:5173/robots.txt` — serves the robots.txt
- `http://localhost:5173/sitemap.xml` — serves the sitemap

Footer links on each page should navigate correctly to the other legal pages.

- [ ] **Step 3: Check responsive at 320px viewport**

In browser devtools, switch to a narrow viewport (320px). Confirm the footer wraps cleanly and all links are tappable. Confirm legal-page content is readable and doesn't overflow.

---

### Task 12: Commit

**Files:** all from previous tasks.

- [ ] **Step 1: Run full CI suite**

```bash
make test
```

Expected: all stages pass (frontend typecheck/lint/tests + backend tests).

- [ ] **Step 2: Stage and commit**

```bash
git add web/src/components/legal-page.tsx web/src/components/public-footer.tsx \
        web/src/pages/about.tsx web/src/pages/terms.tsx web/src/pages/privacy.tsx \
        web/src/App.tsx web/src/pages/sample.tsx \
        web/src/pages/auth/login.tsx web/src/pages/auth/signup.tsx \
        web/src/hooks/use-noindex.ts \
        web/src/__tests__/public-footer.test.tsx \
        web/index.html web/public/robots.txt web/public/sitemap.xml
git commit -m "web: add about/terms/privacy pages, public footer, sitemap, robots.txt, noindex on auth pages"
```

- [ ] **Step 3: Confirm clean tree**

```bash
git status
```

Expected: working tree clean.

---

## Done

W4 is complete. Real about/terms/privacy pages live behind a public footer; SEO basics in place; auth pages noindexed.

**Post-deploy verification (do once after deploy):**
- `curl -s https://sabermatic.dev/about | grep -i 'About Sabermatic'` → matches
- `curl -s https://sabermatic.dev/robots.txt | head` → returns expected content
- `curl -s https://sabermatic.dev/sitemap.xml | head` → returns expected XML
- View source on `https://sabermatic.dev/login` → `noindex` meta present
- View source on `https://sabermatic.dev/about` → `noindex` meta absent
