variable "project_id" {
  description = "The Google Cloud project ID to deploy to"
  type        = string
  validation {
    condition     = var.project_id != ""
    error_message = "Error: project_id is required."
  }
}

variable "org_id" {
  description = "The Google Cloud Organization ID to put the project in"
  type        = string
  validation {
    condition     = var.org_id != ""
    error_message = "Error: org_id is required."
  }
}

variable "billing_account" {
  description = "The Google Cloud billing account ID to attach to the project"
  type        = string
  validation {
    condition     = var.billing_account != ""
    error_message = "Error: billing_account is required."
  }
}

variable "region" {
  description = "The Google Cloud region to deploy to"
  type        = string
  default     = "us-east5"
}

variable "key_file_path" {
  description = "Full path to save the GSA key to. Used to make calls to Google Cloud Vision API"
  type        = string
  validation {
    condition     = var.key_file_path != ""
    error_message = "Error: key_file_path is required."
  }
}
