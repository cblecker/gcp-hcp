# Pipeline Automation Tooling: Tekton for Cloud-Native CI/CD Workflows

***Scope***: GCP-HCP

**Date**: 2025-10-27

## Decision

The GCP-HCP platform will adopt Tekton as the **general-purpose pipeline automation tool** for orchestrating any scheduled, event-driven, or on-demand workflows across the platform. Tekton will run on existing global GKE clusters per environment (integration, stage, production), providing cloud-native, Kubernetes-based automation for diverse use cases including infrastructure provisioning, application deployments, end-to-end testing, data processing, compliance scanning, and operational maintenance tasks. While the initial implementation focuses on Terraform-based infrastructure workflows, Tekton serves as the platform-wide solution for any automation requiring scheduled execution, web hook triggers, or complex multi-step orchestration that operates outside the pull request lifecycle (which remains the domain of Atlantis).

## Context

### Problem Statement

The GCP-HCP platform requires **general-purpose automated pipeline tooling** that can orchestrate workflows requiring scheduled execution, event-driven triggers, or complex multi-step orchestration outside the pull request lifecycle. Current tooling (Atlantis) handles PR-based Terraform automation effectively, but the platform needs a complementary solution for operational automation including scheduled infrastructure testing, drift detection, data processing, compliance scanning, and maintenance tasks.

**Driving Use Case:** Automate the provisioning and de-provisioning of full cloud region environments for end-to-end testing, ensuring consistent testing infrastructure without manual intervention. This represents the initial implementation focus, but the solution must support diverse automation needs across the platform.

**Example Use Cases:**
- **Infrastructure:** Scheduled environment provisioning/de-provisioning, Terraform lifecycle management outside PRs, drift detection, backup operations
- **Application:** End-to-end integration testing across environments
- **Data:** Scheduled ETL workflows, report generation, data validation
- **Platform:** Log aggregation, operational maintenance tasks

**Required Capabilities:**
- Scheduled operations (cron-based) and event-driven triggers (web hooks, pub/sub)
- Complex, multi-step workflows with dependencies and parallelization
- Independence from pull request workflows (complementing Atlantis, not replacing it)
- Leverage existing Kubernetes infrastructure to minimize operational overhead
- Support both interactive (on-demand) and automated (scheduled) execution
- Handle diverse workload types (Terraform, containers, scripts, custom tools)

### Constraints

- **Security:** Must support workload identity federation for keyless GCP authentication
- **Expertise:** Team has strong Kubernetes knowledge and operational experience
- **Portability:** Preference for vendor-neutral, open-source solutions
- **Integration:** Must work alongside Atlantis without overlapping or conflicting responsibilities
- **Scale:** Must support multiple environments and regions with parameterized workflows

### Assumptions

- Existing global GKE clusters per environment will host Tekton components
- Kubernetes RBAC and service accounts provide adequate access control
- Persistent volumes (PVCs) are available for workspace sharing between pipeline tasks
- Team is comfortable with YAML-based infrastructure-as-code for pipeline definitions
- Tekton's Continuous Delivery Foundation (CDF) backing ensures long-term project sustainability

## Alternatives Considered

### 1. **Tekton (Selected)**

Open-source, cloud-native CI/CD framework that runs on Kubernetes, originally created by Google and donated to the Continuous Delivery Foundation. Pipelines are defined as Kubernetes Custom Resources (CRDs) and executed as containerized tasks.

**Key Characteristics:**
- Kubernetes-native architecture using CRDs
- Event-driven triggers via EventListeners and web hooks
- Reusable task library through Tekton Hub
- Container-based task execution with workspace sharing via PVCs
- Native support for CronJob-based scheduled execution
- First-class Terraform integration with GCP authentication

**Why Selected:**
- Leverages existing Kubernetes infrastructure (no additional servers required)
- Strong Red Hat and community adoption aligns with team expertise
- Proven Terraform workflow support validated through extensive POC
- Event-driven and scheduled execution patterns address all use cases
- CNCF project backing ensures long-term sustainability
- Natural complement to Atlantis for non-PR workflows

### 2. **GitHub Actions**

GitHub's integrated CI/CD platform that runs workflows directly within GitHub repositories.

**Key Characteristics:**
- Fully managed by GitHub (no infrastructure to maintain)
- Native GitHub integration with automatic PR triggers
- Large marketplace of community actions
- Free tier for public repositories, paid for private
- Executes workflows on GitHub-hosted or self-hosted runners

