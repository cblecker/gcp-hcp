# Deployment Tooling Policy: Configuration Management Swim Lanes

***Scope***: GCP-HCP

**Date**: 2026-03-09

## Decision

We define two deployment swim lanes for the GCP-HCP platform based on the tool that manages resource lifecycle:

1. **Terraform** manages foundational GCP infrastructure -- resources that must exist before any workload can run.
2. **ArgoCD** manages all software components deployed on clusters, including application-level GCP resources provisioned via Config Connector custom resources.

A lightweight bootstrap mechanism (ConfigSync) glues the two lanes together by installing External Secrets Operator and ArgoCD onto newly created clusters.

```text
┌────────────────────────────────────────────────────────────────────────────────────┐
│                                 GCP-HCP Platform                                   │
│                                                                                    │
│  ┌──────────────────────────────────────────────────────────────────────────────┐  │
│  │  Lane 1: Terraform                                                           │  │
│  │                                                                              │  │
│  │  Foundational GCP infrastructure                                             │  │
│  │  ──────────────────────────────────────────────────────────────────────────  │  │
│  │  GCP projects & folders    VPCs, subnets, Cloud NAT    GKE clusters          │  │
│  │  DNS zones                 Platform-level IAM          Secret Manager        │  │
│  │  Artifact Registry                                                           │  │
│  │                                                                              │  │
│  │  Applied via automation or manual tf apply                                   │  │
│  └──────────────────────────────────┬───────────────────────────────────────────┘  │
│                                     │                                              │
│                    ┌────────────────▼──────────────────┐                           │
│                    │  Bootstrap (glue)                 │                           │
│                    │                                   │                           │
│                    │  Terraform creates Secret Manager │                           │
│                    │  secrets + configures ConfigSync  │                           │
│                    │  to deploy External Secrets +     │                           │
│                    │  ArgoCD on new clusters           │                           │
│                    └────────────────┬──────────────────┘                           │
│                                     │                                              │
│  ┌──────────────────────────────────▼───────────────────────────────────────────┐  │
│  │  Lane 2: ArgoCD                                                              │  │
│  │                                                                              │  │
│  │  All software components deployed on clusters                                │  │
│  │  ─────────────────────────────────────────────────────────────────────────── │  │
│  │  wave -10: Infrastructure operators & configs (cert-manager, external-dns)   │  │
│  │  wave  -5: Config Connector stacks (shared GCP resources across components)  │  │
│  │  wave   0: Application deployments (API services, workers, frontends)        │  │
│  │                                                                              │  │
│  │  Config Connector deploys application-level GCP resources                    │  │
│  │  (Cloud SQL, PubSub, GCS, IAM bindings, ...)                                 │  │
│  └──────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                    │
└────────────────────────────────────────────────────────────────────────────────────┘
```

## Context

### Problem Statement

Multiple tools can provision GCP resources (Terraform, Config Connector). Without clear policies:

- Engineers don't know which tool to use for a given resource
- Multiple tools may try to manage the same resource, causing drift
- Lifecycle ownership is unclear
- The boundary between "infrastructure" and "application resource" is ambiguous

### Constraints

- GKE clusters are private -- Terraform cannot access the Kubernetes API directly. It interacts only with GCP APIs (see [GKE Fleet Management](./gke-fleet-management.md))
- ArgoCD sync waves handle ordering within Lane 2 (see [ArgoCD Sync Wave Standardization](./argocd-sync-wave-standardization.md))
- Automation is used to apply Terraform infrastructure as a general rule, with exceptions for specific bootstrap scenarios

### Assumptions

- How Terraform is applied may evolve -- the swim lane boundary is about Terraform as the tool, not the apply mechanism
- Tekton is an automation orchestrator (e.g., e2e tests) that follows these swim lane definitions when deploying resources -- it is not a swim lane itself

## Alternatives Considered

