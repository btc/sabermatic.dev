#!/usr/bin/env python3
"""Interactive GCP bootstrap for Sabermatic.

Walks through 10 phases from zero to a running Cloud Run service.
Resume-safe: completed phases are tracked in .cloud_bootstrap_state.

Usage: python3 scripts/cloud_bootstrap.py
"""

from __future__ import annotations

import base64
import getpass
import json
import logging
import secrets
import shlex
import shutil
import subprocess
import sys
import textwrap
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

# ── Logging ─────────────────────────────────────────────────────────────────

LOG_FILE = Path("cloud_bootstrap.log")
logging.basicConfig(
    filename=str(LOG_FILE),
    level=logging.DEBUG,
    format="%(asctime)s %(levelname)s %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
log = logging.getLogger("bootstrap")

# ── Colors ──────────────────────────────────────────────────────────────────

GREEN = "\033[32m"
YELLOW = "\033[33m"
RED = "\033[31m"
CYAN = "\033[36m"
BOLD = "\033[1m"
RESET = "\033[0m"


def info(msg: str) -> None:
    print(f"{GREEN}✓{RESET} {msg}")


def warn(msg: str) -> None:
    print(f"{YELLOW}!{RESET} {msg}")


def error(msg: str) -> None:
    print(f"{RED}✗{RESET} {msg}")


def link(url: str) -> str:
    return f"{CYAN}{url}{RESET}"


def header(phase: int, total: int, title: str) -> None:
    print(f"\n{BOLD}[{phase}/{total}] {title}{RESET}")
    print("─" * (len(title) + 8))


def prompt_continue(msg: str = "Press Enter to continue...") -> None:
    input(f"\n{YELLOW}{msg}{RESET}")


def prompt_yes_no(msg: str, default: bool = False) -> bool:
    suffix = "[Y/n]" if default else "[y/N]"
    answer = input(f"{YELLOW}{msg} {suffix}{RESET} ").strip().lower()
    if not answer:
        return default
    return answer in ("y", "yes")


def prompt_value(msg: str, default: str = "") -> str:
    if default:
        raw = input(f"{YELLOW}{msg} [{default}]:{RESET} ").strip()
        return raw if raw else default
    return input(f"{YELLOW}{msg}:{RESET} ").strip()


def prompt_secret(msg: str) -> str:
    return getpass.getpass(f"{YELLOW}{msg}:{RESET} ")


# ── State management ───────────────────────────────────────────────────────

STATE_FILE = Path(".cloud_bootstrap_state")
TOTAL_PHASES = 10


def load_state() -> dict:
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text())
    return {"phases": {}, "outputs": {}}


def save_state(state: dict) -> None:
    STATE_FILE.write_text(json.dumps(state, indent=2) + "\n")


def phase_done(state: dict, phase: str) -> bool:
    val = state["phases"].get(phase)
    if isinstance(val, dict):
        # A dict-tracked phase is complete only when mark_phase has set it to True.
        # Intermediate sub-step dicts are never considered fully done.
        return False
    return val is True


def mark_phase(state: dict, phase: str, done: bool = True) -> None:
    state["phases"][phase] = done
    save_state(state)


# ── Shell helpers ──────────────────────────────────────────────────────────


def run(
    cmd: list[str],
    *,
    check: bool = True,
    capture: bool = False,
    input_data: str | None = None,
    cwd: str | None = None,
) -> subprocess.CompletedProcess:
    """Run a subprocess, tee-ing all output to the log file.

    capture=True: output is logged but not printed to the terminal.
    capture=False: output is logged AND streamed to the terminal.
    """
    cmd_str = shlex.join(cmd)
    log.debug("$ %s", cmd_str)

    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        stdin=subprocess.PIPE if input_data is not None else None,
        cwd=cwd,
        text=True,
    )

    stdout_lines: list[str] = []
    stderr_lines: list[str] = []

    def _drain(stream, lines: list[str], sink) -> None:
        for line in stream:
            lines.append(line)
            log.debug(line.rstrip())
            if not capture:
                sink.write(line)
                sink.flush()

    t_out = threading.Thread(target=_drain, args=(proc.stdout, stdout_lines, sys.stdout))
    t_err = threading.Thread(target=_drain, args=(proc.stderr, stderr_lines, sys.stderr))
    t_out.start()
    t_err.start()

    if input_data is not None:
        if proc.stdin is None:
            raise RuntimeError("proc.stdin is None despite stdin=PIPE")
        proc.stdin.write(input_data)
        proc.stdin.close()

    t_out.join()
    t_err.join()
    proc.wait()

    stdout = "".join(stdout_lines)
    stderr = "".join(stderr_lines)

    if check and proc.returncode != 0:
        log.error("Command failed (exit %d): %s", proc.returncode, cmd_str)
        raise subprocess.CalledProcessError(proc.returncode, cmd, stdout, stderr)

    return subprocess.CompletedProcess(cmd, proc.returncode, stdout, stderr)


