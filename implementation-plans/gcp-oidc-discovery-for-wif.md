# GCP OIDC Discovery for Workload Identity Federation

**Epic**: [GCP-336 — Internal Secrets Management](https://redhat.atlassian.net/browse/GCP-336)
**Spike**: [GCP-588 — Signing key strategy for GCP WIF](https://redhat.atlassian.net/browse/GCP-588)
**Last Updated**: 2026-04-27

---

## Problem

GCP HostedClusters use Workload Identity Federation (WIF) so that in-cluster workloads can authenticate to GCP services without long-lived service account keys. For this to work, GCP's Security Token Service (STS) needs access to the cluster's OIDC public key material so it can validate the kube-apiserver's service account tokens.

GCP supports two ways to provide this key material to STS:

1. **Inline JWKS** — embed the public key directly in the WIF pool provider configuration
2. **Issuer URL discovery** — point the provider to a public OIDC endpoint where STS fetches the keys at token exchange time via `/.well-known/openid-configuration` and `/openid/v1/jwks`

Today we use option 1. The signing keypair is generated externally before any infrastructure exists. The public key (JWKS) is baked into the WIF provider via CLI, and the private key is passed to the HostedCluster at creation time. This couples the key lifecycle to both infrastructure setup and cluster provisioning, and makes key rotation non-trivial.

## Approach

Move to option 2: configure the WIF provider with an `issuerUri` and have the Hypershift operator generate the signing key and publish the OIDC documents to a GCS bucket. The bucket contents are served publicly at the configured issuer URL, allowing GCP to fetch the keys dynamically at token exchange time.

By managing key generation and OIDC publishing within the operator, key management is fully decoupled from infrastructure setup, cluster creation is simpler (no external keypair needed), the private key never leaves the management cluster, and the path to key rotation is straightforward. This mirrors the existing AWS pattern where the operator uploads OIDC documents to S3.

## Architecture

```
hypershift create iam gcp
      │
      │  (1) Creates WIF pool + provider with issuerUri (no inline JWKS)
      ▼
┌─────────────────────┐
│  WIF Pool Provider   │ ◄── issuerUri: https://oidc.{env}.gcp-hcp.devshift.net/{infraID}/
│  (GCP IAM)           │
└─────────────────────┘
                                ▲
                                │  (4) GCP STS fetches JWKS at token exchange
                                │
┌─────────────────────┐    ┌────────────────────┐
│  HostedCluster CR    │    │  GCS Bucket         │
│  (MC)                │    │  (public via proxy)  │
└────────┬────────────┘    └────────▲─────────────┘
         │                          │
         ▼                          │  (3) Upload JWKS + discovery doc
┌─────────────────────┐             │
│  hypershift-operator │─────────────┘
│  (on MC)             │
│                      │
│  reconcileGCPOIDCDocuments()
│   ├─ Extract public key from sa-signing-key secret
│   ├─ Generate JWKS + discovery doc
│   ├─ Upload to gs://{bucket}/{infraID}/
│   └─ Finalizer for cleanup on HC deletion
│
│  (2) CPO generates signing key → sa-signing-key secret
└─────────────────────┘
```


### Infrastructure Layout

```
┌───────────────────────────────────────────────────────────────────────┐
│  Regional Project                                                     │
│                                                                       │
│  ┌──────────────────────────┐       ┌──────────────────────────────┐ │
│  │ OIDC Proxy (GCP-621)     │──────▶│ GCS Bucket                   │ │
│  │ oidc.{regional_domain}   │       │ {project_id}-oidc            │ │
│  └──────────────────────────┘       └──────────────────────────────┘ │
│         ▲                                ▲                ▲          │
└─────────┼────────────────────────────────┼────────────────┼──────────┘
          │                                │                │
  STS fetches OIDC docs         uploads OIDC docs   uploads OIDC docs
          │                                │                │
┌─────────┴──────────┐    ┌────────────────┴───┐   ┌───────┴───────────────┐
│  Customer Project  │    │  MC 1              │   │  MC 2                │
│                    │    │                    │   │                      │
│  WIF Pool Provider │    │  Hypershift Op     │   │  Hypershift Op       │
│  (issuerUri)       │    │  HostedClusters    │   │  HostedClusters      │
│                    │    │  sa-signing-key    │   │  sa-signing-key      │
└────────────────────┘    └────────────────────┘   └──────────────────────┘
```

### GCS Bucket

The OIDC GCS bucket is provisioned in the **regional project**, shared across all management clusters in that region. Documents are namespaced by `{infraID}` (which is globally unique), so there is no collision across MCs.

| Aspect | Detail |
|--------|--------|
| **Bucket location** | Regional project |
| **Bucket naming** | `{regional_project_id}-oidc` |
| **Scope** | One bucket per region, shared across all MCs and HCs in that region |
| **Object layout** | `{infraID}/.well-known/openid-configuration` and `{infraID}/openid/v1/jwks` |
| **Write access** | Each MC's Hypershift operator GSA needs cross-project write access to the regional bucket |
| **Issuer URL** | `https://oidc.{regional_domain}/{infraID}/` |
| **Provisioning** | Terraform region module |
| **Lifecycle** | Operator adds a finalizer per HC; on deletion, objects are cleaned up before the finalizer is removed |

#### Why regional, not per-MC?

- The issuer URL is set during IAM setup, before cluster creation and before placement decides which MC will host the cluster
- The issuer URL must be stable and MC-agnostic — it cannot change if a cluster moves between MCs
- A single regional bucket and issuer URL simplifies DNS, certificate management, and infrastructure provisioning
- Multiple MCs in the same region can all write to one bucket, avoiding per-MC bucket sprawl

> **Note**: The GCS bucket is not publicly accessible due to org policy constraints. How the bucket contents are served at the public issuer URL is addressed separately in [GCP-621](https://redhat.atlassian.net/browse/GCP-621).

### WIF Provider: Before and After

**Current (inline JWKS)**:
```json
{
  "oidc": {
    "issuerUri": "https://hypershift-{infraID}-oidc",
    "jwksJson": "{\"keys\":[{...}]}",
    "allowedAudiences": ["openshift"]
  }
}
```

**New (issuer URL discovery)**:
```json
{
  "oidc": {
    "issuerUri": "https://oidc.{regional_domain}/{infraID}/",
    "jwksJson": "",
    "allowedAudiences": ["openshift"]
  }
}
```

GCP STS fetches `{issuerUri}/.well-known/openid-configuration` at token exchange time, then follows `jwks_uri` to get the public keys.


---

## Implementation Tasks

### 1. CLI: Make JWKS file optional — [GCP-635](https://redhat.atlassian.net/browse/GCP-635)

Update `hypershift create iam gcp` so that `--oidc-jwks-file` is optional when `--oidc-issuer-url` is provided. When JWKS is omitted, the WIF provider is configured with only `issuerUri` and GCP fetches keys from the discovery endpoint at runtime.

**Supported modes**:
- `--oidc-jwks-file` only — embeds JWKS inline (existing behavior, backward compatible)
- `--oidc-issuer-url` only — GCP fetches keys from issuer URL (new)
- Both flags — JWKS embedded inline with issuer URL set
- Neither — validation error

---

### 2. Controller: OIDC document upload — [GCP-636](https://redhat.atlassian.net/browse/GCP-636)

Extend the HostedCluster controller with a GCP OIDC reconciliation step. For each GCP-platform HostedCluster, the operator reads the public signing key from the `sa-signing-key` secret — which the CPO generates automatically when the user does not supply their own `ServiceAccountSigningKey` — produces the OIDC discovery and JWKS documents, and uploads them to the regional GCS bucket under a per-cluster prefix.

**Key design decisions**:
- Mirrors the existing `reconcileAWSOIDCDocuments` / `cleanupOIDCBucketData` pattern for AWS
- Skips upload when `ServiceAccountSigningKey` is set on the HC spec (external key management)
- Enabled via `--gcp-oidc-storage-bucket-name` flag; when absent, the reconciliation step is a no-op (backward compatible)
- Upload is idempotent (re-uploads overwrite existing objects); the finalizer avoids redundant writes by gating it to a single execution
- On HostedCluster deletion, the finalizer removes the per-cluster OIDC documents from GCS before releasing

---

### 3. Infrastructure: Operator permissions and GCS bucket — [GCP-637](https://redhat.atlassian.net/browse/GCP-637)

Three infrastructure changes to support the operator's OIDC upload:

**a. Operator configuration** — Add `--gcp-oidc-storage-bucket-name` to the Hypershift operator deployment via kustomize patch in ArgoCD configuration.

**b. IAM permissions** — Grant `roles/storage.objectCreator` and `roles/storage.objectViewer` to the operator's GSA on the OIDC GCS bucket. Codify as `IAMPolicyMember` Config Connector resources or Terraform.

**c. GCS bucket provisioning** — Provision the OIDC bucket via Terraform in the management-cluster module with public read access via OIDC proxy (see [GCP-621](https://redhat.atlassian.net/browse/GCP-621)).

---

## Out of Scope

- **OIDC proxy implementation** — The proxy that serves private GCS bucket contents at a public OIDC endpoint is tracked separately under [GCP-621](https://redhat.atlassian.net/browse/GCP-621).
- **Private signing key persistence** — The `sa-signing-key` private key currently lives only in the HostedControlPlane namespace. Durable storage of the private key (e.g., in Secret Manager) for cluster migration or key recovery scenarios is not addressed in this work.

---

## JIRA Stories

| Story | Points | Status | Depends on |
|-------|--------|--------|------------|
| [GCP-635](https://redhat.atlassian.net/browse/GCP-635) — CLI | 2 | PR ready | — |
| [GCP-636](https://redhat.atlassian.net/browse/GCP-636) — Controller | 5 | POC validated | GCP-635 |
| [GCP-637](https://redhat.atlassian.net/browse/GCP-637) — Infrastructure | 3 | POC manual | GCP-621 (OIDC proxy) |
| **Total** | **10** | | |

---

## Risk Mitigation

| Constraint | Mitigation |
|---|---|
| GCP org policy blocks public GCS buckets | OIDC proxy serves private bucket contents via public endpoint ([GCP-621](https://redhat.atlassian.net/browse/GCP-621)) |
| Signing key secret may not exist during early reconcile | Graceful handling — log and requeue until CPO creates it |

