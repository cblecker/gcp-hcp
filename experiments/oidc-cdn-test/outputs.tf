output "load_balancer_ip" {
  description = "IP address of the load balancer"
  value       = google_compute_global_address.oidc.address
}

output "test_url" {
  description = "URL to test OIDC discovery document (HTTP-only, test use only)"
  value       = "http://${google_compute_global_address.oidc.address}/test-cluster/.well-known/openid-configuration"
}

output "bucket_name" {
  description = "Name of the created GCS bucket"
  value       = google_storage_bucket.oidc.name
}
