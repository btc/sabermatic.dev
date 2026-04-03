resource "google_cloud_run_v2_service" "drill" {
  name     = "drill"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.drill_app.email

    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }

    max_instance_request_concurrency = 100
    timeout                          = "3600s"

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.drill.connection_name]
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
        value = "https://drill-${var.environment}" # Updated after first deploy with actual URL
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
        value = "drill"
      }
      env {
        name  = "EMAIL_FROM"
        value = "noreply@drill.dev"
      }

      env {
        name  = "STORAGE_BACKEND"
        value = "gcs"
      }
      env {
        name  = "STORAGE_BUCKET"
        value = "${var.project_id}-audio"
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
        name = "MAILGUN_DOMAIN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["mailgun-domain"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "OAUTH_GOOGLE_CLIENT_ID"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-google-client-id"].secret_id
            version = "latest"
          }
        }
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
        name = "OAUTH_GITHUB_CLIENT_ID"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-github-client-id"].secret_id
            version = "latest"
          }
        }
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
    }
  }

  lifecycle {
    ignore_changes = [
      # CI updates the image via gcloud run deploy — don't fight it.
      template[0].containers[0].image,
    ]
  }

  depends_on = [
    google_secret_manager_secret_version.db_password,
  ]
}

# Public access — auth is handled by the application layer.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.drill.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
