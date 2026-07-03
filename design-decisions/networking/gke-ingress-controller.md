# Ingress Controller Selection: GKE Ingress (GCE)

***Scope***: GCP-HCP

**Date**: 2025-10-27

## Decision

We will use the GKE Ingress controller (GCE) as the standard ingress solution for all internal infrastructure tooling services rather than deploying third-party ingress controllers like ingress-nginx.

## Context

The GCP HCP infrastructure requires HTTP/HTTPS ingress capabilities for all internal tooling services (ArgoCD, Atlantis, etc.) with enterprise-grade security, SSL/TLS termination, and load balancing.

- **Problem Statement**: How to provide reliable, secure HTTP/HTTPS ingress for internal infrastructure tooling services while maintaining operational simplicity, leveraging GCP-native capabilities, and minimizing infrastructure overhead.
- **Constraints**: Must support SSL/TLS termination, integrate with security policies (Cloud Armor) for IP allowlisting, provide external load balancing for internal access, support health checks, and minimize operational maintenance burden.
- **Assumptions**: All internal infrastructure tooling services will be deployed on GKE clusters within GCP, and we can leverage GCP-native load balancing and security features rather than deploying additional infrastructure components.

## Alternatives Considered

1. **GKE Ingress (GCE)**: Native GKE ingress controller using Google Cloud Load Balancers, integrated with GCP services like Cloud Armor, Google-managed certificates, and Cloud CDN.
2. **ingress-nginx (Open Source)**: Community Kubernetes project deploying nginx-based ingress controllers as pods within the cluster.

## Decision Rationale

* **Justification**: GKE Ingress is the GCP-native solution that requires zero additional infrastructure deployment while providing enterprise-grade features through tight integration with Google Cloud Platform services. The solution eliminates the operational overhead of managing ingress controller pods, scaling, updates, and high availability while providing superior security integration through Cloud Armor and automatic SSL/TLS certificate provisioning.
* **Evidence**: GKE Ingress is included with GKE at no additional cost, automatically scales with traffic demands, integrates natively with VPC networking, and provides direct access to Google Cloud Load Balancer features including Cloud Armor security policies, Google-managed certificates, Cloud CDN, and global load balancing. The solution is fully managed by Google with no pod management or version upgrades required.
* **Comparison**: ingress-nginx requires deploying and managing nginx controller pods, handling high availability across zones, managing resource allocation, performing version upgrades, and lacks native integration with GCP security and certificate management services.

## Consequences

### Positive

* Zero infrastructure components to deploy, manage, or upgrade for ingress functionality
* Native integration with Cloud Armor for network-level security policies and DDoS protection
* Seamless integration with Google-managed SSL/TLS certificates for automatic provisioning and renewal
* Automatic scaling of load balancer capacity with no manual intervention or capacity planning
* Native VPC integration with no additional networking configuration required
* Access to Google Cloud Load Balancer features: health checks, session affinity, custom headers, timeout configuration
* No pod resource allocation, placement, or high availability concerns for ingress controllers
* Reduced attack surface through elimination of in-cluster ingress controller pods
* Single-pane-of-glass visibility through GCP console for load balancers, backends, and health checks
* Cloud Armor integration provides centralized IP allowlisting for Red Hat infrastructure access control

