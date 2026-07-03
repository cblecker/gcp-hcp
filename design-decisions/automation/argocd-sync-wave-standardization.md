# ArgoCD Sync Wave Standardization

***Scope***: GCP-HCP

**Date**: 2026-01-09

## Decision

We will standardize ArgoCD sync wave annotations across all applications and resources in the GCP-HCP platform using a minimal 3-wave system that relies on Kubernetes reconciliation for most dependencies. This includes both application-level waves (for App-of-Apps patterns) and resource-level waves (within individual applications).

## Context

### Problem Statement

Without standardized sync wave conventions, ArgoCD deployments can fail due to hard blockers:
- Custom Resources being created before their CRDs exist
- Applications attempting to use infrastructure (databases, pubsub topics) before Config Connector provisions them
- Inconsistent deployment ordering across different environments and teams

Note: Many apparent "dependencies" (Secrets, ConfigMaps, ServiceAccounts) are handled naturally by Kubernetes reconciliation - Pods will wait in Pending state until dependencies are available.

### Constraints

- ArgoCD processes sync waves sequentially, waiting for health checks before proceeding
- Sync waves are processed lowest to highest (-N to +N)
- Both applications and individual resources can have sync wave annotations
- Wave values must be strings in annotations
- Large wave gaps may increase overall sync time

### Assumptions

- All applications will be deployed through ArgoCD
- Teams will follow the standardized wave assignments
- Health checks are properly configured for critical resources
- The app-of-apps pattern will be used for organizing application deployments

## Alternatives Considered

1. **No Standardization (Current State)**: Allow teams to define sync waves ad-hoc
2. **Declarative Dependencies Only**: Use ArgoCD's resource hooks and only explicit dependencies
3. **Granular Wave System**: Detailed wave assignments for every resource type (e.g., Namespaces at -10, Secrets at -5, etc.)
4. **Minimal 3-Wave System (Chosen)**: Only distinguish hard blockers (-5), applications (0), and rare dependents (+5)

## Decision Rationale

### Justification

The minimal 3-wave system leverages Kubernetes' built-in reconciliation capabilities rather than fighting them. Most dependencies (Secrets, ConfigMaps, ServiceAccounts) don't need explicit sync waves because Kubernetes handles them gracefully - Pods wait in Pending state, controllers retry, etc. We only use sync waves for true hard blockers where reconciliation cannot help.

### Evidence

- Kubernetes is designed for eventual consistency - most resources will reconcile automatically
- Pods naturally wait for Secrets/ConfigMaps via InitContainers or kubelet retries
- Over-engineering sync waves increases deployment time without meaningful benefit
- The vast majority of resources should use the default wave (0)

### Comparison

- **No Standardization**: Rejected because true hard blockers (CRDs before CRs) need consistent handling
- **Declarative Dependencies**: Rejected because ArgoCD doesn't support this natively
- **Granular Wave System**: Rejected as over-engineering that slows deployments and adds complexity. Most "dependencies" resolve naturally through Kubernetes reconciliation.
- **Minimal 3-Wave System**: Chosen because it handles the few genuine hard blockers while relying on Kubernetes for everything else

## Consequences

### Positive

* Extremely simple to understand and apply (only 3 waves)
* Faster deployments by relying on Kubernetes reconciliation for most resources
* Prevents genuine hard blocker failures (CRDs, infrastructure provisioning)
* Easy onboarding - default wave (0) works for 90%+ of resources
* Minimal impact on sync time - only truly dependent resources wait

### Negative

* Requires understanding when sync waves are actually needed vs. when to rely on reconciliation
* Retrofitting existing applications requires identifying true hard blockers
* Teams accustomed to explicit ordering may initially over-use sync waves
* Rare edge cases may need custom wave values with documentation

## Cross-Cutting Concerns

### Reliability

* **Resiliency**: Sequential wave processing ensures dependencies are met before proceeding, reducing cascading failures. Health checks at each wave prevent unhealthy resources from blocking subsequent waves.
* **Observability**: Standardized waves make ArgoCD UI and logs easier to interpret. Wave numbers clearly indicate deployment stage in sync status.

### Operability

* **Deployment Complexity**: Minimal - most resources use default wave 0 (no annotation). Only Config Connector and CRDs need wave -5 annotation.
* **Maintenance**: Wave assignments rarely change. Simple rules make it easy to apply consistently.
* **Tooling**: Standard enables automated validation. CI/CD pipelines can enforce that Config Connector resources have wave -5 annotation.

### Performance

* **Sync Speed**: Minimal wave system reduces sequential health check pauses, resulting in faster deployments compared to granular wave hierarchies.
* **Resource Ordering**: ArgoCD's kind-based ordering within waves ensures Namespaces are created first automatically, without additional wave overhead.

---

## Standard Sync Wave Assignments

### Resource-Level Waves (Within Applications)

| Wave | Resource Types | Purpose | Examples |
|------|----------------|---------|----------|
| -5   | Hard Blockers | Resources that MUST exist before apps can start | CRDs, Config Connector resources (all GCP infrastructure) |
| 0    | Applications | Everything else (default - no annotation needed) | Namespaces, Deployments, Services, Secrets, ConfigMaps, ServiceMonitor, PodMonitor |
| +5   | Reverse Dependencies | Rare - resource needs app running first | ValidatingWebhookConfiguration, MutatingWebhookConfiguration (with `failurePolicy: Fail`) |

