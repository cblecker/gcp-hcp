# Zero Operator Remediation: Adopt Google Cloud Workflows for Automated Alert Response

***Scope***: GCP-HCP

**Date**: 2026-02-02

## Decision

Adopt Google Cloud Workflows as the automation platform for Zero Operator remediation workflows, enabling alert-driven and human-triggered remediation actions without direct cluster access. Workflows integrate with Vertex AI (Gemini) for intelligent problem analysis and use the native `gke.request` connector for Kubernetes API operations, supporting SOC 2/HIPAA compliance requirements through Cloud Audit Logs and Privileged Access Manager (PAM) approval gates.

## Context

### Problem Statement

The GCP-HCP platform requires an automation platform that enables operators to execute remediation workflows without direct cluster access (no `oc` or `kubectl` commands). This "Zero Operator" model is required for compliance with SOC 2, HIPAA, and PCI standards. The platform must support both human-triggered workflows (for debugging and investigation) and alert-driven workflows (for automated incident response), while maintaining comprehensive audit trails.

### Constraints

- **Security:** No direct operator access to clusters; all actions must be auditable
- **Compliance:** SOC 2/HIPAA/PCI require audit logs, approval workflows, and least-privilege access
- **Architecture:** Distributed deployment model (one workflow instance per project) for regional independence
- **Integration:** Must integrate with GCP-319 alerting framework via Eventarc triggers
- **Authentication:** Workload Identity Federation only (no service account keys)
- **AI Integration:** Must support Vertex AI for intelligent log analysis and remediation recommendations

### Assumptions

- Google Cloud Workflows `gke.request` connector provides sufficient Kubernetes API access for remediation operations
- Privileged Access Manager (PAM) provides mandatory approval for all destructive actions
- Vertex AI (Gemini) can effectively analyze cluster logs and events to identify root causes
- Eventarc can reliably trigger workflows from Cloud Monitoring alerts
- Per-execution pricing model is cost-effective for remediation workflow patterns (low-frequency, high-value operations)

## Alternatives Considered

### 1. **Google Cloud Workflows (Selected)**

Serverless workflow orchestration platform with native GCP integration. Workflows are defined in YAML and execute as managed services without infrastructure to maintain.

**Key Characteristics:**
- Serverless, fully managed execution (no infrastructure)
- Native `gke.request` connector for GKE API operations
- Eventarc triggers for alert-driven automation
- Cloud Scheduler for scheduled execution
- Built-in retry, error handling, and conditional logic
- Cloud Audit Logs for compliance
- Per-execution pricing model

### 2. **Cloud Functions + Cloud Tasks**

Serverless function execution with task queue orchestration.

**Key Characteristics:**
- Event-driven function execution
- Task queues for workflow coordination
- Pay-per-invocation pricing
- Familiar programming model (Python, Node.js, Go)

**Why Not Selected:**
- No native workflow orchestration; requires custom orchestration logic
- Complex error handling and retry implementation
- State management across functions requires external storage
- Less visibility into workflow execution flow
- Debugging distributed function chains is difficult

### 3. **Tekton Pipelines**

Kubernetes-native CI/CD pipeline framework (already used for infrastructure automation).

**Key Characteristics:**
- Kubernetes-native using CRDs
- Runs on existing GKE clusters
- Strong Terraform integration
- Event-driven via EventListeners
- Reusable task library

**Why Not Selected:**
- Requires cluster access to deploy and manage pipelines
- Alert-driven triggers require additional infrastructure (EventListeners, webhook ingress)
- Cross-cluster operations require additional WIF/IAM configuration, conflicting with per-project isolation model
- Heavier operational model for remediation use case
- Better suited for CI/CD workflows than alert response patterns

### 4. **Cloud Run Jobs**

Containerized job execution on Cloud Run.

**Key Characteristics:**
- Container-based job execution
- Serverless with automatic scaling
- Scheduled via Cloud Scheduler
- GPU support for AI workloads

**Why Not Selected:**
- No native workflow orchestration
- Complex multi-step workflows require external coordination
- Container management overhead for simple remediation scripts
- Less integrated with GKE connector pattern

## Decision Rationale

### Justification

