# GCP-Native Data Lake for AI Diagnostics and Compliance Audit Logs

***Scope***: GCP-HCP

**Date**: 2026-04-06

## Decision

Use a two-tier GCP-native data lake architecture: BigQuery for real-time streaming data that needs immediate queryability (e.g., AI diagnostic findings, operational events) and Cloud Logging Log Analytics for compliance audit logs (centrally queryable via standard BigQuery SQL with zero schema management). Data in BigQuery is accessible to AI agents via Google's managed BigQuery MCP server, enabling agents to query historical data as part of their reasoning loop — for example, checking prior diagnostic findings before investigating a new alert, or correlating patterns across clusters and time ranges. Two reusable Terraform child modules (`data-lake` and `data-lake-sink`) provide the streaming data infrastructure. Audit log capture uses a single folder-level aggregated sink to a centralized log bucket with Log Analytics — automatically capturing all current and future projects with no per-project configuration. Audit log shipping is independently toggleable via `enable_audit_logs` for cost control.

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
| **Data Access** | Any API call that **reads** data (get, list, watch) | `kubectl get pods`, reading a secret, querying BigQuery | **Very high** (~1.3 KB/request, scales linearly with API traffic — ~5,000 req/min ≈ 10 GB/day) | No ($0.50/GiB) | 30 days in `_Default` bucket |

**Note on volume estimates**: The per-cluster volumes above are for individual GKE clusters. In practice, audit log volume scales per-project (not per-cluster), and multiple hosted clusters share each MC project. At production scale (~180 projects), the aggregate volume is lower than cluster count × per-cluster estimate would suggest.

**Key insight**: Admin Activity and System Event logs are free with 400-day retention per project, but they are only queryable per-project via `gcloud logging read`. A folder-level aggregated sink to a centralized Cloud Logging log bucket with Log Analytics enabled makes them centrally queryable via standard BigQuery SQL. While BigQuery can technically handle fields like `@type` using backtick-quoted identifiers, Log Analytics eliminates the need to manage schemas and query syntax workarounds entirely — providing a cleaner query experience with zero operational overhead.

### Data Location

The data lake is deployed in a **single region** (e.g., `us-central1`). This applies to both the BigQuery dataset and the Cloud Logging log bucket.

| Location Option | Resilience | CMEK Support | Notes |
|----------------|------------|-------------|-------|
| **Regional** (e.g., `us-central1`) | Single region | Yes | Recommended — predictable, supports CMEK |
| **Global** (log bucket) | **No** added resilience vs regional | **No** | Not recommended — same as regional but loses CMEK |
| **Multi-region** (BigQuery `US`) | **No** cross-region redundancy | Yes | Google picks a region within US — no replication |

**Why not global?** A `global` Cloud Logging log bucket stores data in a single Google-chosen region with no cross-region replication — identical resilience to a regional bucket, but without CMEK support. There is no cost difference.

**Cross-region queryability is not affected by data location.** BigQuery SQL queries can run from any region against a regional dataset. Data location controls where data is stored, not who can query it.

**Future: Cross-region replication.** If resilience requirements emerge, BigQuery supports cross-region replication as a separate feature. This can be added without re-architecting the data lake.

### What We Capture vs What Stays In-Project

The folder-level aggregated sink creates a *copy* of audit logs in a centralized log bucket. The originals remain in each project's Cloud Logging, so per-project Logs Explorer access is unaffected:

| Log Type | In each project's Cloud Logging | In centralized log bucket | Queryable via BigQuery |
|----------|--------------------------------|--------------------------|----------------------|
| Admin Activity | Yes — `_Required`, 400 days, free | Yes — copy via folder sink | Yes — via Log Analytics linked dataset |
| System Event | Yes — `_Required`, 400 days, free | Yes — copy via folder sink | Yes — via Log Analytics linked dataset |
| Policy Denied | Yes — `_Default`, 30 days only | Yes — copy via folder sink (configurable retention) | Yes — via Log Analytics linked dataset |
| Data Access | Yes — `_Default`, 30 days | **Not captured** — too high volume | No |

**Data Access logs are intentionally excluded** due to extreme volume (10-200 GB/day/cluster). If needed for specific compliance requirements, they can be enabled selectively with tight filters.

