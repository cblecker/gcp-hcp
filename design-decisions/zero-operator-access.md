# Zero Operator Access: Layered Access Model for GCP HCP Operations

***Scope***: GCP-HCP

**Date**: 2026-04-16

## Decision

Adopt Zero Operator Access as the architectural principle governing how operators interact with GCP HCP production systems. An **operator** is any human (SRE, platform engineer, developer) or AI agent acting on production resources — GitOps automation service accounts (ArgoCD, CI/CD pipelines) are excluded as trusted mechanisms with their own controls. The principle is enforced through a layered model with progressively stronger access restrictions based on resource sensitivity, with the strongest guarantees for customer-facing resources.

## Context

- **Problem Statement**: The GCP HCP platform has multiple design decisions that collectively implement an operator access control model — Cloud Workflows for mediated execution, PAM for approval-gated access, Workload Identity for credential-free authentication, IAP for identity verification, agent autonomy levels for AI-based operations, and deployment swim lanes for infrastructure ownership. However, there is no formal, unified definition of "Zero Operator Access" that articulates the overall principle, defines its scope, identifies the boundaries between resource types, and honestly states where the aspiration differs from the current implementation.

- **Constraints**:
  - **Compliance**: SOC 2, HIPAA, and PCI standards require auditable, least-privilege access controls with approval workflows for sensitive operations
  - **Hypershift architecture**: Hosted control planes run in Red Hat's management infrastructure but serve customer clusters — the security boundary between platform and customer resources is architecturally significant
  - **Credential chain**: Customer cluster credentials (kubeadmin, service account tokens) reside as Kubernetes secrets in the control plane namespace on the management cluster, creating a transitive access path from control plane access to customer cluster access
  - **Operational reality**: Incident response requires operators to diagnose and remediate issues in production — a model that prevents all access would also prevent all operations
  - **AI agent evolution**: The platform's agent autonomy framework introduces non-human operators that require the same access controls as humans

- **Assumptions**:
  - Cloud Workflows with `gke.request` connector provides sufficient Kubernetes API coverage for all operational tasks, eliminating the need for direct cluster access
  - PAM approval latency is acceptable for all operational scenarios, including incident response
  - The credential chain gap (Layer 1) can be architecturally resolved in a future phase through mechanisms such as credential elimination, out-of-band delivery, or customer-managed encryption
  - GitOps automation service accounts operate under sufficient existing controls (Workload Identity, least privilege, code review) that they do not require the same approval gates as human/AI operators

## Layered Access Model

The Zero Operator Access principle applies differently based on resource sensitivity:

### Layer 1: Customer Workloads and API

**Access model**: Aspirational truly zero; currently zero unmediated.

Customer pods, secrets, persistent volumes, application data, and the hosted cluster's Kubernetes API are the most sensitive resources. Operators have no legitimate reason to access customer workloads directly.

**Current state**: No direct access path exists outside the control plane. However, access is technically possible by extracting customer cluster credentials (kubeadmin password, service account tokens) from Kubernetes secrets in the control plane namespace with PAM-approved elevated access. This credential chain means Layer 1 isolation depends entirely on Layer 2 controls.

**Aspiration**: Break the credential chain entirely so that even with full control plane namespace access, no human-usable credential exists that grants access to the customer's cluster API. Potential approaches include eliminating static credentials (OIDC-only authentication), delivering credentials out-of-band to the customer and discarding them from the control plane, or encrypting credentials with customer-managed keys (CMEK).

### Layer 2: Hosted Control Plane Components

**Access model**: Zero unmediated.

Control plane components (etcd, kube-apiserver, controllers, operators) running in the management cluster's control plane namespaces. All operational actions are performed exclusively through Cloud Workflows with PAM approval. No direct `kubectl`, no SSH, no exec into pods. Every action is audited, time-bounded, and traceable to an individual identity (human or AI agent).

**Current state**: Implemented via Cloud Workflows + PAM workflow gating. Read-only workflows (get, describe, logs) are permanently accessible. Sensitive workflows (delete, modify, restart) require PAM-approved, time-bounded grants.

### Layer 3: Platform Infrastructure

**Access model**: Zero unmediated.