Cloud Workflows provides the optimal balance of simplicity, native GCP integration, and compliance capabilities for Zero Operator remediation workflows. The `gke.request` connector enables Kubernetes API operations without direct cluster access or additional authentication infrastructure. Serverless execution eliminates operational overhead while providing built-in audit logging for compliance.

The distributed deployment model (one workflow per project) aligns with GCP-HCP's regional independence architecture, ensuring each project's workflows are isolated and can operate independently.

### Evidence

**Key Technical Validations:**
- `gke.request` connector supports all necessary Kubernetes API operations (get, list, describe, logs, restart)
- Eventarc can trigger workflows from Cloud Monitoring alert policies
- Cloud Audit Logs capture all workflow executions with full parameter details
- Vertex AI Gemini API accessible from workflows for log analysis
- PAM integration provides mandatory approval gates for all destructive actions

**Alignment with Existing Decisions:**
- Complements Tekton (used for CI/CD pipelines) without overlap
- Extends automation-first philosophy to incident response
- Supports regional independence through per-project deployment
- Maintains Workload Identity Federation for authentication

### Comparison

| Criterion | Cloud Workflows | Functions + Tasks | Tekton | Cloud Run Jobs |
|-----------|----------------|-------------------|--------|----------------|
| **Infrastructure** | None (serverless) | None | GKE clusters | None |
| **GKE Connector** | Native `gke.request` | Custom SDK | kubectl in pods | Custom SDK |
| **Alert Triggers** | Eventarc native | Eventarc | Custom webhooks | Eventarc |
| **Audit Logging** | Cloud Audit Logs | Custom | Kubernetes logs | Cloud Logging |
| **Workflow Logic** | Built-in YAML | Custom code | Pipeline YAML | Custom code |
| **Error Handling** | Built-in retry | Custom | Task retry | Custom |
| **Approval Gates** | PAM integration | Custom | Manual approval | Custom |
| **AI Integration** | Vertex AI connector | SDK calls | Container tools | SDK calls |
| **Operational Overhead** | Minimal | Low | Medium | Low |
| **Cost Model** | Per-execution | Per-invocation | Cluster resources | Per-execution |

## Consequences

### Positive

- **Zero Operator Access:** Operators trigger workflows without cluster credentials; all actions via authenticated API calls
- **Native GKE Integration:** `gke.request` connector provides kubectl-equivalent operations without kubectl
- **Serverless Operations:** No infrastructure to maintain, scale, or patch for workflow execution
- **Built-in Compliance:** Cloud Audit Logs automatically capture all workflow executions for SOC 2/HIPAA
- **AI-Assisted Triage:** Vertex AI integration enables intelligent log analysis and remediation recommendations
- **Alert-Driven Automation:** Eventarc triggers enable automatic workflow execution when alerts fire
- **Regional Independence:** Per-project deployment ensures workflow isolation and independent operation
- **Approval Gates:** PAM integration requires approval for all destructive actions (Remediate/Failover), regardless of trigger source
- **Cost Efficiency:** Per-execution pricing aligns costs with actual remediation events (low-frequency, high-value)
- **Developer Experience:** YAML-based workflow definitions; CLI and Console execution options

### Negative

- **Learning Curve:** Team must learn Cloud Workflows syntax and connector patterns
- **Per-Execution Costs:** High-frequency workflows could accumulate costs (mitigated by remediation use case)
- **Vertex AI Token Costs:** AI-powered analysis adds per-request costs for Gemini API usage
- **Limited Local Testing:** Workflows primarily testable in GCP environment
- **No WebSocket Support:** `gke.request` uses HTTP, so `exec`, `attach`, and `port-forward` operations requiring WebSocket connections are not supported (use standard REST API operations instead)
- **Debugging Complexity:** Workflow execution logs in Cloud Logging require navigation
- **YAML Verbosity:** Complex workflows require significant YAML configuration

## Cross-Cutting Concerns

### Reliability

**Scalability:**
- Serverless execution scales automatically with demand
- Per-project deployment prevents cross-tenant interference
- No capacity planning required for workflow execution

**Observability:**
- Workflow execution visible in Cloud Console
- Step-by-step execution logs in Cloud Logging
- Correlation IDs enable end-to-end tracing from alert to remediation
- BigQuery export for long-term analysis and compliance reporting