def run_quiet(cmd: list[str], **kwargs) -> subprocess.CompletedProcess:
    """Run a command, capture output, don't raise on failure."""
    return run(cmd, check=False, capture=True, **kwargs)


def cmd_exists(name: str) -> bool:
    return shutil.which(name) is not None


# ── Phase 1: Check dependencies ───────────────────────────────────────────

DEPS = {
    "gcloud": "brew install google-cloud-sdk",
    "terraform": "brew install hashicorp/tap/terraform",
    "docker": "brew install --cask docker",
    "go": "brew install go",
    "node": "brew install node",
    "npm": "(installed with node)",
}


def phase_deps(state: dict) -> None:
    header(1, TOTAL_PHASES, "Check Dependencies")

    missing = {name: hint for name, hint in DEPS.items() if not cmd_exists(name)}

    if missing:
        error("Missing tools:")
        for name, hint in missing.items():
            print(f"  {name:12s} →  {hint}")
        print("\nInstall these and re-run the script.")
        sys.exit(1)

    # Check Python version
    if sys.version_info < (3, 10):
        error(f"Python 3.10+ required, found {sys.version}")
        sys.exit(1)

    # Check Docker is running
    result = run_quiet(["docker", "info"])
    if result.returncode != 0:
        error("Docker is installed but not running. Start Docker Desktop and re-run.")
        sys.exit(1)

    info("All dependencies found")
    mark_phase(state, "deps")


# ── Phase 2: GCP Auth ─────────────────────────────────────────────────────


def phase_auth(state: dict) -> None:
    header(2, TOTAL_PHASES, "GCP Auth")

    result = run_quiet(["gcloud", "auth", "list", "--format=json"])
    accounts = json.loads(result.stdout) if result.returncode == 0 else []
    active = [a for a in accounts if a.get("status") == "ACTIVE"]

    if active:
        info(f"Authenticated as {active[0]['account']}")
    else:
        warn("No active gcloud account.")
        prompt_continue("Press Enter to open browser login...")
        run(["gcloud", "auth", "login"])

    # Application Default Credentials for Terraform
    adc_path = Path.home() / ".config" / "gcloud" / "application_default_credentials.json"
    if not adc_path.exists():
        warn("No Application Default Credentials found (needed for Terraform).")
        prompt_continue("Press Enter to open browser login for ADC...")
        run(["gcloud", "auth", "application-default", "login"])
    else:
        info("Application Default Credentials present")

    mark_phase(state, "auth")


# ── Phase 3: Create GCP Project + Enable Billing ──────────────────────────


def phase_project(state: dict) -> None:
    header(3, TOTAL_PHASES, "Create GCP Project + Enable Billing")

    project_id = prompt_value("GCP project ID", state.get("project_id", "sabermatic-production"))
    state["project_id"] = project_id
    save_state(state)

    region = prompt_value("GCP region", state.get("region", "us-central1"))
    state["region"] = region
    save_state(state)

    github_repo = prompt_value("GitHub repo (owner/repo)", state.get("github_repo", "btc/drill"))
    state["github_repo"] = github_repo
    save_state(state)

    # Create project
    print(f"\nCreating project {project_id}...")
    result = run_quiet(["gcloud", "projects", "create", project_id, "--name=Sabermatic"])
    if result.returncode == 0:
        info(f"Project {project_id} created")
    elif "ALREADY_EXISTS" in (result.stderr or "") or "already in use" in (result.stderr or ""):
        info(f"Project {project_id} already exists")
    else:
        error(f"Failed to create project: {result.stderr}")
        sys.exit(1)

    run(["gcloud", "config", "set", "project", project_id])
    info(f"Active project set to {project_id}")

    # Enable billing — link via CLI if billing account ID is known
    result = run_quiet([
        "gcloud", "billing", "projects", "describe", project_id,
        "--format=json",
    ])
    already_billed = False
    if result.returncode == 0:
        try:
            already_billed = json.loads(result.stdout).get("billingEnabled", False)
        except json.JSONDecodeError:
            pass

    if already_billed:
        info("Billing already enabled")
    else:
        billing_account = state.get("billing_account", "")
        if not billing_account:
            print("\nFind your billing account ID:")
            run(["gcloud", "billing", "accounts", "list"])
            billing_account = prompt_value("Billing account ID (e.g. 012345-ABCDEF-012345)")
            state["billing_account"] = billing_account
            save_state(state)

        print(f"\nLinking billing account {billing_account} to {project_id}...")
        result = run_quiet([
            "gcloud", "billing", "projects", "link", project_id,
            f"--billing-account={billing_account}",
        ])
        if result.returncode == 0:
            info("Billing enabled")
        else:
            error(f"Failed to link billing account: {result.stderr}")
            billing_url = f"https://console.cloud.google.com/billing/linkedaccount?project={project_id}"
            warn(f"Link manually: {link(billing_url)}")
            prompt_continue("Press Enter when billing is enabled...")

    mark_phase(state, "project")


# ── Phase 4: Enable APIs + Bootstrap Infrastructure ───────────────────────

