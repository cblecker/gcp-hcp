# Federated Authentication from OpenShift/ROSA to GCP via Workload Identity Federation

## TL;DR

Any OpenShift or ROSA cluster publishes an OIDC discovery document with public keys for verifying service account tokens. GCP Workload Identity Federation can trust this OIDC issuer, allowing pods on the cluster to authenticate to GCP without static credentials.

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

Every OpenShift cluster (including ROSA with STS) has an OIDC provider that backs Kubernetes service account tokens:

- **ROSA/STS clusters**: The OIDC discovery document and JWKS (public keys) are hosted in a public S3 bucket. The bucket name is derived from the cluster's infrastructure ID.
- **Self-managed clusters**: The OIDC endpoint may be hosted differently (e.g., on the API server itself or a custom endpoint).

The kube-apiserver signs projected service account tokens with a private key. The corresponding public key is published in the JWKS endpoint, allowing external parties (like GCP) to verify token signatures without accessing the private key.

### Token flow

```
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

**Option B: Direct resource access (principalSet for broader matching)**

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
    "aud": ["openshift"],
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
