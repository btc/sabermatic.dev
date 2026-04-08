resource "google_cloud_run_v2_service" "sabermatic" {
  name     = "sabermatic"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.sabermatic_app.email

    scaling {
      max_instance_count = 2
    }

    max_instance_request_concurrency = 100
    timeout                          = "3600s"

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.sabermatic.connection_name]
      }
    }

    containers {
      # Placeholder image — first CI deploy will replace this.
      # Use a known-good public image that serves HTTP on 8080.
      image = "us-docker.pkg.dev/cloudrun/container/hello"

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      startup_probe {
        http_get {
          path = "/api/health"
          port = 8080
        }
        initial_delay_seconds = 5
        period_seconds        = 5
        failure_threshold     = 3
        timeout_seconds       = 3
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      # ---- Non-secret env vars ----
      env {
        name  = "SERVER_PORT"
        value = "8080"
      }
      env {
        name  = "SERVER_SHUTDOWN_TIMEOUT_SEC"
        value = "30"
      }
      env {
        name  = "BASE_URL"
        value = "https://sabermatic.dev"
      }
      env {
        name  = "OTEL_ENABLED"
        value = "true"
      }
      env {
        name  = "OTEL_EXPORTER"
        value = "google"
      }
      env {
        name  = "OTEL_SAMPLE_RATE"
        value = "1.0"
      }
      env {
        name  = "OTEL_SERVICE_NAME"
        value = "sabermatic"
      }
      env {
        name  = "EMAIL_FROM"
        value = "noreply@sabermatic.dev"
      }

      env {
        name  = "STORAGE_BACKEND"
        value = "gcs"
      }
      env {
        name  = "STORAGE_BUCKET"
        value = "${var.project_id}-audio"
      }
      env {
        name  = "PUBLIC_STORAGE_BUCKET"
        value = google_storage_bucket.public.name
      }
      env {
        name  = "GOOGLE_CLOUD_PROJECT"
        value = var.project_id
      }
      env {
        name  = "GEMINI_MODEL"
        value = "gemini-3.1-flash-image-preview"
      }
      env {
        name  = "GEMINI_LOCATION"
        value = var.region
      }

      # ---- Secret env vars ----
      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["database-url"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "AUTH_TOKEN_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["auth-token-secret"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "ANTHROPIC_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["anthropic-api-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "OPENAI_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["openai-api-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "MAILGUN_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["mailgun-api-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "MAILGUN_DOMAIN"
        value = var.mailgun_domain
      }
      env {
        name  = "OAUTH_GOOGLE_CLIENT_ID"
        value = var.oauth_google_client_id
      }
      env {
        name = "OAUTH_GOOGLE_CLIENT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-google-client-secret"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "OAUTH_GITHUB_CLIENT_ID"
        value = var.oauth_github_client_id
      }
      env {
        name = "OAUTH_GITHUB_CLIENT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-github-client-secret"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "STRIPE_SECRET_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["stripe-secret-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "STRIPE_WEBHOOK_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["stripe-webhook-secret"].secret_id
            version = "latest"
          }
        }
      }

      # ---- Stripe price IDs (non-secret) ----
      env {
        name  = "STRIPE_PRO_PRICE_ID"
        value = var.stripe_pro_price_id
      }
      env {
        name  = "STRIPE_PACK_120_PRICE_ID"
        value = var.stripe_pack_120_price_id
      }
      env {
        name  = "STRIPE_PACK_300_PRICE_ID"
        value = var.stripe_pack_300_price_id
      }
      env {
        name  = "STRIPE_PACK_600_PRICE_ID"
        value = var.stripe_pack_600_price_id
      }
    }
  }

  lifecycle {
    ignore_changes = [
      # CI/gcloud stamps client metadata after every deploy — don't fight it.
      client,
      client_version,
      # CI updates the image via gcloud run deploy — don't fight it.
      template[0].containers[0].image,
      # Provider bug: GCP always returns top-level scaling with manual_instance_count=0.
      scaling,
    ]
  }

  depends_on = [
    google_secret_manager_secret_version.db_password,
  ]
}

# Public access — auth is handled by the application layer.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.sabermatic.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