GCP projects, VPCs, GKE management clusters, IAM policies, and all foundational infrastructure. Normal operations use GitOps exclusively (Terraform for infrastructure, ArgoCD for software deployments). Incident response and emergency operations use Cloud Workflows with PAM approval — no direct `gcloud`, no Console modifications, no manual Terraform applies.

**Current state**: Implemented via deployment swim lanes (Terraform for Lane 1, ArgoCD for Lane 2) with PAM gating for sensitive Cloud Workflows that operate on infrastructure.

### Layer 4: Observability Data

**Access model**: Controlled access.

Logs, metrics, traces, and dashboards are accessible to operators for diagnosis and monitoring. Platform telemetry (control plane metrics, operator logs, infrastructure health) is available through standard observability tooling (Google-Managed Prometheus, Cloud Logging, Grafana). Customer-originated data within observability signals (application logs forwarded through the control plane, customer workload metrics) requires redaction or exclusion to maintain the Layer 1 boundary.

**Current state**: Observability stack is operational with Google-Managed Prometheus and Cloud Logging. Data classification boundaries between platform and customer telemetry are an area for further definition.

## Definition of Operator

An **operator** is any principal — human or AI agent — that acts on GCP HCP production systems in the context of operations, incident response, or platform management. Specifically:

**In scope (subject to Zero Operator Access controls):**
- SREs and on-call engineers responding to incidents
- Platform engineers performing operational tasks
- AI agents executing diagnosis, remediation, or analysis (per the agent autonomy levels framework)

**Out of scope (governed by their own control frameworks):**
- ArgoCD sync service accounts — governed by GitOps review, RBAC, and sync wave controls
- CI/CD pipeline service accounts — governed by code review, Workload Identity, and least privilege
- Terraform automation — governed by plan/apply review, state management, and PR approval
- Alert-triggered Cloud Workflows — governed by workflow-level PAM gating and audit logging

The distinction is intentional: automation service accounts are the *mechanism* through which Zero Operator Access is enforced, not the principals it constrains.

## Alternatives Considered

1. **Flat "zero access" policy**: Apply the same "truly zero" standard to all resource types. All operations fully automated with no human access under any circumstances.

2. **Zero standing access only**: Focus exclusively on eliminating permanent elevated privileges (PAM for everything) without restricting the access mechanism (operators could still use `kubectl` directly, as long as the privilege was PAM-granted and time-bounded).

3. **Layered access model with aspirational Layer 1 (chosen)**: Define different access levels per resource type. Enforce zero unmediated access (all actions through Cloud Workflows) for Layers 2-3, controlled access for Layer 4, and aspire to truly zero for Layer 1 while acknowledging the current credential chain gap.

## Decision Rationale

* **Justification**: The layered model is the only alternative that is both honest and aspirational. A flat "truly zero" policy is not achievable today due to the credential chain in the control plane namespace, and claiming it would be misleading. A "zero standing" model is necessary but insufficient — eliminating permanent privileges is important, but allowing direct `kubectl` access (even time-bounded) defeats the audit and mediation guarantees that Cloud Workflows provides. The layered model captures what we have, names the gap, and sets a clear architectural direction.

* **Evidence**: The existing design decisions already implement the layered model in practice:
  - Cloud Workflows + PAM provide zero unmediated access for Layers 2-3 ([cloud-workflows-automation-platform](cloud-workflows-automation-platform.md), [pam-workflow-gating](pam-workflow-gating.md))
  - Agent autonomy levels extend the same controls to AI agents ([agent-autonomy-levels](agent-autonomy-levels.md))
  - Workload Identity eliminates long-lived credentials ([workload-identity-implementation](workload-identity-implementation.md))
  - IAP provides identity verification at service boundaries ([iap-authentication](iap-authentication.md))
  - Deployment swim lanes enforce ownership and prevent cross-boundary escalation ([deployment-tooling-swim-lanes](deployment-tooling-swim-lanes.md))

* **Comparison**:
  - Alternative 1 (flat zero) sets an unachievable standard that would require architectural changes to Hypershift's credential model before it could be claimed. It also prevents any human involvement in incident response, which is operationally unrealistic at this stage.
  - Alternative 2 (zero standing only) addresses privilege persistence but not access mediation. An operator with a time-bounded PAM grant and direct `kubectl` access can still perform unaudited actions, access customer credentials, and bypass the workflow-level controls that provide the actual security guarantees.

