locals {
  secret_ids = [
    "database-url",
    "auth-token-secret",
    "anthropic-api-key",
    "openai-api-key",
    "mailgun-api-key",
    "mailgun-domain",
    "oauth-google-client-id",
    "oauth-google-client-secret",
    "oauth-github-client-id",
    "oauth-github-client-secret",
  ]
}

resource "google_secret_manager_secret" "secrets" {
  for_each  = toset(local.secret_ids)
  secret_id = each.key

  replication {
    auto {}
  }
}

# Auto-populate DATABASE_URL with the Cloud SQL Auth Proxy socket path.
resource "google_secret_manager_secret_version" "db_password" {
  secret      = google_secret_manager_secret.secrets["database-url"].id
  secret_data = "postgres://drill:${random_password.db_password.result}@/drill?host=/cloudsql/${google_sql_database_instance.drill.connection_name}"
}

# Placeholder versions so Cloud Run can mount "latest" on first deploy.
# Replace with real values via gcloud or the console before the app will work.
locals {
  placeholder_secret_ids = toset([
    for id in local.secret_ids : id if id != "database-url"
  ])
}

resource "google_secret_manager_secret_version" "placeholders" {
  for_each    = local.placeholder_secret_ids
  secret      = google_secret_manager_secret.secrets[each.key].id
  secret_data = "REPLACE_ME"
}
