# No Direct Cross-Cluster Network Connectivity

***Scope***: GCP-HCP

**Date**: 2026-02-09

**Related**: GCP-388

## Decision

All direct TCP/UDP network connections between clusters (Global, Regional, Management) are forbidden by default. Cross-cluster coordination must use asynchronous, indirect mechanisms or GCP-level APIs. Direct access (via mechanisms such as GKE Fleet Connect) may be permitted only in rare, audited occasions through controlled escalation procedures to be defined. This prohibition applies to control-plane connectivity; PSC connections for worker-node-to-control-plane data-plane connectivity are explicitly out of scope.

## Context

The GCP HCP architecture consists of multiple cluster tiers (Global, Regional, Management, Customer) that must coordinate and exchange state information for workload distribution, resource management, and status synchronization.

- **Problem Statement**: Without an explicit rule, components (Terraform, ArgoCD, CLS/CLM controllers) and operators (SREs) could establish direct connections to other clusters' Kubernetes APIs — creating tight coupling, expanding the attack surface, enabling lateral movement across cluster boundaries, and bypassing audit controls.

- **Constraints**:
  - Must not break existing data-plane connectivity (PSC connections from worker nodes to hosted control planes)
  - Must provide viable alternatives for every current cross-cluster control-plane use case
  - Must align with regional independence principles and zero-trust networking best practices
  - Must support compliance with data residency regulations through network-level isolation
  - Must allow rare, audited direct access for exceptional operational scenarios (break-glass, deep debugging)
  - SRE operations should rely on automated workflow triggers and indirect mechanisms by default

- **Assumptions**:
  - Indirect mechanisms (pub-sub, GCP APIs, automated workflows) can replace all routine direct cross-cluster control-plane interactions
  - The operational benefits of blast radius isolation outweigh the added complexity
  - Rare exceptional access requirements can be satisfied through controlled escalation mechanisms (to be designed)

## Alternatives Considered

1. **No Direct Connectivity by Default (Chosen)**: Strict network isolation; all cross-cluster communication via indirect mechanisms. Exceptional access requires explicit escalation and audit.

2. **Controlled Direct Connectivity**: Allow direct Kubernetes API access between specific clusters with strict controls including mTLS authentication, network policies limiting source/destination, RBAC restrictions, and audit logging. Each cross-cluster connection would be explicitly approved and secured.

3. **VPN Mesh Between Clusters**: Establish encrypted VPN tunnels (Cloud VPN or Cloud Interconnect) between clusters, enabling direct but secured connectivity. Network policies and firewall rules would restrict which components can communicate across the VPN mesh.

## Decision Rationale

* **Justification**: Defense in depth through network segmentation provides the strongest security posture. Even if one cluster is fully compromised, the attacker cannot pivot to other clusters via network connections, effectively containing the breach. This approach reduces the blast radius of security incidents and enforces the regional independence architecture principle by ensuring clusters can operate independently without network dependencies.

* **Evidence**:
  - Aligns with existing decisions: `rc-mc-transport-layer.md` (indirect RC-MC communication) and `regional-independence-architecture.md` (regional fault isolation)
  - Consistent with Zero Trust Architecture (NIST SP 800-207) and least privilege principles

* **Comparison**:
  - **Controlled Direct Connectivity**: Still exposes kube API endpoints across boundaries and relies on perfect mTLS/RBAC/network-policy configuration. Any misconfiguration creates unauthorized access paths.
  - **VPN Mesh**: Creates the very network paths we want to eliminate. VPN infrastructure becomes a high-value target, adds operational complexity, and complicates troubleshooting.

## Consequences

### Positive

* **Blast radius isolation**: Compromise of one cluster cannot cascade via network connections; no lateral movement possible
* **Cleaner architecture**: Forces explicit design of inter-cluster communication through well-defined, auditable interfaces
* **Reinforces regional independence**: Clusters operate without network dependencies on each other
* **Enforces automation**: Pushes operational workflows toward automation and self-service rather than ad-hoc kubectl commands

### Negative

* **Higher design complexity**: Every cross-cluster interaction must be designed through indirect mechanisms
* **Latency**: Asynchronous patterns add latency compared to direct kube API calls
* **Debugging friction**: SREs lose familiar kubectl access across clusters; break-glass scenarios require escalation
* **Tooling investment**: Controlled access mechanisms (PAM, audit logging, GKE Fleet Connect workflows) must be built

## Cross-Cutting Concerns

### Security:
- No kube API credentials need to be distributed across cluster boundaries for routine operations
- All cross-cluster operations flow through auditable channels; direct access requires explicit escalation with full audit trail

### Reliability:
- Clusters are independently resilient; failure in one cluster cannot cascade via network connections
- Requires investment in observability for indirect communication flows (cannot rely on direct kubectl inspection)

### Performance:
- Indirect patterns add latency vs. direct kube API calls; eliminates cross-cluster API polling traffic

### Cost:
- Controlled access tooling (PAM, Fleet Connect workflows, audit systems) must be developed
- No significant infrastructure cost impact; indirect communication infrastructure defined in separate decisions

### Operability:
- SREs must rely on automated workflows and observability dashboards by default
- Break-glass procedures (controlled direct access with PAM, time-limited credentials, audit logging) are future work to be designed and implemented

## Implementation Impact

### Terraform (running on Global/Regional clusters)
- Manages GCP-level resources only (projects, VPCs, GKE clusters, IAM)
- Cannot access Kubernetes API of other clusters (no CRDs, ConfigMaps, Secrets, etc.)
- In-cluster configuration handled by each cluster's own ArgoCD or indirect mechanisms

### ArgoCD
- Each cluster runs its own ArgoCD instance managing only local resources
- Regional ArgoCD cannot manage Management Cluster resources (and vice versa)

### Cross-Cluster Controllers (CLS/CLM, etc.)
- Cannot establish direct kube API connections to other clusters
- Must use indirect mechanisms for all cross-cluster resource management

### SRE Operational Access
- **Default**: No direct kubectl access to other clusters; routine operations use automated workflows and self-service tooling
- **Exceptional access**: Direct access (e.g., GKE Fleet Connect) permitted only for break-glass or deep debugging scenarios — time-limited, audited, and escalated through PAM or equivalent
- **Future work**: Controlled access mechanisms (PAM integration, Fleet Connect workflows, time-limited credentials, approval workflows) to be designed

### Data Plane (Out of Scope)
- PSC connections for worker-node-to-control-plane remain unchanged
- This decision covers control-plane and operator access patterns only
