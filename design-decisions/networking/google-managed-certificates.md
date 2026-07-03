# SSL/TLS Certificate Management: Google-Managed Certificates

***Scope***: GCP-HCP

**Date**: 2025-10-27

## Decision

We will use Google-managed SSL/TLS certificates for all internal tooling service ingress resources to provide automatic certificate provisioning and lifecycle management without requiring third-party certificate management tools.

## Context

The GCP HCP infrastructure requires secure HTTPS access to all internal tooling services (ArgoCD, Atlantis, etc.) with valid SSL/TLS certificates that are automatically provisioned and renewed.

- **Problem Statement**: How to provide secure, trusted SSL/TLS certificates for all internal infrastructure tooling services without manual certificate lifecycle management, while maintaining security best practices and operational simplicity.
- **Constraints**: Must integrate with GKE ingress controllers, support the `{env}.example.com` Cloud DNS zone for internal services, provide automatic renewal, and minimize operational overhead for development teams.
- **Assumptions**: All services will use GCE-based ingress controllers and have properly configured DNS records pointing to the ingress load balancer. Google's HTTP-01 challenge mechanism will be used for domain validation.

## Alternatives Considered

1. **Google-Managed Certificates**: Native GCP solution using `ManagedCertificate` CRD with automatic provisioning via GCE ingress and HTTP-01 validation.
2. **Cert-Manager with Let's Encrypt**: Kubernetes-native certificate management using Cert-Manager, ACME protocol, and DNS-01 challenges with Workload Identity for Cloud DNS access.
3. **Manual Certificate Management**: Traditional approach using purchased or self-signed certificates uploaded as Kubernetes secrets.

## Decision Rationale

* **Justification**: Google-managed certificates provide the simplest operational model with zero additional infrastructure requirements beyond GKE and Cloud DNS. The solution is fully managed by Google, requires no additional components or permissions management, and integrates seamlessly with GCE ingress controllers already in use for our infrastructure.
* **Evidence**: Google-managed certificates are a GCP-native solution with no additional cost, automatic 90-day renewal, and proven reliability within the GCP ecosystem. The integration requires only Kubernetes-native resources (`ManagedCertificate` CRD and ingress annotations) with no external dependencies.
* **Comparison**: Cert-Manager requires additional deployment, Workload Identity configuration, DNS-01 challenge permissions, ongoing maintenance, and introduces an additional failure domain. Manual certificate management creates unacceptable operational overhead with certificate expiration risk and no automation.

## Consequences

### Positive

* Zero additional infrastructure components or dependencies to deploy and maintain
* No Workload Identity or IAM permission configuration required for certificate management
* Automatic certificate provisioning and 90-day renewal with no operational intervention
* Native integration with GCE ingress controllers already in use
* No additional cost beyond standard GCP service usage
* Simplified troubleshooting through GCP console certificate status visibility
* Reduced attack surface through elimination of additional certificate management workloads

### Negative

* Requires use of GCE ingress controller (not compatible with nginx-ingress or other ingress controllers)
* Certificate provisioning depends on HTTP-01 challenge requiring publicly accessible ingress endpoint
* Limited to domain validation (DV) certificates; no support for organization validation (OV) or extended validation (EV)
* Tightly coupled to GCP infrastructure; migration to other cloud providers would require certificate management redesign
* Initial provisioning can take 10-30 minutes depending on DNS propagation and Google's validation process
* Debugging limited to GCP console and Kubernetes events; less detailed than Cert-Manager logging

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: Google-managed certificates scale automatically with no limit on the number of certificates per cluster; no resource overhead from certificate management workloads
* **Observability**: Certificate status visible through `kubectl describe managedcertificate` and GCP console; Kubernetes events for provisioning failures; integration with Cloud Monitoring for certificate expiration alerts
* **Resiliency**: Automatic renewal 30 days before expiration; Google manages all renewal operations; no single points of failure from certificate management infrastructure; built-in retry logic for provisioning failures

### Security:
- Industry-standard domain validation (DV) certificates trusted by all major browsers
- No long-lived credentials or service account keys required for certificate operations
- Certificates are automatically provisioned into Kubernetes secrets managed by GKE
- Google's infrastructure handles private key generation and storage
- Automatic rotation eliminates certificate expiration security risks
- Integration with GCP Cloud Armor for additional ingress security policies

### Performance:
- No performance impact from certificate management operations
- Certificate serving performance identical to manually managed certificates
- HTTP-01 validation occurs only during initial provisioning and renewal
- No additional network hops or latency introduced by certificate management infrastructure
- Standard TLS handshake performance with no proxy or intermediary components

