# Analytics events pipeline:
#   Cloud Run stdout (slog "analytics_event":true)
#     -> Cloud Logging
#     -> Logs Router sink (filtered)
#     -> BigQuery dataset (raw LogEntry table, sink-managed schema)
#     -> BigQuery view (flattens jsonPayload.* to named columns)

resource "google_bigquery_dataset" "analytics" {
  dataset_id    = "sabermatic_analytics"
  friendly_name = "Sabermatic analytics events"
  location      = var.region
  description   = "Funnel-reconstruction events emitted from Cloud Run via Cloud Logging Logs Router."

  # Refuse a Terraform-side `terraform destroy` of this dataset — the sink
  # populates analytics_events_raw asynchronously and a destroy would lose
  # historical funnel data with no easy recovery path. Mirrors the
  # deletion_protection pattern on google_sql_database_instance.sabermatic.
  delete_contents_on_destroy = false
  lifecycle {
    prevent_destroy = true
  }
}

resource "google_logging_project_sink" "analytics" {
  name        = "sabermatic-analytics-events"
  description = "Routes slog analytics events from Cloud Run to BigQuery."
  destination = "bigquery.googleapis.com/projects/${var.project_id}/datasets/${google_bigquery_dataset.analytics.dataset_id}"

  # Note: jsonPayload.analytics_event is a JSON boolean (slog writes booleans
  # as native JSON booleans, not strings). Cloud Logging filter syntax
  # supports unquoted boolean literals; do NOT quote the value or the filter
  # will silently match zero entries.
  filter = <<-EOT
    resource.type = "cloud_run_revision"
    AND resource.labels.service_name = "sabermatic"
    AND jsonPayload.analytics_event = true
  EOT

  unique_writer_identity = true

  bigquery_options {
    use_partitioned_tables = true
  }
}

resource "google_bigquery_dataset_iam_member" "sink_writer" {
  dataset_id = google_bigquery_dataset.analytics.dataset_id
  role       = "roles/bigquery.dataEditor"
  member     = google_logging_project_sink.analytics.writer_identity
}

# Flattening view. Reads from the sink-managed table `analytics_events_raw`
# (created lazily by Logs Router on first matching log entry) and projects
# the slog payload fields into named columns. BQ permits view creation
# against a non-existent table; queries will return errors until the first
# write — that's expected.
resource "google_bigquery_table" "analytics_events" {
  dataset_id = google_bigquery_dataset.analytics.dataset_id
  table_id   = "analytics_events"

  # Match the dataset's prevent_destroy stance — the view is cheap to recreate
  # but `terraform destroy -target=google_bigquery_table.analytics_events`
  # would briefly drop query access while the BQ raw table is still flowing in.
  lifecycle {
    prevent_destroy = true
  }

  view {
    use_legacy_sql = false
    query          = <<-EOT
      SELECT
        timestamp                                                  AS event_time,
        jsonPayload.event_id                                       AS event_id,
        jsonPayload.event_name                                     AS event_name,
        jsonPayload.visitor_id                                     AS visitor_id,
        NULLIF(jsonPayload.user_id,    '')                         AS user_id,
        NULLIF(jsonPayload.session_id, '')                         AS session_id,
        REGEXP_EXTRACT(trace, r'traces/(.+)$')                     AS trace_id,
        NULLIF(jsonPayload.referer,      '')                       AS referer,
        NULLIF(jsonPayload.utm_source,   '')                       AS utm_source,
        NULLIF(jsonPayload.utm_medium,   '')                       AS utm_medium,
        NULLIF(jsonPayload.utm_campaign, '')                       AS utm_campaign,
        NULLIF(jsonPayload.path,         '')                       AS path,
        NULLIF(jsonPayload.user_agent,   '')                       AS user_agent,
        jsonPayload.properties                                     AS properties
      FROM `${var.project_id}.${google_bigquery_dataset.analytics.dataset_id}.analytics_events_raw`
      WHERE jsonPayload.analytics_event IS TRUE
    EOT
  }

  depends_on = [google_bigquery_dataset.analytics]
}