APIS = [
    "run.googleapis.com",
    "sqladmin.googleapis.com",
    "secretmanager.googleapis.com",
    "artifactregistry.googleapis.com",
    "storage.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "cloudtrace.googleapis.com",
    "monitoring.googleapis.com",
    "logging.googleapis.com",
    "cloudbilling.googleapis.com",
]


def phase_bootstrap(state: dict) -> None:
    header(4, TOTAL_PHASES, "Enable APIs + Bootstrap Infrastructure")

    project_id = state["project_id"]
    region = state["region"]
    github_repo = state["github_repo"]

    # Enable APIs
    print("Enabling GCP APIs (this may take a minute)...")
    run(["gcloud", "services", "enable"] + APIS)
    info("APIs enabled")

    # Terraform state bucket
    state_bucket = f"{project_id}-tfstate"
    print(f"\nCreating Terraform state bucket: {state_bucket}")
    result = run_quiet([
        "gcloud", "storage", "buckets", "create", f"gs://{state_bucket}",
        "--location", region,
        "--uniform-bucket-level-access",
    ])
    if result.returncode == 0:
        info(f"Bucket gs://{state_bucket} created")
    else:
        info(f"Bucket gs://{state_bucket} already exists")

    run_quiet(["gcloud", "storage", "buckets", "update", f"gs://{state_bucket}", "--versioning"])
    info("Versioning enabled on state bucket")

    # Service accounts
    terraform_sa = f"terraform@{project_id}.iam.gserviceaccount.com"
    deployer_sa = f"deployer@{project_id}.iam.gserviceaccount.com"

    print("\nCreating service accounts...")
    for sa_id, display in [("terraform", "Terraform"), ("deployer", "CI/CD Deployer")]:
        result = run_quiet([
            "gcloud", "iam", "service-accounts", "create", sa_id,
            "--display-name", display,
        ])
        if result.returncode == 0:
            info(f"Created {sa_id} service account")
        else:
            info(f"Service account {sa_id} already exists")

    # IAM bindings
    print("\nBinding IAM roles...")
    terraform_roles = ["roles/editor", "roles/secretmanager.admin", "roles/iam.securityAdmin"]
    deployer_roles = ["roles/artifactregistry.writer", "roles/run.developer", "roles/iam.serviceAccountUser"]

    for sa, roles in [(terraform_sa, terraform_roles), (deployer_sa, deployer_roles)]:
        for role in roles:
            run_quiet([
                "gcloud", "projects", "add-iam-policy-binding", project_id,
                "--member", f"serviceAccount:{sa}",
                "--role", role,
                "--condition=None",
                "--quiet",
            ])
    info("IAM roles bound")

    # Workload Identity Federation
    print("\nSetting up Workload Identity Federation...")
    wif_pool = "github-actions"
    wif_provider = "github"

    run_quiet([
        "gcloud", "iam", "workload-identity-pools", "create", wif_pool,
        "--location=global",
        "--display-name=GitHub Actions",
    ])

    run_quiet([
        "gcloud", "iam", "workload-identity-pools", "providers", "create-oidc", wif_provider,
        "--location=global",
        f"--workload-identity-pool={wif_pool}",
        "--display-name=GitHub",
        "--issuer-uri=https://token.actions.githubusercontent.com",
        "--attribute-mapping=google.subject=assertion.sub,attribute.repository=assertion.repository",
        f"--attribute-condition=assertion.repository=='{github_repo}'",
    ])

    result = run_quiet([
        "gcloud", "iam", "workload-identity-pools", "describe", wif_pool,
        "--location=global", "--format=value(name)",
    ])
    wif_pool_id = result.stdout.strip()
    if result.returncode != 0 or not wif_pool_id:
        error(f"Could not retrieve WIF pool ID:\n{result.stderr}")
        sys.exit(1)

    for sa in [deployer_sa, terraform_sa]:
        run_quiet([
            "gcloud", "iam", "service-accounts", "add-iam-policy-binding", sa,
            "--role=roles/iam.workloadIdentityUser",
            f"--member=principalSet://iam.googleapis.com/{wif_pool_id}/attribute.repository/{github_repo}",
            "--quiet",
        ])

    info("Workload Identity Federation configured")

    state["outputs"]["wif_pool_id"] = wif_pool_id
    state["outputs"]["deployer_sa"] = deployer_sa
    state["outputs"]["terraform_sa"] = terraform_sa
    mark_phase(state, "bootstrap")


# ── Phase 5: Terraform Init + Apply ───────────────────────────────────────


