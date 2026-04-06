#!/usr/bin/env python3
"""Interactive GCP bootstrap for Sabermatic (Drill).

Walks through 9 phases from zero to a running Cloud Run service.
Resume-safe: completed phases are tracked in .cloud_bootstrap_state.

Usage: python3 scripts/cloud_bootstrap.py
"""

from __future__ import annotations

import base64
import getpass
import json
import secrets
import shutil
import subprocess
import sys
import textwrap
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

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
TOTAL_PHASES = 9


def load_state() -> dict:
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text())
    return {"phases": {}, "outputs": {}}


def save_state(state: dict) -> None:
    STATE_FILE.write_text(json.dumps(state, indent=2) + "\n")


def phase_done(state: dict, phase: str) -> bool:
    val = state["phases"].get(phase)
    if isinstance(val, dict):
        return all(val.values())
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
    """Run a subprocess, streaming output unless capture=True."""
    kwargs: dict = {
        "check": check,
        "cwd": cwd,
    }
    if capture:
        kwargs["capture_output"] = True
        kwargs["text"] = True
    if input_data is not None:
        if capture:
            kwargs["input"] = input_data
        else:
            kwargs["input"] = input_data.encode()
    return subprocess.run(cmd, **kwargs)


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

    project_id = prompt_value("GCP project ID", state.get("project_id", "sabermatic-prod"))
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
    elif "ALREADY_EXISTS" in (result.stderr or ""):
        info(f"Project {project_id} already exists")
    else:
        error(f"Failed to create project: {result.stderr}")
        sys.exit(1)

    run(["gcloud", "config", "set", "project", project_id])
    info(f"Active project set to {project_id}")

    # Enable billing
    print("\n  Billing must be enabled before we can create resources.\n")
    billing_url = f"https://console.cloud.google.com/billing/linkedaccount?project={project_id}"
    print(f"  Open this URL and link a billing account:")
    print(f"    {link(billing_url)}\n")
    prompt_continue("Press Enter when billing is enabled...")

    # Verify billing
    result = run_quiet([
        "gcloud", "billing", "projects", "describe", project_id,
        "--format=json",
    ])
    if result.returncode == 0:
        billing_info = json.loads(result.stdout)
        if billing_info.get("billingEnabled"):
            info("Billing is enabled")
        else:
            warn("Billing does not appear enabled. Continuing anyway — Terraform will fail if it's not.")
    else:
        warn("Could not verify billing status (API may not be enabled yet). Continuing.")

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

    # Write terraform.tfvars
    tfvars_content = textwrap.dedent(f"""\
        project_id  = "{project_id}"
        region      = "{region}"
        environment = "prod"
        github_repo = "{github_repo}"
    """)
    tfvars_path = Path(tf_dir) / "terraform.tfvars"
    tfvars_path.write_text(tfvars_content)
    info(f"Wrote {tfvars_path}")

    # Init
    state_bucket = f"{project_id}-tfstate"
    print("\nRunning terraform init...")
    run(["terraform", "init", f"-backend-config=bucket={state_bucket}"], cwd=tf_dir)
    info("Terraform initialized")

    # Plan
    print("\nRunning terraform plan...")
    print(f"{YELLOW}(Cloud SQL takes 5–10 minutes to provision){RESET}\n")
    run(["terraform", "plan", "-out=tfplan"], cwd=tf_dir)

    if not prompt_yes_no("\nApply this plan?"):
        warn("Skipping terraform apply. Re-run the script to try again.")
        sys.exit(0)

    # Apply
    print("\nApplying (this may take 5–10 minutes for Cloud SQL)...")
    run(["terraform", "apply", "tfplan"], cwd=tf_dir)
    info("Terraform apply complete")

    # Capture outputs
    for key in ["cloud_run_url", "sql_connection_name", "artifact_registry_url"]:
        result = run_quiet(["terraform", "output", "-raw", key], cwd=tf_dir)
        state["outputs"][key] = result.stdout.strip()

    info(f"Cloud Run URL: {state['outputs'].get('cloud_run_url', 'unknown')}")
    mark_phase(state, "terraform")


# ── Phase 6: Populate Secrets ─────────────────────────────────────────────