## Consequences

### Positive

* **Clear communication**: Provides a single, referenceable definition for what "Zero Operator Access" means in GCP HCP — useful for compliance documentation, customer-facing materials, and internal alignment
* **Honest gap identification**: The Layer 1 credential chain gap is explicitly named, creating architectural pressure to resolve it rather than leaving it as an undocumented risk
* **Unified principle**: Connects six existing design decisions under a coherent architectural principle, making the overall access model easier to reason about
* **AI agent inclusion**: Explicitly brings AI agents under the same access controls as human operators, preventing a class of access control bypass as agent autonomy increases
* **Compliance alignment**: The layered model maps directly to SOC 2/HIPAA/PCI access control requirements with clear evidence for each layer

### Negative

* **Layer 1 is aspirational, not enforced**: Until the credential chain is broken, the "truly zero" claim for customer workloads depends on Layer 2 controls. A sufficiently privileged operator with PAM access to the control plane namespace can still reach customer clusters.
* **Operational friction**: Zero unmediated access means all incident response goes through Cloud Workflows. If a workflow doesn't exist for a needed operation, there is no fallback — the workflow must be created first. This may slow response to novel failure modes.
* **Automation coverage gap**: The model assumes Cloud Workflows covers all necessary Kubernetes API operations. Operations requiring WebSocket connections (exec, attach, port-forward) are not supported by the `gke.request` connector, limiting some debugging capabilities.
* **Layer 4 boundary is under-defined**: The distinction between platform telemetry and customer-originated data in observability signals needs further specification to prevent data leakage that violates the Layer 1 principle.

## Cross-Cutting Concerns

### Security:

* Zero Operator Access is itself a security architecture — all access is mediated, time-bounded, audited, and identity-traced
* The credential chain gap is the primary security risk: Layer 1 isolation depends on Layer 2 controls until the gap is architecturally resolved
* Tag management for PAM-gated workflows must be restricted — anyone with `roles/resourcemanager.tagUser` could remove the `pam-gated` tag, bypassing the gate
* AI agents introduce prompt injection as a threat vector; the agent autonomy framework mitigates this through architectural separation (agents propose, humans execute)

### Operability:

* Incident response requires pre-built Cloud Workflows for all anticipated failure modes — missing workflows create operational gaps
* Novel incidents may require workflow development under time pressure, which conflicts with the GitOps review requirement
* Operators must understand the layered model to know which access controls apply to their specific task
* PAM approval latency (notification + human review) adds time to incident response — acceptable for planned operations, potentially challenging during high-severity incidents

### Reliability:

* **Resiliency**: The model depends on Cloud Workflows and PAM availability. If either service is unavailable during an incident, the mediation layer itself becomes a point of failure. The current architecture has no documented fallback for GCP control plane outages affecting these services.
* **Observability**: End-to-end traceability from PAM grant request through workflow execution to Kubernetes API action is achieved through Cloud Audit Logs. This audit chain is a key compliance artifact.

---

## Related Design Decisions

| Decision | Relationship |
|----------|-------------|
| [cloud-workflows-automation-platform](cloud-workflows-automation-platform.md) | Implements Layers 2-3: zero unmediated access via Cloud Workflows |
| [pam-workflow-gating](pam-workflow-gating.md) | Implements approval gates for sensitive operations across all layers |
| [agent-autonomy-levels](agent-autonomy-levels.md) | Extends Zero Operator Access to AI agents with staged autonomy |
| [workload-identity-implementation](workload-identity-implementation.md) | Eliminates long-lived credentials for all workloads |
| [iap-authentication](iap-authentication.md) | Provides identity verification at service boundaries |
| [deployment-tooling-swim-lanes](deployment-tooling-swim-lanes.md) | Enforces GitOps ownership boundaries for Layer 3 |

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
- [x] Cost impact is evaluated where applicable (N/A — this is a principle, not infrastructure)

### Best Practices
- [x] Document is written in clear, accessible language
- [x] Technical terms are used appropriately
- [x] Document provides sufficient detail for future reference
- [x] All placeholder text has been replaced
- [x] Links to related documentation are included where relevant
