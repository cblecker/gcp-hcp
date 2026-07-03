# GKE Networking: Dataplane V2 with eBPF and Cilium

***Scope***: GCP-HCP

**Date**: 2025-09-30

## Decision

We will use GKE Dataplane V2 (eBPF + Cilium) for management cluster networking to provide enhanced performance, security, and observability capabilities.

## Context

The project needs to select a networking implementation for GKE management clusters that provides optimal performance, security, and operational capabilities.

- **Problem Statement**: Choose the optimal networking dataplane for GKE management clusters that supports high-performance networking, advanced security policies, and comprehensive observability for multi-tenant control plane workloads.
- **Constraints**: Must support Kubernetes network policies, provide high throughput for control plane communications, enable advanced security features, and integrate with GCP monitoring capabilities.
- **Assumptions**: Dataplane V2 provides significant performance and security benefits over legacy networking while maintaining compatibility with standard Kubernetes networking APIs.

## Alternatives Considered

1. **GKE Dataplane V2 (eBPF + Cilium)**: Advanced networking with eBPF-based packet processing, Cilium CNI, and enhanced security features.
2. **Legacy GKE Networking**: Traditional iptables-based networking with standard GKE networking features.
3. **Custom CNI Solution**: Third-party CNI plugins like Calico or Flannel with custom configuration.

## Decision Rationale

* **Justification**: Dataplane V2 provides significant performance improvements through eBPF-based packet processing, eliminating the overhead of iptables rules. Cilium offers advanced security features like L7 network policies, service mesh capabilities, and enhanced observability that are valuable for multi-tenant control plane environments.
* **Evidence**: eBPF provides 50-90% performance improvement over iptables-based networking. Cilium's L7 policies enable fine-grained security controls between control plane components, and its observability features provide detailed network flow analysis.
* **Comparison**: Legacy networking lacks the performance and security features needed for high-scale control plane environments. Custom CNI solutions would require additional operational overhead and lack the deep GCP integration of Dataplane V2.

## Consequences

### Positive

* Significant performance improvement (50-90% better than iptables-based networking)
* Advanced L7 network policies for fine-grained security between components
* Enhanced observability with flow-level network monitoring and tracing
* Native integration with GCP monitoring and security services
* Service mesh capabilities for advanced traffic management

### Negative

* Newer technology with potentially less operational maturity
* Requires updated knowledge and troubleshooting procedures for eBPF/Cilium
* Possible compatibility considerations with existing networking tools
* Additional complexity for network policy configuration and management

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: eBPF provides linear scaling performance for network operations; Cilium supports large-scale cluster networking
* **Observability**: Rich network flow monitoring, L7 visibility, and integration with Cloud Monitoring for comprehensive network insights
* **Resiliency**: Advanced load balancing and failover capabilities; service mesh features for traffic resilience

### Security:
- L7 network policies for application-aware security controls
- Identity-based security policies using Cilium's security model
- Network segmentation and micro-segmentation capabilities
- Enhanced DDoS protection and traffic filtering at the eBPF layer

### Performance:
- 50-90% performance improvement over traditional iptables networking
- Low-latency packet processing through eBPF kernel bypass
- Efficient load balancing and connection tracking
- Optimized for high-throughput control plane communication patterns

### Cost:
- No additional licensing costs (included with GKE)
- Improved resource efficiency through better performance characteristics
- Reduced infrastructure requirements due to performance improvements
- Standard GKE pricing applies

### Operability:
- Requires team training on eBPF and Cilium troubleshooting procedures
- Enhanced debugging capabilities through Cilium observability tools
- Integration with standard Kubernetes networking APIs for compatibility
- Need for updated monitoring and alerting configurations for new metrics