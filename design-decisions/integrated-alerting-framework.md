# Integrated Alerting Framework for HCP Monitoring

***Scope***: GCP-HCP

**Date**: 2026-03-04

## Decision

Use Cloud Monitoring alerting policies with PromQL conditions evaluated against Google Managed Prometheus (GMP) metrics, with dual notification routing to PagerDuty (incident management) and a Cloud Run diagnose agent (automated diagnosis) via Pub/Sub and Eventarc.

## Context

### Problem Statement

GCP HCP requires GCP-native alerting for HyperShift Hosted Control Planes and region cluster infrastructure. The solution must evaluate PromQL-based alert conditions against metrics already collected by GMP (see [observability-google-managed-prometheus.md](observability-google-managed-prometheus.md)), route incidents to PagerDuty for SRE response, and trigger automated diagnosis workflows to accelerate mean time to resolution. Alert policies are scoped per project (management cluster and region cluster) to keep alerting isolated within regions.

### Constraints

- **Per-project alert policies required**: Alert policies are scoped per project (management cluster and region cluster) to keep alerting isolated within regions
- **Dual notification routing**: Alerts must notify both PagerDuty (human response) and a Cloud Run diagnose agent (automated analysis) simultaneously
- **GMP metric dependency**: Alert conditions use PromQL against metrics already ingested into GMP via the hybrid Prometheus architecture (GCP-343)
- **Multi-severity alerting**: Alerts require WARNING and ERROR thresholds for graduated incident response
- **Ephemeral HCP namespaces**: HCP clusters are frequently created and deleted, requiring auto-close strategies to prevent zombie alerts
- **Config Connector for alert rules**: Alert policies and notification channels are deployed as Config Connector (KCC) custom resources via ArgoCD, enabling consistent deployment across hundreds of projects without per-project Terraform applies. Supporting infrastructure (Pub/Sub topics, Eventarc triggers, Cloud Run, IAM) remains Terraform-managed.

### Assumptions

- GMP metrics referenced by alert policies are available because they pass through the Prometheus export allowlist filter (GCP-343 filtering strategy)
- PagerDuty service integration keys will be shared across projects and retrieved from Google Secret Manager
- The Cloud Run `diagnose-agent` service will be deployed per project alongside the alerting infrastructure
- The `hypershift_cluster_limited_support_enabled` metric will have a mechanism to be toggled per cluster (outside the scope of this work)

## Alternatives Considered

1. **Cloud Monitoring alerting policies with PromQL (chosen)**: Define alert policies in Cloud Monitoring using `conditionPrometheusQueryLanguage` to evaluate PromQL expressions against GMP metrics. Notification channels route to PagerDuty and Pub/Sub (for Eventarc-triggered Cloud Run diagnosis). Policies are per-project, deployed as Config Connector custom resources via ArgoCD.

2. **Self-managed Alertmanager (cluster-side alerting)**: Deploy Alertmanager alongside the existing self-managed Prometheus instances. Alert rules evaluated locally in-cluster, with Alertmanager routing to PagerDuty and webhooks. Requires managing Alertmanager infrastructure, configuring inhibition rules, and maintaining receiver configurations.

## Decision Rationale

### Justification

Cloud Monitoring alerting policies provide native GCP integration with GMP metrics, per-project scoping that keeps alerting isolated within regions across both management cluster and region cluster projects, built-in PromQL support via `conditionPrometheusQueryLanguage`, and native notification channel types for both PagerDuty and Pub/Sub — eliminating the need to deploy and manage additional alerting infrastructure.

### Evidence

Spike validation confirmed:
- PromQL conditions use the `_id` label (HCP cluster UUID) directly — available in notification template variables as `${metric.label._id}`
- Dual notification routing (PagerDuty + Pub/Sub) works from a single alert policy with multiple notification channels
- Eventarc successfully triggers Cloud Run `/diagnose` endpoint from Pub/Sub messages published by Cloud Monitoring
- Auto-close correctly handles ephemeral HCP namespace deletion without leaving zombie alerts
- Mutually exclusive WARNING/ERROR threshold queries prevent duplicate notifications across severity levels
- Config Connector `MonitoringAlertPolicy` and `MonitoringNotificationChannel` resources validated on integration cluster — KCC resolves cross-references by resource name, eliminating hardcoded channel IDs

