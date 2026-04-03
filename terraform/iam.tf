# ---- Cloud Run runtime service account ----
resource "google_service_account" "drill_app" {
  account_id   = "drill-app"
  display_name = "Drill Cloud Run Runtime"
}

# Cloud SQL Auth Proxy
resource "google_project_iam_member" "app_cloudsql" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Secret Manager access
resource "google_project_iam_member" "app_secrets" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Cloud Trace
resource "google_project_iam_member" "app_trace" {
  project = var.project_id
  role    = "roles/cloudtrace.agent"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Cloud Monitoring metrics
resource "google_project_iam_member" "app_monitoring" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Cloud Logging
resource "google_project_iam_member" "app_logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# GCS audio bucket — scoped to bucket, not project-wide
resource "google_storage_bucket_iam_member" "app_audio" {
  bucket = google_storage_bucket.audio.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.drill_app.email}"
}
