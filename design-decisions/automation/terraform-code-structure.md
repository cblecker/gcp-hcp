# Terraform Code Structure

***Scope***: GCP-HCP

***Date***: 2025-10-06

## Decision

We will organize Terraform configuration using a hierarchical directory structure that separates global resources from regional resources, with dedicated directories for each environment (integration, staging, production), deployment tracks and region.

## Context

The project needs a consistent, scalable Terraform code structure that supports multiple environments, regions, and deployment tracks while maintaining clear separation of concerns.

- **Problem Statement**: Design a Terraform directory structure that enables independent management of global and regional resources across multiple environments and deployment tracks, while maintaining consistency and avoiding code duplication.
- **Constraints**: Must support multiple GCP regions, environment isolation, and independent state management per region.
- **Assumptions**: Infrastructure will be deployed across multiple regions with both global (example: DNS, API Gateway) and regional (example: GKE clusters) components. Each region will maintain its own Terraform state for independent lifecycle management.

## Alternatives Considered

1. **Hierarchical Structure with Dedicated State per Region**: Organize code by global/regional split with separate directories per environment and region. Each region directory contains its own main.tf file with dedicated state.
2. **YAML-driven Configuration with Loops**: Use YAML files to define region configurations and loop through them in a single main.tf, sharing state across regions.
3. **Monolithic Module with Combined State**: Single directory structure with all resources managed in one state file per environment.

## Decision Rationale

* **Justification**: The hierarchical structure with dedicated state per region provides the clearest separation of concerns, enables independent regional deployments, and makes the configuration explicit and easy to understand. This approach aligns with infrastructure-as-code best practices and Terraform's state management patterns. Each region should be as autonomous as possible to avoid central points of failure and security risks.
* **Evidence**: Separate state files per region eliminate blast radius concerns, enable parallel deployments, and simplify troubleshooting. The explicit directory structure makes it immediately clear what resources are deployed where. State will be broken up by region for better management and risk mitigation.
* **Comparison**: YAML-driven approaches reduce duplication but sacrifice explicitness and make debugging more complex. Monolithic state files create dependencies between regions and increase risk during changes.
* **Scope**: Terraform use is narrowed to region bootstrapping (creating projects, VPCs, and initial GKE clusters), with ArgoCD and future operators handling subsequent configurations and application lifecycle management.

## Example Directory Structure

```
└── terraform
    ├── config
    │   ├── global
    │   │   ├── integration
    │   │   │   ├── api_gateway
    │   │   │   └── dns
    │   │   ├── production
    │   │   │   ├── api_gateway
    │   │   │   └── dns
    │   │   └── staging
    │   │       ├── api_gateway
    │   │       └── dns
    │   └── region
    │       ├── integration
    │       │   ├── canary
    │       │   │   ├── us-central1
    │       │   │   └── us-east2
    │       │   └── main
    │       │       ├── us-central1
    │       │       └── us-east2
    │       ├── production
    │       │   ├── canary
    │       │   │   ├── us-central1
    │       │   │   └── us-east2
    │       │   └── main
    │       │       ├── us-central1
    │       │       └── us-east2
    │       └── staging
    │           ├── canary
    │           │   ├── us-central1
    │           │   └── us-east2
    │           └── main
    │               ├── us-central1
    │               └── us-east2
    └── modules
        ├── api_gateway
        ├── global_dns
        └── region
```

Each region directory (e.g., `config/region/integration/main/us-central1/`) contains:
- `main.tf` - Terraform configuration referencing the appropriate module
- Dedicated state file path for independent lifecycle management backed by GCS

## Module Management

Modules are stored in the same repository under the `terraform/modules/` directory to enable:
- Consistent module versions across regions within the same environment
- Simplified development workflow with local references
- Clear separation between infrastructure configuration and reusable modules

Module references will use relative local paths within the repository (e.g., `source = "../../../../modules/region"`). As the project matures, there may be opportunities to move modules to dedicated repositories with version pinning via Git tags/refs, but that decision will be made based on future requirements.

The team will utilize Google-provided Terraform modules where appropriate, acknowledging potential version compatibility challenges.

## Terraform State Management

- **Backend**: GCS (Google Cloud Storage) will be used as the Terraform state backend
- **State Organization**: State files are broken up by region rather than using one massive state file
- **Plan Review**: Terraform plan changes require review and approval before apply operations. Initially, all plans will require manual approval. As the number of environments and regions scales, the approval process will evolve to support automated approval for low-risk changes (e.g., non-production canary deployments) while maintaining manual review for critical paths (e.g., production infrastructure, global resources)

## Consequences

### Positive

* Clear separation between global and regional resources
* Independent state management per region reduces blast radius
* Explicit configuration makes infrastructure layout immediately understandable
* Supports parallel regional deployments
* Enables canary deployment patterns for gradual rollouts
* Simplifies troubleshooting and debugging with isolated state files
* Module versioning via Git refs provides consistency and rollback capability
* Regional autonomy reduces central points of failure and security risks
* Enables automated, GitOps-driven region provisioning where configuration changes can trigger entire region stand-up
* Supports nightly end-to-end testing by standing up entire regions, running tests, and tearing down

### Negative

* Some duplication of `main.tf` files across region directories
* More directories to navigate compared to YAML-driven approaches
* Requires tooling or automation (e.g., `make region` command) to scaffold new regions
* Multiple state files to manage and track

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: Structure supports adding new regions and environments without refactoring existing code
* **Observability**: Clear directory layout makes it easy to identify which infrastructure is affected by changes
* **Resiliency**: Independent state files prevent cascading failures across regions; failure in one region doesn't impact others

### Security:
- Explicit separation of environments prevents accidental cross-environment changes
- Per-region state files enable granular access controls
- Module versioning via Git refs ensures consistent, auditable infrastructure deployments

### Performance:
- Parallel regional deployments reduce overall deployment time
- Smaller state files per region improve Terraform plan/apply performance
- Independent state eliminates contention during concurrent deployments

### Cost:
- No additional infrastructure costs for this organizational pattern
- May require tooling development for scaffolding and automation
- Reduced risk of costly mistakes through clear separation of concerns

### Operability:
- Automated scaffolding (e.g., `make region`) reduces manual effort for new regions
- Explicit structure reduces cognitive load when navigating infrastructure
- Compatible with GitOps workflows and CI/CD automation
- Module repository separation enables clear ownership and development workflows

**See also**: [Deployment Tooling Policy](./deployment-tooling-swim-lanes.md) — the Terraform code hierarchy maps to Lane 1 (foundational infrastructure), with ArgoCD handling all cluster workloads in Lane 2.
