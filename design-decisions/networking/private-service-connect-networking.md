# Worker Node Connectivity: Private Service Connect (PSC)

***Scope***: GCP-HCP

**Date**: 2025-09-30

## Decision

We will use Private Service Connect (PSC) for worker-to-control-plane connectivity, implementing PSC endpoints on customer VPCs connecting to PSC attachments on the service side via Internal Load Balancers.

## Context

The project needs a secure and scalable networking solution for connecting customer worker nodes to the hosted control plane infrastructure.

- **Problem Statement**: How to provide secure, private connectivity between customer worker nodes in their VPCs and the hosted control plane services while maintaining network isolation and security boundaries.
- **Constraints**: Must maintain network isolation between customers, provide secure communication channels, support scale requirements, and integrate with existing GCP networking primitives.
- **Assumptions**: PSC provides the necessary security, isolation, and performance characteristics for production workloads while leveraging GCP-native networking capabilities.

## Alternatives Considered

1. **Private Service Connect (PSC)**: Customer VPC endpoints connect to service-side PSC attachments via Internal Load Balancers, providing private connectivity.
2. **VPC Peering**: Direct VPC peering between customer VPCs and service provider VPC with routing controls.
3. **Cloud VPN/Interconnect**: Secure tunneling or dedicated connectivity between customer and service provider networks.

## Decision Rationale

* **Justification**: PSC provides the optimal balance of security, isolation, and operational simplicity. It ensures that customer traffic never traverses the public internet while maintaining strict network boundaries between different customers. PSC endpoints allow customers to connect from their own VPCs without exposing the service provider's network topology.
* **Evidence**: PSC is designed specifically for service provider scenarios and provides built-in multi-tenancy, security isolation, and scaling capabilities that align with the hosted control plane architecture.
* **Comparison**: VPC peering would create complex routing scenarios and potential IP address conflicts. VPN/Interconnect solutions would add operational complexity and latency without significant benefits over PSC.

## Consequences

### Positive

* Strong network isolation between customers and from service provider infrastructure
* No public internet exposure for control plane communication
* Native GCP integration with existing networking primitives
* Built-in load balancing and high availability through Internal Load Balancers

### Negative

* Requires PSC endpoint configuration in customer VPCs
* Additional complexity in network architecture and troubleshooting
* PSC endpoint costs for customers
* Dependency on GCP PSC service availability and roadmap

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: PSC supports high-bandwidth connections and can scale to accommodate large numbers of customer connections
* **Observability**: Integration with VPC Flow Logs and Cloud Monitoring for network traffic analysis
* **Resiliency**: Internal Load Balancer provides automatic failover and health checking for backend services

### Security:
- Complete network isolation between customer VPCs and service infrastructure
- No public internet exposure for control plane traffic
- Support for customer-controlled firewall rules and network policies
- IAM-based access control for PSC endpoint creation and management

### Performance:
- Low-latency private connectivity within GCP network backbone
- High-bandwidth capabilities suitable for control plane communication
- Regional deployment reduces latency for worker-control plane communication
- Load balancing optimizes traffic distribution across control plane replicas

### Cost:
- PSC endpoint charges for customers (predictable pricing model)
- Internal Load Balancer costs for service provider
- Reduced data transfer costs compared to public internet routing
- Cost optimization through regional deployment and traffic locality

### Operability:
- Requires documentation and tooling for customer PSC endpoint setup
- Network troubleshooting procedures for PSC connectivity issues
- Monitoring and alerting for PSC endpoint health and connectivity
- Integration with customer onboarding and lifecycle management processes