def phase_terraform(state: dict) -> None:
    header(5, TOTAL_PHASES, "Terraform Init + Apply")

    project_id = state["project_id"]
    region = state["region"]
    github_repo = state["github_repo"]
    tf_dir = str(Path(__file__).resolve().parent.parent / "terraform")

    if not state.get("mailgun_domain"):
        state["mailgun_domain"] = prompt_value(
            "Mailgun sending subdomain", "mg.sabermatic.dev"
        )
        save_state(state)
    mailgun_domain = state["mailgun_domain"]

    if not state.get("oauth_google_client_id"):
        redirect_base = state["outputs"].get("cloud_run_url") or "https://sabermatic.dev"
        print(textwrap.dedent(f"""
          Google OAuth setup:
            {link(f"https://console.cloud.google.com/apis/credentials?project={project_id}")}
            → Create credentials → OAuth 2.0 Client ID
            → Application type: Web application
            → Authorized redirect URI: {redirect_base}/api/auth/oauth/google/callback
        """))
        state["oauth_google_client_id"] = prompt_value("Google OAuth client ID")
        save_state(state)
    oauth_google_client_id = state["oauth_google_client_id"]

    if not state.get("oauth_github_client_id"):
        redirect_base = state["outputs"].get("cloud_run_url") or "https://sabermatic.dev"
        print(textwrap.dedent(f"""
          GitHub OAuth setup:
            {link("https://github.com/settings/developers")}
            → OAuth Apps → New OAuth App
            → Homepage URL: https://sabermatic.dev
            → Authorization callback URL: {redirect_base}/api/auth/oauth/github/callback
        """))
        state["oauth_github_client_id"] = prompt_value("GitHub OAuth client ID")
        save_state(state)
    oauth_github_client_id = state["oauth_github_client_id"]

    # Write terraform.tfvars — sanitize values to prevent HCL injection
    def hcl_escape(s: str) -> str:
        # Escape backslash and double-quote for HCL string literals.
        # Also escape ${ and %{ which trigger HCL template interpolation.
        s = s.replace("\\", "\\\\").replace('"', '\\"')
        s = s.replace("${", "$${").replace("%{", "%%{")
        return s.replace("\n", " ").replace("\r", "")

    tfvars_content = textwrap.dedent(f"""\
        project_id             = "{hcl_escape(project_id)}"
        region                 = "{hcl_escape(region)}"
        environment            = "prod"
        github_repo            = "{hcl_escape(github_repo)}"
        mailgun_domain         = "{hcl_escape(mailgun_domain)}"
        oauth_google_client_id = "{hcl_escape(oauth_google_client_id)}"
        oauth_github_client_id = "{hcl_escape(oauth_github_client_id)}"
    """)
    tfvars_path = Path(tf_dir) / "terraform.tfvars"
    tfvars_path.write_text(tfvars_content)
    info(f"Wrote {tfvars_path}")

    # Ensure ADC quota project matches this project (Terraform GCS backend
    # sends the quota project for billing; a stale value causes 403 errors).
    adc_path = Path.home() / ".config" / "gcloud" / "application_default_credentials.json"
    if adc_path.exists():
        adc = json.loads(adc_path.read_text())
        if adc.get("quota_project_id") != project_id:
            warn(f"ADC quota project is '{adc.get('quota_project_id')}', updating to '{project_id}'...")
            prompt_continue("Press Enter to re-authenticate ADC with correct project...")
            run(["gcloud", "auth", "application-default", "login", f"--project={project_id}"])

    # Init — billing propagation can take a few minutes after project setup
    state_bucket = f"{project_id}-tfstate"
    print("\nRunning terraform init...")
    attempt = 0
    max_billing_attempts = 10  # ~40 min total with exponential backoff
    while True:
        result = run_quiet(
            ["terraform", "init", f"-backend-config=bucket={state_bucket}"],
            cwd=tf_dir,
        )
        if result.returncode == 0:
            break
        if "billing" in (result.stderr or "").lower():
            attempt += 1
            if attempt > max_billing_attempts:
                error("Billing failed to propagate after 10 attempts. Check billing account linkage and retry.")
                sys.exit(1)
            delay = min(30 * (2 ** (attempt - 1)), 300)  # 30s, 60s, 120s, 240s, 300s cap
            warn(f"Billing not yet propagated. Retrying in {delay}s... (attempt {attempt}/{max_billing_attempts})")
            time.sleep(delay)
        elif "Backend configuration changed" in (result.stderr or ""):
            # Backend bucket changed (e.g. new project) — reconfigure without migrating
            warn("Backend configuration changed, reconfiguring...")
            result = run_quiet(
                ["terraform", "init", "-reconfigure", f"-backend-config=bucket={state_bucket}"],
                cwd=tf_dir,
            )
            if result.returncode == 0:
                break
            error(f"terraform init -reconfigure failed:\n{result.stderr}")
            sys.exit(1)
        else:
            error(f"terraform init failed:\n{result.stderr}")
            sys.exit(1)
    info("Terraform initialized")

    # Plan — use -detailed-exitcode: 0=no changes, 1=error, 2=changes pending
    print("\nRunning terraform plan...")
    already_applied = phase_done(state, "terraform")
    if not already_applied:
        print(f"{YELLOW}(Cloud SQL takes 5–10 minutes to provision){RESET}\n")
    result = run_quiet(
        ["terraform", "plan", "-detailed-exitcode", "-out=tfplan"],
        cwd=tf_dir,
    )
    if result.returncode == 1:
        error(f"terraform plan failed:\n{result.stderr}")
        sys.exit(1)

    def _capture_outputs() -> None:
        for key in ["cloud_run_url", "sql_connection_name", "artifact_registry_url"]:
            r = run_quiet(["terraform", "output", "-raw", key], cwd=tf_dir)
            if r.returncode == 0:
                state["outputs"][key] = r.stdout.strip()

    if result.returncode == 0:
        info("Infrastructure is up to date — no changes needed")
        _capture_outputs()
        mark_phase(state, "terraform")
        return

    # Exit code 2: changes present — show the plan then prompt
    print(result.stdout)
    if not prompt_yes_no("\nApply this plan?"):
        warn("Skipping terraform apply. Re-run the script to apply later.")
        if not already_applied:
            sys.exit(0)
        return

    # Apply
    suffix = "" if already_applied else " (this may take 5–10 minutes for Cloud SQL)"
    print(f"\nApplying{suffix}...")
    run(["terraform", "apply", "tfplan"], cwd=tf_dir)
    info("Terraform apply complete")

    _capture_outputs()
    info(f"Cloud Run URL: {state['outputs'].get('cloud_run_url', 'unknown')}")
    mark_phase(state, "terraform")


