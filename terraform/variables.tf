variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region for all resources"
  type        = string
  default     = "us-central1"
}

variable "environment" {
  description = "Environment name, used in resource naming"
  type        = string
  default     = "prod"
}

variable "github_repo" {
  description = "GitHub repository in owner/repo format for WIF binding"
  type        = string
}
