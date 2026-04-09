locals {
  bucket_name = "oidc-cdn-test-${var.project_id}"
}

# Enable required APIs
resource "google_project_service" "compute" {
  project = var.project_id
  service = "compute.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service" "storage" {
  project = var.project_id
  service = "storage.googleapis.com"

  disable_on_destroy = false
}

# Look up project number for CDN fill service account
data "google_project" "this" {
  project_id = var.project_id
}

# Private GCS bucket with uniform access and public access prevention
resource "google_storage_bucket" "oidc" {
  project  = var.project_id
  name     = local.bucket_name
  location = var.region

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  force_destroy = true

  depends_on = [google_project_service.storage]
}

# Test OIDC discovery document
resource "google_storage_bucket_object" "openid_configuration" {
  bucket       = google_storage_bucket.oidc.name
  name         = "test-cluster/.well-known/openid-configuration"
  content_type = "application/json"
  content = jsonencode({
    issuer                                = "https://oidc.example.com/test-cluster"
    jwks_uri                              = "https://oidc.example.com/test-cluster/keys.json"
    authorization_endpoint                = "urn:kubernetes:programmatic_authorization"
    response_types_supported              = ["id_token"]
    subject_types_supported               = ["public"]
    id_token_signing_alg_values_supported = ["RS256"]
    claims_supported                      = ["sub", "iss"]
  })
}

# Test JWKS
resource "google_storage_bucket_object" "jwks" {
  bucket       = google_storage_bucket.oidc.name
  name         = "test-cluster/keys.json"
  content_type = "application/json"
  content = jsonencode({
    keys = [{
      kty = "RSA"
      alg = "RS256"
      use = "sig"
      kid = "test-key-id"
      n   = "test-modulus"
      e   = "AQAB"
    }]
  })
}

# Signing key for the backend bucket — adding a key triggers creation of the
# CDN fill service account (service-{PROJECT_NUMBER}@cloud-cdn-fill.iam.gserviceaccount.com).
resource "random_id" "cdn_sign_key" {
  byte_length = 16
}

resource "google_compute_backend_bucket_signed_url_key" "oidc" {
  project        = var.project_id
  name           = "oidc-cdn-test-key"
  key_value      = random_id.cdn_sign_key.b64_url
  backend_bucket = google_compute_backend_bucket.oidc.name
}

# Grant CDN fill service account objectViewer on the bucket
resource "google_storage_bucket_iam_member" "cdn_fill_viewer" {
  bucket = google_storage_bucket.oidc.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:service-${data.google_project.this.number}@cloud-cdn-fill.iam.gserviceaccount.com"

  depends_on = [google_compute_backend_bucket_signed_url_key.oidc]
}

# Static IP for the load balancer
resource "google_compute_global_address" "oidc" {
  project = var.project_id
  name    = "oidc-cdn-test-ip"

  depends_on = [google_project_service.compute]
}

# Backend bucket with CDN enabled
resource "google_compute_backend_bucket" "oidc" {
  project     = var.project_id
  name        = "oidc-cdn-test-backend"
  bucket_name = google_storage_bucket.oidc.name
  enable_cdn  = true

  depends_on = [google_project_service.compute]
}

# URL map routing all traffic to the backend bucket
resource "google_compute_url_map" "oidc" {
  project         = var.project_id
  name            = "oidc-cdn-test-url-map"
  default_service = google_compute_backend_bucket.oidc.id
}

# HTTP proxy (no SSL for test)
resource "google_compute_target_http_proxy" "oidc" {
  project = var.project_id
  name    = "oidc-cdn-test-http-proxy"
  url_map = google_compute_url_map.oidc.id
}

# Global forwarding rule on port 80
resource "google_compute_global_forwarding_rule" "oidc" {
  project    = var.project_id
  name       = "oidc-cdn-test-forwarding-rule"
  target     = google_compute_target_http_proxy.oidc.id
  ip_address = google_compute_global_address.oidc.address
  port_range = "80"
}
