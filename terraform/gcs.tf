resource "google_storage_bucket" "audio" {
  name     = "${var.project_id}-audio"
  location = var.region

  uniform_bucket_level_access = true
  storage_class               = "STANDARD"

  force_destroy = false
}

resource "google_storage_bucket" "public" {
  name     = "${var.project_id}-public"
  location = var.region

  uniform_bucket_level_access = true
  storage_class               = "STANDARD"

  force_destroy = false
}

# Public read access for question card images.
resource "google_storage_bucket_iam_member" "public_read" {
  bucket = google_storage_bucket.public.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}

# Service account write access for image uploads.
resource "google_storage_bucket_iam_member" "app_public" {
  bucket = google_storage_bucket.public.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.sabermatic_app.email}"
}
