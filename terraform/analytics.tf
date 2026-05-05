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

# Flattening view. Reads from the sink-managed table created by Logs Router.
#
# IMPORTANT: with `use_partitioned_tables = true`, Cloud Logging names the
# destination table after the source log stream, NOT after the sink. The
# Cloud Run app emits to stderr, so the sink writes to `run_googleapis_com_stderr`.
# (If the app ever switches to stdout, this view's FROM must be updated to
# `run_googleapis_com_stdout`, or use a UNION ALL across both.)
#
# The Terraform provider validates view queries at create time — the FROM
# table must exist. Bootstrap order: deploy the app, fire one event so the
# sink lazily creates the destination table, THEN terraform apply this view.
resource "google_bigquery_table" "analytics_events" {
  dataset_id = google_bigquery_dataset.analytics.dataset_id
  table_id   = "analytics_events"

  # Match the dataset's prevent_destroy stance — the view is cheap to recreate
  # but `terraform destroy -target=google_bigquery_table.analytics_events`
  # would briefly drop query access while the BQ raw table is still flowing in.
  # If you need to schema-iterate the view (e.g., add a column), update the
  # `query` field in place — Terraform applies in-place updates without
  # destroy/create. Use `terraform apply -replace=...` only as a last resort.
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
      FROM `${var.project_id}.${google_bigquery_dataset.analytics.dataset_id}.run_googleapis_com_stderr`
      WHERE jsonPayload.analytics_event IS TRUE
    EOT
  }

  depends_on = [google_bigquery_dataset.analytics]
}
