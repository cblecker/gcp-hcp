# GCP-Native Data Lake for AI Diagnostics and Compliance Audit Logs

***Scope***: GCP-HCP

**Date**: 2026-04-06

## Decision

Use a two-tier GCP-native data lake architecture: BigQuery for real-time streaming data that needs immediate queryability (e.g., AI diagnostic findings, operational events) and GCS for high-volume compliance data that is rarely queried (e.g., audit logs) with BigQuery external tables for ad-hoc access. Data in BigQuery is accessible to AI agents via Google's managed BigQuery MCP server, enabling agents to query historical data as part of their reasoning loop — for example, checking prior diagnostic findings before investigating a new alert, or correlating patterns across clusters and time ranges. Two reusable Terraform child modules (`data-lake` and `data-lake-sink`) provide the infrastructure, deployed per region with sinks per source project.

## Context

### Problem Statement

The GCP HCP platform generates significant operational data with no durable, queryable persistence. As the platform matures and compliance requirements grow, the need for a centralized data lake will expand. The following are the immediate use cases driving this decision, but the architecture must support additional data sources as they emerge:

1. **AI diagnostic findings lack a queryable history.** The Cloud Run diagnose-agent processes Prometheus alerts via Pub/Sub and routes analysis to PagerDuty. While findings are preserved in PagerDuty, its API is not well-suited for cross-cluster trend analysis, pattern detection, or programmatic querying. BigQuery SQL provides a far more powerful interface for SRE investigation, AI enrichment, and fleet-wide analysis.

2. **E2E test logs are destroyed with ephemeral projects.** Integration test pipelines create temporary GCP projects that are torn down after each run, permanently destroying all operational logs and diagnostic history.

3. **Audit logs lack long-term queryable retention.** GKE audit logs and policy denied events are available in Cloud Logging with limited retention (30 days for Data Access, 400 days for Admin Activity). Future compliance certifications (ISO 27001, SOC 2 Type II) require durable, queryable audit log retention with controlled access. This provides a GCP-native option that the Security team can evaluate alongside other solutions when defining certification requirements.

All three use cases share a common architectural pattern: route logs from source projects to a centralized, queryable data store.

### GCP Audit Log Types

GCP generates four types of audit logs. Understanding their volume, cost, and retention is essential for the data lake design:

| Log Type | What Generates It | Examples | Volume | Free in Cloud Logging? | Retention |
|----------|------------------|----------|--------|----------------------|-----------|
| **Admin Activity** | Any API call that **modifies** a resource | Creating a GKE cluster, changing IAM policy, deploying a workload, `kubectl create/apply/delete` | Low (5-50 MB/day/cluster) | Yes | 400 days in `_Required` bucket |
| **System Event** | **Google-initiated** changes (not user actions) | GKE auto-repair replacing a node, autoscaler scaling, VM live migration | Very low (1-5 MB/day/cluster) | Yes | 400 days in `_Required` bucket |
| **Policy Denied** | Any API call **rejected** by IAM or org policy | Service account denied access, VPC Service Controls blocked, org policy violation | Low-Medium (spiky) | No | 30 days in `_Default` bucket |
| **Data Access** | Any API call that **reads** data (get, list, watch) | `kubectl get pods`, reading a secret, querying BigQuery | **Very high** (10-200 GB/day/cluster) | No ($0.50/GiB) | 30 days in `_Default` bucket |

**Key insight**: Admin Activity and System Event logs are free with 400-day retention per project. However, they are only queryable per-project via `gcloud logging read`. A folder-level aggregated sink to GCS makes them centrally queryable via BigQuery at minimal additional cost (GCS storage only — the sink routing itself is free).

### What We Capture vs What Stays In-Project

The folder-level aggregated sink creates a *copy* of audit logs. The originals remain in each project's Cloud Logging, so per-project Logs Explorer access is unaffected:

