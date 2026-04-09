# OIDC CDN Test

Tests whether Cloud CDN can serve content from a **private** GCS bucket (with `public_access_prevention = "enforced"`) by granting the CDN fill service account `objectViewer` on the bucket.

## Goal

Validate a zero-code, pure infrastructure solution for exposing OIDC discovery documents publicly via an External HTTP(S) Load Balancer + Cloud CDN backend bucket, without needing `allUsers` IAM on the bucket.

## Prerequisites

- Terraform >= 1.5
- GCP project with billing enabled
- `gcloud` authenticated with permissions to manage Compute, Storage, and IAM resources

## Usage

```bash
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your project ID

terraform init
terraform apply
```

## Test

After `terraform apply`, wait ~5 minutes for the load balancer to provision, then:

```bash
LB_IP=$(terraform output -raw load_balancer_ip)
curl -v "http://${LB_IP}/test-cluster/.well-known/openid-configuration"
curl -v "http://${LB_IP}/test-cluster/keys.json"
```

### Expected Results

- **HTTP 200**: CDN fill service account successfully reads from the private bucket. This confirms the zero-code infrastructure solution works.
- **HTTP 403**: CDN fill SA cannot bypass `public_access_prevention`. Fallback to a Cloud Function proxy is required.

## Findings

### CDN Fill Service Account

The CDN fill service account (`service-{PROJECT_NUMBER}@cloud-cdn-fill.iam.gserviceaccount.com`) is **not** created automatically when enabling the Compute API or creating a backend bucket with CDN. It is only created when a [signed URL key is added](https://docs.cloud.google.com/cdn/docs/using-signed-urls#configure_permissions) to the backend bucket. The Terraform config handles this by creating a `google_compute_backend_bucket_signed_url_key` resource first.

### Org Policy Blocks CDN Fill SA

The `iam.allowedPolicyMemberDomains` org policy constraint on the folder prevents granting IAM roles to the CDN fill SA because it is a Google-owned service account outside the allowed customer domains. The error is:

```
Error 412: One or more users named in the policy do not belong to a permitted customer., conditionNotMet
```

This is the same class of org policy restriction that prevents adding `allUsers` to buckets. **The zero-code CDN solution does not work under these org constraints without a policy change.**

### Org Policy Options

To make the CDN approach work, the org policy must be updated to allow the CDN fill and HTTPS LB service agents. Three options were evaluated:

#### Option 1: Managed Constraint (`iam.managed.allowedPolicyMembers`)

Replaces the legacy `iam.allowedPolicyMemberDomains` constraint. Supports `allowedMemberSubjects` for individual principals and `allowedPrincipalSets` for domains, but `allowedMemberSubjects` does **not** support wildcards — each project's CDN fill SA would need to be listed individually.

#### Option 2: Tag-Based Conditional Exception

Use the managed constraint with a tag condition (`resource.matchTag(...)`) to exempt tagged resources from domain restriction. Downside: `roles/resourcemanager.tagUser` on the tag effectively grants the ability to bypass domain restriction on any taggable resource.

#### Option 3: Custom Organization Constraint (Recommended)

A [custom constraint](https://docs.cloud.google.com/iam/docs/org-policy-custom-constraints) on `iam.googleapis.com/AllowPolicy` can combine domain restriction with CDN SA exceptions using pattern matching, without per-project enumeration or tags:

```yaml
name: organizations/ORG_ID/customConstraints/custom.allowedPolicyMembers
resourceTypes:
  - iam.googleapis.com/AllowPolicy
methodTypes:
  - CREATE
  - UPDATE
condition: >-
  resource.bindings.all(binding,
    binding.members.all(member,
      MemberInPrincipalSet(member,
        ['//cloudresourcemanager.googleapis.com/organizations/NUMERIC_ORG_ID_1',
         '//cloudresourcemanager.googleapis.com/organizations/NUMERIC_ORG_ID_2'])
      || MemberSubjectEndsWith(member,
        ['@cloud-cdn-fill.iam.gserviceaccount.com',
         '@https-lb.iam.gserviceaccount.com'])
    )
  )
actionType: ALLOW
displayName: Domain-restricted sharing with Cloud CDN exceptions
description: >-
  Restricts IAM bindings to members within our two orgs, plus
  Cloud CDN fill and HTTPS LB service agents from any project.
```

Key details:
- `MemberInPrincipalSet` with the [organization principal set](https://docs.cloud.google.com/iam/docs/org-policy-custom-constraints) covers all Google Workspace users, service accounts, workload identity pools, workforce identity pools, and service agents within the org.
- `MemberSubjectEndsWith` matches the CDN SA suffix across all projects — no per-project enumeration needed.
- `MemberInPrincipalSet` only accepts the `//cloudresourcemanager.googleapis.com/organizations/ORG_ID` format. The `principalSet://goog/cloudIdentityCustomerId/CUSTOMER_ID` format is for [deny policies only](https://docs.cloud.google.com/iam/docs/principals-overview).
- The legacy constraint uses Workspace customer IDs (e.g. `C04j7mbwl`). To find the corresponding numeric org IDs, an org admin must run `gcloud organizations list`.
- After applying the custom constraint, the legacy `iam.allowedPolicyMemberDomains` policy should be removed to avoid conflicts.

## Notes

- **HTTP-only**: This experiment uses plain HTTP to avoid certificate provisioning delays. Production OIDC endpoints must use HTTPS to prevent MITM substitution of JWKS/issuer values.
- **Synthetic keys**: The JWKS in `keys.json` contains non-functional placeholder values. This experiment tests CDN accessibility (HTTP response codes), not token validation.
- **API cleanup**: `disable_on_destroy = false` is set on API enablement resources to avoid breaking other services in a shared project. If using a dedicated test project, disable APIs manually after cleanup if desired.

## Related

- [Phase 1 OIDC Design](../auth/phase1-pilot/google-oidc.md) — initial OIDC serving approach
- [Phase 2 Identity Platform OIDC](../auth/phase2-poc/identity-platform-oidc.md) — Cloud Function proxy fallback

## Cleanup

```bash
terraform destroy
```