### Constraints

- **Data location**: The data lake can be hosted at a global scope (e.g., in the global project or a dedicated project). Per-region datasets are not required unless sovereignty constraints demand it — the architecture supports both models.
- **Cost**: Audit log volume can be substantial when unfiltered and should be estimated at the project level for this architecture (multiple hosted clusters per MC project). Tight inclusion filters and cost-efficient storage tiers are essential.
- **Schema-on-first-write**: Cloud Logging determines BigQuery table schema from the first log entry. Type mismatches silently route to error tables. Any application emitting structured logs to BigQuery via a sink must have its schema locked before the first write.
- **Existing patterns**: Infrastructure modules must follow the established child module pattern (similar to the `workflows` module).
- **Automation tooling**: All infrastructure must be expressible as Terraform and compatible with automated CI/CD pipelines. No manual `gcloud` commands or shell scripts in the provisioning path.
- **Compliance readiness**: Audit log retention and access patterns should support future ISO 27001 (A.12.4) and SOC 2 Type II (CC6, CC7, CC8) certification requirements without re-architecture.

### Assumptions

- The diagnose-agent will continue running on Cloud Run, emitting structured JSON to stdout (captured by Cloud Logging automatically).
- A centralized dataset (global or dedicated project) is the target deployment model. Cross-region federated queries are not required.

## Alternatives Considered

1. **BigQuery streaming for all log types**: Route everything to BigQuery via Cloud Logging sinks with `use_partitioned_tables = true`. Single destination, uniform query surface.

2. **GCS for all log types with BigQuery external tables**: Route everything to GCS buckets with lifecycle policies. Create BigQuery external tables for ad-hoc querying. Cheapest storage, but higher query latency and no streaming capability.

3. **Two-tier: BigQuery streaming + GCS for audit logs**: Route real-time operational data to BigQuery (small volume, frequent queries) and audit logs to GCS (high volume, compliance retention, rare queries) with BigQuery external tables for ad-hoc access.

4. **Two-tier: BigQuery streaming + Log Analytics for audit logs (chosen)**: Route real-time operational data (e.g., AI diagnostic findings) to BigQuery via per-project sinks. Route audit logs to a centralized Cloud Logging log bucket via folder-level aggregated sink, with Log Analytics enabled for BigQuery SQL querying.

5. **Third-party solutions (Datadog, Splunk, Elastic)**: External log aggregation and analysis platforms.

## Decision Rationale

### Justification

The two-tier approach optimizes for the different access patterns and cost profiles of real-time operational data vs audit logs:

- **Real-time operational data** (e.g., AI diagnostic findings, E2E test results, operational events) is typically small volume, queried frequently, and needs immediate visibility. BigQuery streaming is ideal — near-instant query availability at negligible cost for this volume.

- **Audit logs** are high volume (potentially GiB/day/cluster), queried rarely (compliance audits, security investigations), and primarily need durable retention. A centralized Cloud Logging log bucket with Log Analytics provides a linked BigQuery dataset with standard SQL querying — no external tables, no schema management, and no issues with complex audit log field names.

- **Folder-level aggregated sinks** capture audit logs from all projects under a folder with a single sink definition. New projects are automatically included — no per-project configuration required. The sink creates a copy; original logs remain in each project for Logs Explorer access.

- **Log Analytics** was initially rejected but reconsidered after evaluating the alternative (GCS + BigQuery external tables). External tables require explicit schema management, and Cloud Audit Log fields like `@type` and `authorization.k8s.io/` require backtick-quoting in every query. While technically functional, this adds ongoing query complexity and schema maintenance burden. Log Analytics handles these transparently, providing a clean query surface with zero schema maintenance. The cost premium is justified by the elimination of operational complexity.

### Evidence