| Log Type | In each project's Cloud Logging | In central GCS bucket | In BigQuery (external table) |
|----------|--------------------------------|----------------------|------------------------------|
| Admin Activity | Yes — `_Required`, 400 days, free | Yes — copy via folder sink | Yes — queryable via `audit_activity` |
| System Event | Yes — `_Required`, 400 days, free | Yes — copy via folder sink | Yes — queryable via `audit_system_event` |
| Policy Denied | Yes — `_Default`, 30 days only | Yes — copy via folder sink (730-day retention) | Yes — queryable via `audit_policy_denied` |
| Data Access | Yes — `_Default`, 30 days | **Not captured** — too high volume | No |

**Data Access logs are intentionally excluded** due to extreme volume (10-200 GB/day/cluster). If needed for specific compliance requirements, they can be enabled selectively with tight filters.

### Constraints

- **Data location**: The data lake can be hosted at a global scope (e.g., in the global project or a dedicated project). Per-region datasets are not required unless sovereignty constraints demand it — the architecture supports both models.
- **Cost**: Audit log volume can reach 10-50 GiB/day/cluster unfiltered. Tight inclusion filters and cost-efficient storage tiers are essential.
- **Schema-on-first-write**: Cloud Logging determines BigQuery table schema from the first log entry. Type mismatches silently route to error tables. The diagnostic agent's Pydantic schema must be locked before the first write.
- **Existing patterns**: Infrastructure modules must follow the established child module pattern (similar to the `workflows` module).
- **Automation tooling**: All infrastructure must be expressible as Terraform and compatible with automated CI/CD pipelines. No manual `gcloud` commands or shell scripts in the provisioning path.
- **Compliance readiness**: Audit log retention and access patterns should support future ISO 27001 (A.12.4) and SOC 2 Type II (CC6, CC7, CC8) certification requirements without re-architecture.

### Assumptions

- The diagnose-agent will continue running on Cloud Run, emitting structured JSON to stdout (captured by Cloud Logging automatically).
- A centralized dataset (global or dedicated project) is the target deployment model. Cross-region federated queries are not required.

## Alternatives Considered

1. **BigQuery streaming for all log types (diagnostic findings + audit logs)**: Route everything to BigQuery via Cloud Logging sinks with `use_partitioned_tables = true`. Single destination, uniform query surface.

2. **GCS for all log types with BigQuery external tables**: Route everything to GCS buckets with lifecycle policies. Create BigQuery external tables for ad-hoc querying. Cheapest storage, but higher query latency and no streaming capability.

3. **Two-tier: BigQuery streaming for diagnostics + GCS for audit logs (chosen)**: Route diagnostic findings to BigQuery (small volume, frequent queries, real-time visibility) and audit logs to GCS (high volume, compliance retention, rare queries) with BigQuery external tables for ad-hoc access.

4. **Cloud Logging Log Analytics linked datasets**: Use Cloud Logging's built-in BigQuery integration. Zero configuration — just enable Log Analytics on the log bucket.

5. **Third-party solutions (Datadog, Splunk, Elastic)**: External log aggregation and analysis platforms.

## Decision Rationale

### Justification

The two-tier approach optimizes for the different access patterns and cost profiles of diagnostic findings vs audit logs:

- **Diagnostic findings** are small (KB per finding), queried frequently (SRE investigations, AI enrichment, trend analysis), and need real-time visibility. BigQuery streaming is ideal — near-instant query availability at negligible cost for this volume.

- **Audit logs** are high volume (potentially GiB/day/cluster), queried rarely (compliance audits, security investigations), and primarily need durable retention. GCS is 5-10x cheaper than BigQuery for storage, with lifecycle policies that automatically transition to Nearline (90 days) and Archive (365 days) storage classes.

- **Folder-level aggregated sinks** capture audit logs from all projects under a folder with a single sink definition. New projects are automatically included — no per-project configuration required. The sink creates a copy; original logs remain in each project for Logs Explorer access.

BigQuery external tables bridge the gap — audit logs in GCS are queryable via standard SQL with zero data duplication. The external tables cost nothing until queried, and when queried, only scan the relevant files.

### Evidence

