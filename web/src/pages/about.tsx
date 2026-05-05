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