### Negative
* Load balancer provisioning takes longer (2-5 minutes) compared to in-cluster ingress controllers (seconds)
* Limited to GCP Cloud Load Balancer feature set; advanced nginx features (rate limiting, request rewriting, custom middleware) not available
* Debugging requires familiarity with GCP Load Balancer concepts vs. standard Kubernetes ingress troubleshooting
* Each ingress resource creates a separate Cloud Load Balancer by default (cost consideration for many ingresses)
* Less granular control over load balancing algorithms compared to nginx configuration
* GCP-specific CRDs (BackendConfig, FrontendConfig) required for advanced configuration vs. standard annotations

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: Google Cloud Load Balancers automatically scale to handle traffic demands with no capacity planning or manual intervention; no pod resource constraints or horizontal scaling configuration required; supports global load balancing across regions for multi-region deployments
* **Observability**: Integration with Cloud Monitoring for load balancer metrics (request rate, latency, error rate); Cloud Logging for access logs and error logs; GCP console provides real-time visibility into backend health, traffic distribution, and connection metrics; Kubernetes events for ingress status changes
* **Resiliency**: Google-managed infrastructure with automatic failover and redundancy; multi-zone load balancer distribution; automatic health checks with configurable intervals and thresholds; no single point of failure from in-cluster controller pods; SLA backed by Google Cloud Platform

### Security:
- Cloud Armor integration for network-level security policies with IP allowlisting and DDoS protection
- Centralized IP allowlist management for Red Hat infrastructure (bastions, squid proxies, GitHub webhooks)
- Default-deny security posture with explicit allow rules per service
- Google-managed SSL/TLS certificate integration for automatic certificate provisioning and renewal
- Separation of ingress infrastructure from cluster workloads (reduced attack surface)
- Integration with Cloud Audit Logs for comprehensive access auditing
- Support for IAP (Identity-Aware Proxy) integration for application-level access control
- TLS 1.2/1.3 enforcement with modern cipher suites managed by Google

### Performance:
- Google's global network infrastructure for low-latency routing
- Cloud CDN integration available for static content caching (if needed)
- Connection pooling and HTTP/2 support enabled by default
- No in-cluster proxy overhead; traffic routed directly to service backends
- Configurable timeout and keep-alive settings via BackendConfig
- Session affinity support for stateful applications
- Automatic connection draining during pod termination

### Cost:
- No additional cost for GKE Ingress controller functionality (included with GKE)
- Standard Cloud Load Balancer pricing applies: per-hour forwarding rule fees + data processing charges
- Cost scales linearly with traffic; no fixed infrastructure costs for ingress controller pods
- Potential cost optimization through ingress consolidation (multiple paths on single ingress)
- Cloud Armor security policies incur additional charges per policy and per request
- Cost savings from eliminated ingress controller pod resource allocation (CPU, memory)
- Reduced operational costs from elimination of ingress controller management overhead

### Operability:
- Zero operational overhead for ingress controller deployment, upgrades, or scaling
- Simplified troubleshooting through GCP console visibility into load balancer and backend health
- Standardized BackendConfig pattern for Cloud Armor security policy attachment
- ArgoCD sync wave annotations ensure proper resource creation ordering (certificates, BackendConfig, Ingress)
- GCP-specific knowledge required for advanced troubleshooting vs. standard Kubernetes ingress
- Longer initial provisioning time (2-5 minutes) must be considered in CI/CD pipelines
- Consistent behavior across all GKE clusters with no version drift concerns

## Implementation Pattern

### Basic Ingress with SSL and Cloud Armor

```yaml
# 1. Google Managed Certificate
apiVersion: networking.gke.io/v1
kind: ManagedCertificate
metadata:
  name: my-service-cert
  namespace: my-namespace
  annotations:
    argocd.argoproj.io/sync-wave: "-1"
spec:
  domains:
    - my-service.region.environment.example.com

---
# 2. BackendConfig for Cloud Armor
apiVersion: cloud.google.com/v1
kind: BackendConfig
metadata:
  name: my-service-armor-config
  namespace: my-namespace
  annotations:
    argocd.argoproj.io/sync-wave: "-1"
spec:
  securityPolicy:
    name: my-service-ingress-policy

---
# 3. Service with BackendConfig annotation
apiVersion: v1
kind: Service
metadata:
  name: my-service
  namespace: my-namespace
  annotations:
    cloud.google.com/backend-config: '{"default": "my-service-armor-config"}'
spec:
  type: ClusterIP
  ports:
    - port: 80
      targetPort: 8080
  selector:
    app: my-service

---
# 4. Ingress resource
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-service-ingress
  namespace: my-namespace
  annotations:
    kubernetes.io/ingress.class: "gce"
    networking.gke.io/managed-certificates: "my-service-cert"
spec:
  ingressClassName: "gce"
  rules:
    - host: my-service.region.environment.example.com
      http:
        paths:
          - path: /*
            pathType: ImplementationSpecific
            backend:
              service:
                name: my-service
                port:
                  number: 80
```