Spike validation ([GCP-497](https://redhat.atlassian.net/browse/GCP-497)) confirmed:

- End-to-end flow validated: Cloud Monitoring alert → Pub/Sub → Eventarc → Cloud Run diagnose-agent → structured JSON stdout → Cloud Logging → Sink → BigQuery table → SQL query returns results
- Diagnostic findings arrive in BigQuery within 1-2 minutes of the agent emitting them
- Cloud Logging sinks to GCS deliver in hourly batches (up to 3 hours for first batch)
- GCS lifecycle policies (Standard → Nearline → Archive → Delete) work correctly with Cloud Logging sink output
- BigQuery external tables successfully read Cloud Logging JSON files from GCS with explicit schema (required to skip fields with invalid column name characters like `@type` and `k8s.io/`)
- BigQuery views provide a clean query surface that flattens `jsonPayload.*` fields — users query `SELECT * FROM view_recent_findings` without needing to know the Cloud Logging envelope schema
- Folder-level aggregated sink captures Admin Activity, System Event, and Policy Denied from all projects under a folder with a single sink definition
- Pydantic schema validation (`DiagnosticFinding` model) ensures type consistency before the first write to BigQuery, preventing silent schema-on-first-write errors

### Comparison

**Alternative 1 (BigQuery for everything)** was rejected because:
- Audit log volume at scale (10-50 GiB/day/cluster) makes BigQuery streaming ingestion expensive ($0.05/GiB)
- Audit logs are rarely queried — paying for BigQuery active storage ($0.02/GiB/month) when GCS Archive is $0.0012/GiB/month wastes 94% of the storage budget
- At 200 clusters: ~$5,800/month (BigQuery) vs ~$281/month (GCS with lifecycle)

**Alternative 4 (Log Analytics)** was rejected because:
- Log Analytics linked datasets are read-only — no DML, no clustering, no views
- Cannot create external tables or materialized views
- Limited SQL dialect compared to standard BigQuery
- No partition filter enforcement

**Alternative 5 (Third-party)** was rejected because:
- Adds external dependency and vendor lock-in
- Requires data egress from GCP (cost and sovereignty concerns)
- GCP-native solution aligns with the platform's observability stack (GMP, Cloud Monitoring, Cloud Logging)

## Consequences

### Positive

- **Cost-optimized storage**: Diagnostic findings in BigQuery (pennies/month at expected volume), audit logs in GCS with automatic tiering (Standard → Nearline → Archive) — 94% cheaper than all-BigQuery at scale
- **Unified query surface**: BigQuery SQL for both data tiers — native tables for diagnostics, external tables for audit logs. Views flatten the Cloud Logging envelope schema for ease of use
- **Compliance-ready**: GCS bucket with versioning, public access prevention, and configurable retention (default 730 days) meets ISO 27001 and SOC 2 audit log retention requirements
- **Zero-maintenance audit capture**: Folder-level aggregated sink automatically captures audit logs from all current and future projects under the folder — no per-project configuration needed
- **Reusable modules**: `data-lake` (dataset + bucket + external tables + views) and `data-lake-sink` (configurable sink for diagnostic findings) follow the established child module pattern
- **AI enrichment via MCP**: The diagnose-agent connects to Google's managed BigQuery MCP server to query a cluster's prior diagnostic history before investigating new alerts. This creates a feedback loop — the agent references prior root causes, detects recurring patterns, and escalates systemic issues instead of repeating point remediations. Validated end-to-end: agent queries history, finds prior PVC alerts, and incorporates that context into current etcd diagnosis.
- **Extensible MCP pattern**: The MCP client bridge (`McpToolRegistry`) is generic — any MCP server can be registered with a prefix. Future integrations (Cloud Logging MCP, custom MCPs) follow the same pattern with no agent code changes.
- **SRE investigation**: Per-cluster diagnostic timeline available via `view_recent_findings` with `WHERE cluster_id = '...'`
- **No infrastructure to manage**: Cloud Logging handles sink routing, BigQuery handles query execution, GCS handles storage lifecycle — no ETL pipelines, no Dataflow, no custom code

### Negative

- **Two-phase external table deployment**: BigQuery external tables require data in GCS before they can be created with autodetect. The `enable_audit_external_tables` flag gates creation — operators must set it to `true` after audit sinks deliver their first batch (up to 3 hours). This adds operational friction to initial deployment.
- **Explicit audit schema maintenance**: The external table schema must be maintained manually in Terraform (JSON) because Cloud Audit Log entries contain fields with characters invalid in BigQuery column names (`@type`, `authorization.k8s.io/`). If Google adds important new fields to the audit log format, the schema must be updated manually. The `ignore_unknown_values = true` setting prevents breakage but silently drops unrecognized fields.
- **Sink-auto-created table name fragility**: The diagnostic findings table is auto-created by Cloud Logging with a name derived from the log source (e.g., `run_googleapis_com_stdout` for Cloud Run stdout). If the agent moves to a different execution environment, the table name changes. The `diagnostic_findings_table` variable mitigates this but doesn't eliminate the coupling.
- **No real-time audit log queries**: GCS sink delivery is batched (hourly). Audit logs are not queryable in BigQuery until the next batch arrives. For real-time audit queries, use Cloud Logging directly (per-project).
- **Folder sink requires folder-level permissions**: The Terraform identity creating the folder sink needs `roles/logging.configWriter` at the folder level, which may require coordination with platform admins in production.

## Cross-Cutting Concerns

### Reliability

* **Sink delivery guarantees**: Cloud Logging sinks provide at-least-once delivery. BigQuery streaming sinks deliver within minutes. GCS sinks deliver in hourly batches. Both handle transient failures with built-in retry.
* **Sink health monitoring**: Sink health should be monitored via Cloud Monitoring's `logging.googleapis.com/exports/error_count` metric at the project level.
* **Schema evolution**: The diagnostic agent uses a versioned Pydantic schema (`schema_version` field). New fields are additive and safe — BigQuery auto-extends the schema on new columns. Type changes or renames require a schema version bump and migration plan. The `evidence` field is serialized as a JSON string to avoid repeated field schema complexity.

### Security

* **Encryption at rest**: For the spike, Google-managed encryption is used. Production deployment MUST add CMEK (Customer-Managed Encryption Keys) to both the GCS bucket (`encryption` block) and BigQuery dataset (`default_encryption_configuration`).
* **Public access prevention**: GCS bucket enforces `public_access_prevention = "enforced"` and `uniform_bucket_level_access = true`.
* **Bucket versioning**: Enabled for audit log immutability — prevents accidental overwrites or deletes.
* **IAM least privilege**: Sink writer identities get only the minimum required role (BigQuery `dataEditor` or GCS `objectCreator`).
* **MCP security**: The MCP client uses Application Default Credentials with `bigquery.readonly` scope (read-only). Access tokens are cached with thread-safe refresh. Error messages are sanitized to strip project IDs and service account emails before returning to the model. The `DATA_LAKE_PROJECT_ID` env var is validated against GCP project ID format regex before prompt injection.
* **SQL injection prevention**: Alert payload fields (cluster_id, namespace, region) are validated against strict format regexes (UUID, k8s namespace, GCP region) before the model constructs SQL queries. SQL metacharacters (`;`, `'`, `"`, `\`) are stripped.

### Cost

**Estimated monthly costs (folder-level aggregated sink, on-demand BigQuery pricing):**

| Component | 10 Clusters | 50 Clusters | 200 Clusters |
|-----------|-------------|-------------|--------------|
| BigQuery streaming (diagnostics) | $0.02 | $0.08 | $0.30 |
| BigQuery storage (diagnostics) | $0.02 | $0.09 | $0.36 |
| BigQuery queries (on-demand) | FREE (<1 TB) | FREE (<1 TB) | $0.01 |
| GCS storage (all audit types, tiered) | $15 | $75 | $300 |
| **Total** | **~$15** | **~$75** | **~$300** |

**Key cost decisions:**
- Folder-level aggregated sink captures all three audit log types (Admin Activity, System Event, Policy Denied) in one GCS bucket. Admin Activity and System Event are low volume — the marginal GCS storage cost is minimal compared to the cross-project queryability benefit.
- GCS lifecycle automatically transitions to cheaper tiers: Standard ($0.020/GiB) → Nearline ($0.010/GiB at 90 days) → Archive ($0.0012/GiB at 365 days).
- Default retention is 730 days (2 years). Configurable up to 10 years.
- Data Access logs are NOT captured — too high volume ($0.50/GiB ingestion). Enable selectively if needed.
- BigQuery storage alert and GCS storage alert provide bill shock protection.
- Filter validation on the sink module prevents accidental empty-filter cost explosion.

### Operability

* **Deployment model**: `data-lake` module deploys the BigQuery dataset, GCS bucket, external tables, views, and alerts. `data-lake-sink` module deploys per-project sinks for diagnostic findings (streaming to BigQuery). Audit log routing uses a folder-level aggregated sink (single Terraform resource).
* **Two-phase for external tables**: Initial deploy creates bucket and sinks. After audit data arrives (up to 3 hours), set `enable_audit_external_tables = true` and re-apply.
* **BigQuery views**: Four pre-built views (`view_recent_findings`, `view_findings_by_cluster`, `view_repeat_offenders`, `view_daily_summary`) provide immediate value without SQL knowledge.
* **Notebook templates**: Jupyter notebooks for diagnostic findings analysis and audit log investigation are provided for local use (VS Code) with BigQuery Python client.

## Architecture

### Data Flow

```
Folder (contains all region + MC projects)
    │
    ├── Folder-Level Aggregated Sink ──→ GCS Bucket
    │     filter: Admin Activity OR        ├── /activity/YYYY/MM/DD/*.json
    │             System Event OR          ├── /system_event/YYYY/MM/DD/*.json
    │             Policy Denied            └── /policy/YYYY/MM/DD/*.json
    │     include_children: true                    │
    │     (captures ALL projects                    ▼
    │      under folder automatically)    BigQuery External Tables
    │                                     ├── audit_activity
    │                                     ├── audit_system_event
    │                                     └── audit_policy_denied
    │
    ├── Per-Project: Cloud Run diagnose-agent
    │       │
    │       ├── Query history via MCP ──→ BigQuery MCP Server
    │       │     ◄──── Prior findings ──┘
    │       │
    │       ├── Investigate cluster via Cloud Workflows
    │       │
    │       └── Structured JSON (stdout) ──→ Cloud Logging
    │                                            │
    │                   ┌────────────────────────┘
    │                   ▼
    │            Per-Project Log Sink (BigQuery)
    │            filter: jsonPayload.log_type="diagnostic_finding"
    │                   │
    │                   ▼
    │            BigQuery Dataset (data_lake)
    │            ├── run_googleapis_com_stdout (streaming)
    │            ├── view_recent_findings
    │            ├── view_findings_by_cluster
    │            ├── view_repeat_offenders
    │            └── view_daily_summary
    │
    └── Logs also remain in each project's Cloud Logging
          ├── _Required bucket (Admin Activity, System Event) — 400 days, free
          └── _Default bucket (Policy Denied) — 30 days
```

### Module Structure

| Module | Purpose | Deploys From |
|--------|---------|-------------|
| `data-lake` | BigQuery dataset, GCS bucket, external tables, views, alerts | Region or global module |
| `data-lake-sink` | Per-project log sink for diagnostic findings (BigQuery streaming) | Caller (region config, MC config) |
| Folder sink | Aggregated audit log sink (Admin Activity + System Event + Policy Denied → GCS) | Folder-level Terraform config |

### Component Summary

| Component | Resource Type | Managed By | Purpose |
|-----------|-------------|-----------|---------|
| BigQuery dataset | `google_bigquery_dataset` | Terraform (data-lake module) | Houses diagnostic findings and external tables |
| GCS audit bucket | `google_storage_bucket` | Terraform (data-lake module) | Stores audit logs from all projects |
| External tables | `google_bigquery_table` (external) | Terraform (data-lake module) | Queryable view over GCS audit data (activity, system_event, policy_denied) |
| BigQuery views | `google_bigquery_table` (view) | Terraform (data-lake module) | Pre-built diagnostic queries |
| Storage alerts | `google_monitoring_alert_policy` | Terraform (data-lake module) | BigQuery and GCS cost guardrails |
| Diagnostic sink | `google_logging_project_sink` | Terraform (data-lake-sink module) | Per-project: routes findings to BigQuery |
| Audit sink | `google_logging_folder_sink` | Terraform (folder-level config) | Folder-level: routes all audit logs to GCS |
| MCP client bridge | `mcp_client.py` (Python) | Agent code | Connects agent to BigQuery MCP for history queries |
| MCP cross-project IAM | `google_project_iam_member` | Terraform (MC region-iam.tf) | `bigquery.dataViewer`, `bigquery.jobUser`, `mcp.toolUser` for MC agent → data lake |

## Spike Findings Summary

| Question | Finding |
|----------|---------|
| Native tables vs external tables? | Use both: native for diagnostics (streaming), external for audit logs (GCS) |
| Per-project vs folder-level audit sinks? | Folder-level aggregated sink — one sink captures all projects, auto-includes new ones |
| Schema-on-first-write risk? | Mitigated by Pydantic schema validation in agent code. Schema version field tracks evolution |
| Cost at scale? | ~$300/month at 200 clusters (all audit types). Would be ~$5,800 if using BigQuery streaming |
| External table schema? | Must be explicit (not autodetect) due to `@type` and `k8s.io/` field name characters |
| Two-phase deployment? | Required — external tables gated by `enable_audit_external_tables` flag |
| MCP BigQuery integration? | Google Managed MCP server works for cross-project queries. Requires `roles/mcp.toolUser`, `bigquery.dataViewer`, `bigquery.jobUser` on the data lake project |
| Does the agent use history? | YES — agent queries prior findings as Step 1, references recurring patterns in diagnosis, and escalates systemic issues |
| SQL injection risk? | Mitigated via UUID validation on cluster_id, namespace regex, SQL metacharacter stripping |

## Next Steps

Implementation will be split into the following PRs, each with a corresponding Jira story under an Epic:

1. **Structured diagnostic logging** (`agent/diagnose/schema.py`): Pydantic schema for `DiagnosticFinding`, emission at all agent exit paths
2. **Data lake Terraform module** (`terraform/modules/data-lake/`): BigQuery dataset, GCS bucket, external tables, views, alerts
3. **Data lake sink Terraform module** (`terraform/modules/data-lake-sink/`): Configurable sink with BigQuery and GCS support, filter validation
4. **Region module integration**: Wire data-lake into region module, Atlantis IAM, variables, outputs
5. **Folder-level audit sink**: Aggregated sink for Admin Activity + System Event + Policy Denied
6. **MC module integration**: Cross-project BigQuery/MCP IAM grants, `DATA_LAKE_PROJECT_ID` env var, `agent_image_override` variable
7. **MCP BigQuery integration** (`agent/diagnose/mcp_client.py`): MCP-to-Gemini bridge, `gemini_schema.py` shared module, knowledge docs, investigation strategy updates
8. **Dev-all-in-one integration**: Enable data lake in example config with diagnostic + audit sinks
9. **Production hardening**: CMEK encryption, narrow Atlantis IAM, VPC Service Controls, dedicated compliance project evaluation

---

**Related Documentation:**
- Jira Spike: [GCP-497](https://redhat.atlassian.net/browse/GCP-497) — BigQuery Observability Lake Spike
- Alerting Framework: [integrated-alerting-framework.md](integrated-alerting-framework.md)
- Observability Platform: [observability-google-managed-prometheus.md](observability-google-managed-prometheus.md)