### Cost:
- No additional cost for Google-managed certificate usage
- Elimination of third-party certificate purchase costs (vs. commercial CAs)
- No operational costs for certificate management infrastructure deployment or maintenance
- Reduced incident response costs through automatic renewal and provisioning
- Standard GCP ingress and load balancer costs apply

### Operability:
- Minimal initial setup complexity: ManagedCertificate resource + ingress annotation
- Standardized Helm template pattern for consistent deployment across services
- DNS must be configured before certificate provisioning can succeed
- Clear error messages in Kubernetes events for troubleshooting provisioning failures
- No ongoing operational tasks required after initial setup
- ArgoCD sync wave annotation ensures certificate provisioning before ingress creation
- Simplified developer experience with reusable patterns documented in implementation guide

## Implementation Pattern

### ManagedCertificate Resource

```yaml
apiVersion: networking.gke.io/v1
kind: ManagedCertificate
metadata:
  name: <service>-cert
  namespace: <namespace>
  annotations:
    # Deploy before Ingress to ensure certificate is available
    argocd.argoproj.io/sync-wave: "-1"
spec:
  domains:
    - <service>.<environment>.example.com
```

### Ingress Configuration

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: <service>-ingress
  namespace: <namespace>
  annotations:
    kubernetes.io/ingress.class: "gce"
    # Reference the ManagedCertificate resource
    networking.gke.io/managed-certificates: "<service>-cert"
spec:
  ingressClassName: "gce"
  rules:
    - host: <service>.<environment>.example.com
      http:
        paths:
          - path: /*
            pathType: ImplementationSpecific
            backend:
              service:
                name: <service>
                port:
                  number: 80
```

### Prerequisites

1. **DNS Configuration**: A-record in Cloud DNS pointing to the ingress load balancer external IP
2. **GCE Ingress Controller**: Ingress must use `ingressClassName: "gce"`
3. **Public Accessibility**: Ingress endpoint must be publicly accessible for HTTP-01 challenge validation
4. **Unique Certificate Name**: ManagedCertificate name must be unique within the namespace

### Verification

```bash
# Check certificate status
kubectl describe managedcertificate <service>-cert -n <namespace>

# Look for Status: Active
# Provisioning typically takes 10-30 minutes

# Verify certificate in use
kubectl get ingress <service>-ingress -n <namespace> -o yaml

# Test HTTPS access
curl -v https://<service>.<environment>.example.com
```

### Troubleshooting Common Issues

| Issue | Cause | Resolution |
|-------|-------|------------|
| Status: ProvisioningCertificate (stuck) | DNS not configured or not propagated | Verify A-record exists and points to ingress IP |
| Status: FailedNotVisible | Ingress not publicly accessible | Check firewall rules and ingress configuration |
| Multiple certificates for same domain | Domain specified in multiple ManagedCertificate resources | Remove duplicate; only one ManagedCertificate per domain |
| Certificate not attached to ingress | Missing or incorrect annotation | Verify `networking.gke.io/managed-certificates` annotation matches ManagedCertificate name |

## Reference Implementation

The Atlantis service demonstrates the complete pattern:
- ManagedCertificate definition: `helm/charts/atlantis-stack/templates/managedcertificate.yaml`
- ArgoCD application with certificate configuration: `argocd/config/central/region-atlantis/template.yaml`

## Required GKE Cluster Configuration

Google-managed certificates require the following GKE cluster capabilities:
- GKE version 1.21+ (ManagedCertificate CRD availability)
- GCE ingress controller enabled (default for GKE clusters)
- No additional IAM permissions or Workload Identity configuration required
- Standard GKE cluster networking configuration

## Deployment Workflow

1. **Create ManagedCertificate resource** with ArgoCD sync wave `-1` to ensure early provisioning
2. **Configure DNS A-record** in Cloud DNS pointing to ingress load balancer IP
3. **Create Ingress resource** with `networking.gke.io/managed-certificates` annotation
4. **Wait for provisioning**: Monitor certificate status (typically 10-30 minutes)
5. **Verify HTTPS access**: Test endpoint with curl or browser

## Developer Guide Summary

For teams deploying new internal tooling services:

1. Add ManagedCertificate resource to Helm chart templates
2. Configure ingress with GCE ingress class and managed-certificates annotation
3. Ensure DNS record is created for the service FQDN
4. Deploy via ArgoCD and monitor certificate provisioning status
5. Verify HTTPS access once certificate status shows "Active"

Detailed configuration examples and Helm value patterns are available in the Atlantis reference implementation.