### Advanced BackendConfig Features

```yaml
apiVersion: cloud.google.com/v1
kind: BackendConfig
metadata:
  name: advanced-backend-config
  namespace: my-namespace
spec:
  # Cloud Armor security policy
  securityPolicy:
    name: my-ingress-policy

  # Connection draining timeout
  connectionDraining:
    drainingTimeoutSec: 60

  # Session affinity
  sessionAffinity:
    affinityType: "CLIENT_IP"
    affinityCookieTtlSec: 3600

  # Health check configuration
  healthCheck:
    checkIntervalSec: 10
    timeoutSec: 5
    healthyThreshold: 2
    unhealthyThreshold: 3
    type: HTTP
    requestPath: /healthz
    port: 8080

  # Timeout configuration
  timeoutSec: 30
```

## GKE Ingress Features Used

### Cloud Armor Integration
- **Purpose**: Network-level security with IP allowlisting and DDoS protection
- **Implementation**: BackendConfig CRD with `securityPolicy` specification
- **Use Case**: Restrict access to Red Hat infrastructure (bastions, squid proxies, GitHub webhooks)
- **Configuration**: Centralized Cloud Armor policies managed via Terraform module

### Google-Managed Certificates
- **Purpose**: Automatic SSL/TLS certificate provisioning and renewal
- **Implementation**: ManagedCertificate CRD referenced via ingress annotation
- **Use Case**: All infrastructure services require valid HTTPS certificates
- **Integration**: HTTP-01 challenge validation via Cloud Load Balancer

### Health Checks
- **Purpose**: Backend health monitoring and automatic traffic routing
- **Implementation**: BackendConfig `healthCheck` specification or automatic detection from readiness probes
- **Use Case**: Ensure traffic only routes to healthy pods
- **Configuration**: Customizable intervals, thresholds, and request paths

## Prerequisites

1. **GKE Cluster**: Version 1.21+ with GCE ingress controller enabled (default)
2. **DNS Configuration**: A-records in Cloud DNS for each service FQDN
3. **Cloud Armor Policies**: Terraform-managed security policies for IP allowlisting
4. **Service Account Annotations**: Not required for ingress functionality (unlike Cert-Manager)

## Deployment Workflow

1. **Create Cloud Armor policy** via Terraform (centralized in `terraform/modules/cloud-armor-ingress-policy`)
2. **Create ManagedCertificate** resource with ArgoCD sync wave `-1`
3. **Create BackendConfig** resource with Cloud Armor policy reference (sync wave `-1`)
4. **Create Service** with `cloud.google.com/backend-config` annotation
5. **Configure DNS A-record** pointing to load balancer external IP (initially unknown; can use wildcard or update after creation)
6. **Create Ingress** resource with GCE ingress class and managed-certificates annotation
7. **Wait for provisioning**: Load balancer creation (2-5 minutes) + certificate validation (10-30 minutes)
8. **Verify**: Check load balancer health, certificate status, and HTTPS access

## Troubleshooting Common Issues

