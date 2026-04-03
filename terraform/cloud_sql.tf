resource "google_sql_database_instance" "drill" {
  name             = "drill-${var.environment}"
  database_version = "POSTGRES_16"
  region           = var.region

  settings {
    tier              = "db-f1-micro"
    disk_size         = 10
    disk_type         = "PD_SSD"
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
      ipv4_enabled = true
      require_ssl  = true
    }
  }

  deletion_protection = true
}

resource "google_sql_database" "drill" {
  name     = "drill"
  instance = google_sql_database_instance.drill.name
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "google_sql_user" "drill" {
  name     = "drill"
  instance = google_sql_database_instance.drill.name
  password = random_password.db_password.result
}
