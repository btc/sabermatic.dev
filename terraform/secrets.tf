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

# Store the generated DB password so the DATABASE_URL secret can reference it.
# The full DATABASE_URL must still be set manually (includes socket path).
resource "google_secret_manager_secret_version" "db_password" {
  secret      = google_secret_manager_secret.secrets["database-url"].id
  secret_data = "postgres://drill:${random_password.db_password.result}@/drill?host=/cloudsql/${google_sql_database_instance.drill.connection_name}"
}