### Comparison

**Self-managed Alertmanager** was rejected because:
- Requires deploying and maintaining Alertmanager pods (HA pair recommended), adding operational burden
- Alertmanager's `inhibit_rules` provide native severity suppression (advantage), but Cloud Monitoring's mutually exclusive PromQL ranges achieve the same effect without additional infrastructure
- Alertmanager receivers require separate configuration for PagerDuty and webhook integrations, whereas Cloud Monitoring provides built-in notification channel types
- Alert rules evaluated locally in Prometheus would not benefit from GMP's managed evaluation infrastructure
- Would create a divergent alerting architecture from the GCP-native observability stack established in GCP-343

## Consequences

### Positive

- **Native GCP integration**: Alert policies are first-class GCP resources, visible in Cloud Console, queryable via API, and deployable as Config Connector custom resources (`MonitoringAlertPolicy`)
- **No alerting infrastructure to manage**: Cloud Monitoring handles alert evaluation, notification dispatch, and incident lifecycle — no Alertmanager pods, no HA configuration
- **PromQL support**: `conditionPrometheusQueryLanguage` enables direct use of PromQL expressions, including complex multi-metric conditions and joins against recording rules
- **Template variables in notifications**: Documentation fields support `${metric.label.*}` and `${resource.label.*}` variables for dynamic notification content (e.g., `${metric.label._id}` for cluster UUID)
- **Per-project isolation**: Alert policies evaluate metrics within their respective project (management cluster or region cluster), keeping alerting isolated within regions and matching the data isolation model from GCP-343
- **Built-in notification channels**: Native PagerDuty and Pub/Sub channel types eliminate custom webhook plumbing
- **Automated diagnosis pipeline**: Pub/Sub + Eventarc + Cloud Run provides a serverless, auto-scaling diagnosis trigger path with no infrastructure to manage beyond the Cloud Run service

### Negative