Spike validation ([GCP-497](https://redhat.atlassian.net/browse/GCP-497)) confirmed:

- End-to-end diagnostic flow validated: Cloud Monitoring alert → Pub/Sub → Eventarc → Cloud Run diagnose-agent → structured JSON stdout → Cloud Logging → per-project sink → BigQuery table → SQL query returns results
- Diagnostic findings arrive in BigQuery within 1-2 minutes of the agent emitting them
- BigQuery views provide a clean query surface that flattens `jsonPayload.*` fields — users query `SELECT * FROM view_recent_findings` without needing to know the Cloud Logging envelope schema
- Folder-level aggregated sink captures Admin Activity, System Event, and Policy Denied from all projects under a folder with a single sink definition — cross-project audit logs queryable via standard SQL immediately
- Log Analytics linked dataset handles complex audit log schema transparently — no backtick-quoting for `@type` fields, no schema management
- GCS external tables were initially implemented but rejected due to schema management burden and query complexity with special-character field names — Log Analytics eliminates both
- MCP BigQuery integration validated: diagnose-agent queries diagnostic history before investigating alerts, references prior findings in diagnosis
- Pydantic schema validation (`DiagnosticFinding` model) ensures type consistency before the first write to BigQuery, preventing silent schema-on-first-write errors

### Comparison

**Alternative 1 (BigQuery for everything)** was rejected because:
- Audit log volume at scale (10-50 GiB/day/cluster) makes BigQuery streaming ingestion expensive ($0.05/GiB)
- Audit logs are rarely queried — paying for BigQuery active storage ($0.02/GiB/month) when GCS Archive is $0.0012/GiB/month wastes 94% of the storage budget
- At intermediate scale (200 clusters): ~$5,800/month (BigQuery) vs ~$281/month (GCS with lifecycle). At production scale (~2,500 clusters, ~180 projects): costs scale proportionally — the chosen Log Analytics solution costs ~$85-225/month (see Cost section)

**Alternative 3 (GCS + external tables)** was initially implemented but rejected because:
- Cloud Audit Logs contain `@type` fields that require backtick-quoting in BigQuery — external tables with autodetect fail, and explicit schemas require workarounds for every query
- Workaround (flat schema with `JSON_VALUE()` queries) adds ongoing complexity and non-standard query patterns
- Two-phase deployment required (create bucket → wait for data → create external tables)
- Cost savings (~$720/yr at production scale) did not justify the operational burden

**Alternative 5 (Third-party)** was rejected because:
- Adds external dependency and vendor lock-in
- Requires data egress from GCP (cost and sovereignty concerns)
- GCP-native solution aligns with the platform's observability stack (GMP, Cloud Monitoring, Cloud Logging)

## Consequences

### Positive

- **Cost-optimized storage**: Real-time operational data in BigQuery (pennies/month at expected volume), audit logs in a centralized log bucket with Log Analytics (~$1,020/yr at production scale)
- **Unified query surface**: Standard BigQuery SQL for both data tiers — native tables for streaming operational data, Log Analytics linked dataset for audit logs. No external tables, no schema management, no `JSON_VALUE()` workarounds
- **Compliance-ready**: Centralized log bucket with configurable retention meets ISO 27001 and SOC 2 audit log retention requirements. Logs are immutable within Cloud Logging
- **Zero-maintenance audit capture**: Folder-level aggregated sink automatically captures audit logs from all current and future projects under the folder — no per-project configuration needed
- **Independently toggleable**: Diagnostic findings (`enable_data_lake`) and audit log shipping (`enable_audit_logs`) can be enabled/disabled independently for cost control
- **Reusable modules**: `data-lake` (dataset + log bucket + views) and `data-lake-sink` (per-project streaming sink) follow the established child module pattern
- **AI enrichment via MCP**: The diagnose-agent connects to Google's managed BigQuery MCP server to query a cluster's prior diagnostic history before investigating new alerts. This creates a feedback loop — the agent references prior root causes, detects recurring patterns, and escalates systemic issues instead of repeating point remediations. Validated end-to-end: agent queries history, finds prior PVC alerts, and incorporates that context into current etcd diagnosis.
- **Extensible MCP pattern**: The MCP client bridge (`McpToolRegistry`) is generic — any MCP server can be registered with a prefix. Future integrations (Cloud Logging MCP, custom MCPs) follow the same pattern with no agent code changes.
- **SRE investigation**: Per-cluster diagnostic timeline available via `view_recent_findings` with `WHERE cluster_id = '...'`
- **No infrastructure to manage**: Cloud Logging handles sink routing and audit log storage, BigQuery handles query execution, Log Analytics handles the linked dataset — no ETL pipelines, no Dataflow, no custom code

### Negative

- **Log Analytics is read-only**: The linked BigQuery dataset does not support DML, views, clustering, or materialized views. Queries use standard SQL but cannot create derived tables within the linked dataset. Saved queries or views in a separate dataset can reference the linked dataset if needed.
- **Log Analytics ingestion cost**: Routing audit logs to a centralized log bucket incurs Cloud Logging ingestion pricing ($0.50/GiB), even for Admin Activity and System Event logs that are free in per-project `_Required` buckets. At production scale, this is ~$85-225/month (volume-dependent) — a deliberate trade-off for centralized queryability.
- **Sink-auto-created table name fragility**: BigQuery tables created by Cloud Logging sinks are named after the log source (e.g., `run_googleapis_com_stdout` for Cloud Run stdout). If the source changes execution environment, the table name changes. The `diagnostic_findings_table` variable mitigates this but doesn't eliminate the coupling.
- **Folder sink requires folder-level permissions**: The Terraform identity creating the folder sink needs `roles/logging.configWriter` at the folder level, which may require coordination with platform admins in production.
- **Log bucket retention limits**: Cloud Logging log buckets support a maximum retention of 3,650 days (10 years). If longer retention is required for compliance, a secondary GCS archive sink can be added alongside the Log Analytics bucket.

## Cross-Cutting Concerns

### Reliability

* **Sink delivery guarantees**: Cloud Logging sinks provide at-least-once delivery. BigQuery streaming sinks deliver data within minutes. The folder sink to the centralized log bucket delivers audit logs in near real-time (same Cloud Logging infrastructure). Both handle transient failures with built-in retry.
* **Sink health monitoring**: Sink health should be monitored via Cloud Monitoring's `logging.googleapis.com/exports/error_count` metric at the project level.
* **Schema evolution**: The diagnostic agent uses a versioned Pydantic schema (`schema_version` field). New fields are additive and safe — BigQuery auto-extends the schema on new columns. Type changes or renames require a schema version bump and migration plan. The `evidence` field is serialized as a JSON string to avoid repeated field schema complexity.

### Security

* **Encryption at rest**: For the spike, Google-managed encryption is used. Production deployment MUST add CMEK (Customer-Managed Encryption Keys) to the BigQuery dataset (`default_encryption_configuration`) and the centralized log bucket.
* **Audit log immutability**: Cloud Logging log buckets are append-only — logs cannot be modified or deleted once written. This is stronger than GCS bucket versioning.
* **IAM least privilege**: Sink writer identities get only the minimum required role. The centralized log bucket restricts access to authorized principals only.
* **MCP security**: The MCP client uses Application Default Credentials with `bigquery.readonly` scope (read-only). Access tokens are cached with thread-safe refresh. Error messages are sanitized to strip project IDs and service account emails before returning to the model. The `DATA_LAKE_PROJECT_ID` env var is validated against GCP project ID format regex to prevent prompt injection attacks.
* **SQL injection prevention**: Alert payload fields (cluster_id, namespace, region) are validated against strict format regexes (UUID, k8s namespace, GCP region) before the model constructs SQL queries. SQL metacharacters (`;`, `'`, `"`, `\`) are stripped.

### Cost

**Estimated monthly costs (production scale: 30 regions, ~180 projects, ~2,500 hosted clusters):**

| Component | Dev (10 clusters) | Production (~2,500 clusters) |
|-----------|-------------------|------------------------------|
| BigQuery streaming (operational data) | $0.02 | $0.30 |
| BigQuery storage (operational data) | $0.02 | $0.36 |
| BigQuery queries (on-demand) | FREE (<1 TB) | FREE (<1 TB) |
| Log Analytics ingestion (audit logs) | $3 | $85-225 |
| **Total** | **~$3** | **~$85-225/mo (~$1,020-2,700/yr)** |

**Audit log volume estimate**: ~180 projects × ~32 MB/day average (Admin Activity + System Event + Policy Denied) = ~170 GiB/month at the low end. Actual volume depends on API activity — projects with many hosted clusters or active deployments will be higher. Range shown reflects low-to-high per-project estimates.

**Key cost decisions:**
- Folder-level aggregated sink captures all three audit log types (Admin Activity, System Event, Policy Denied) in one centralized log bucket with Log Analytics enabled.
- Log Analytics ingestion costs $0.50/GiB but eliminates GCS storage costs, external table management, and schema maintenance — justified by operational simplicity.
- Data Access logs are NOT captured — too high volume ($0.50/GiB ingestion, 10-200 GB/day/cluster). Enable selectively if needed.
- BigQuery storage alert provides bill shock protection for streaming data.
- Filter validation on the sink module prevents accidental empty-filter cost explosion.

**Cost comparison — why Log Analytics over GCS:**

| Approach | Annual Cost (production) | Schema Maintenance | Setup Complexity |
|----------|-------------------------|-------------------|-----------------|
| GCS + External Tables | ~$300-700/yr | High (manual JSON schema, quoted-identifier workarounds) | Two-phase deployment |
| Log Analytics | ~$1,020-2,700/yr | None (handled by Google) | Single deployment |

The premium eliminates ongoing schema management, query syntax workarounds, and two-phase deployment complexity.

### Operability

* **Deployment model**: `data-lake` module deploys the BigQuery dataset, views, alerts, and (optionally) the centralized audit log bucket with Log Analytics. The folder-level aggregated sink is a single Terraform resource that captures all current and future projects. `data-lake-sink` module deploys per-project sinks for streaming operational data to BigQuery.
* **Single-phase deployment**: Unlike the GCS approach (which required two applies), Log Analytics works immediately. The folder sink routes to a centralized log bucket, Log Analytics creates the linked BigQuery dataset, and audit logs are queryable via SQL as soon as data arrives. No external tables to create, no schema to manage.
* **Zero-config for new projects**: The folder sink's `include_children = true` automatically captures audit logs from any new project added under the folder. No additional Terraform changes, sinks, or configuration needed.
* **BigQuery views**: Four pre-built views (`view_recent_findings`, `view_findings_by_cluster`, `view_repeat_offenders`, `view_daily_summary`) provide immediate value without SQL knowledge.
* **Notebook templates**: Jupyter notebooks for data lake analysis and audit log investigation are provided for local use (VS Code) with BigQuery Python client.

## Architecture

### Data Flow

```text
Folder (contains all region + MC projects)
    │
    ├── Folder-Level Aggregated Sink ──→ Centralized Log Bucket
    │     filter: Admin Activity OR        (Log Analytics enabled)
    │             System Event OR                   │
    │             Policy Denied                     ▼
    │     include_children: true          Linked BigQuery Dataset (automatic)
    │     (captures ALL projects          ├── standard SQL queries
    │      under folder automatically)    ├── full audit log schema
    │                                     └── no @type issues
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
| `data-lake` | BigQuery dataset, views, alerts for streaming operational data. Centralized log bucket with Log Analytics for audit logs (gated by `enable_audit_logs`). | Global module |
| `data-lake-sink` | Per-project log sink for streaming operational data (BigQuery) | Region module, MC module |
| Folder sink | Routes audit logs from all projects to the centralized log bucket | Global or environment-level Terraform config |

### Component Summary

| Component | Resource Type | Managed By | Purpose |
|-----------|-------------|-----------|---------|
| BigQuery dataset | `google_bigquery_dataset` | Terraform (data-lake module) | Houses streaming operational data and views |
| BigQuery views | `google_bigquery_table` (view) | Terraform (data-lake module) | Pre-built queries for operational data |
| Storage alerts | `google_monitoring_alert_policy` | Terraform (data-lake module) | BigQuery cost guardrails |
| Streaming data sink | `google_logging_project_sink` | Terraform (data-lake-sink module) | Per-project: routes operational data to BigQuery |
| Centralized log bucket | `google_logging_project_bucket_config` | Terraform (data-lake module) | Stores audit logs from all projects with Log Analytics (gated by `enable_audit_logs`) |
| Audit sink | `google_logging_folder_sink` | Terraform (global/env config) | Folder-level: routes all audit logs to centralized log bucket |
| Log Analytics linked dataset | `google_logging_linked_dataset` | Terraform (data-lake module) | Creates the linked BigQuery dataset (`_AllLogs` view) for SQL querying |
| MCP client bridge | `mcp_client.py` (Python) | Agent code | Connects agent to BigQuery MCP for history queries |
| MCP cross-project IAM | `google_project_iam_member` | Terraform (MC region-iam.tf) | `bigquery.dataViewer`, `bigquery.jobUser`, `mcp.toolUser` for MC agent → data lake |

## Spike Findings Summary

| Question | Finding |
|----------|---------|
| Native tables vs external tables vs Log Analytics? | Native BigQuery for real-time operational data (streaming). Log Analytics for audit logs (eliminates schema issues, no external tables needed) |
| Per-project vs folder-level audit sinks? | Folder-level aggregated sink — one sink captures all projects, auto-includes new ones |
| GCS vs Log Analytics for audit logs? | Log Analytics chosen. GCS external tables require backtick-quoting for `@type` fields and manual schema management. Log Analytics handles this transparently. Cost premium justified by zero operational overhead |
| Schema-on-first-write risk? | Mitigated by Pydantic schema validation in agent code. Schema version field tracks evolution |
| Cost at scale? | ~$85-225/month at production scale (~180 projects). Would be ~$5,800 if using BigQuery streaming for audit logs |
| MCP BigQuery integration? | Google Managed MCP server works for cross-project queries. Requires `roles/mcp.toolUser`, `bigquery.dataViewer`, `bigquery.jobUser` on the data lake project |
| Does the agent use history? | YES — agent queries prior findings as Step 1, references recurring patterns in diagnosis, and escalates systemic issues |
| SQL injection risk? | Mitigated via UUID validation on cluster_id, namespace regex, SQL metacharacter stripping |

## Table Naming

BigQuery tables created by Cloud Logging sinks are auto-named after the log source (e.g., `run_googleapis_com_stdout` for Cloud Run stdout). For production clarity, each data source uses the `google-cloud-logging` Python client to write to a **custom log name**, producing a meaningful BigQuery table name:

| Data Source | Log Name | BigQuery Table | Status |
|------------|----------|---------------|--------|
| Diagnose agent findings | `diagnostic_findings` | `diagnostic_findings` | Implemented |
| E2E test results | `e2e_test_results` | `e2e_test_results` | Future |
| ArgoCD promotion state | `argocd_promotions` | `argocd_promotions` | Future |

BigQuery views (e.g., `view_recent_findings`) provide a clean query surface on top of these tables.

**Important: Cloud Run does not support log name overrides via stdout/stderr.** The `logging.googleapis.com/logName` field in structured JSON is only processed by the Logging agent on GCE/GKE, not by Cloud Run. Applications running on Cloud Run must use the `google-cloud-logging` Python client (or equivalent) to write directly to the Cloud Logging API with a custom log name. This requires `roles/logging.logWriter` on the application's service account.

## Future Data Sources

The data lake architecture is designed to support additional streaming data sources beyond the initial diagnostic findings. Potential future tenants include:

- **ArgoCD application state**: Version, target revision, image overrides, and promotion status for each application — enabling promotion tracking and rollback analysis
- **E2E promotion test results**: Pass/fail results from environment-targeted promotion tests, enabling confidence scoring before promoting to the next environment
- **Cluster lifecycle events**: Creation, deletion, scaling, and upgrade events for fleet-wide tracking

Each new data source follows the same pattern:
1. Define a Pydantic schema for structured output
2. Use `google-cloud-logging` to write to a custom log name (requires `roles/logging.logWriter`)
3. Create a Cloud Logging sink filtering on `log_id("<log_name>")`
4. BigQuery auto-creates a table named after the log name
5. Create views for user-friendly querying

---

**Related Documentation:**
- Jira Spike: [GCP-497](https://redhat.atlassian.net/browse/GCP-497) — BigQuery Observability Lake Spike
- Alerting Framework: [integrated-alerting-framework.md](integrated-alerting-framework.md)
- Observability Platform: [observability-google-managed-prometheus.md](observability-google-managed-prometheus.md)
