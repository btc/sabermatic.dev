resource "google_artifact_registry_repository" "drill" {
  repository_id = "drill"
  location      = var.region
  format        = "DOCKER"
  description   = "Drill container images"
}
