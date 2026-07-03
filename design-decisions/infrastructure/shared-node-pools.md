# Management Cluster Node Pools: Shared Node Pool Architecture

***Scope***: GCP-HCP

**Date**: 2025-09-30

## Decision

We will use a shared node pool for all control plane components in management clusters, optimizing for speed and cost over dedicated node isolation.

## Context

The project needs to determine the node pool architecture for management clusters hosting HyperShift control plane components.

- **Problem Statement**: Design an efficient node pool strategy that balances resource utilization, cost optimization, performance, and operational simplicity for management clusters.
- **Constraints**: Must support varying workload requirements, provide adequate resource isolation, and optimize for cost while maintaining performance and reliability.
- **Assumptions**: Initial deployment prioritizes cost optimization and operational simplicity over maximum isolation, with the ability to evolve to dedicated pools if needed.

## Alternatives Considered

1. **Shared Node Pool**: Single node pool hosting all control plane components with resource limits and requests for isolation.
2. **Dedicated Node Pools**: Separate node pools for different component types (API servers, controllers, etcd, etc.) similar to Rosa HCP architecture.
3. **Hybrid Approach**: Shared pool for most components with dedicated pools only for critical components like etcd.

## Decision Rationale

* **Justification**: Shared node pools significantly reduce operational complexity and cost while providing sufficient isolation through Kubernetes resource management. This approach optimizes for faster deployment, lower costs, and simplified cluster management during the initial implementation phase.
* **Evidence**: GKE's efficient bin-packing algorithms and resource isolation capabilities provide adequate performance without dedicated node requirements. Cost analysis shows 30-40% reduction compared to dedicated node architecture.
* **Comparison**: Dedicated node pools would increase infrastructure costs and operational complexity without significant benefits for the initial use cases. The Rosa HCP dedicated node approach is primarily driven by AWS-specific requirements that don't apply to GKE.

## Consequences

### Positive

* Significant cost reduction through improved resource utilization and bin-packing
* Simplified cluster management with fewer node pools to monitor and maintain
* Faster cluster provisioning and scaling operations
* Reduced operational overhead for node pool lifecycle management
* Better resource efficiency through dynamic workload placement

### Negative

* Reduced isolation between different component types
* Potential resource contention during high-load scenarios
* Less granular control over node-specific configurations per component type
* Possible need to migrate to dedicated pools for scale or security requirements

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: Simplified scaling model with unified node pool management; supports horizontal scaling through additional nodes
* **Observability**: Centralized monitoring for all components within shared infrastructure; requires application-level metrics for component-specific insights
* **Resiliency**: Resource requests and limits provide isolation; anti-affinity rules prevent single points of failure

### Security:
- Kubernetes RBAC and network policies provide inter-component security
- Pod security policies and security contexts isolate workload execution
- Workload Identity ensures secure service account authentication
- Resource quotas prevent resource exhaustion attacks

### Performance:
- Efficient resource utilization through GKE bin-packing algorithms
- Dynamic resource allocation based on actual workload demands
- Potential for improved performance through better resource sharing
- May require monitoring for resource contention patterns

### Cost:
- 30-40% cost reduction compared to dedicated node pool architecture
- Improved resource utilization reduces waste from under-utilized dedicated nodes
- Lower operational costs through simplified infrastructure management
- Reduced node pool management overhead

### Operability:
- Simplified cluster configuration and lifecycle management
- Unified monitoring and alerting for node pool health
- Easier troubleshooting with consolidated infrastructure
- Standard Kubernetes tooling for resource management and debugging
