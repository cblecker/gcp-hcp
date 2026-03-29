# Adopt Cincinnati for Version Resolution

***Scope***: GCP-HCP

**Date**: 2026-03-30

## Decision

Replace the hardcoded release image in the CLS Controller with dynamic version resolution via the OpenShift Cincinnati update service.

This is the first step toward version selection and upgrades for GCP HCP — not a complete upgrade system. The CLS Backend will proxy Cincinnati queries to resolve version strings to release images, and the CLS Controller will read the release image from the cluster spec instead of hardcoding it. This also gives us an opportunity to validate the pattern and surface any limitations of querying Cincinnati directly — learnings that will inform the CLM implementation.

## Context

- **Problem Statement**: Users cannot select an OCP version when creating a GCP HCP cluster, and there is no mechanism to trigger upgrades. The controller hardcodes `quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64`, requiring a Helm chart update and redeployment for every version change.
- **Constraints**:
  - CLS Backend and Controller will be retired soon — changes must be minimal and pragmatic
  - GCP HCP is behind `TechPreviewNoUpgrade` feature gate, so Cincinnati channels may have limited content for newer versions
  - No additional infrastructure (caches, databases) should be introduced
  - The controller is template-driven; Go code changes should be avoided where possible
- **Assumptions**:
  - The Cincinnati API (`https://api.openshift.com/api/upgrades_info/v1/graph`) remains stable and publicly accessible
  - GCP HCP targets OCP 4.22+ as the minimum supported version; older versions are not relevant for this platform
  - Setting `spec.channel` on the HostedCluster will cause CVO to query Cincinnati and populate `status.version.availableUpdates` — this has not been validated on GCP HCP clusters yet and should be tested early

## Out of Scope

This decision covers introducing Cincinnati as the version source and basic upgrade support. A complete upgrade system is not in scope:

- Automatic upgrade policies (when and how hosted clusters upgrade to new z-streams)
- Upgrade scheduling or orchestration
- Channel persistence per-cluster (storing the channel in the cluster record so future operations default to it — users pass `--channel-group` explicitly each time instead). Not worth adding given the upcoming CLS retirement in favor of CLM.
- Cluster-specific upgrade status and available upgrade listing (ROSA has `rosa list upgrades` and `rosa describe upgrade` — can be added later)
- Cincinnati response caching
- Version skew validation (e.g., NodePool-to-HostedCluster version compatibility — HyperShift enforces this). Note: upgrade path validation against Cincinnati edges *is* in scope (see implementation plan, Task 3c).

## Alternatives Considered

1. **Cincinnati proxy in CLS Backend (chosen)**: Backend exposes a `/api/v1/versions` endpoint that queries Cincinnati on-demand and returns available versions. Backend resolves version-to-image at cluster creation time. No caching.

2. **Hardcoded release image (current approach)**: The controller Helm template hardcodes a single release image (`quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64`). All clusters get the same version. Changing it requires updating the Helm chart and redeploying the controller.