| Issue | Cause | Resolution |
|-------|-------|------------|
| Ingress stuck in "Creating" | Load balancer provisioning in progress | Wait 2-5 minutes; check GCP console for load balancer status |
| Backend health check failing | Misconfigured health check path or pod not ready | Verify BackendConfig health check settings match application; check pod readiness probe |
| 502 Bad Gateway | No healthy backends | Check pod status, readiness probes, and backend health in GCP console |
| Cloud Armor policy not applied | BackendConfig annotation missing or incorrect | Verify Service has `cloud.google.com/backend-config` annotation matching BackendConfig name |
| Certificate not provisioning | DNS not configured or ingress not accessible | Verify A-record exists and points to load balancer IP; ensure ingress is publicly accessible |
| Multiple load balancers created | Multiple Ingress resources instead of multiple paths | Consolidate multiple services into single Ingress with multiple path rules if appropriate |

## Cost Optimization Strategies

1. **Ingress Consolidation**: Use multiple path rules on a single Ingress to share load balancer costs
2. **Health Check Tuning**: Adjust check intervals to balance cost vs. responsiveness
3. **Cloud Armor Policy Sharing**: Reuse security policies across multiple services where access patterns match
4. **Regional vs. Global**: Use regional load balancers for single-region deployments to reduce costs

## Comparison: GKE Ingress vs. ingress-nginx

| Feature | GKE Ingress (GCE) | ingress-nginx |
|---------|-------------------|---------------|
| **Deployment** | Zero deployment (GKE-native) | Deploy controller pods |
| **Management** | Fully managed by Google | Self-managed (upgrades, scaling, HA) |
| **Scaling** | Automatic | Manual HPA configuration |
| **SSL/TLS** | Google-managed certificates | Manual cert management or Cert-Manager |
| **Security** | Cloud Armor integration | Kubernetes NetworkPolicy or manual rules |
| **Cost** | Load balancer + traffic charges | Pod resources + traffic |
| **Provisioning** | 2-5 minutes | Seconds |
| **Features** | GCP load balancer features | Full nginx feature set |
| **Portability** | GCP-only | Cloud-agnostic |
| **Debugging** | GCP console + kubectl | kubectl + nginx logs |
| **High Availability** | Built-in (Google-managed) | Manual multi-replica deployment |

## Migration Considerations

If future requirements demand ingress-nginx features not available in GKE Ingress:
- ingress-nginx can coexist with GKE Ingress using different ingress classes
- Services can be migrated incrementally by changing `ingressClassName` and annotations
- Cloud Armor policies would need to be replaced with nginx rate limiting or Kubernetes NetworkPolicy
- SSL/TLS certificates would need alternative management (Cert-Manager or manual)

## Reference Implementations

Production implementations demonstrating the complete pattern:
- **Atlantis**: `argocd/config/central/region-atlantis/template.yaml`
  - Google-managed certificate
  - Cloud Armor security policy with GitHub webhook access
  - BackendConfig with security policy attachment
  - GCE ingress with SSL termination

- **ArgoCD**: `argocd/config/central/region-argocd/template.yaml`
  - ApplicationSet deploying to multiple regional clusters
  - Google-managed certificate per region
  - Cloud Armor security policy for internal access only
  - BackendConfig with security policy attachment

- **Cloud Armor Module**: `terraform/modules/cloud-armor-ingress-policy/`
  - Centralized IP allowlist management
  - Reusable Terraform module for security policies
  - Support for Red Hat infrastructure, GitHub webhooks, and custom IPs

## Developer Guide Summary

For teams deploying new internal tooling services with ingress:

1. **Create Cloud Armor policy** (if not using existing policy) via Terraform module
2. **Add ManagedCertificate resource** to Helm chart with sync wave `-1`
3. **Add BackendConfig resource** referencing Cloud Armor policy (sync wave `-1`)
4. **Annotate Service** with `cloud.google.com/backend-config` reference
5. **Configure Ingress** with `ingressClassName: "gce"` and managed-certificates annotation
6. **Create DNS A-record** for service FQDN (can be wildcard or updated post-deployment)
7. **Deploy via ArgoCD** and monitor load balancer + certificate provisioning
8. **Verify**: Check GCP console for backend health and test HTTPS access

Detailed examples and Helm value patterns are available in the Atlantis and ArgoCD reference implementations.