# ── Phase 6: Populate Secrets ─────────────────────────────────────────────

SECRETS = [
    # (secret_id, method, help_text, help_url)
    # Note: "database-url" is intentionally excluded — Terraform populates it
    # automatically from the Cloud SQL instance (see terraform/secrets.tf).
    # Note: "stripe-webhook-secret" is set in Phase 7 after the webhook endpoint
    # is created, so it is excluded here too.
    ("auth-token-secret", "auto", None, None),
    ("anthropic-api-key", "prompt", "Paste your Anthropic API key",
     "https://console.anthropic.com/settings/keys"),
    ("openai-api-key", "prompt", "Paste your OpenAI API key",
     "https://platform.openai.com/api-keys"),
    ("mailgun-api-key", "prompt", "Paste your Mailgun API key",
     "https://app.mailgun.com/settings/api_security"),
    ("oauth-google-client-secret", "prompt", "Paste Google OAuth client secret",
     "https://console.cloud.google.com/apis/credentials?project={project_id}"),
    ("oauth-github-client-secret", "prompt", "Paste GitHub OAuth client secret",
     "https://github.com/settings/developers"),
    ("stripe-secret-key", "prompt", "Paste your Stripe secret key (sk_live_... or sk_test_...)",
     "https://dashboard.stripe.com/apikeys"),
    # stripe-webhook-secret is set in Phase 7 — skip here
]


def phase_secrets(state: dict) -> None:
    header(6, TOTAL_PHASES, "Populate Secrets")

    project_id = state["project_id"]
    secrets_state = state["phases"].get("secrets", {})
    if not isinstance(secrets_state, dict):
        secrets_state = {}

    all_done = True
    for secret_id, method, help_text, help_url in SECRETS:
        if secrets_state.get(secret_id):
            info(f"{secret_id}: already set")
            continue

        # Check if secret already has a non-placeholder value in GCP
        result = run_quiet([
            "gcloud", "secrets", "versions", "access", "latest",
            f"--secret={secret_id}",
        ])
        if result.returncode == 0 and result.stdout.strip() not in ("", "REPLACE_ME"):
            info(f"{secret_id}: already set in Secret Manager")
            secrets_state[secret_id] = True
            continue

        if method == "auto":
            value = secrets.token_hex(32)
            info(f"{secret_id}: auto-generating")
        else:
            # Show link if available
            if help_url:
                resolved_url = help_url.format(project_id=project_id)
                print(f"\n  {link(resolved_url)}")
            raw = prompt_secret(f"{help_text} (or 's' to skip)")
            if raw.lower() == "s":
                warn(f"Skipping {secret_id} — re-run to set later")
                secrets_state[secret_id] = False
                all_done = False
                continue
            value = raw

        # Write to Secret Manager
        run(
            ["gcloud", "secrets", "versions", "add", secret_id, "--data-file=-"],
            input_data=value,
        )
        info(f"{secret_id}: set")
        secrets_state[secret_id] = True

    state["phases"]["secrets"] = secrets_state
    save_state(state)

    if all_done:
        info("All secrets populated")
        mark_phase(state, "secrets")
    else:
        warn("Some secrets were skipped. Re-run the script to set them later.")


# ── Phase 7: Stripe Setup ────────────────────────────────────────────────


