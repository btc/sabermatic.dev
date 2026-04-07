resource "google_artifact_registry_repository" "sabermatic" {
  repository_id = "sabermatic"
  location      = var.region
  format        = "DOCKER"
  description   = "Sabermatic container images"
}
