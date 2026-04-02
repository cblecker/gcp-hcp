# Adopt Cincinnati for Version Resolution

***Scope***: GCP-HCP

**Date**: 2026-04-02

## Decision

Replace the hardcoded release image in the CLS Controller with controller-driven version resolution via the OpenShift Cincinnati update service.

The flow is: the user specifies a full target version (e.g., `4.22.1`) and an optional channel group when creating a cluster. These are stored in the cluster spec as `release.version` and `release.channelGroup`. A new purpose-specific controller resolves the version to a release image via Cincinnati and reports it as a status condition. The existing HostedCluster templating controller then uses the resolved image when creating the HostedCluster CR.

This gives us an opportunity to validate the pattern and surface any limitations of querying Cincinnati directly — learnings that will inform the CLM implementation.

## Context

- **Problem Statement**: Users cannot select an OCP version when creating a GCP HCP cluster, and there is no mechanism to trigger upgrades. The controller hardcodes `quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64`, requiring a Helm chart update and redeployment for every version change.
- **CLM context**: This is temporary and scoped to CLS — not a complete upgrade system. In CLM, upgrade edges will come from the HostedCluster CR's `status.version.availableUpdates`, which is populated by HyperShift and may filter edges beyond what Cincinnati provides.
- **Constraints**:
  - CLS Backend and Controller will be retired soon — changes must be minimal and pragmatic
  - GCP HCP is behind `TechPreviewNoUpgrade` feature gate, so Cincinnati channels may have limited content for newer versions
  - No additional infrastructure (caches, databases) should be introduced
  - The backend should not contain image resolution logic — that is controller responsibility
  - The cluster spec is user-owned — the backend stores user intent but does not mutate it internally (e.g., no writing resolved images back into the spec)
- **Assumptions**:
  - The Cincinnati API (`https://api.openshift.com/api/upgrades_info/v1/graph`) remains stable and publicly accessible
  - GCP HCP targets OCP 4.22+ as the minimum supported version; older versions are not relevant for this platform
  - Setting `spec.channel` on the HostedCluster will cause CVO to query Cincinnati and populate `status.version.availableUpdates` — this has not been validated on GCP HCP clusters yet and should be tested early
  - The `release.version` field requires a full version (e.g., `4.22.0-ec.4`), validated by a pattern in the API schema (`^4\.22\..+$`). Adding a new minor version means updating the pattern. Versions not found in Cincinnati fail at the controller level.

## Out of Scope

This decision covers introducing Cincinnati as the version source for initial cluster creation. A complete upgrade system is not in scope:

- Automatic upgrade policies (when and how hosted clusters upgrade to new z-streams)
- Upgrade scheduling or orchestration
- Cluster-specific upgrade status and available upgrade listing (ROSA has `rosa list upgrades` and `rosa describe upgrade` — can be added later)
- Version skew validation (e.g., NodePool-to-HostedCluster version compatibility — HyperShift enforces this)
- Versions listing endpoint (supported versions are defined in the API schema)

## Alternatives Considered

1. **Controller-driven Cincinnati resolution (chosen)**: A purpose-specific controller queries Cincinnati to resolve the ``release.version`` to a release image and reports it as a status condition. The backend only stores the user's intent. No image resolution logic in the backend.

2. **Hardcoded release image (current approach)**: The controller Helm template hardcodes a single release image (`quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64`). All clusters get the same version. Changing it requires updating the Helm chart and redeploying the controller.

3. **Cincinnati proxy in CLS Backend**: Backend queries Cincinnati and resolves version-to-image at cluster creation time. Rejected because image resolution is controller responsibility, and the backend should not contain this logic.

4. **Curated ClusterImageSets (ROSA pattern)**: ROSA uses a curated ClusterImageSets repository consumed via uhc-clusters-service, adding an intermediary between Cincinnati and the managed service that can filter versions, disable specific upgrade edges, and control which images are offered per environment.

## Decision Rationale