3. **Curated ClusterImageSets (ROSA pattern)**: ROSA uses a curated [clusterimagesets](https://gitlab.cee.redhat.com/service/clusterimagesets) repository consumed via uhc-clusters-service, adding an intermediary between Cincinnati and the managed service that can filter versions, disable specific upgrade edges, and control which images are offered per environment.

## Decision Rationale

* **Justification**: Alternative 1 (Cincinnati proxy in backend) centralizes version resolution in the backend, keeping the CLI thin and the controller template-only. Cincinnati is the canonical upstream source for OCP release versions and upgrade paths.

* **Comparison**:
  - Alternative 2 (hardcoded image) was rejected because it requires manual updates for every new OCP release — the same problem as the current hardcoded image.
  - Alternative 3 (ClusterImageSets) provides curation on top of Cincinnati (used by ROSA) but is more complexity than needed at this stage. Cincinnati directly is simpler and good enough to start. Whether additional curation is needed will be evaluated as part of the CLM implementation.

## Environment-Specific Version Strategy

A key benefit of channel and version support is enabling different version strategies per environment:

| Environment | Channel | Purpose |
|-------------|---------|---------|
| **E2E tests** | `candidate-4.22` | Earliest access to new builds, including release candidates — catches regressions before GA |
| **Integration** | `fast-4.22` | GA releases only, available days after release — validates without RC noise |
| **Stage** (future) | `stable-4.22` | Fully soaked GA releases — mirrors production version policy |
| **Production** (future) | `stable-4.22` | Proven releases only, weeks after GA |

Each environment is progressively more conservative. E2E tests use `candidate` to catch issues at the earliest opportunity, including release candidates before they GA. Integration uses `fast` to validate with GA releases that haven't completed the full soak period. Stage and production use `stable`, where releases have been proven across the `fast` channel with no significant regressions.

**Current state (as of 2026-03-30):** OCP 4.22 is pre-GA. Only `candidate-4.22` has content (Engineering Candidates: `4.22.0-ec.0` through `4.22.0-ec.4`). `fast-4.22` and `stable-4.22` are empty. Until 4.22 GA, all environments must use `candidate-4.22` or provide an explicit `--release-image`. The environment-specific channel strategy takes effect once 4.22 reaches GA and propagates through `fast` and `stable`.

The channel is specified per cluster creation — there is no global environment-level channel configuration. Each environment's automation (CI jobs, ArgoCD, scripts) passes the appropriate `--channel-group` flag when creating clusters.

## Consequences

### Positive

* Users can select a specific OCP version when creating clusters without knowing the image pullspec
* Version changes no longer require controller Helm chart updates and redeployments
* Upgrade paths are validated against Cincinnati edges before being applied
* Minimal code changes: backend gets a thin Cincinnati client, controller changes are template-only
* CLI gains version listing and upgrade commands, improving user experience
* Cluster and nodepool upgrades are independent operations, matching the ROSA CLI pattern (`rosa upgrade cluster` / `rosa upgrade machinepool`)
* Channel group defaults to `stable` (same as ROSA CLI `--channel-group`), with fallback through `fast` → `candidate` when a default version is needed
* Different environments (e2e, integration, stage) can target different channels, enabling early regression detection in CI while maintaining stability in integration and production-like environments

### Negative

* Each version query hits the Cincinnati API directly (no caching) — acceptable for low request volume during CLS lifetime
* Cincinnati may not have entries for `candidate-*` or `stable-*` channels for unreleased versions (e.g., `stable-4.22` may be empty until 4.22 GA), limiting version selection to `candidate` or `fast` channels initially

## Cross-Cutting Concerns

### Reliability:

* **Resiliency**: If Cincinnati is unavailable, the backend returns an error and the user must provide an explicit image pullspec. No fallback or retry logic is added given the short-lived nature of this solution.

### Security:

* The Cincinnati API is public and requires no authentication. No credentials are stored or transmitted.

### Performance:

* Cincinnati queries add latency to cluster creation (single HTTP round-trip to `api.openshift.com`). This is acceptable for a synchronous cluster creation flow that already takes minutes.

### Operability:

* No new infrastructure to maintain (no cache, no database tables, no background jobs)
* Version availability is determined by what Red Hat publishes to Cincinnati channels — no manual curation required
* Controller template changes are deployed via existing Helm/ArgoCD pipeline

## References

* [Cincinnati update service (OpenShift docs)](https://docs.openshift.com/container-platform/latest/updating/understanding_updates/intro-to-updates.html)
* [Cincinnati API endpoint](https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-4.22&arch=amd64)
* [Cincinnati source code (github.com/openshift/cincinnati)](https://github.com/openshift/cincinnati)
* [ClusterImageSets repository (gitlab.cee.redhat.com)](https://gitlab.cee.redhat.com/service/clusterimagesets)
* [ROSA CLI documentation](https://docs.openshift.com/rosa/cli_reference/rosa_cli/rosa-manage-objects-cli.html)