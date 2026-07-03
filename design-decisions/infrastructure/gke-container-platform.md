# Container Platform: Google Kubernetes Engine (GKE)

***Scope***: GCP-HCP

**Date**: 2025-09-30

## Decision

We will use Google Kubernetes Engine (GKE) as the container platform for management clusters and regional clusters, not OpenShift.

## Context

The project needs to select a container orchestration platform for hosting the control plane components and backend services.

- **Problem Statement**: Choose the optimal Kubernetes platform that balances cost-effectiveness, operational simplicity, and GCP-native integration for hosting HyperShift management clusters.
- **Constraints**: Must support HyperShift operator workloads, provide enterprise-grade reliability, integrate well with GCP services, and minimize operational overhead.
- **Assumptions**: GKE will be more cost-effective than OpenShift for management clusters and aligns with the strategy of making GCP HCP as "Google-like as possible."

## Alternatives Considered

1. **Google Kubernetes Engine (GKE)**: Fully managed Kubernetes service with native GCP integration, automatic updates, and cost optimization features.
2. **OpenShift on GCP**: Self-managed OpenShift clusters running on GCP compute instances, matching Rosa HCP architecture.
3. **GKE Autopilot**: Fully managed, serverless Kubernetes experience with per-pod billing and automatic infrastructure management.

## Decision Rationale

* **Justification**: GKE provides the most cost-effective solution while maintaining enterprise-grade capabilities. It offers native GCP integration, automatic cluster management, and eliminates the licensing costs associated with OpenShift. This aligns with the goal of leveraging GCP-native services wherever possible.
* **Evidence**: GKE provides automatic node upgrades, built-in monitoring with Cloud Monitoring, and seamless integration with other GCP services like Cloud IAM, Cloud Logging, and Cloud Storage.
* **Comparison**: OpenShift would introduce additional licensing costs and operational complexity without providing significant benefits for management cluster use cases. GKE Autopilot was considered but may have limitations for specialized HyperShift operator requirements.

## Consequences

### Positive

* Significant cost reduction through elimination of OpenShift licensing fees
* Native GCP integration with Cloud IAM, monitoring, logging, and billing
* Automatic cluster management including node upgrades and security patches
* Simplified operational model with Google-managed control plane
* Better alignment with GCP-native architecture strategy

### Negative

* Different platform from customer-facing OpenShift clusters (operational complexity)
* Team may need to develop new expertise in GKE-specific features
* Potential compatibility considerations for HyperShift operator components
* Deviation from Rosa HCP architecture precedent

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: GKE provides automatic cluster scaling and node pool management; supports large-scale HyperShift operator deployments
* **Observability**: Native integration with Cloud Monitoring and Cloud Logging; supports Prometheus and other observability tools
* **Resiliency**: Google-managed control plane with 99.95% SLA; automatic failover and disaster recovery capabilities

### Security:
- Native integration with Google Cloud IAM for RBAC
- Workload Identity for secure service account authentication
- Automatic security updates and CVE patching
- Network policy support and private cluster configurations

### Performance:
- Optimized networking with GKE Dataplane V2 (eBPF + Cilium)
- High-performance persistent storage with GCP Persistent Disks
- Regional clusters for low-latency access to GCP services

### Cost:
- Elimination of OpenShift licensing fees (significant cost reduction)
- Pay-per-use model for cluster management fees
- Cost optimization through automatic node scaling and preemptible instances
- Efficient resource utilization with bin-packing algorithms

### Operability:
- Simplified cluster lifecycle management with automatic upgrades
- Native integration with GCP deployment tools (Terraform, Cloud Deployment Manager)
- Standard kubectl and Kubernetes tooling compatibility
- Reduced operational overhead compared to self-managed OpenShift
