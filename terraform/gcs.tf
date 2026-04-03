resource "google_storage_bucket" "audio" {
  name     = "${var.project_id}-audio"
  location = var.region

  uniform_bucket_level_access = true
  storage_class               = "STANDARD"

  force_destroy = false
}