def stripe_api(
    endpoint: str,
    secret_key: str,
    params: dict,
) -> dict:
    """POST to Stripe API, return parsed JSON response."""
    url = f"https://api.stripe.com/v1/{endpoint}"
    data = urllib.parse.urlencode(params, doseq=True).encode()
    auth = base64.b64encode(f"{secret_key}:".encode()).decode()
    req = urllib.request.Request(
        url,
        data=data,
        headers={"Authorization": f"Basic {auth}"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode()
        error(f"Stripe API error ({e.code}): {body}")
        raise SystemExit(1)


def phase_stripe(state: dict) -> None:
    header(7, TOTAL_PHASES, "Stripe Setup")

    stripe_state = state["phases"].get("stripe", {})
    if not isinstance(stripe_state, dict):
        stripe_state = {}

    # Get the Stripe secret key from Secret Manager
    result = run_quiet([
        "gcloud", "secrets", "versions", "access", "latest",
        "--secret=stripe-secret-key",
    ])
    if result.returncode != 0 or not result.stdout.strip():
        error("stripe-secret-key not found in Secret Manager. Run Phase 6 first.")
        sys.exit(1)
    stripe_key = result.stdout.strip()

    # ── 7a: Create products & prices ──

    if not stripe_state.get("products"):
        print(textwrap.dedent("""\

        Stripe Product Setup
        ────────────────────
        Cost to serve: ~$0.035/min (Sonnet interviewer, Whisper STT, OpenAI TTS, Opus eval)
        GCP hosting baseline: ~$20-30/month

        Suggested pricing (each tier discounts over the last):

          Product              Price      $/min    vs 120 pack   Margin
          ────────────────────────────────────────────────────────────────
          120-min pack         $14.99     $0.125   baseline      72% ($10.79)
          300-min pack         $29.99     $0.100   20% off       65% ($19.49)
          600-min pack         $49.99     $0.083   33% off       58% ($28.99)
          Pro monthly (600m)   $39.00/mo  $0.065   48% off       73% ($28.50)*

          * Pro margin assumes 50% utilization (300 of 600 min used).
            At 100% utilization: 46% ($18.00). Still profitable.
        """))

        products = [
            {
                "key": "pro",
                "name": "Sabermatic Pro",
                "description": "600 min/month, coach access, full educator analysis",
                "default_cents": "3900",
                "recurring": True,
                "env_var": "STRIPE_PRO_PRICE_ID",
            },
            {
                "key": "pack_120",
                "name": "120-Minute Pack",
                "description": "120 minutes of interview practice",
                "default_cents": "1499",
                "recurring": False,
                "env_var": "STRIPE_PACK_120_PRICE_ID",
            },
            {
                "key": "pack_300",
                "name": "300-Minute Pack",
                "description": "300 minutes of interview practice",
                "default_cents": "2999",
                "recurring": False,
                "env_var": "STRIPE_PACK_300_PRICE_ID",
            },
            {
                "key": "pack_600",
                "name": "600-Minute Pack",
                "description": "600 minutes of interview practice",
                "default_cents": "4999",
                "recurring": False,
                "env_var": "STRIPE_PACK_600_PRICE_ID",
            },
        ]

        price_ids = state["outputs"].get("stripe_price_ids", {})

        for i, prod in enumerate(products, 1):
            print(f"\n{BOLD}[{i}/4] {prod['name']}{RESET}")
            print(f"  {prod['description']}")
            cents_str = prompt_value("  Price in cents", prod["default_cents"])
            cents = int(cents_str)

            # Create product
            product_resp = stripe_api("products", stripe_key, {
                "name": prod["name"],
                "description": prod["description"],
            })
            product_id = product_resp["id"]

            # Create price
            price_params: dict = {
                "product": product_id,
                "unit_amount": str(cents),
                "currency": "usd",
            }
            if prod["recurring"]:
                price_params["recurring[interval]"] = "month"

            price_resp = stripe_api("prices", stripe_key, price_params)
            price_id = price_resp["id"]
            price_ids[prod["key"]] = price_id

            dollars = cents / 100
            info(f"{prod['name']}: ${dollars:.2f} → {price_id}")

        state["outputs"]["stripe_price_ids"] = price_ids
        stripe_state["products"] = True
        state["phases"]["stripe"] = stripe_state
        save_state(state)

    # ── 7b: Register webhook endpoint ──

    if not stripe_state.get("webhook"):
        cloud_run_url = state["outputs"].get("cloud_run_url", "")
        if not cloud_run_url:
            error("Cloud Run URL not found. Run Phase 5 first.")
            sys.exit(1)

        webhook_url = f"{cloud_run_url}/api/webhooks/stripe"
        print(f"\nRegistering webhook endpoint: {webhook_url}")

        webhook_resp = stripe_api("webhook_endpoints", stripe_key, {
            "url": webhook_url,
            "enabled_events[]": [
                "checkout.session.completed",
                "invoice.paid",
                "customer.subscription.deleted",
                "customer.subscription.updated",
            ],
        })
        webhook_secret = webhook_resp["secret"]

        # Store webhook secret in Secret Manager
        run(
            ["gcloud", "secrets", "versions", "add", "stripe-webhook-secret", "--data-file=-"],
            input_data=webhook_secret,
        )
        info("Webhook registered and signing secret stored")

        stripe_state["webhook"] = True
        state["phases"]["stripe"] = stripe_state
        save_state(state)

    # ── 7c: Set price ID env vars on Cloud Run ──

    if not stripe_state.get("env_vars"):
        price_ids = state["outputs"].get("stripe_price_ids", {})
        if not price_ids:
            error("No price IDs found in state. Run step 7a first.")
            sys.exit(1)

        env_vars = ",".join([
            f"STRIPE_PRO_PRICE_ID={price_ids['pro']}",
            f"STRIPE_PACK_120_PRICE_ID={price_ids['pack_120']}",
            f"STRIPE_PACK_300_PRICE_ID={price_ids['pack_300']}",
            f"STRIPE_PACK_600_PRICE_ID={price_ids['pack_600']}",
        ])

        run([
            "gcloud", "run", "services", "update", "sabermatic",
            "--region", state["region"],
            f"--update-env-vars={env_vars}",
        ])
        info("Stripe price IDs set as Cloud Run env vars")

        stripe_state["env_vars"] = True
        state["phases"]["stripe"] = stripe_state
        save_state(state)

    mark_phase(state, "stripe")


# ── Phase 8 + 9: Build & Deploy ───────────────────────────────────────────


def phase_build(state: dict) -> None:
    header(8, TOTAL_PHASES, "Build & Push Container")
    project_root = str(Path(__file__).resolve().parent.parent)
    ar_url = state["outputs"].get("artifact_registry_url", "")
    if not ar_url:
        error("Artifact Registry URL not found. Run Phase 5 first.")
        sys.exit(1)
    image = f"{ar_url}/sabermatic:latest"
    region = state["region"]
    run(["gcloud", "auth", "configure-docker", f"{region}-docker.pkg.dev", "--quiet"])
    run(["docker", "build", "--platform", "linux/amd64", "-t", image, "."], cwd=project_root)
    run(["docker", "push", image])
    state["outputs"]["image"] = image
    mark_phase(state, "build")


# ── Phase 9: Deploy to Cloud Run ──────────────────────────────────────────


def phase_deploy(state: dict) -> None:
    header(9, TOTAL_PHASES, "Deploy to Cloud Run")

    image = state["outputs"].get("image", "")
    if not image:
        error("No image found in state. Run Phase 8 first.")
        sys.exit(1)

    print(f"Deploying {image} to Cloud Run...")
    run([
        "gcloud", "run", "deploy", "sabermatic",
        "--image", image,
        "--region", state["region"],
        "--project", state["project_id"],
    ])

    # Fetch the live URL
    result = run_quiet([
        "gcloud", "run", "services", "describe", "sabermatic",
        "--region", state["region"],
        "--project", state["project_id"],
        "--format=value(status.url)",
    ])
    live_url = result.stdout.strip()
    if not live_url:
        warn("Could not fetch Cloud Run URL — check the service manually.")
    state["outputs"]["cloud_run_url"] = live_url

    # Print summary
    price_ids = state["outputs"].get("stripe_price_ids", {})
    print(textwrap.dedent(f"""
    {GREEN}{BOLD}Deploy complete!{RESET}

    Cloud Run URL:  {live_url}
    Cloud SQL:      {state['outputs'].get('sql_connection_name', 'unknown')}
    Artifact Reg:   {state['outputs'].get('artifact_registry_url', 'unknown')}

    Stripe:
      Pro monthly:  {price_ids.get('pro', 'not set')}
      120-min pack: {price_ids.get('pack_120', 'not set')}
      300-min pack: {price_ids.get('pack_300', 'not set')}
      600-min pack: {price_ids.get('pack_600', 'not set')}
      Webhook:      {live_url}/api/webhooks/stripe

    Next steps:
      - Phase 10 will set up the sabermatic.dev custom domain
      - Set up GitHub Actions secrets for CI/CD:
          WIF_PROVIDER: {state['outputs'].get('wif_pool_id', 'unknown')}/providers/github
          DEPLOYER_SA:  {state['outputs'].get('deployer_sa', 'unknown')}
    """))

    mark_phase(state, "deploy")


# ── Phase 10: Custom Domain Setup ────────────────────────────────────────


def phase_domain(state: dict) -> None:
    header(10, TOTAL_PHASES, "Custom Domain Setup")

    region = state["region"]
    domain = "sabermatic.dev"
    service = "sabermatic"

    domain_state = state["phases"].get("domain", {})
    if not isinstance(domain_state, dict):
        domain_state = {}

    # ── 10a: Verify domain ownership with Google ──
    if not domain_state.get("verified"):
        print(textwrap.dedent(f"""
          Domain verification:
            {link("https://search.google.com/search-console")}
            → Add property → Domain → enter: {domain}
            → Copy the TXT record value shown
            → Add it at Dynadot: My Domains → {domain} → DNS Settings → Domain Record → TXT
            → Click Verify in Search Console

          Or via CLI:
            gcloud domains verify {domain}
        """))
        if not prompt_yes_no(f"  {domain} verified in Google Search Console?"):
            prompt_continue("  Complete verification, then press Enter to continue...")
        domain_state["verified"] = True
        state["phases"]["domain"] = domain_state
        save_state(state)

    # ── 10b: Create Cloud Run domain mapping ──
    if not domain_state.get("mapping"):
        print(f"\nCreating Cloud Run domain mapping for {domain}...")
        result = run_quiet([
            "gcloud", "beta", "run", "domain-mappings", "create",
            "--service", service,
            "--domain", domain,
            "--region", region,
        ])

        # Mapping may already exist — that's fine
        already = result.returncode != 0 and (
            "already exists" in (result.stderr or "")
            or "already mapped" in (result.stderr or "")
        )
        if result.returncode != 0 and not already:
            error(f"Failed to create domain mapping:\n{result.stderr}")
            sys.exit(1)
        if already:
            info("Domain mapping already exists — continuing")
        else:
            info("Domain mapping created")

        domain_state["mapping"] = True
        state["phases"]["domain"] = domain_state
        save_state(state)

    # ── 10c: Show DNS records to add ──
    if not domain_state.get("dns"):
        print("\nFetching required DNS records from GCP...")
        result = run_quiet([
            "gcloud", "beta", "run", "domain-mappings", "describe",
            "--domain", domain,
            "--region", region,
            "--format=value(status.resourceRecords)",
        ])

        print(textwrap.dedent(f"""
          DNS setup:
            → dynadot.com → My Domains → {domain} → DNS Settings
            → Replace any existing A/AAAA records with the GCP records below
            → Do NOT use a CNAME to the run.app URL — it will not provision SSL

          GCP resource records:
            {result.stdout.strip().replace(chr(10), chr(10) + "    ")}

          DNS propagation can take up to 30 minutes.
          SSL certificate provisioning takes an additional 15–30 minutes after that.
        """))

        if not prompt_yes_no("  DNS records added at Dynadot?"):
            prompt_continue("  Add the records above, then press Enter to continue...")
        domain_state["dns"] = True
        state["phases"]["domain"] = domain_state
        save_state(state)

    # ── 10d: Wait for SSL cert and verify health ──
    if not domain_state.get("live"):
        print("\nWaiting for SSL certificate to become ACTIVE...")
        deadline = time.time() + 60 * 45  # 45 min max
        cert_active = False
        while time.time() < deadline:
            cert_result = run_quiet([
                "gcloud", "beta", "run", "domain-mappings", "describe",
                "--domain", domain,
                "--region", region,
                "--format=json",
            ])
            try:
                mapping_json = json.loads(cert_result.stdout)
                conditions = mapping_json.get("status", {}).get("conditions", [])
                cert_cond = next(
                    (c for c in conditions if c.get("type") == "CertificateProvisioned"),
                    None,
                )
                if cert_cond and cert_cond.get("status") == "True":
                    cert_active = True
                    break
                msg = cert_cond.get("message", "provisioning...") if cert_cond else "provisioning..."
            except (json.JSONDecodeError, KeyError):
                msg = "checking..."
            print(f"  Certificate status: {msg} (checking again in 30s)")
            time.sleep(30)

        if not cert_active:
            print(f"{YELLOW}Certificate did not become active within 45 minutes.{RESET}")
            print(f"  Check manually: gcloud beta run domain-mappings describe --domain={domain} --region={region}")
        else:
            info("SSL certificate is ACTIVE")

        # Health check
        print(f"\nVerifying https://{domain}/api/health ...")
        health = run_quiet(
            ["curl", "-sf", "-o", "/dev/null", "-w", "%{http_code}", f"https://{domain}/api/health"],
        )
        if health.stdout.strip() == "200":
            info(f"https://{domain}/api/health → 200 OK")
        else:
            print(f"{YELLOW}Health check returned: {health.stdout.strip() or 'no response'}{RESET}")
            print("  The domain may still be propagating — you can re-run to retry.")

        domain_state["live"] = True
        state["phases"]["domain"] = domain_state
        save_state(state)

    info(f"Custom domain https://{domain} is live")
    mark_phase(state, "domain")


# ── Main ──────────────────────────────────────────────────────────────────

PHASES = [
    ("deps", phase_deps),
    ("auth", phase_auth),
    ("project", phase_project),
    ("bootstrap", phase_bootstrap),
    ("terraform", phase_terraform),
    ("secrets", phase_secrets),
    ("stripe", phase_stripe),
    ("build", phase_build),
    ("deploy", phase_deploy),
    ("domain", phase_domain),
]


def main() -> None:
    print(f"\n{BOLD}Sabermatic Cloud Bootstrap{RESET}")
    print("─" * 26)

    state = load_state()

    # Show resume info
    completed = [name for name, _ in PHASES if phase_done(state, name)]
    if completed:
        info(f"Resuming — completed phases: {', '.join(completed)}")

    # terraform always runs so it can detect and apply config drift
    ALWAYS_CHECK = {"terraform"}

    try:
        for name, fn in PHASES:
            if phase_done(state, name) and name not in ALWAYS_CHECK:
                continue
            fn(state)
    except KeyboardInterrupt:
        print(f"\n\n{YELLOW}Interrupted. State saved — re-run to resume.{RESET}")
        save_state(state)
        sys.exit(130)


if __name__ == "__main__":
    main()
