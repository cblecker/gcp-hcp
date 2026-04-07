# Prow CI Must Use Workload Identity Federation for GCP Access

***Scope***: GCP-HCP

**Date**: 2026-04-07

## Decision

All Prow CI jobs that access GCP resources must authenticate exclusively via Workload Identity Federation (WIF), using the CI cluster's OIDC issuer as the identity provider. Static GCP service account JSON keys are prohibited for CI workloads.

## Context

- **Problem Statement**: Prow CI jobs running on the `app.ci` ROSA cluster need to authenticate to GCP APIs (e.g., to create GKE clusters, manage Terraform state, push container images). The traditional approach uses static GCP service account JSON keys stored as Kubernetes secrets on the CI cluster. These keys are long-lived, difficult to rotate, and create a large blast radius if leaked — any holder of the key can impersonate the service account with no expiration.
- **Constraints**:
  - The CI cluster is shared infrastructure (`app.ci`) managed by the TRT/DPTP team; we do not control its lifecycle or security posture
  - CI jobs run in ephemeral pods with projected service account tokens signed by the cluster's kube-apiserver
  - The cluster's OIDC issuer (`https://ci-dv2np-oidc.s3.amazonaws.com`) is publicly accessible and serves JWKS for token verification
  - GCP Workload Identity Federation supports OIDC providers natively
- **Assumptions**:
  - The `app.ci` cluster's OIDC issuer remains stable and publicly accessible
  - GCP projects used by CI have IAM APIs enabled and can create Workload Identity Pools
  - CI job pods can request projected tokens with custom audiences via `oc create token` or volume projection

## Alternatives Considered

1. **Static GCP service account JSON keys**: Generate a JSON key for a GCP service account, store it as a Kubernetes secret on the CI cluster, and mount it into CI job pods.
2. **Workload Identity Federation (OIDC)**: Configure a GCP Workload Identity Pool trusting the CI cluster's OIDC issuer. CI pods exchange their projected SA tokens for short-lived GCP federated tokens via the STS endpoint.
3. **OAuth2 / Application Default Credentials with external token broker**: Deploy a platform-side token broker service that CI jobs call to obtain GCP credentials.

## Decision Rationale

* **Justification**: WIF eliminates static credentials entirely. Federated tokens are short-lived (~1h), scoped to a specific service account identity (`system:serviceaccount:<ns>:<sa>`), and cannot be used outside the token exchange flow. This dramatically reduces blast radius compared to JSON keys that grant indefinite access.
* **Evidence**: We validated the full flow on `app.ci` — discovered the OIDC issuer, created a WIF pool/provider in `patmarti-1`, and successfully exchanged a projected SA token for a GCP federated access token. See [rosa-to-gcp-wif study](../studies/rosa-to-gcp-wif.md) for the complete walkthrough.
* **Comparison**:
  - **Static JSON keys** are the simplest to set up but the weakest security posture. A leaked key grants permanent access until manually rotated. Keys stored on shared CI infrastructure are accessible to anyone with namespace access. Rotation requires coordinated secret updates across all consuming jobs.
  - **External token broker** adds operational complexity (a new service to build, deploy, and maintain) without clear benefit over native WIF, which is a first-party GCP capability requiring no custom infrastructure.
  - **WIF** is native to GCP, requires no custom services, produces short-lived tokens, and enforces identity at the pod/SA level via GCP IAM.

## Consequences

### Positive

* No static credentials to leak, rotate, or manage — tokens are ephemeral and scoped
* Fine-grained access control: IAM bindings target specific K8s service accounts (`system:serviceaccount:<ns>:<sa>`), not broad service account keys
* Audit trail: GCP Cloud Audit Logs show the federated identity (including K8s namespace and SA name) for every API call
* Consistent with how GCP HCP production workloads authenticate (WIF is the standard pattern across the platform)
* No secret rotation burden — projected tokens are automatically refreshed by the kubelet

### Negative

* Initial setup requires creating WIF pools/providers and IAM bindings per GCP project (one-time cost)
* The WIF provider's `allowed-audiences` is set to the OIDC issuer URL, which matches the default projected token audience — no special configuration needed in CI job manifests
* Dependency on the CI cluster's OIDC endpoint availability (S3 bucket) — if the OIDC endpoint is unreachable, GCP cannot verify tokens
* Slightly more complex debugging: token exchange failures require understanding of OIDC/STS flow

## Cross-Cutting Concerns

### Security:

* Federated tokens are short-lived (~1h) and non-replayable outside the STS exchange, minimizing credential exposure window
* IAM bindings enforce least privilege at the K8s service account level — each CI job can be granted only the GCP permissions it needs
* No secrets stored on the CI cluster — eliminates the risk class of secret exfiltration from shared infrastructure
* The OIDC endpoint only serves public keys (JWKS) and discovery metadata — no private material is exposed

### Cost:

* WIF and STS token exchanges are free — no additional GCP charges
* Eliminates operational cost of secret rotation workflows

### Operability:

* Zero friction for CI job authors: the WIF provider accepts the default projected token audience (the OIDC issuer URL), so no special `--audience` flag is needed
* WIF pool/provider setup is a one-time operation per GCP project; documented in the [rosa-to-gcp-wif study](../studies/rosa-to-gcp-wif.md)
* Troubleshooting guide provided in the study doc covers common failure modes (audience mismatch, issuer mismatch, missing IAM bindings)