1. **No formal policy**: Let teams decide per resource. Rejected -- leads to inconsistent ownership and dual-management drift as the platform scales.
2. **4-lane model** (Terraform, ConfigSync, ArgoCD, Config Connector as separate lanes): Rejected -- ConfigSync is bootstrap glue, not an ongoing deployment mechanism. Config Connector resources follow the same ArgoCD workflow (Git → PR → ArgoCD sync) as any other component. Splitting them creates artificial boundaries around a single deployment mechanism.
3. **2-lane model (selected)**: Clean boundary between infrastructure tooling (Terraform) and workload deployment (ArgoCD). Config Connector is a reconciler used within Lane 2, not a separate lane.

## Decision Rationale

### Justification

The swim lane boundary follows a simple principle: **Terraform manages the platform, ArgoCD manages everything that runs on it.**

This maps to two fundamentally different workflows:

| | Lane 1: Terraform | Lane 2: ArgoCD |
|---|---|---|
| **What** | GCP API resources | Kubernetes manifests (including Config Connector CRs) |
| **Workflow** | PR → automated plan/apply | PR → ArgoCD sync |
| **State** | Terraform state in GCS | Git + K8s API (+ GCP API for Config Connector) |
| **Reconciliation** | Explicit plan/apply | Continuous pull-based |
| **Blast radius** | Platform-wide | Per component/application |

Config Connector resources are Kubernetes manifests -- the engineer's workflow is identical to any other ArgoCD-deployed resource. The fact that they create GCP resources is an implementation detail handled by the Config Connector controller, not a reason for a separate swim lane.

### Evidence

- Terraform modules in `gcp-hcp-infra/terraform/` are scoped to foundation infrastructure with no Kubernetes resource management
- ArgoCD sync wave standardization already distinguishes component types within Lane 2 ([ArgoCD Sync Wave Standardization](./argocd-sync-wave-standardization.md))
- The bootstrap architecture explicitly separates Terraform from cluster-internal resources via ConfigSync and Secret Manager ([GKE Fleet Management](./gke-fleet-management.md))

## Consequences

### Positive

- **Simple mental model**: Two lanes, one boundary question -- "Is this foundational infrastructure or a software component?"
- **Clear ownership**: Every resource has exactly one tool managing its lifecycle
- **No workflow ambiguity**: Engineers know whether to open a PR against `terraform/` or an ArgoCD-managed chart
- **Aligned with existing architecture**: Formalizes boundaries already in place without requiring changes

### Negative

