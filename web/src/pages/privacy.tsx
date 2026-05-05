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
