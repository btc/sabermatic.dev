output "cloud_run_url" {
  description = "Cloud Run service URL"
  value       = google_cloud_run_v2_service.drill.uri
}

output "sql_connection_name" {
  description = "Cloud SQL connection name for Auth Proxy"
  value       = google_sql_database_instance.drill.connection_name
}

output "artifact_registry_url" {
  description = "Artifact Registry Docker repository URL"
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.drill.repository_id}"
}