* **Justification**: Alternative 1 (controller-driven resolution) keeps the backend simple (stores user intent only), follows the CLS controller pattern of purpose-specific controllers, and places image resolution logic where it belongs — in the controller layer. Cincinnati is the canonical upstream source for OCP release versions and upgrade paths.

* **Comparison**:
  - Alternative 2 (hardcoded image) was rejected because it requires manual updates for every new OCP release — the same problem as the current hardcoded image.
  - Alternative 3 (backend resolution) was rejected because the backend should not contain image resolution logic. The backend's role is to store user intent and validate it against the API schema.
  - Alternative 4 (ClusterImageSets) provides curation on top of Cincinnati (used by ROSA) but is more complexity than needed at this stage. Cincinnati directly is simpler and good enough to start. Whether additional curation is needed will be evaluated as part of the CLM implementation.

## Environment-Specific Version Strategy

A key benefit of channel group support is enabling different version strategies per environment:

| Environment | Channel Group | Purpose |
|-------------|--------------|---------|
| **E2E tests** | `candidate` | Earliest access to new builds, including release candidates — catches regressions before GA |
| **Integration** | `fast` | GA releases only, available days after release — validates without RC noise |
| **Stage** (future) | `stable` | Fully soaked GA releases — mirrors production version policy |
| **Production** (future) | `stable` | Proven releases only, weeks after GA |

Each environment is progressively more conservative. E2E tests use `candidate` to catch issues at the earliest opportunity, including release candidates before they GA. Integration uses `fast` to validate with GA releases that haven't completed the full soak period. Stage and production use `stable`, where releases have been proven across the `fast` channel with no significant regressions.

**Current state (as of 2026-04-02):** OCP 4.22 is pre-GA. Only `candidate-4.22` has content (Engineering Candidates: `4.22.0-ec.0` through `4.22.0-ec.4`). `fast-4.22` and `stable-4.22` are empty. Until 4.22 GA, all environments must use channel group `candidate`. The environment-specific channel strategy takes effect once 4.22 reaches GA and propagates through `fast` and `stable`.

The channel group is persisted in the cluster spec so upgrades use the same channel group as creation. Each environment's automation (CI jobs, ArgoCD, scripts) passes the appropriate `--channel-group` flag when creating clusters.

## Consequences

### Positive

* Users can select a specific OCP version when creating clusters (e.g., `4.22.0-ec.4`)
* Version changes no longer require controller Helm chart updates and redeployments
* Backend remains simple — stores user intent, validates format via API schema pattern
* Image resolution is async and controller-driven — keeps the API fast and follows the CLS purpose-specific controller pattern
* Channel group defaults to `stable` (same as ROSA CLI `--channel-group`), persisted for upgrade consistency
* Different environments (e2e, integration, stage) can target different channel groups

### Negative

* No significant negative consequences identified

## Cross-Cutting Concerns

### Reliability:

* **Resiliency**: If Cincinnati is unavailable, the version resolution controller reports a failure condition. The cluster creation is not blocked — it waits for the condition to be resolved.

### Security:

* The Cincinnati API is public and requires no authentication. No credentials are stored or transmitted.

### Performance:

* Cincinnati resolution happens asynchronously in the controller, not in the request path. No added latency to cluster creation API calls.

### Operability:

* No new infrastructure to maintain (no cache, no database tables, no background jobs beyond the new controller)
* Supported versions are managed via the API schema pattern (`^4\.22\..+$`) — adding a new minor version is a pattern update
* Version availability within a supported minor is determined by what Red Hat publishes to Cincinnati channels

## References

* [Cincinnati update service (OpenShift docs)](https://docs.openshift.com/container-platform/latest/updating/understanding_updates/intro-to-updates.html)
* [Cincinnati API endpoint](https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-4.22&arch=amd64)
* [Cincinnati source code (github.com/openshift/cincinnati)](https://github.com/openshift/cincinnati)
* [ROSA CLI documentation](https://docs.openshift.com/rosa/cli_reference/rosa_cli/rosa-manage-objects-cli.html)