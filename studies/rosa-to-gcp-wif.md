# Federated Authentication from OpenShift/ROSA to GCP via Workload Identity Federation

## TL;DR

OpenShift clusters with `credentialsMode: Manual` (STS) publish their OIDC discovery document and JWKS at a public URL (S3 bucket or CloudFront). GCP Workload Identity Federation can trust this OIDC issuer, allowing pods on the cluster to authenticate to GCP without static credentials.

**Important**: Not all clusters have a public OIDC endpoint. Clusters installed without STS (e.g., GCP-hosted build clusters) use `kubernetes.default.svc` as issuer, which is not publicly accessible. WIF only works from clusters with a publicly reachable OIDC issuer. See the [build farm inventory](#build-farm-cluster-inventory) for details.

**Setup in 3 steps:**

1. **Discover the cluster's OIDC issuer** (works without cluster-admin):
   ```bash
   oc get --raw /.well-known/openid-configuration | python3 -m json.tool
   # Look for the "issuer" field — e.g. https://some-oidc-bucket.s3.amazonaws.com
   ```

2. **Create a GCP Workload Identity Pool and OIDC provider** trusting that issuer:
   ```bash
   gcloud iam workload-identity-pools create <POOL_NAME> \
     --project=<GCP_PROJECT> \
     --location=global \
     --display-name="<description>"

   gcloud iam workload-identity-pools providers create-oidc <PROVIDER_NAME> \
     --project=<GCP_PROJECT> \
     --location=global \
     --workload-identity-pool=<POOL_NAME> \
     --issuer-uri="<OIDC_ISSUER_URL>" \
     --allowed-audiences="<OIDC_ISSUER_URL>" \
     --attribute-mapping="google.subject=assertion.sub"
   ```

3. **Grant IAM permissions** to the federated identity so it can access GCP resources:
   ```bash
   gcloud iam service-accounts add-iam-policy-binding <GCP_SA>@<PROJECT>.iam.gserviceaccount.com \
     --project=<GCP_PROJECT> \
     --role=roles/iam.workloadIdentityUser \
     --member="principal://iam.googleapis.com/projects/<PROJECT_NUMBER>/locations/global/workloadIdentityPools/<POOL>/subject/system:serviceaccount:<NAMESPACE>:<SA_NAME>"
   ```

That's it. Pods using that service account can now exchange their projected tokens for GCP access tokens via the STS endpoint.

---

## Problem Statement

CI jobs running on OpenShift clusters (e.g., Prow jobs on the `app.ci` ROSA cluster) need to authenticate to GCP APIs. The traditional approach uses static GCP service account JSON keys stored as secrets, which are long-lived, hard to rotate, and increase blast radius if leaked.

Workload Identity Federation (WIF) eliminates static credentials by letting GCP trust the cluster's native OIDC provider to verify service account tokens.

## How It Works

### OIDC on OpenShift/ROSA

Every OpenShift cluster has a kube-apiserver that signs projected service account tokens with a private key. The corresponding public key is published via JWKS. However, **where** the OIDC discovery document and JWKS are hosted depends on how the cluster was installed:

- **Clusters with `credentialsMode: Manual` (STS/WIF)**: The OIDC discovery document and JWKS are hosted at a **public URL** — typically an S3 bucket (e.g. `<cluster>-oidc.s3.<region>.amazonaws.com`) or CloudFront distribution. This is set up by `ccoctl` during installation. **WIF works from these clusters.**
- **ROSA/STS clusters**: Same as above — ROSA with STS automatically creates a public S3 OIDC bucket.
- **Clusters without STS** (e.g., GCP-hosted OCP, vSphere): The issuer defaults to `https://kubernetes.default.svc`, which is an internal-only address. **WIF does NOT work from these clusters** because GCP cannot reach the OIDC endpoint to verify token signatures.

The OIDC issuer URL is baked into the cluster at install time and cannot be changed post-install without reinstalling.

### Token flow

```text
OpenShift Cluster                          GCP
┌─────────────────────┐                    ┌──────────────────────┐
│ Pod with SA token    │                    │ Workload Identity    │
│ (projected, signed   │                    │ Pool + OIDC Provider │
│  by kube-apiserver)  │                    │ (trusts cluster's    │
│                      │                    │  OIDC issuer)        │
│  aud: <issuer URL>   │                    │                      │
│  iss: <S3 bucket>    │                    │                      │
│  sub: system:sa:...  │                    │                      │
└──────────┬───────────┘                    └──────────┬───────────┘
           │                                           │
           │  1. POST token to STS                     │
           │     sts.googleapis.com/v1/token            │
           ├──────────────────────────────────────────►│
           │                                           │
           │  2. GCP fetches JWKS from S3 bucket       │
           │     (verifies token signature)            │
           │                                           │
           │  3. Returns federated access_token        │
           │◄──────────────────────────────────────────┤
           │                                           │
           │  4. Use access_token to call GCP APIs     │
           ├──────────────────────────────────────────►│
           │                                           │
```

### Key concepts

- **OIDC Issuer**: A URL pointing to the public OIDC discovery document. For ROSA/STS, this is an S3 bucket URL.
- **JWKS (JSON Web Key Set)**: The public keys used to verify token signatures. Published at the issuer URL under `/keys.json`.
- **Projected Service Account Token**: A short-lived JWT issued by the kube-apiserver with a specific audience. This is NOT the same as the legacy long-lived SA token.
- **Audience**: The `aud` claim in the token. Must match the `allowed-audiences` configured on the GCP OIDC provider. By default, projected SA tokens use the OIDC issuer URL as the audience. Setting `allowed-audiences` to the issuer URL means no special audience configuration is needed — it works out of the box.
- **Subject**: The `sub` claim in the token. For K8s service accounts, the format is `system:serviceaccount:<namespace>:<name>`.

## Discovering the OIDC Issuer

### Method 1: OIDC discovery endpoint (no special permissions needed)

```bash
oc get --raw /.well-known/openid-configuration | python3 -m json.tool
```

Example output:

```json
{
    "issuer": "https://ci-dv2np-oidc.s3.amazonaws.com",
    "jwks_uri": "https://10.0.137.108:6443/openid/v1/jwks",
    "response_types_supported": ["id_token"],
    "subject_types_supported": ["public"],
    "id_token_signing_alg_values_supported": ["RS256"]
}
```

The `issuer` field is what you need. Note that `jwks_uri` may point to the internal API server IP — GCP uses the public JWKS at the issuer URL instead.

### Method 2: Authentication cluster resource (requires cluster-admin)

```bash
oc get authentication cluster -o jsonpath='{.spec.serviceAccountIssuer}'
```

### Verifying the public OIDC endpoint

The issuer URL should be publicly accessible:

```bash
# Discovery document
curl -s https://<issuer-url>/.well-known/openid-configuration | python3 -m json.tool

# Public keys
curl -s https://<issuer-url>/keys.json | python3 -m json.tool
```

## GCP Setup

### Creating the Workload Identity Pool

A pool groups related external identities. One pool can serve multiple clusters or one pool per cluster — depending on your isolation needs.

```bash
gcloud iam workload-identity-pools create <POOL_NAME> \
  --project=<GCP_PROJECT> \
  --location=global \
  --display-name="<description>"
```

### Creating the OIDC Provider

Each provider trusts a specific OIDC issuer (i.e., a specific cluster):

```bash
gcloud iam workload-identity-pools providers create-oidc <PROVIDER_NAME> \
  --project=<GCP_PROJECT> \
  --location=global \
  --workload-identity-pool=<POOL_NAME> \
  --issuer-uri="<OIDC_ISSUER_URL>" \
  --allowed-audiences="<OIDC_ISSUER_URL>" \
  --attribute-mapping="google.subject=assertion.sub"
```

Key parameters:
- `--issuer-uri`: The OIDC issuer URL discovered in the previous step.
- `--allowed-audiences`: Must match the `aud` claim in the token. Setting this to the OIDC issuer URL matches the default projected token audience, requiring no special configuration in CI jobs.
- `--attribute-mapping`: Maps JWT claims to Google attributes. `google.subject=assertion.sub` maps the K8s SA subject (`system:serviceaccount:namespace:name`) to the Google principal.

### Granting IAM permissions

The federated identity alone proves who the caller is but grants no permissions. You must bind the identity to a GCP service account or grant it direct resource access.

**Option A: Service account impersonation**

```bash
gcloud iam service-accounts add-iam-policy-binding <GCP_SA>@<PROJECT>.iam.gserviceaccount.com \
  --project=<GCP_PROJECT> \
  --role=roles/iam.workloadIdentityUser \
  --member="principal://iam.googleapis.com/projects/<PROJECT_NUMBER>/locations/global/workloadIdentityPools/<POOL>/subject/system:serviceaccount:<NAMESPACE>:<SA_NAME>"
```

**Option B: Direct resource access**

```bash
gcloud projects add-iam-policy-binding <GCP_PROJECT> \
  --role=roles/storage.objectViewer \
  --member="principal://iam.googleapis.com/projects/<PROJECT_NUMBER>/locations/global/workloadIdentityPools/<POOL>/subject/system:serviceaccount:<NAMESPACE>:<SA_NAME>"
```

## Testing & Validation

### Step 1: Create a test service account and get a token

```bash
oc create serviceaccount <SA_NAME> -n <NAMESPACE>
TOKEN=$(oc create token <SA_NAME> -n <NAMESPACE> --duration=3600s)
```

### Step 2: Decode the token to verify claims

```bash
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | python3 -m json.tool
```

Expected claims:

```json
{
    "aud": ["https://<issuer-url>"],
    "iss": "https://<issuer-url>",
    "sub": "system:serviceaccount:<namespace>:<sa-name>",
    "kubernetes.io": {
        "namespace": "<namespace>",
        "serviceaccount": { "name": "<sa-name>" }
    }
}
```

Verify that:
- `iss` matches the OIDC issuer URL configured in the GCP provider
- `aud` contains the OIDC issuer URL (matches the default projected token audience)
- `sub` is the expected service account identity

### Step 3: Exchange the token for a GCP federated access token

```bash
curl -s -X POST https://sts.googleapis.com/v1/token \
  -d "grant_type=urn:ietf:params:oauth:grant-type:token-exchange" \
  -d "subject_token_type=urn:ietf:params:oauth:token-type:jwt" \
  -d "subject_token=$TOKEN" \
  -d "audience=//iam.googleapis.com/projects/<PROJECT_NUMBER>/locations/global/workloadIdentityPools/<POOL>/providers/<PROVIDER>" \
  -d "scope=https://www.googleapis.com/auth/cloud-platform" \
  -d "requested_token_type=urn:ietf:params:oauth:token-type:access_token" | python3 -m json.tool
```

A successful response looks like:

```json
{
    "access_token": "ya29.d...",
    "issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
    "token_type": "Bearer",
    "expires_in": 3600
}
```

### Troubleshooting

| Symptom | Likely cause |
|---------|-------------|
| `INVALID_AUDIENCE` | Token audience doesn't match `--allowed-audiences` on the provider. Use the default (no `--audience` flag) or match explicitly |
| `INVALID_ISSUER` | Issuer URL in the token doesn't match `--issuer-uri` on the provider |
| `INVALID_SIGNATURE` | GCP can't fetch or verify JWKS from the issuer URL (bucket not public?) |
| STS returns token but GCP API calls fail with 403 | Federated identity has no IAM bindings — identity is proven but not authorized |

## Build Farm Cluster Inventory

The OpenShift CI build farm consists of multiple clusters across AWS, GCP, and vSphere. WIF requires a publicly accessible OIDC issuer — only clusters installed with STS/Manual credentials mode have one.

The OIDC issuer URL is not stored in any config repository. It can be discovered at runtime by querying each cluster's API server:

```bash
curl -sk https://<api-url>/.well-known/openid-configuration | python3 -c "import sys,json; print(json.load(sys.stdin)['issuer'])"
```

### Inventory (as of 2026-04-08)

| Cluster | Provider | API URL | OIDC Issuer | Public | WIF |
|---------|----------|---------|-------------|--------|-----|
| app.ci | ROSA | `api.ci.l2s4.p1.openshiftapps.com` | `https://ci-dv2np-oidc.s3.amazonaws.com` | Yes | Yes |
| build01 | AWS | `api.build01.ci.devcluster.openshift.com` | `https://build01-oidc.s3.us-east-1.amazonaws.com` | Yes | Yes |
| build02 | GCP | `api.build02.gcp.ci.openshift.org` | `https://kubernetes.default.svc` | No | **No** |
| build03 | AWS | `api.build03.ci.devcluster.openshift.com` | `https://d3fwbo89i814ul.cloudfront.net` | Yes | Yes |
| build04 | GCP | `api.build04.gcp.ci.openshift.org` | `https://kubernetes.default.svc` | No | **No** |
| build05 | AWS | `api.build05.ci.devcluster.openshift.com` | `https://dj45gf85bjih9.cloudfront.net` | Yes | Yes |
| build06 | AWS | `api.build06.ci.devcluster.openshift.com` | `https://ccoctl-build06-oidc.s3.us-east-1.amazonaws.com` | Yes | Yes |
| build07 | AWS | `api.build07.ci.devcluster.openshift.com` | `https://ccoctl-build07-oidc.s3.us-east-1.amazonaws.com` | Yes | Yes |
| build08 | GCP | `api.build08.gcp.ci.openshift.org` | `https://kubernetes.default.svc` | No | **No** |
| build09 | AWS | `api.build09.ci.devcluster.openshift.com` | `https://d3ocow90ke8648.cloudfront.net` | Yes | Yes |
| build10 | AWS | `api.build10.ci.devcluster.openshift.com` | `https://d31jkmksv2w6vu.cloudfront.net` | Yes | Yes |
| build11 | AWS | `api.build11.ci.devcluster.openshift.com` | `https://ccoctl-build11-oidc.s3.us-east-2.amazonaws.com` | Yes | Yes |
| vsphere02 | vSphere | `api.build02.vmc.ci.openshift.org` | `https://kubernetes.default.svc` | No | **No** |

**Summary**: 8 of 11 active build clusters (all AWS) have public OIDC endpoints. The 3 GCP/vSphere clusters (build02/blocked, build04, build08) do not — they would need to be reinstalled with `credentialsMode: Manual` and `ccoctl` to get public OIDC.

### OIDC naming patterns

The public OIDC issuer URL varies across clusters due to different `ccoctl` versions and installation methods:

- `<cluster>-oidc.s3.<region>.amazonaws.com` (build01)
- `ccoctl-<cluster>-oidc.s3.<region>.amazonaws.com` (build06, build07, build11)
- CloudFront distributions with random IDs (build03, build05, build09, build10)
- ROSA-generated bucket names (app.ci: `ci-dv2np-oidc`)

This means the OIDC issuer cannot be predicted from the cluster name — it must be discovered from the cluster itself.

### Dispatcher and capabilities

Jobs are dispatched to build clusters by the [prow-job-dispatcher](https://github.com/openshift/ci-tools/tree/main/cmd/prow-job-dispatcher) based on capabilities defined in [`core-services/sanitize-prow-jobs/_clusters.yaml`](https://github.com/openshift/release/blob/main/core-services/sanitize-prow-jobs/_clusters.yaml).

To ensure WIF-dependent jobs only run on clusters with a public OIDC endpoint, we use the `arm64` capability as a temporary workaround. This capability is only assigned to AWS clusters (openshift/release#77542 added it to all of them), which all have public OIDC issuers. Despite the name, these clusters are dual-arch — jobs run on amd64 nodes by default.

```yaml
labels:
  capability/arm64: arm64
```

Per [DPTP team feedback](https://redhat-internal.slack.com/archives/CBN38N3MW/p1775592706242139), adding a new semantic capability (e.g., `public-oidc`) was rejected to avoid bloating the capability list. The `arm64` workaround is temporary — once the GCP build clusters are migrated to STS ([DPTP-4758](https://redhat.atlassian.net/browse/DPTP-4758)), WIF will work across all clusters and the capability constraint can be removed.

**Note**: The dispatcher supports AND-matching of capabilities but does **not** support negation (e.g., "NOT gcp").

### Multi-cluster WIF setup

Since each cluster has a different OIDC issuer, the GCP-side WIF configuration needs one OIDC provider per cluster in the Workload Identity Pool. The pool can be shared, but each provider trusts a specific issuer:

```bash
# One pool for all CI clusters
gcloud iam workload-identity-pools create prowci --project=<PROJECT> --location=global

# One provider per cluster
for each WIF-capable cluster:
  gcloud iam workload-identity-pools providers create-oidc <cluster>-oidc \
    --workload-identity-pool=prowci \
    --issuer-uri="<cluster-specific-issuer-url>" \
    --allowed-audiences="<cluster-specific-issuer-url>" \
    --attribute-mapping="google.subject=assertion.sub"
```

IAM bindings use `principal://...subject/system:serviceaccount:<ns>:<sa>` and apply across all providers in the pool — so a single IAM binding grants access regardless of which cluster the job runs on.

## Concrete Example: app.ci ROSA Cluster

This was validated on the `app.ci` Prow cluster used for OpenShift CI.

| Item | Value |
|------|-------|
| Cluster | app.ci (`api.ci.l2s4.p1.openshiftapps.com`) |
| OIDC Issuer | `https://ci-dv2np-oidc.s3.amazonaws.com` |
| JWKS | `https://ci-dv2np-oidc.s3.amazonaws.com/keys.json` |
| GCP Project | `patmarti-1` (project number `925104043997`) |
| WIF Pool | `prowci` |
| OIDC Provider | `prowci-oidc` |
| Full provider resource | `projects/925104043997/locations/global/workloadIdentityPools/prowci/providers/prowci-oidc` |
| Allowed audiences | `https://ci-dv2np-oidc.s3.amazonaws.com` (matches default token audience) |
| Attribute mapping | `google.subject` = `assertion.sub` |
| Test SA | `prowci-test` in namespace `patmarti` |
| Result | STS exchange returned valid federated access token |

## Notes

- Only **projected service account tokens** (issued by the kube-apiserver) use the S3-backed OIDC issuer. User tokens from OpenShift OAuth are issued by a different identity provider and will not work with this federation.
- The `google.subject` attribute is mapped from `assertion.sub`, which for K8s SA tokens has the format `system:serviceaccount:<namespace>:<name>`.
- The federated access token proves identity but does not grant GCP permissions on its own. IAM bindings are required.
- For ROSA clusters, the S3 bucket hosting OIDC documents is created during cluster installation and is publicly readable by design (it only contains public keys and discovery metadata).

## References

- [GCP Workload Identity Federation with OIDC](https://cloud.google.com/iam/docs/workload-identity-federation-with-oidc)
- [OpenID Connect Discovery Spec](https://openid.net/specs/openid-connect-discovery-1_0.html)
- [Kubernetes Service Account Token Volume Projection](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#service-account-token-volume-projection)
- Related: [WIF SA Key Management Study](wif-sa-key-management.md) — deeper analysis of key management for platform-hosted OIDC
