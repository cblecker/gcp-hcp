Transport Layer: Maestro for Regional-Management Cluster Communication

> **Status**: Superseded by [Use Firestore as a Database-Backed Transport to Replace Maestro](datastore-transport.md) (2026-06-05)

***Scope***: GCP-HCP

**Date**: 2025-09-30

## Decision

We will use Maestro in gRPC Mode (no MQTT) as the transport mechanism for asynchronous communication between Regional Clusters and Management Clusters.

## Context

The Regional Architecture needs a reliable transport mechanism for communication between Regional Clusters and Management Clusters in the distributed architecture.

- **Problem Statement**: How to enable secure, scalable, and reliable communication between Regional Clusters and Management Clusters for workload distribution, status reporting, and resource synchronization.
- **Constraints**: Must support asynchronous communication patterns, handle network partitions gracefully, provide delivery guarantees, and integrate with the Cloud Provider.
- **Assumptions**: Maestro can use gRPC-only, providing a reliable channel for Stream of CloudEvents, avoiding the load of polling the KubeAPI for resource/status updates.

## Alternatives Considered

1. **Maestro on Google Cloud Pub/Sub**: Adapt Maestro to use GCP Pub/Sub as the underlying messaging infrastructure for regional-management cluster communication.
   * Pros:
     * Cloud-native Pub/Sub implementation.
     * Broad scalability, similar to MQTT.
     * Compliant with ADR-0300.
   * Cons:
     * Development work required on Maestro.
2. **Direct Kube API Communication**: Implement direct  HTTP Kube API communication between Regional and Management Clusters with retry logic and circuit breakers.
   * Pros:
     * No extra component in the architecture, simplified troubleshooting.
   * Cons:
     * Risk of overloading the Management Clusters Kube API, given the constant polling of resources.
     * Regional Clusters Service Account has KubeAPI access to all Management Clusters.
3. **ACM**: Replicate ROSA HCP and use ACM Hub to distribute resources to Management Clusters.
   * Pros:
     * Strong upstream community.
     * API for Backend is a CRD in a Kube API (ManifestWork) - simple implementation.
   * Cons:
     * Scalability concerns from the ROSA HCP experience. Mitigation would be to run one ACH Hub in each Management Cluster.
     * Still requires Kube API access from the Regional Cluster into the Management Cluster (ACM Hub).

## Decision Rationale

* **Justification**: Maestro provides a proven asynchronous messaging pattern for OpenShift environments and offers the sophisticated message handling capabilities required for distributed cluster management.
* **Evidence**: Maestro has been successfully used in OpenShift environments for similar use cases. 
* **Comparison**: Direct REST communication would require building complex retry, queuing, and failure handling logic that Maestro already provides. Custom solutions would require significant development effort to achieve the same reliability and feature set.

## Consequences

### Positive

* gRPC-only Mode already supported by Maestro, no additional development necessary.
* Well-defined security boundaries.
* Asynchronous communication reduces tight coupling between Regional and Management Clusters.
* Supports complex routing and filtering patterns for multi-tenant scenarios.
* Compliant with ADR-0300.

### Negative

* Additional complexity compared to direct REST communication

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: Maestro handles consumer scaling patterns.
* **Observability**: Maestro provides application-level observability
* **Resiliency**: Maestro implements database persistence and retry mechanisms.

### Security:
- IAM-based or mTLS-based authentication supported.
- Strong permission isolation between Regional Clusters and Management Clusters. 

### Performance:
- Asynchronous processing reduces latency for API responses
- Batch message processing capabilities for high-throughput scenarios
- Regional message routing for optimal latency

### Operability:
- Need for Maestro operational expertise and runbook development
- Integration with existing GCP monitoring and alerting systems