**Why Not Selected:**
- Requires self-hosted runners or paid GitHub-hosted compute
- Less suitable for complex, multi-step Terraform workflows
- Lacks native Kubernetes integration for cloud-native operations
- Not vendor-neutral (tightly coupled to GitHub)
- Scheduled operations less flexible than Kubernetes CronJobs
- Weaker separation between PR automation and operational pipelines
- Requires storing credentials as a secret in github

### 3. **Argo Workflows**

Open-source, Kubernetes-native workflow engine designed for orchestrating parallel jobs.

**Key Characteristics:**
- Kubernetes-native using CRDs similar to Tekton
- Directed Acyclic Graph (DAG) based workflow definitions
- CNCF graduated project (higher maturity than Tekton)
- Strong support for parallel execution and complex dependencies
- Web-based UI for workflow visualization and monitoring

**Why Not Selected:**
- Steeper learning curve for team already familiar with simpler pipeline patterns
- Tekton's task reusability model better fits infrastructure automation needs
- UI advantage not significant given team's kubectl proficiency

### 4. **Jenkins**

Traditional CI/CD automation server with extensive plugin ecosystem.

**Key Characteristics:**
- Mature, battle-tested platform with decades of usage
- Massive plugin ecosystem for integrations
- Web-based GUI for pipeline management
- Can be deployed on Kubernetes via Jenkins Operator
- Supports both declarative and scripted pipelines

**Why Not Selected:**
- Heavyweight infrastructure requirements (dedicated server/pods)
- Plugin management creates maintenance overhead
- Verbose pipeline definitions compared to Tekton's YAML
- Team preference for Kubernetes-native tooling

### 5. **Cloud Workflows (GCP)**

**Evaluated via hands-on testing:** October 27, 2025

Google Cloud's fully managed orchestration platform for executing workflows in response to events or schedules.

**Key Characteristics:**
- Serverless workflow orchestration (no infrastructure to maintain)
- Service account specification for least-privilege security
- Extensive event triggers via Eventarc (pub/sub, cloud events, etc.)
- Scheduled execution via Cloud Scheduler with cron syntax
- Can trigger via gcloud CLI or HTTP endpoints
- YAML-based workflow definitions
- Native GCP integration with automatic authentication
- Environment variable support and parameterization

**Why Not Selected:**
- **Poor log visibility for delegated work:** When orchestrating Cloud Build jobs (the tested pattern), actual build step logs require navigating to Cloud Build console; workflow execution logs are in Cloud Logging, but work logs are separate
- **Indirect Kubernetes integration:** Can call GKE APIs via connectors, but not as natural as in-cluster execution; requires additional API calls vs. direct CRD manipulation
- **Workspace management complexity:** When using Cloud Build integration, relies on GCS buckets for artifact/workspace sharing between workflow steps, less intuitive than PVC-based approach
- **Multi-service observability:** Workflow execution spans multiple GCP services (Workflows → Cloud Build → Cloud Logging), requiring navigation across consoles
- **Separation from cluster:** Running outside Kubernetes makes direct integration with cluster-based workloads more complex
- **Vendor lock-in:** GCP-specific solution, not portable to other cloud providers

## Decision Rationale

### Justification

Tekton provides the optimal balance of cloud-native architecture, operational simplicity, and functional completeness for the GCP-HCP platform's pipeline automation needs. Its Kubernetes-native design eliminates the need for additional infrastructure by running directly on existing GKE clusters, while its event-driven and scheduled execution capabilities address both on-demand and automated workflow requirements.

Tekton's relationship with Atlantis creates a clear separation of concerns:
- **Atlantis:** Pull request-based Terraform automation (plan on push, apply on merge)
- **Tekton:** General-purpose pipeline automation for workflows requiring scheduled execution, event-driven triggers, or complex orchestration outside the PR lifecycle

This complementary approach prevents tool overlap while ensuring comprehensive automation coverage across the platform.

### Evidence

**POC Validation:**
- Successfully implemented 8-task provisioning pipeline with web hook triggers
- Validated GCP workload identity integration for keyless authentication
- Created 7-task E2E testing pipeline with CronJob scheduling
- Built production-grade CLI tool (gcpctl) demonstrating excellent developer UX
- Verified Terraform workflow execution (init, validate, plan, apply, destroy)
- Tested both local (Kind) and production (GKE) authentication patterns

**Terraform Integration:**
- Created reusable `terraform-gcp` task supporting all Terraform commands
- Validated workspace persistence via PVCs for Terraform state sharing
- Confirmed GCP authentication works seamlessly with both JSON keys and Workload Identity
- Demonstrated parameterization for environment/region/sector flexibility

