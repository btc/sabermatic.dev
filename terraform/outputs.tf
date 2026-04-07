output "cloud_run_url" {
  description = "Cloud Run service URL"
  value       = google_cloud_run_v2_service.sabermatic.uri
}

output "sql_connection_name" {
  description = "Cloud SQL connection name for Auth Proxy"
  value       = google_sql_database_instance.sabermatic.connection_name
}

output "artifact_registry_url" {
  description = "Artifact Registry Docker repository URL"
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.sabermatic.repository_id}"
}
