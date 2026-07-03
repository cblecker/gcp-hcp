# Standardized Naming Conventions for GCP Infrastructure

***Scope***: GCP-HCP

**Date**: 2026-01-12

## Decision

Adopt a standardized naming pattern `{env}-{type}-{region_code}-{identifier}` with region code mapping and YAML-based configuration for all GCP HCP infrastructure resources (region and management-cluster modules).

## Context

### Problem Statement

Current naming pattern has two critical failures:

**Problem 1: Length limit violations**
- Full GCP region names in project IDs exceed GCP's 30-char limit for 12+ regions (28% of infrastructure)
- Example: `int-northamerica-northeast1-mc-abc123` = 38 chars (exceeds by 8)
- Blocks deployment to northamerica-*, australia-*, southamerica-* regions

**Problem 2: Naming collisions**
- No unique identifier in GKE cluster names
- Example: Two sectors (main, canary) in us-central1 both create `int-us-central1-gke`
- Breaks ArgoCD's ability to distinguish between clusters
- Prevents deploying multiple sectors to the same GCP region

### Constraints

**GCP Limits:**
- Project ID: 30 chars max, globally unique, immutable
- Folder display_name: 30 chars max
- GKE cluster name: 40 chars max

**Operational Requirements:**
- 4 dimensions per project: environment, type, region, sector
- Sector must be mutable (canary→main promotion without resource recreation)
- Environment must be visible (prevent production mistakes)
- All 42 GCP regions must be supported

### Assumptions

- Infrastructure rebuilds are acceptable for migration (no in-place migration required)
- Sector names typically short (<15 chars), but must handle edge cases
- Region codes can be abbreviated if intuitive and reversible

## Alternatives Considered

1. **Keep current pattern with longer limits**: Request GCP increase project ID limit
   - Not viable - GCP limits are fixed
   - Would not solve sector visibility issue

2. **Use only random IDs**: `abc123-def456` with all context in labels
   - Pros: Shortest possible names, maximum flexibility
   - Cons: Zero human readability, high operational risk (no visual env/region identification)

3. **Cryptic region codes**: `northamerica-northeast1` → `cane` (4 chars)
   - Pros: Very short codes
   - Cons: Not intuitive (cane = Canada Northeast?), harder to reverse-map

4. **Selected approach: Literal region abbreviations**: `northamerica-northeast1` → `na-ne1`
   - Pros: Highly predictable, easy to decode, consistent pattern
   - Cons: Slightly longer (5-6 chars vs 4), but still well within limits

5. **Sector in project_id**: Include sector in immutable identifier
   - Pros: Maximum visibility
   - Cons: Cannot change sector without resource recreation (blocks canary→main promotion)

## Decision Rationale

### Justification

The `{env}-{type}-{region_code}-{identifier}` pattern solves the critical blocking issue (region name length) while providing:
- Environment visibility for operational safety (3-char env code always visible)
- Type identification (reg/mgt clearly distinguishes infrastructure purpose)
- Human-readable region codes using literal abbreviations (na=North America, eu=Europe)
- Sector visibility in mutable project_name (enables promotion without recreation)

### Evidence

**Length analysis:**
- Current worst case: 38 chars (exceeds limit by 8)
- Proposed worst case: 19 chars (11-char margin)
- Proposed typical: 18 chars (12-char margin)

**Region coverage:**
- Current: Fails for 12 out of 42 regions (28% blocked)
- Proposed: Works for all 42 regions with consistent margin

**Sector promotion workflow:**
- Current: Sector only in labels (not visible in console)
- Proposed: Sector in project_name (visible, mutable via simple Terraform update)

### Comparison

**vs Alternative 2 (random only):**
- Rejected: Zero human readability creates high operational risk
- Selected approach provides clear env/type/region visibility for safety

**vs Alternative 3 (cryptic codes):**
- Rejected: `cane` not intuitive, requires lookup table
- Selected approach: `na-ne1` immediately recognizable as "North America Northeast 1"

**vs Alternative 5 (sector in ID):**
- Rejected: Blocks canary→main promotion (requires resource recreation)
- Selected approach: Sector in mutable project_name (zero downtime promotion)

### Region Code Mapping

Complete mapping of all 42 GCP regions to short codes:

**Americas - United States (9 regions):**
| Region | Code | Location |
|--------|------|----------|
| us-west1 | us-w1 | Oregon |
| us-west2 | us-w2 | Los Angeles |
| us-west3 | us-w3 | Salt Lake City |
| us-west4 | us-w4 | Las Vegas |
| us-central1 | us-c1 | Iowa |
| us-east1 | us-e1 | South Carolina |
| us-east4 | us-e4 | N. Virginia |
| us-east5 | us-e5 | Columbus |
| us-south1 | us-s1 | Dallas |