**Key Principle**: Default to wave 0 (no annotation). Only use -5 or +5 for genuine hard blockers where Kubernetes reconciliation cannot help.

**Note on Namespace Ordering**: ArgoCD automatically orders resources by kind within each wave. Namespaces are created before other resources in the same wave. However, if wave -5 resources (CRDs, Config Connector) are in a custom namespace, that namespace must ALSO be in wave -5.

### Application-Level Waves (App-of-Apps Pattern)

| Wave | Application Type | Examples |
|------|------------------|----------|
| -10  | Infrastructure Operators | cert-manager, external-secrets-operator, sealed-secrets, CRD-only applications |
| -5   | Config Connector Stacks | GCP infrastructure (Cloud SQL, PubSub, GCS, IAM) |
| 0    | Business Applications | API services, web frontends, background workers |

**Note**: Application-level waves require a custom health check in ArgoCD v1.8+ (add to `argocd-cm` ConfigMap). This was removed by default in v1.8 to prevent child health from affecting parent health, but it's needed for sync waves to work properly. See [Application Health Check](#application-health-check-required-for-app-of-apps) below for the simple Lua configuration.

## Implementation Guidelines

### Annotation Format

Always use quoted string values:

```yaml
metadata:
  annotations:
    argocd.argoproj.io/sync-wave: "-5"
```

### ApplicationSet Example

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
spec:
  template:
    metadata:
      annotations:
        argocd.argoproj.io/sync-wave: "-5"
    spec:
      # Application specification
```

### Sync Options

Combine waves with sync options for optimal behavior:

```yaml
spec:
  syncPolicy:
    syncOptions:
      - PruneLast=true  # Delete resources in reverse wave order
      - RespectIgnoreDifferences=true
```

### Application Health Check (Required for App-of-Apps)

For sync waves to work between Applications in app-of-apps patterns, add this to the `argocd-cm` ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  resource.customizations.health.argoproj.io_Application: |
    hs = {}
    hs.status = "Progressing"
    hs.message = ""
    if obj.status ~= nil then
      if obj.status.health ~= nil then
        hs.status = obj.status.health.status
        if obj.status.health.message ~= nil then
          hs.message = obj.status.health.message
        end
      end
    end
    return hs
```

**Why this is needed**: ArgoCD v1.8+ removed the default Application health check to prevent child health from affecting parent health. This Lua script re-enables it, allowing sync waves to wait for child Applications to become healthy.

**Reference**: [ArgoCD v1.7 to v1.8 Upgrade Notes - Health assessment of argoproj.io/Application CRD has been removed](https://argo-cd.readthedocs.io/en/stable/operator-manual/upgrading/1.7-1.8/#health-assessment-of-argoprojioapplication-crd-has-been-removed)

## Best Practices

1. **When to use wave -5:**
   - **CRDs**: Custom Resource Definitions that your Custom Resources depend on
   - **Config Connector resources**: ALL GCP infrastructure (CloudSQL, PubSub, GCS, IAM, etc.)
   - **NOT** for Secrets, ConfigMaps, Namespaces, ServiceAccounts - Kubernetes handles these via reconciliation

2. **When to use wave 0 (default):**
   - **90%+ of all resources** - this is the default, no annotation needed
   - Namespaces (unless they contain wave -5 resources - see exception below)
   - Deployments, StatefulSets, DaemonSets, Jobs
   - Services, Secrets, ConfigMaps, ServiceAccounts
   - Monitoring resources (ServiceMonitor, PodMonitor, PrometheusRules)
   - ExternalSecret resources (Pods wait gracefully for resulting Secrets)
   - Any resource where "eventual consistency" is acceptable
   - **Exception**: If a namespace contains wave -5 resources (CRDs, Config Connector), the namespace must also be wave -5

3. **When to use wave +5:**
   - **Rarely needed** (hopefully never)
   - ValidatingWebhookConfiguration or MutatingWebhookConfiguration with `failurePolicy: Fail` that target the application
   - **Important**: Webhooks with `failurePolicy: Ignore` can use wave 0
   - **Ask yourself**: "Will Kubernetes reconciliation handle this naturally?" If yes, use wave 0

4. **Health Checks and Conditions:**
   - Sync waves only work if resources have proper health checks configured
   - ArgoCD waits for resources in each wave to be "Healthy" before proceeding to the next wave
   - **CRDs**: Healthy when `status.conditions` shows "Established"
   - **Config Connector**: Healthy when `status.conditions` shows "Ready"
   - **Applications** (app-of-apps): ArgoCD v1.8+ removed the default health check for Application resources. Add a simple Lua health check to `argocd-cm` ConfigMap to enable sync wave ordering between Applications (see [Application Health Check](#application-health-check-required-for-app-of-apps))
   - If a resource doesn't report health status correctly, ArgoCD may proceed to the next wave prematurely

5. **Testing:**
   - Test full sync sequences in dev/staging environments
   - Verify that Pods wait gracefully for dependencies (check Pending states, events)
   - Use `argocd app sync --dry-run` to preview sync order
   - Monitor sync duration - minimal waves = faster deployments

6. **Documentation:**
   - Document any use of wave +5 (should be rare)
   - Challenge proposed custom wave values - "Can Kubernetes reconcile this?"
   - Require architectural review for non-standard wave values (not -5, 0, or +5)

**See also**: [Deployment Tooling Policy](./deployment-tooling-swim-lanes.md) — sync waves are a Lane 2 (ArgoCD) construct that orders component deployment within the ArgoCD swim lane.
