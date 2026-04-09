# Prow CI Must Use Workload Identity Federation for GCP Access

***Scope***: GCP-HCP

**Date**: 2026-04-07

## Decision

All Prow CI jobs that access GCP resources must authenticate exclusively via Workload Identity Federation (WIF), using the CI cluster's OIDC issuer as the identity provider. Static GCP service account JSON keys are prohibited for CI workloads.

## Context

- **Problem Statement**: Prow CI jobs running on the OpenShift CI build farm need to authenticate to GCP APIs (e.g., to create GKE clusters, manage Terraform state, push container images). The traditional approach uses static GCP service account JSON keys stored as Kubernetes secrets on the CI cluster. These keys are long-lived, difficult to rotate, and create a large blast radius if leaked — any holder of the key can impersonate the service account with no expiration.
- **Constraints**:
  - CI jobs run on build farm clusters (build01-build11), not on app.ci directly. The dispatcher assigns jobs to clusters based on capabilities and load.
  - These clusters are shared infrastructure managed by the TRT/DPTP team; we do not control their lifecycle or security posture
  - CI jobs run in ephemeral pods with projected service account tokens signed by the cluster's kube-apiserver
  - WIF requires the cluster's OIDC issuer to be publicly accessible. AWS build clusters (build01, build03, build05-07, build09-11) have public OIDC endpoints (S3/CloudFront). GCP build clusters (build04, build08) use `kubernetes.default.svc` as issuer — WIF does NOT work from these clusters.
  - Each cluster has a different OIDC issuer URL; one WIF provider per cluster is needed in the GCP Workload Identity Pool
  - GCP Workload Identity Federation supports OIDC providers natively
- **Assumptions**:
  - Build cluster OIDC issuers remain stable and publicly accessible
  - GCP projects used by CI have IAM APIs enabled and can create Workload Identity Pools
  - CI jobs requiring WIF are dispatched only to clusters with public OIDC (see dispatcher capability below)

## Alternatives Considered

1. **Static GCP service account JSON keys**: Generate a JSON key for a GCP service account, store it as a Kubernetes secret on the CI cluster, and mount it into CI job pods.
2. **Workload Identity Federation (OIDC)**: Configure a GCP Workload Identity Pool trusting the CI cluster's OIDC issuer. CI pods exchange their projected SA tokens for short-lived GCP federated tokens via the STS endpoint.
3. **OAuth2 / Application Default Credentials with external token broker**: Deploy a platform-side token broker service that CI jobs call to obtain GCP credentials.

## Decision Rationale

* **Justification**: WIF eliminates static credentials entirely. Federated tokens are short-lived (~1h), scoped to a specific service account identity (`system:serviceaccount:<ns>:<sa>`), and cannot be used outside the token exchange flow. This dramatically reduces blast radius compared to JSON keys that grant indefinite access.
* **Evidence**: We validated the full flow on `app.ci` — discovered the OIDC issuer, created a WIF pool/provider in `patmarti-1`, and successfully exchanged a projected SA token for a GCP federated access token. We also inventoried all 11 build clusters and confirmed 8 of them (all AWS) have public OIDC endpoints compatible with WIF. See [rosa-to-gcp-wif study](../studies/rosa-to-gcp-wif.md) for the complete walkthrough and inventory.
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

* Initial setup requires creating WIF pools/providers per build cluster and IAM bindings per GCP project (one-time cost). Each cluster has a different OIDC issuer, so the WIF pool needs one provider per cluster.
* WIF only works from clusters with a public OIDC endpoint. Currently 8 of 11 build clusters qualify (all AWS). GCP build clusters (build04, build08) and vsphere02 have private issuers (`kubernetes.default.svc`) and cannot use WIF.
* Jobs must be dispatched to WIF-capable clusters only. Temporarily using the `intranet` capability (AWS-only) as a proxy until GCP build clusters are migrated to STS.
* Dependency on the CI cluster's OIDC endpoint availability (S3 bucket / CloudFront) — if the OIDC endpoint is unreachable, GCP cannot verify tokens
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

## Implementation Requirements

### Dispatcher capability

Jobs requiring WIF must only run on clusters with a public OIDC endpoint. After discussing with the DPTP team ([Slack thread](https://redhat-internal.slack.com/archives/CBN38N3MW/p1775592706242139)), they advised against adding a new semantic capability (`public-oidc`) to avoid bloating the capability list. Instead:

**Temporary workaround**: Use the existing `intranet` capability, which is only assigned to AWS clusters. Since all AWS clusters have public OIDC (as a consequence of the STS migration), this effectively limits jobs to WIF-capable clusters. GCP build clusters do not have the `intranet` capability and are unlikely to get it.

```yaml
labels:
  capability/intranet: intranet
```

This is a pragmatic short-term solution. Once the GCP build clusters are migrated to STS (see below), the `intranet` requirement can be removed.

### WIF pool with per-cluster providers

A single GCP Workload Identity Pool should contain one OIDC provider per build cluster. IAM bindings are pool-scoped, so they apply regardless of which cluster the job runs on.

### GCP build cluster STS migration

The AWS build clusters gained public OIDC issuers as a consequence of their STS migration, which was completed over the past year by the DPTP team. The GCP build clusters (build04, build08) were never migrated to STS.

The DPTP team confirmed that GCP/OCP does support STS (`credentialsMode: Manual` with `ccoctl`) and agreed to look into migrating the GCP build clusters. This is tracked in [DPTP-4758](https://redhat.atlassian.net/browse/DPTP-4758). Once the GCP clusters have public OIDC endpoints, the `intranet` capability workaround can be removed and WIF will work across all build clusters.