**Americas - North America (3 regions):**
| Region | Code | Location |
|--------|------|----------|
| northamerica-northeast1 | na-ne1 | Montréal |
| northamerica-northeast2 | na-ne2 | Toronto |
| northamerica-south1 | na-s1 | Mexico |

**Americas - South America (2 regions):**
| Region | Code | Location |
|--------|------|----------|
| southamerica-west1 | sa-w1 | Santiago |
| southamerica-east1 | sa-e1 | São Paulo |

**Europe (13 regions):**
| Region | Code | Location |
|--------|------|----------|
| europe-west1 | eu-w1 | Belgium |
| europe-west2 | eu-w2 | London |
| europe-west3 | eu-w3 | Frankfurt |
| europe-west4 | eu-w4 | Netherlands |
| europe-west6 | eu-w6 | Zurich |
| europe-west8 | eu-w8 | Milan |
| europe-west9 | eu-w9 | Paris |
| europe-west10 | eu-w10 | Berlin |
| europe-west12 | eu-w12 | Turin |
| europe-southwest1 | eu-sw1 | Madrid |
| europe-north1 | eu-n1 | Finland |
| europe-north2 | eu-n2 | Stockholm |
| europe-central2 | eu-c2 | Warsaw |

**Asia Pacific (11 regions):**
| Region | Code | Location |
|--------|------|----------|
| asia-south1 | as-s1 | Mumbai |
| asia-south2 | as-s2 | Delhi |
| asia-southeast1 | as-se1 | Singapore |
| asia-southeast2 | as-se2 | Jakarta |
| asia-east1 | as-e1 | Taiwan |
| asia-east2 | as-e2 | Hong Kong |
| asia-northeast1 | as-ne1 | Tokyo |
| asia-northeast2 | as-ne2 | Osaka |
| asia-northeast3 | as-ne3 | Seoul |
| australia-southeast1 | au-se1 | Sydney |
| australia-southeast2 | au-se2 | Melbourne |

**Middle East & Africa (4 regions):**
| Region | Code | Location |
|--------|------|----------|
| me-west1 | me-w1 | Tel Aviv |
| me-central1 | me-c1 | Doha |
| me-central2 | me-c2 | Dammam |
| africa-south1 | af-s1 | Johannesburg |

**Naming Pattern Rules:**
- Main region: 2 letters (na, sa, as, eu, au, af, me, us)
- Direction: Single letter (e, w, n, s, c) or compound (ne, se, nw, sw)
- Number: Region number (1, 2, 3, etc.)
- Result: All codes 5-6 characters, highly predictable and easy to decode

## Consequences

### Positive

* **Unblocks global expansion**: All 42 GCP regions supported (vs 30 currently) - solves length limit problem
* **Prevents naming collisions**: Unique cluster_id in all GKE cluster names - multiple sectors can coexist in same region
* **Operational safety**: Environment clearly visible in all resource names (prevents prod mistakes)
* **Flexible sector management**: Canary→main promotion via Terraform update (no resource recreation)
* **Human-readable**: Region codes intuitive and predictable (na-ne1 = North America Northeast 1)
* **11-12 char safety margin**: Future-proof for region name growth or additional identifiers
* **Single source of truth**: YAML configuration prevents duplication, works with validation
* **Compact format**: Consistent pattern across project_id and project_name
* **ArgoCD compatibility**: Unique cluster names enable proper cluster registration and management

### Negative

* **Migration required**: All existing deployments use old naming (rebuild needed)
* **Region code learning curve**: Operators must learn mapping (mitigated by intuitive pattern)
* **YAML file dependency**: Modules depend on external YAML files for validation
* **Sector truncation edge case**: Very long sector names (>15 chars) get truncated in project_name
* **Scripted cluster IDs**: Manual step to generate IDs (vs automatic random generation)

## Cross-Cutting Concerns

### Reliability

* **Observability**: Resource names clearly identify environment/type/region for troubleshooting
* **Scalability**: Pattern scales to all current and future GCP regions with ample margin

### Security

* **Bad word filtering**: Cluster ID generation script prevents offensive identifiers
* **No sensitive data**: Sector/environment/region are operational metadata, not sensitive

### Operability

* **GCP Console UX**: Compact names with sector visible (e.g., `prd-reg-na-ne1-canary`)
* **Sector promotion**: Simple Terraform variable update (low operational overhead)
* **Validation**: Early failure on invalid region/environment (Terraform variable validation)
* **Documentation**: YAML files self-documenting with inline comments
* **Script integration**: Same YAML used by Terraform and Python scripts (cluster ID generation)

---

**Related Documentation:**
- Technical specification: `gcp-hcp-infra/docs/GCP-309-NAMING-CONVENTIONS.md`
- Jira issue: GCP-309