**Community Health:**
- Continuous Delivery Foundation (CDF) project ensuring governance stability
- Active development with regular releases
- Used by major organizations (Google, IBM, Red Hat)
- Strong Red Hat adoption aligns with team's expertise and ecosystem

**Team Expertise Alignment:**
- Team's Kubernetes proficiency reduces learning curve
- kubectl-based workflow matches existing operational patterns
- YAML-based pipeline definitions align with infrastructure-as-code practices
- Self-hosting on existing clusters eliminates new operational domains

### Comparison

| Criterion | Tekton | GitHub Actions | Argo Workflows | Jenkins | Cloud Workflows |
|-----------|--------|----------------|----------------|---------|-----------------|
| **Infrastructure** | Existing GKE | Self-hosted runners | Existing K8s | Dedicated pods | Fully managed |
| **Cost** | Free (existing infra) | Paid (private repos) | Free (existing infra) | Free (self-host) | Pay-per-use |
| **Kubernetes-Native** | ✓ Yes (CRDs) | ✗ No | ✓ Yes (CRDs) | ~ Via operator | ✗ No |
| **K8s Integration** | ✓ In-cluster | ✗ External | ✓ In-cluster | ~ In-cluster | ✗ External |
| **Terraform Support** | ✓ Excellent | ~ Moderate | ~ Moderate | ✓ Good (plugins) | ~ Moderate |
| **Event-Driven** | ✓ EventListeners | ✓ Webhooks | ✓ Webhooks | ✓ Webhooks | ✓ Eventarc |
| **Scheduled Execution** | ✓ CronJobs | ✓ Cron syntax | ✓ CronWorkflows | ✓ Cron triggers | ✓ Cloud Scheduler |
| **Reusability** | ✓ Tasks/Hub | ✓ Actions | ✓ Templates | ~ Shared libs | ~ Subworkflows |
| **Vendor Neutrality** | ✓ CNCF | ✗ GitHub-only | ✓ CNCF | ✓ OSS | ✗ GCP-only |
| **Setup Complexity** | Medium | Low | Medium | High | Low |
| **Learning Curve** | Medium (K8s req'd) | Low | High (DAG) | Medium | Low |
| **Log Visibility** | ✓ Excellent | ✓ Good | ✓ Good | ✓ Excellent | ✗ Poor |
| **GUI/Dashboard** | Basic | Excellent | Good | Excellent | Good |
| **Workspace Sharing** | ✓ PVCs | ~ Artifacts | ✓ PVCs | ✓ Workspace | ~ GCS buckets |

Tekton was selected over GitHub Actions because:
- Leverages existing Kubernetes infrastructure (no additional compute costs)
- Kubernetes-native design aligns with team's operational expertise
- Better Terraform integration validated through POC
- Vendor-neutral and portable across cloud providers

Tekton was selected over Argo Workflows because:
- Simpler sequential task model better fits infrastructure automation patterns
- Better established Terraform integration and community tasks
- Lower learning curve for team's use cases

Tekton was selected over Jenkins because:
- Cloud-native architecture vs. adapted traditional CI/CD server
- Lighter operational footprint (no dedicated server management)
- Modern YAML-based configuration vs. plugin management overhead

Tekton was selected over Cloud Workflows because:
- **Superior observability:** Logs easily accessible via kubectl/Tekton Dashboard vs. requiring navigation to Cloud Build console
- **In-cluster execution:** Direct integration with Kubernetes resources and services
- **Better workspace management:** PVC-based sharing more intuitive than GCS bucket artifacts
- **Vendor neutrality:** Portable across any Kubernetes cluster vs. GCP-only
- **CRD-based architecture:** Pipelines queryable and manageable as native Kubernetes resources
- **Team expertise alignment:** Leverages existing kubectl proficiency and operational patterns

## Consequences

### Positive

- **General-Purpose Automation Platform:** Single solution for any pipeline automation need (infrastructure, applications, data, operations)
- **Zero Additional Infrastructure Costs:** Runs on existing GKE clusters with no new servers or services
- **Kubernetes-Native Architecture:** Leverages team's K8s expertise and operational patterns
- **Comprehensive Trigger Support:** Scheduled (cron), event-driven (web hooks), and on-demand execution patterns
- **Clear Separation from Atlantis:** Non-overlapping responsibilities prevent tool conflicts (PR automation vs. general pipelines)
- **Proven Terraform Integration:** POC validated end-to-end Terraform workflows with GCP authentication
- **Extensible to Any Workload:** Container-based tasks support Terraform, scripts, custom tools, application builds, data processing, etc.
- **Excellent Developer Experience:** Custom CLI tooling (gcpctl) provides clean UX for pipeline management
- **Vendor Neutrality:** CNCF project with no cloud provider lock-in
- **Reusable Components:** Tekton Hub and custom tasks reduce duplication across all pipeline types
- **Infrastructure-as-Code:** YAML-based pipeline definitions version-controlled in Git
- **Event-Driven Flexibility:** Webhook triggers enable integration with external systems
- **Security Best Practices:** Workload identity integration enables keyless GCP authentication

### Negative

- **Kubernetes Knowledge Required:** Team members unfamiliar with K8s face learning curve
- **Verbose YAML Configuration:** More boilerplate than some alternatives (GitHub Actions)
- **Limited GUI:** Basic Tekton Dashboard less polished than commercial CI/CD tools
- **Debugging Complexity:** Logs spread across multiple pods, requiring kubectl inspection
- **Resource Consumption:** Each task runs in separate pod, consuming cluster resources
- **Operational Overhead:** Team responsible for Tekton upgrades and troubleshooting
- **Setup Complexity:** Initial pipeline creation requires understanding multiple CRD concepts (Task, Pipeline, PipelineRun, Trigger)
- **Workspace Management:** PVC-based workspace sharing adds storage overhead and cleanup requirements
- **Parameter Passing Verbosity:** Complex parameter propagation across tasks can be cumbersome
- **No Built-in Rollback:** Failed pipelines require manual cleanup or custom destroy workflows
- **Monitoring Gaps:** No integrated monitoring; requires custom alerting setup
- **Slower Development Iteration:** Testing pipeline changes requires applying CRDs to cluster

## Cross-Cutting Concerns

### Reliability

**Scalability:**
- Tekton scales horizontally with Kubernetes cluster resources
- Each pipeline run creates isolated pods (no shared state contention)
- Workspace PVCs use ReadWriteOnce (single-node) or ReadWriteMany (multi-node) access modes
- CronJob-based scheduling scales with cluster capacity
- POC demonstrated 3-10 minute execution times for full infrastructure lifecycle

**Observability:**
- Pipeline status visible via `kubectl get pipelineruns`
- Basic Tekton Dashboard provides web-based monitoring
- Task logs accessible via `kubectl logs` or `tkn` CLI
- Custom CLI (gcpctl) provides user-friendly status checking
- No integrated metrics/alerting (requires Prometheus/Grafana integration)

**Resiliency:**
- Failed tasks can retry with configurable retry policies
- Pipeline-level timeouts prevent runaway executions
- Workspace persistence ensures data survives pod failures
- No automatic rollback (requires explicit destroy pipelines)
- CronJob failure handling via `successfulJobsHistoryLimit` and `failedJobsHistoryLimit`

### Security

- **Authentication:** GCP workload identity for keyless authentication (production) or JSON keys (development)
- **Authorization:** Kubernetes RBAC controls access to Tekton resources
- **Secret Management:** Kubernetes secrets for credentials, accessed via volume mounts
- **Workload Identity:** Service accounts impersonate GCP service accounts without credential files
- **Network Security:** Webhook endpoints require exposure (LoadBalancer or Ingress with authentication)
- **Isolation:** Per-environment pipelines isolate credentials (integration cannot access production)
- **Least Privilege:** Task-level service accounts scoped to minimum required permissions
- **Audit Trail:** Pipeline runs logged as Kubernetes resources with creation timestamps and user context

### Performance

- **Latency:** Pipeline startup ~10-30 seconds (Kubernetes pod scheduling overhead)
- **Execution Time:** POC demonstrated 3-10 minutes for full Terraform lifecycle
- **Concurrency:** Multiple pipelines run in parallel (limited by cluster resources)
- **Resource Utilization:** Each task consumes dedicated pod resources (CPU/memory)
- **Optimization Opportunities:**
  - Reusable tasks reduce duplication and resource consumption
  - Parallel task execution where dependencies allow
  - PVC caching for Terraform providers reduces init times

### Cost

- **Compute Costs:** Uses existing GKE cluster resources (no incremental costs)
- **Storage Costs:** PVC storage for workspaces (minimal - typically 1-10 GB per pipeline)
- **Networking Costs:** Webhook ingress traffic (negligible)
- **Operational Costs:** Team time for pipeline development, maintenance, and troubleshooting
- **No Licensing Fees:** Open-source with no subscription or usage-based costs
- **Cost Avoidance:** Eliminates need for GitHub Actions runners or Cloud Build usage charges

### Operability

- **Deployment:** Install Tekton via `kubectl apply` or Helm chart
- **Maintenance:** Requires periodic Tekton version upgrades (community release cadence)
- **Configuration Management:** Pipeline definitions version-controlled in Git
- **Monitoring Requirements:** Custom Prometheus metrics and Grafana dashboards needed
- **Troubleshooting:** Requires kubectl proficiency for log inspection and debugging
- **Documentation:** POC includes comprehensive guides (setup, testing, authentication)
- **Backup/Recovery:** Pipeline definitions in Git, workspace PVCs require backup policies
- **Upgrade Path:** Tekton upgrades via `kubectl apply` with CRD version migrations

---

## POC Artifacts and Learnings

### Pipelines Developed

**1. gcp-region-provision Pipeline (8 tasks)**
- Webhook-triggered infrastructure provisioning
- Tasks: validate-inputs → create-directory-structure → generate-terraform-config → terraform-init → terraform-validate → terraform-plan → terraform-apply → commit-to-git
- Creates GCS buckets in GCP via Terraform with full parameter validation
- EventListener exposes web hook endpoint for external triggering
- Reusable `terraform-gcp` task supports all Terraform commands

**2. gcp-region-e2e Pipeline (7 tasks)**
- Scheduled end-to-end testing with full infrastructure lifecycle
- Tasks: git-clone → terraform-init → terraform-validate → terraform-plan → terraform-apply → e2e-tests → terraform-destroy
- CronJob runs nightly at 2 AM UTC
- Clones Terraform repository and tests against real infrastructure
- Automatic cleanup via destroy task prevents resource leaks

### Custom Tooling Created

**gcpctl CLI (Production-Grade)**
- Go-based CLI using Cobra framework
- Triggers pipelines via HTTP webhooks
- Monitors pipeline status via kubectl API
- Event ID tracking for asynchronous execution
- Configuration via file, environment variables, or flags
- Clean UX with progress indicators and human-readable output

### Reusable Components

**terraform-gcp Task**
- Parameterized Terraform execution with GCP authentication
- Supports all Terraform commands (init, plan, apply, destroy)
- Automatic credential mounting (JSON key or workload identity)
- Configurable working directory for multi-environment support

**Authentication Setup Scripts**
- `setup-local-gcp-auth.sh`: JSON key-based authentication for Kind/Minikube
- `setup-workload-identity.sh`: Workload identity for GKE clusters
- `grant-storage-admin.sh`: IAM role assignment automation

### Documentation Created

- Architecture diagrams (Mermaid) illustrating pipeline flows
- Step-by-step setup guides for both local and GKE environments
- Comprehensive troubleshooting sections with common issues
- Decision trees for authentication method selection
- Performance benchmarks and execution time expectations
- Testing guides with example commands and expected outputs

### Key Learnings

**What Worked Well:**
- Terraform integration exceeded expectations (seamless workflow execution)
- GCP workload identity integration worked flawlessly
- Shared workspaces (PVCs) elegantly solved state persistence
- Reusable tasks reduced duplication across pipelines
- Event-driven architecture (EventListener/TriggerBinding/TriggerTemplate) proved powerful and flexible
- Custom CLI (gcpctl) demonstrated that Tekton can provide excellent developer UX when properly wrapped

**Challenges Encountered:**
- Debugging failed tasks requires kubectl proficiency (logs spread across pods)
- YAML verbosity creates boilerplate for simple workflows
- Local development requires port forwarding for webhook testing
- PVC cleanup necessary between test runs to avoid stale data
- Parameter passing requires careful planning due to verbosity

**Production Readiness Gaps:**
- Need manual approval gates between plan and apply for production safety
- Remote Terraform backend (GCS) required for state management
- Slack/email notifications needed for pipeline status alerts
- Drift detection pipeline not yet implemented
- Resource limits and retry policies need hardening
- Monitoring and alerting integration incomplete

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
- [x] Links to POC artifacts and related documentation are included where relevant

**See also**: [Deployment Tooling Policy](./deployment-tooling-swim-lanes.md) — Tekton is an automation orchestrator that follows the swim lane definitions when deploying resources, complementing Atlantis (Lane 1) and ArgoCD (Lane 2).
