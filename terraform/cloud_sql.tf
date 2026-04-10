resource "google_sql_database_instance" "sabermatic" {
  name             = var.project_id
  database_version = "POSTGRES_16"
  region           = var.region

  settings {
    edition           = "ENTERPRISE"
    tier              = "db-g1-small"
    disk_size         = 10
    disk_type         = "PD_HDD"
    disk_autoresize   = false
    availability_type = "ZONAL"

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = false
      start_time                     = "03:00"
      transaction_log_retention_days = 7
      backup_retention_settings {
        retained_backups = 7
      }
    }

    ip_configuration {
      ipv4_enabled    = true
      ssl_mode        = "ENCRYPTED_ONLY"
    }
  }

  deletion_protection = true
}

resource "google_sql_database" "sabermatic" {
  name     = "sabermatic"
  instance = google_sql_database_instance.sabermatic.name
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "google_sql_user" "sabermatic" {
  name     = "sabermatic"
  instance = google_sql_database_instance.sabermatic.name
  password = random_password.db_password.result
}