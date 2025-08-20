terraform {
  required_version = ">= 1.2.4"

  backend "gcs" {
    bucket = "${TF_VAR_project_id}-terraform"
  }

  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.0"
    }
    google = {
      source  = "hashicorp/google"
      version = "6.45.0"
    }
  }
}

resource "google_project" "project" {
  name            = var.project_id
  project_id      = var.project_id
  org_id          = var.org_id
  billing_account = var.billing_account

  lifecycle {
    prevent_destroy = true
  }
}

locals {
  project_id  = google_project.project.project_id
  project_num = google_project.project.number
  region      = var.region

  default_services = toset([
    "aiplatform.googleapis.com",
    "artifactregistry.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "compute.googleapis.com",
    "config.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "logging.googleapis.com",
    "run.googleapis.com",
    "serviceusage.googleapis.com",
    "storage-api.googleapis.com",
    "storage.googleapis.com",
    "vision.googleapis.com"
  ])
}

resource "google_project_service" "service" {
  for_each = local.default_services

  service            = each.value
  project            = local.project_id
  disable_on_destroy = false
}

resource "google_storage_bucket" "terraform" {
  name                        = "${local.project_id}-terraform"
  project                     = local.project_id
  location                    = "US"
  uniform_bucket_level_access = true
  versioning {
    enabled = true
  }
}

resource "google_service_account" "vision" {
  account_id   = "vision"
  project      = local.project_id
  display_name = "Vision API Service Account"
}

resource "google_service_account_key" "vision" {
  service_account_id = google_service_account.vision.name
  public_key_type    = "TYPE_X509_PEM_FILE"
}

resource "local_file" "service_account_key" {
  content  = base64decode(google_service_account_key.vision.private_key)
  filename = var.key_file_path

  file_permission = "0600"
}