**Resiliency:**
- Built-in retry logic with configurable backoff
- Automatic rollback on workflow failure (configurable)
- Kill switches via workflow metadata flags for emergency disable
- Rate limiting prevents runaway remediation loops

### Security

- **Authentication:** Workload Identity Federation for GCP services; no service account keys
- **Authorization:** Workflow service account has scoped GKE permissions per cluster
- **Data Protection:** Sensitive data filtered before Vertex AI analysis (no secrets, credentials, or PII in prompts)
- **Audit Trail:** All workflow executions logged with user identity, parameters, and results
- **Approval Gates:** PAM approval required for all destructive actions (Remediate and Failover categories), whether human-triggered or automated
- **Network Security:** Workflows execute within GCP; no external network exposure required

### Network Access: Public and Private GKE Clusters

The `gke.request` connector supports both public and private GKE clusters without additional network configuration:

**Public Clusters:**
- Accessible via public IP endpoint or DNS-based endpoint
- No special configuration required

**Private Clusters:**
- Accessible via DNS-based endpoint (FQDN)
- FQDN resolves to Google Cloud infrastructure, not the cluster's internal IP
- Requests route through Google Frontend (GFE) and GKE Frontend Proxy
- IAM authorization verified before reaching GKE control plane
- **No bastion host, VPN, or VPC peering required**

**How It Works:**
1. Cloud Workflows calls `gke.request` with cluster name and location
2. Connector resolves cluster's DNS-based endpoint (FQDN)
3. Request routes through Google's network to GKE Frontend Proxy
4. IAM checks `container.clusters.connect` permission on workflow service account
5. Request forwarded to GKE control plane (public or private)

**Requirements:**
- GKE cluster must have DNS-based endpoint enabled (default for new clusters)
- Workflow service account needs `roles/container.developer` (includes `container.clusters.connect`)

### Performance

- **Latency:** Workflow startup typically 1-3 seconds; Kubernetes API operations add per-call latency
- **Throughput:** Concurrent workflow executions limited by quota (configurable)
- **AI Analysis:** Vertex AI calls add 2-10 seconds per analysis depending on log volume
- **Rate Limiting:** Configurable per-workflow-type rate limits prevent API quota exhaustion

### Cost

**Execution Costs:**
- Cloud Workflows: ~$0.01 per 1,000 steps (remediation workflows typically 10-50 steps)
- Eventarc: Minimal cost for trigger delivery
- Vertex AI: Token-based pricing for Gemini API calls (~$0.001-0.01 per analysis)

**Optimization Strategies:**
- AI analysis optional and configurable per workflow type
- Rate limiting prevents cost spikes from alert storms
- Kill switches enable rapid cost control if needed

### Operability

**Deployment:**
- Terraform modules for workflow deployment per project
- GitOps workflow definitions in version control
- Automated deployment via existing CI/CD pipelines

**Maintenance:**
- No infrastructure maintenance required (serverless)
- Workflow version management via Terraform
- Rollback to previous versions via standard deployment

**Monitoring:**
- Cloud Monitoring dashboards for workflow metrics
- Alerting on workflow failures and anomalies
- BigQuery for historical analysis and compliance reporting

---

## Template Validation Checklist

### Structure Completeness
- [x] Title is descriptive and action-oriented
- [x] Scope is GCP-HCP
- [x] Date is present and in ISO format (YYYY-MM-DD)
- [x] All core sections are present: Decision, Context, Alternatives Considered, Decision Rationale, Consequences
- [x] Both positive and negative consequences are listed

### Content Quality
- [x] Decision statement is clear and unambiguous
- [x] Problem statement articulates the "why"
- [x] Constraints and assumptions are explicitly documented
- [x] Rationale includes justification, evidence, and comparison
- [x] Consequences are specific and actionable
- [x] Trade-offs are honestly assessed

### Cross-Cutting Concerns
- [x] Each included concern has concrete details (not just placeholders)
- [x] Irrelevant sections have been removed
- [x] Security implications are considered where applicable
- [x] Cost impact is evaluated where applicable

### Best Practices
- [x] Document is written in clear, accessible language
- [x] Technical terms are used appropriately
- [x] Document provides sufficient detail for future reference
- [x] All placeholder text has been replaced
- [x] Links to related documentation are included where relevant