SECRETS = [
    # (secret_id, method, help_text, help_url)
    ("auth-token-secret", "auto", None, None),
    ("anthropic-api-key", "prompt", "Paste your Anthropic API key", None),
    ("openai-api-key", "prompt", "Paste your OpenAI API key", None),
    ("mailgun-api-key", "prompt", "Paste your Mailgun API key",
     "https://app.mailgun.com/settings/api_security"),
    ("mailgun-domain", "prompt", "Paste your Mailgun domain (e.g. mg.sabermatic.dev)",
     "https://app.mailgun.com/settings/api_security"),
    ("oauth-google-client-id", "prompt", "Paste Google OAuth client ID",
     "https://console.cloud.google.com/apis/credentials?project={project_id}"),
    ("oauth-google-client-secret", "prompt", "Paste Google OAuth client secret", None),
    ("oauth-github-client-id", "prompt", "Paste GitHub OAuth client ID",
     "https://github.com/settings/developers"),
    ("oauth-github-client-secret", "prompt", "Paste GitHub OAuth client secret", None),
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

        price_ids = state.get("stripe_price_ids", {})

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

        state["stripe_price_ids"] = price_ids
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
        price_ids = state.get("stripe_price_ids", {})
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
            "gcloud", "run", "services", "update", "drill",
            "--region", state["region"],
            f"--update-env-vars={env_vars}",
        ])
        info("Stripe price IDs set as Cloud Run env vars")

        stripe_state["env_vars"] = True
        state["phases"]["stripe"] = stripe_state
        save_state(state)


# ── Phase 8: Build & Push Container ───────────────────────────────────────


def phase_build(state: dict) -> None:
    header(8, TOTAL_PHASES, "Build & Push Container")

    region = state["region"]
    ar_url = state["outputs"].get("artifact_registry_url", "")
    if not ar_url:
        error("Artifact Registry URL not found. Run Phase 5 first.")
        sys.exit(1)

    image = f"{ar_url}/drill:latest"
    project_root = str(Path(__file__).resolve().parent.parent)

    # Configure Docker auth
    print("Configuring Docker auth for Artifact Registry...")
    run(["gcloud", "auth", "configure-docker", f"{region}-docker.pkg.dev", "--quiet"])
    info("Docker auth configured")

    # Build
    print(f"\nBuilding container image: {image}")
    run(["docker", "build", "-t", image, "."], cwd=project_root)
    info("Image built")

    # Push
    print("\nPushing to Artifact Registry...")
    run(["docker", "push", image])
    info("Image pushed")

    state["outputs"]["image"] = image
    mark_phase(state, "build")


# ── Phase 9: Deploy to Cloud Run ──────────────────────────────────────────


def phase_deploy(state: dict) -> None:
    header(9, TOTAL_PHASES, "Deploy to Cloud Run")

    region = state["region"]
    image = state["outputs"].get("image", "")
    if not image:
        error("No image found in state. Run Phase 8 first.")
        sys.exit(1)

    print(f"Deploying {image} to Cloud Run...")
    run([
        "gcloud", "run", "deploy", "drill",
        "--image", image,
        "--region", region,
    ])

    # Fetch the live URL
    result = run_quiet([
        "gcloud", "run", "services", "describe", "drill",
        "--region", region,
        "--format=value(status.url)",
    ])
    live_url = result.stdout.strip()
    state["outputs"]["cloud_run_url"] = live_url

    # Print summary
    price_ids = state.get("stripe_price_ids", {})
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
      - Point sabermatic.dev DNS to the Cloud Run URL
      - Set up GitHub Actions secrets for CI/CD:
          WIF_PROVIDER: {state['outputs'].get('wif_pool_id', 'unknown')}/providers/github
          DEPLOYER_SA:  {state['outputs'].get('deployer_sa', 'unknown')}
      - Update BASE_URL in cloud_run.tf to https://sabermatic.dev
    """))

    mark_phase(state, "deploy")


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
]


def main() -> None:
    print(f"\n{BOLD}Sabermatic Cloud Bootstrap{RESET}")
    print("─" * 26)

    state = load_state()

    # Show resume info
    completed = [name for name, _ in PHASES if phase_done(state, name)]
    if completed:
        info(f"Resuming — completed phases: {', '.join(completed)}")

    try:
        for name, fn in PHASES:
            if phase_done(state, name):
                continue
            fn(state)
    except KeyboardInterrupt:
        print(f"\n\n{YELLOW}Interrupted. State saved — re-run to resume.{RESET}")
        save_state(state)
        sys.exit(130)


if __name__ == "__main__":
    main()