- **GCP resource classification sometimes requires judgment**: Most cases are straightforward (a VPC is clearly foundational, an app's database is clearly app-scoped). For resources used by multiple applications but not foundational (e.g., shared PubSub infrastructure), the app-of-apps wave -5 pattern allows deploying them as standalone Config Connector stacks. Truly ambiguous cases are rare.
- **Config Connector coverage**: While Config Connector support is comprehensive, some resource types may not have reached GA. Resources without Config Connector support fall back to Terraform. There is also the opportunity to contribute upstream.

## Cross-Cutting Concerns

### Security

- **Lane 1** has the highest privilege (project creation, VPC management, IAM binding). All changes go through PR review with Terraform plan output.
- **Lane 2** uses Workload Identity scoped per GCP project. Config Connector permissions are broad today (`roles/editor`) and will be scoped down to a limited subset of managed resource types. Kubernetes RBAC on Config Connector resources per namespace can provide additional access control.
- **IAM ownership**: Terraform creates core platform IAM bindings (Config Connector, Atlantis, e2e service accounts). Application-level IAM bindings (e.g., access to a database or PubSub) are deployed alongside the application via Config Connector. Cross-project IAM bindings always belong in Lane 1 (Terraform), even when they grant access for application-level use cases -- this prevents applications from escalating permissions across project boundaries.

### Operability

- The decision tree provides deterministic tool selection for new resources
- Built-in labels (`goog-terraform-provisioned`, `managed-by-cnrm`) and ArgoCD annotations provide traceability without custom labeling conventions.

---

## Lane Definitions

### Lane 1: Foundational Infrastructure (Terraform)

Terraform manages GCP resources that form the platform foundation -- things that must exist before any application can run.

**What belongs here:**

- GCP organizational resources: projects, folders
- Networking: VPCs, subnets, Cloud NAT, platform-level firewall rules, Cloud Armor
- GKE clusters and node pools
- GKE Fleet membership and ConfigSync configuration
- DNS zones
- Secret Manager secrets for platform configuration (cluster metadata, git credentials, infrastructure tool credentials)
- Platform-level IAM: project-level role bindings, cross-project bindings, service accounts for infrastructure tools
- Cross-cluster communication infrastructure (e.g., Maestro PubSub topics, subscriptions, and associated IAM between region and MC)
- Artifact Registry repositories

**What does NOT belong here:**

- Kubernetes resources (Deployments, Services, ConfigMaps)
- Application-scoped GCP resources (a database for one app, a PubSub topic for one service)
- Application-scoped IAM (Workload Identity bindings for a specific app)

**Boundary rule**: Terraform creates GCP API resources and configures GKE Fleet. It does not access the Kubernetes API. It writes cluster-specific configuration to Secret Manager, which is consumed inside clusters by External Secrets Operator.

### Bootstrap (Glue Between Lanes)

Terraform configures GKE Fleet ConfigSync to pull a minimal set of static, cluster-agnostic manifests from the public `gcp-hcp` repository (`bootstrap/` directory). These manifests install:

- External Secrets Operator
- ArgoCD
- Namespace-scoped SecretStore and ExternalSecrets
- ArgoCD root ApplicationSet

Once ArgoCD is running, all further deployments flow through Lane 2. The bootstrap is intentionally ultra-lean -- its sole purpose is to bridge Terraform-managed infrastructure to ArgoCD-managed workloads.

### Lane 2: Software Components (ArgoCD)

ArgoCD manages all software deployed on clusters, using the app-of-apps pattern. This includes platform operators, application workloads, and application-level GCP resources via Config Connector.

Component types are ordered by [ArgoCD sync waves](./argocd-sync-wave-standardization.md):

**Application-level waves (App-of-Apps ordering):**

| Wave | Component Type | Examples |
|------|---------------|----------|
| -10 | Infrastructure operators & configs | cert-manager, external-secrets-operator, Config Connector, external-dns |
| -5 | Config Connector stacks | Shared GCP resources used by wave 0 components but not embedded in them |
| 0 | Application deployments | API services, web frontends, background workers |

**Resource-level waves (within each application):**

| Wave | Resource Type | Examples |
|------|--------------|----------|
| -5 | Hard blockers | CRDs, embedded Config Connector resources (e.g., an app's database) |
| 0 | Application resources | Deployments, Services, ConfigMaps (default -- no annotation needed) |
| +5 | Reverse dependencies | Webhook configurations with `failurePolicy: Fail` (rare) |

**What belongs here:**

- Platform operators: cert-manager, external-dns, Config Connector, Tekton
- Application deployments: Deployments, StatefulSets, DaemonSets, Jobs
- Kubernetes resources: Services, Ingress, Gateway API, ConfigMaps, Secrets, RBAC, Namespaces
- Monitoring: ServiceMonitor, PodMonitor, PrometheusRules
- Application-level GCP resources via Config Connector: Cloud SQL, PubSub, GCS buckets, Cloud Run, app-scoped IAM, app-specific firewall rules, API Gateway, Certificate Manager, Secret Manager secrets (application-level)

**What does NOT belong here:**

- GCP foundation infrastructure (VPCs, GKE clusters, DNS zones) -- Lane 1
- Platform-level networking or IAM -- Lane 1

**Boundary rule**: If it's a Kubernetes manifest deployed via ArgoCD, it belongs in Lane 2 -- regardless of whether the manifest creates a GCP resource via Config Connector.

## Decision Tree

To determine which lane a resource belongs to:

```text
Is it a GCP resource?
├── YES: Is it foundational infrastructure?
│   │   (Must exist before any application can run)
│   ├── YES → Lane 1: Terraform
│   └── NO: Does Config Connector support it?
│       ├── YES → Lane 2: Config Connector CR (via ArgoCD)
│       │         Deploy it within the app, or as a standalone
│       │         Config Connector stack (wave -5) if shared
│       └── NO  → Lane 1: Terraform (document as exception) or contribute upstream
└── NO: Is it a Kubernetes resource?
    └── YES → Lane 2: ArgoCD
```

### Ownership Heuristic

The key question for GCP resources is: **is this foundational infrastructure?**

- **"Does this resource need to exist before any application can run?"** (GKE cluster, VPC, Fleet membership, platform IAM, cross-cluster communication) → YES: Lane 1 (Terraform).
- **"Does this involve cross-project IAM bindings?"** → YES: Lane 1 (Terraform). Cross-project IAM is a security-sensitive operation that should not be delegated to application-level tooling.
- **"Is this an application-level resource?"** → YES: Lane 2 (Config Connector). Deploy it alongside the app that owns it, or in a dedicated Config Connector stack (wave -5) if multiple apps depend on it.
- When a non-foundational resource is shared across applications (e.g., PubSub topics enabling cross-app communication), it can be deployed as a standalone ArgoCD application at wave -5, or owned by one of the consuming apps. It does not need to be in Terraform.

### Examples

| Resource | Lane | Rationale |
|----------|------|-----------|
| GCP Project | 1 | Must exist before anything else |
| VPC, subnet | 1 | Shared networking foundation |
| GKE cluster | 1 | Must exist before workloads |
| Platform IAM (Atlantis SA) | 1 | Infrastructure tooling |
| cert-manager | 2 | Operator deployed via ArgoCD (wave -10) |
| Config Connector operator | 2 | Operator deployed via ArgoCD (wave -10) |
| Cloud SQL for an app | 2 | App-scoped, deployed as Config Connector CR |
| PubSub topic for an app | 2 | App-scoped, deployed as Config Connector CR |
| App Workload Identity SA | 2 | App-scoped IAM via Config Connector |
| App-specific firewall rule | 2 | App-scoped, deployed as Config Connector CR |
| Maestro PubSub (region ↔ MC) | 1 | Cross-cluster communication infrastructure, created by MC Terraform |
| Cross-cluster firewall rule | 1 | Platform networking, must exist before cluster |
| Shared PubSub infrastructure | 2 | Non-foundational, deployed as Config Connector stack (wave -5) |
| API Gateway, certificates | 2 | Application-level, deployed via Config Connector |
| App Secret Manager secrets | 2 | Application-level (e.g., per-customer secrets) |
| Cloud Run AI agent | 2 | Application workload on Cloud Run, deployed as Config Connector `RunService` CR |
| API service Deployment | 2 | Application workload |

## Resource Labeling

Where the resource type supports GCP labels, tool-managed resources are labeled by their provisioning tool:

| Label | Applied by | Purpose |
|-------|-----------|---------|
| `goog-terraform-provisioned: true` | Terraform (GCP provider) | Identifies Terraform-managed resources |
| `managed-by-cnrm: true` | Config Connector | Identifies Config Connector-managed resources |

These built-in labels provide traceability on supported resource types without requiring custom labeling conventions. ArgoCD annotations on Kubernetes resources similarly identify the source application and repository.

Additionally, common cost-reporting and operational labels (`app-code`, `service-phase`, `cost-center`, `environment`, `sector`, `region`) are applied to GCP resources that support labels.

## Policy Enforcement

This policy is enforced through PR review. Reviewers verify that new resources are created by the correct tool based on the swim lane definitions and decision tree above. AI-powered review agents can further automate this validation.