- **Multi-severity requires separate policies**: Cloud Monitoring does not support multiple severity levels within a single alert policy. Every alert needing WARNING and ERROR thresholds requires two independent policies (2x maintenance, coordination risk if one policy is updated but not the other)
- **WARNING/ERROR mutual exclusion is a workaround**: To prevent duplicate notifications, the WARNING query must include an upper bound (e.g., `> 30 AND <= 85`). When the metric crosses into ERROR range, the WARNING auto-resolves because the condition is no longer met — this is a side effect of the query bounds, not a native suppression mechanism. Unlike Alertmanager's `inhibit_rules`, there is no explicit relationship between the two policies
- **PagerDuty resolution is one-way only**: Resolving an incident in PagerDuty does not resolve the corresponding Cloud Monitoring incident. If the alert condition is still true, Cloud Monitoring reopens the PagerDuty incident. The "PagerDuty Sync" console option does not provide bidirectional resolution despite the name
- **PagerDuty incident title is not customizable from GCP**: The PagerDuty incident title is auto-generated from Cloud Monitoring internal fields (condition name, duration, policy name, incident link). The `documentation.subject` field appears in the payload `details` but does not control the PagerDuty title. Workarounds: (A) PagerDuty Event Orchestration rules to rewrite the summary (recommended), or (B) custom Pub/Sub pipeline to send fully custom PagerDuty events
- **No label-based alert suppression across policies**: Cloud Monitoring snoozes support label filters, but only when targeting a single alert policy (max 16 policies per snooze, label filters restricted to exactly 1). There is no native way to suppress all alert policies for a specific cluster or namespace for a time window. See the [Recording Rule for Cluster Alertability](#recording-rule-for-cluster-alertability) design pattern for the mitigation.
- **Template variable resolution in PromQL alerts**: For PromQL-based alert conditions, `metric.type` and `metric.displayName` resolve to `"__missing__"` in the notification payload. Only `metric.labels.*` and `resource.labels.*` contain useful values

## Cross-Cutting Concerns

### Reliability

* **Auto-close strategy**: All alert policies use a configurable `autoClose` duration. When a metric stops reporting data entirely (e.g., HCP namespace deleted), the alert auto-closes after the configured window. When the metric returns to normal, the alert closes immediately. When the metric is still violating the threshold, the alert stays open indefinitely. The auto-close window should balance ephemeral HCP lifecycle (prevents zombie alerts for decommissioned clusters) against brief scrape gaps (avoids premature closure of real incidents).

* **Mutual exclusion for severity levels**: WARNING policies include an upper bound in the PromQL condition so that when the metric crosses into ERROR range, the WARNING condition becomes false and auto-resolves. This prevents both WARNING and ERROR from firing simultaneously. The thresholds are coupled across two separate policy definitions — changing the ERROR threshold requires updating the WARNING upper bound to match.

* **Notification delivery**: Cloud Monitoring handles notification dispatch with built-in retry. Pub/Sub provides at-least-once delivery to the Eventarc trigger. The Cloud Run diagnose agent receives the alert payload via HTTP push with OIDC authentication. A dead letter topic is needed on the Pub/Sub subscription to prevent retry amplification when the diagnose-agent is unhealthy.

* **Alert storms and notification rate limits**: Cloud Monitoring creates one incident per matching time series, not one per policy. During a widespread failure, each affected HCP generates a separate incident and notification per alert policy. PagerDuty content-based grouping is configured and will collapse related alerts into a single PagerDuty incident.

* **Alert suppression via recording rules**: All alert policies join against a single `hcp:cluster:alertable` recording rule that evaluates cluster-level exclusion signals (e.g., `hypershift_cluster_limited_support_enabled != 1`). This centralizes suppression in one place: updating the recording rule expression suppresses alerts across all policies, without touching any alert policy definitions. Future exclusion signals (maintenance mode, cluster decommissioning) are added to the same recording rule. See the [Recording Rule for Cluster Alertability](#recording-rule-for-cluster-alertability) design pattern.

### Security

* **IAM bindings**: Three service agents require specific IAM bindings for the alerting pipeline. See the [IAM Requirements](#iam-requirements) section for the full table.

* **Cloud Run IAM must be service-level**: The `roles/run.invoker` binding must be applied at the Cloud Run service level (`google_cloud_run_service_iam_member`), not the project level. Project-level `run.invoker` is not sufficient for Eventarc push invocations.

* **Authenticated Cloud Run invocations**: Cloud Run requires OIDC identity tokens for all requests. The Eventarc push subscription authenticates using the `alerting-pipeline` service account.

### Performance

* **Alert detection latency**: The minimum time from metric violation to notification is determined by the evaluation interval plus the duration. For example, with a 30-second evaluation interval and 120-second duration, the minimum detection time is ~150 seconds. Longer durations (e.g., 30 minutes) ensure alerts fire only after sustained violations, reducing noise at the cost of slower detection. These values are tunable per alert policy.

* **Notification dispatch**: Cloud Monitoring dispatches notifications to all channels in parallel. PagerDuty notifications arrive within seconds of the alert firing. The Pub/Sub → Eventarc → Cloud Run path adds minimal latency (typically under 10 seconds end-to-end).

* **PromQL evaluation overhead**: Multi-metric ratio queries (e.g., etcd disk utilization) scan more samples per evaluation than single-metric threshold queries, which impacts alert evaluation cost at scale.

### Cost

* **Notification channel costs**: PagerDuty and Pub/Sub notification channels have no additional Cloud Monitoring cost beyond the alert evaluation itself. PagerDuty costs are per the PagerDuty subscription plan. Pub/Sub and Cloud Run costs are negligible at expected alert volumes.

* **Cost per alert type at scale**: Cloud Monitoring charges $0.10/month per alert condition (fixed) plus $0.35 per million time-series returned by alert query evaluation. Assuming a 30-second evaluation interval and ~3 time series per HCP per evaluation (typical for multi-metric or ratio queries), the cost per alert policy and per alert type (WARNING + ERROR = 2 policies) is:

  | HCPs | Per policy/month | Per alert type/month |
  |------|-----------------|---------------------|
  | 10 | $1.01 | $2.02 |
  | 100 | $9.17 | $18.34 |
  | 500 | $45.46 | $90.92 |
  | 1,000 | $90.82 | $181.64 |

  These are additive per alert type. For example, 20 alert types at 1,000 HCPs ≈ $3,633/month in alerting evaluation costs. See [COST-ANALYSIS.md](../experiments/google-managed-prometheus/COST-ANALYSIS.md) for the full GMP cost model including ingestion.

### Operability

* **Required APIs**: Three APIs must be enabled per project (management cluster and region cluster):

  | API | Purpose |
  |-----|---------|
  | `eventarc.googleapis.com` | Eventarc trigger management |
  | `eventarcpublishing.googleapis.com` | Eventarc event delivery |
  | `run.googleapis.com` | Cloud Run service |

## Architecture

### Alert Notification Flow

```
Cloud Monitoring Alert Policy (per management cluster / region cluster project)
  │
  ├──→ PagerDuty Notification Channel → PagerDuty Incident (SRE response)
  │
  └──→ Pub/Sub Notification Channel
        → Pub/Sub Topic: diagnose-workflow
          → Eventarc Trigger: diagnose-alert
            → Cloud Run: diagnose-agent /diagnose (automated diagnosis)
```

Each alert policy attaches multiple notification channels. When a condition fires:
1. **PagerDuty** receives the incident for human triage and response
2. **Pub/Sub** publishes the alert payload to the `diagnose-workflow` topic
3. **Eventarc** triggers a push to the Cloud Run `diagnose-agent` service at `/diagnose`
4. **Cloud Run** performs automated diagnosis (pod inspection, log analysis, AI-assisted root cause analysis) and surfaces results

### Component Summary

| Component | Resource | Managed By | Purpose |
|-----------|----------|-----------|---------|
| Alert policy | `MonitoringAlertPolicy` | Config Connector / ArgoCD | Evaluates PromQL condition against GMP metrics |
| PagerDuty channel | `MonitoringNotificationChannel` (type: `pagerduty`) | Config Connector / ArgoCD | Routes incidents to PagerDuty |
| Pub/Sub channel | `MonitoringNotificationChannel` (type: `pubsub`) | Config Connector / ArgoCD | Publishes alert to diagnosis pipeline |
| Pub/Sub topic | `google_pubsub_topic` | Terraform | Receives alert notifications |
| Eventarc trigger | `google_eventarc_trigger` | Terraform | Dispatches Pub/Sub messages to Cloud Run |
| Cloud Run service | `google_cloud_run_service` | Terraform | Hosts diagnose-agent for automated analysis |

## Alert Policy Design Patterns

### Recording Rule for Cluster Alertability

A single Prometheus recording rule pre-calculates which clusters are eligible for alerting. All alert policies join against this recording rule to exclude clusters that should not trigger alerts (e.g., clusters in limited support).

```yaml
groups:
  - name: hcp-cluster-alertability
    rules:
      - record: hcp:cluster:alertable
        expr: hypershift_cluster_limited_support_enabled != 1
```

Alert policies include `AND on(_id) hcp:cluster:alertable` in their PromQL condition:

```promql
kube_deployment_status_replicas_available{deployment="kube-apiserver"}
  AND on(_id) hcp:cluster:alertable < 1
```

**Why this pattern is required**: Cloud Monitoring has no native mechanism to suppress alerts across all policies for a specific cluster or namespace. Snoozes support label filters but only when targeting a single policy, making them impractical at scale. By joining against a single recording rule, one exclusion condition automatically suppresses alerts across every policy — no per-policy changes needed.

**Adding new exclusion conditions**: Future exclusion signals (e.g., maintenance mode, cluster decommissioning) are added to the recording rule expression. All downstream alert policies inherit the filter automatically via the join.

The recording rule is evaluated locally by the self-managed Prometheus instance and the result is exported to GMP. Cloud Monitoring alert policies then join their metric queries against `hcp:cluster:alertable`.

### Mutually Exclusive WARNING/ERROR Thresholds

To prevent both WARNING and ERROR from firing simultaneously, the WARNING policy bounds its condition with both a lower and upper threshold:

| Severity | Condition | Behavior |
|----------|-----------|----------|
| WARNING | `metric > 30 AND metric <= 85` | Fires when degraded; auto-resolves when ERROR fires |
| ERROR | `metric > 85` | Fires at critical threshold; no upper bound |

When the metric crosses from WARNING into ERROR range, the WARNING condition becomes false (value no longer `<= 85`) and auto-resolves. This is not a native suppression mechanism — it relies on the query bounds being coordinated across two independent policies.

**Note**: This upper-bound technique is only necessary for continuous metrics (e.g., percentages). For integer-valued metrics like `kube_deployment_status_replicas_available`, the WARNING and ERROR ranges are naturally mutually exclusive (e.g., exactly 1 replica vs 0 replicas) and do not require an explicit upper bound.

### Auto-Close for Ephemeral HCP Namespaces

All policies use a configurable `alertStrategy.autoClose` duration (e.g., 1 hour):

| Scenario | Behavior |
|----------|----------|
| Metric still violating threshold | Alert stays open indefinitely |
| Metric returns to normal | Alert closes immediately |
| Metric disappears (namespace deleted) | Alert auto-closes after the configured window |

The auto-close window should be tuned to prevent zombie alerts for decommissioned HCP clusters while avoiding premature closure of real incidents from brief scrape gaps.

### Documentation Template Variables

Alert policies include `documentation.content` and `documentation.subject` fields that support template variable substitution:

```json
{
  "documentation": {
    "content": "kube-apiserver pods not ready on cluster ${metric.label._id}\n\nHCP Namespace: ${resource.label.namespace}\nManagement Cluster: ${resource.label.cluster}",
    "subject": "ControlPlanePodsNotReadyforHCP kube-apiserver - ${metric.label._id}",
    "mimeType": "text/markdown"
  }
}
```

**Note**: Cloud Monitoring prepends `[ALERT - <Severity>]` to the configured subject automatically. The rendered subject in the notification payload appears as `[ALERT - Error] ControlPlanePodsNotReadyforHCP kube-apiserver - aaaaaaaa-bbbb-...`.

## Notification Payload

Cloud Monitoring delivers alert notifications as JSON payloads with schema version 1.2. The payload is delivered to Pub/Sub (and subsequently to Cloud Run via Eventarc) and contains the incident details.

### Captured Real Payload (EtcdHighDiskUtilization - Error)

```json
{
  "version": "1.2",
  "incident": {
    "incident_id": "0.abc123def456",
    "scoping_project_id": "{PROJECT_ID}",
    "scoping_project_number": 123456789012,
    "severity": "Error",
    "state": "open",
    "started_at": 1772574279,
    "ended_at": null,
    "policy_name": "EtcdHighDiskUtilization - Error",
    "condition_name": "Etcd disk utilization > 85%",
    "summary": "Condition \"Etcd disk utilization > 85%\" was true for the last 2m0s alert started.",
    "url": "https://console.cloud.google.com/monitoring/alerting/alerts/...",
    "documentation": {
      "content": "Etcd disk utilization exceeds 85% of its database quota on cluster aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee. Immediate action is required...",
      "mime_type": "text/markdown",
      "subject": "[ALERT - Error] EtcdHighDiskUtilization Error - cluster aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
    },
    "resource": {
      "type": "prometheus_target",
      "labels": {
        "namespace": "clusters-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-my-hcp"
      }
    },
    "metric": {
      "type": "__missing__",
      "displayName": "__missing__",
      "labels": {
        "cluster_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
        "region": "us-central1",
        "value": "99.50849887364325"
      }
    }
  }
}
```

### Key Payload Observations

| Field | Notes |
|-------|-------|
| `version` | `"1.2"` for real alerts; `"test"` for Test Channel payloads |
| `metric.type` / `metric.displayName` | `"__missing__"` for PromQL-based alerts — do not rely on these fields |
| `metric.labels._id` | HCP cluster UUID — available directly as a metric label |
| `metric.labels.value` | Contains the actual observed metric value as a string |
| `resource.labels` | Only `namespace` present for PromQL alerts; use `scoping_project_id` for project identification |
| `documentation.subject` | Cloud Monitoring prepends `[ALERT - <Severity>]` to the configured subject |
| `documentation.content` | Template variables (`${metric.label._id}`) are resolved to actual values |
| `state` | Lowercase `"open"` in real alerts vs uppercase `"OPEN"` in test payloads |

## IAM Requirements

### Service Accounts and Agents

| Service Agent / Account | Role | Scope | Purpose |
|------------------------|------|-------|---------|
| `service-{NUM}@gcp-sa-monitoring-notification.iam.gserviceaccount.com` | `roles/pubsub.publisher` | Pub/Sub topic `diagnose-workflow` | Publish alert notifications to topic |
| `service-{NUM}@gcp-sa-pubsub.iam.gserviceaccount.com` | `roles/iam.serviceAccountTokenCreator` | On `alerting-pipeline` SA | Mint OIDC tokens for Eventarc push subscription |
| `alerting-pipeline@{PROJECT}.iam.gserviceaccount.com` | `roles/run.invoker` | Cloud Run service `diagnose-agent` (service-level) | Invoke Cloud Run diagnose endpoint |
| `alerting-pipeline@{PROJECT}.iam.gserviceaccount.com` | `roles/eventarc.eventReceiver` | Project | Receive Pub/Sub events via Eventarc |
| `alerting-pipeline@{PROJECT}.iam.gserviceaccount.com` | `roles/logging.logWriter` | Project | Write diagnostic logs |

**Critical**: `roles/run.invoker` must be a service-level IAM binding (`google_cloud_run_service_iam_member`), not project-level. Project-level `run.invoker` is not sufficient for Eventarc push invocations.

## Deployment Model

Alert policies and notification channels are deployed via **Config Connector (KCC)** as Kubernetes custom resources, synced to each management and region cluster by ArgoCD. This enables a single set of YAML manifests in Git to be applied across hundreds of projects without per-project Terraform applies.

Supporting infrastructure (Pub/Sub topics, Eventarc triggers, Cloud Run, IAM bindings) remains **Terraform-managed** as it is provisioned once per project during cluster setup.

### Config Connector Resources (ArgoCD-managed)

| Component | KCC Kind | API Version | Notes |
|-----------|----------|-------------|-------|
| Alert policy | `MonitoringAlertPolicy` | `monitoring.cnrm.cloud.google.com/v1beta1` | One per alert × severity level |
| PagerDuty notification channel | `MonitoringNotificationChannel` | `monitoring.cnrm.cloud.google.com/v1beta1` | `type: pagerduty`, key from Secret Manager |
| Pub/Sub notification channel | `MonitoringNotificationChannel` | `monitoring.cnrm.cloud.google.com/v1beta1` | `type: pubsub`, references topic |

Alert policies reference notification channels by KCC resource `name` — KCC resolves the Cloud Monitoring channel IDs automatically. No hardcoded channel IDs are needed across projects.

The `cnrm.cloud.google.com/project-id` annotation on each resource controls which GCP project the resource is created in, allowing deployment from any Config Connector-enabled namespace.

**Validated during spike**: Both `MonitoringAlertPolicy` with `conditionPrometheusQueryLanguage` and `MonitoringNotificationChannel` (type `pubsub`) were successfully created and cross-referenced via KCC on the integration cluster.

### Terraform Resources (per-project infrastructure)

| Component | Terraform Resource | Notes |
|-----------|-------------------|-------|
| Pub/Sub topic | `google_pubsub_topic` | `diagnose-workflow` |
| Pub/Sub topic IAM | `google_pubsub_topic_iam_member` | Monitoring notification SA → `pubsub.publisher` |
| Eventarc trigger | `google_eventarc_trigger` | Event type: `google.cloud.pubsub.topic.v1.messagePublished` |
| Cloud Run service | `google_cloud_run_service` | `diagnose-agent` |
| Cloud Run IAM (invoker) | `google_cloud_run_service_iam_member` | Service-level binding, not project-level |
| SA IAM (project-level) | `google_project_iam_member` | `eventarc.eventReceiver`, `logging.logWriter` |
| SA IAM (on SA) | `google_service_account_iam_member` | Pub/Sub agent → `serviceAccountTokenCreator` on `alerting-pipeline` |
| API enablement | `google_project_service` | `eventarc`, `eventarcpublishing`, `run` |

## Acceptance Criteria Coverage (GCP-344)

| Criterion | Status | Evidence |
|-----------|--------|----------|
| Alert policies created using Cloud Monitoring with PromQL | Complete | Two alert types validated (kube-apiserver, etcd) with `conditionPrometheusQueryLanguage` conditions |
| Multi-severity alerting (WARNING + ERROR) | Complete | Separate policies per severity with mutually exclusive thresholds; WARNING auto-resolves when ERROR fires |
| PagerDuty notification routing | Complete | `pagerduty` notification channel type configured and validated |
| Automated diagnosis pipeline triggered by alerts | Complete | Pub/Sub → Eventarc → Cloud Run `/diagnose` pipeline validated end-to-end with real alert payloads |
| Template variables resolve in notifications | Complete | `${metric.label._id}` confirmed working in `documentation.content` and `documentation.subject` |
| Auto-close strategy for ephemeral namespaces | Complete | Configurable `autoClose` duration validated — metric disappearance closes alert after the configured window |
| Infrastructure expressible as IaC | Complete | Alert policies and notification channels mapped to Config Connector resources (ArgoCD-deployed); supporting infrastructure mapped to Terraform resources |

## Next Steps

Implementation stories to be created under [GCP-319](https://redhat.atlassian.net/browse/GCP-319):

1. **Alerting infrastructure Terraform module**: Pub/Sub topic, Eventarc trigger, IAM bindings, API enablement — deployed per project via Terraform during cluster setup
2. **Recording rule for cluster alertability**: Define `hcp:cluster:alertable` recording rule that evaluates `hypershift_cluster_limited_support_enabled != 1`. All alert policies must join against this recording rule.
3. **Config Connector alert policy manifests**: KCC manifests for WARNING + ERROR policy pairs with PromQL conditions joined against `hcp:cluster:alertable`, documentation templates, and notification channel references — deployed via ArgoCD
4. **Config Connector notification channel manifests**: KCC manifests for PagerDuty and Pub/Sub notification channels with Secret Manager integration for PagerDuty keys — deployed via ArgoCD
5. **Initial alert policy deployment**: Deploy kube-apiserver and etcd alert policies to integration environment with production-tuned duration (1800s)
6. **PagerDuty title customization**: Implement PagerDuty Event Orchestration rules (or custom Pub/Sub pipeline) to rewrite verbose auto-generated incident titles
7. **Cloud Run diagnose-agent deployment**: Terraform module for Cloud Run service with IAM, deployed per project
8. **Additional alert definitions**: Extend alert coverage to remaining HCP control plane components (kube-controller-manager, cloud-controller-manager, etc.)
9. **Operational runbooks**: Create runbooks for alert tuning, threshold adjustment, and diagnosis pipeline troubleshooting
10. **Canary alerts for pipeline validation**: End-to-end validation of the alerting pipeline — [GCP-438](https://redhat.atlassian.net/browse/GCP-438)

---

**Related Documentation:**
- Jira Epic: [GCP-319](https://redhat.atlassian.net/browse/GCP-319) — Alerting for HCP Control Plane
- Jira Spike: [GCP-344](https://redhat.atlassian.net/browse/GCP-344) — Alerting Spike
- Observability Platform Decision: [observability-google-managed-prometheus.md](observability-google-managed-prometheus.md)
