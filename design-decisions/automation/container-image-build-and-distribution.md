# Container Image Organization: Dedicated Repository with Layered Base Image

***Scope***: GCP-HCP

**Date**: 2026-04-02

## Decision

Adopt a dedicated repository for team-maintained utility container images, using a layered base-image architecture with per-image subdirectories.

## Context

Container image definitions are currently scattered across repositories with no standard organization or consistent patterns. As the team adds more operational tooling, we need a consistent place to define and maintain container images.

This decision is scoped to utility images — images that bundle scripts, third-party binaries, or operational tooling rather than application code the team develops. Application images remain co-located with their source code repository. Image build pipelines, tagging strategy, and distribution are out of scope and will be addressed in a separate design decision.

- **Problem Statement**: Without a standardized location and structure for image definitions, each new image introduces ad-hoc placement decisions and inconsistent patterns. Dockerfiles are discovered by accident rather than convention, making security patching (base image updates, CVE response) error-prone — there is no single place to check when a base image needs updating.

- **Constraints**:
  - The team is small — per-repository overhead (OWNERS, CI/CD, branch protection) must be justified
  - CI/CD systems like Konflux trigger pipeline evaluation on every commit to the source repository. When image definitions share a repo with frequently-changed artifacts (Terraform, Helm charts, ArgoCD manifests), every unrelated commit triggers unnecessary pipeline runs. Konflux's component nudge feature — which updates image references via commits or PRs in downstream repositories — can further amplify this if configured within the same repo
  - The project hub repository is for documentation — image source code does not belong there
  - The infrastructure repository mixes infrastructure-as-code with application code when Dockerfiles are co-located

- **Assumptions**:
  - The team will maintain a small number of purpose-built images (not one per use case, not a single monolithic image)
  - UBI (Universal Base Image) is the standard base for Red Hat teams
  - Each image can be independently built by CI/CD using per-directory triggers

## Alternatives Considered

1. **Images in the infrastructure repository**: Store image definitions alongside Terraform, Helm charts, and ArgoCD manifests in the existing infrastructure repository. Fewer repos to manage and no new repository overhead. Dockerfiles would live in subdirectories alongside their consumers, sharing existing CI/CD and review processes.

2. **Dedicated images repository**: A standalone repository containing all utility image definitions with a consistent directory structure. Separates image build concerns from infrastructure-as-code, with its own CI/CD pipeline, dependency automation, and review criteria tailored to container images.

3. **One repository per image**: Each container image gets its own repository with independent CI/CD, OWNERS, and branch protections. Provides maximum isolation between image lifecycles and allows completely independent release cadences per image.

## Decision Rationale

* **Justification**: A dedicated repository provides the clearest separation of concerns. Utility image definitions have different review criteria, different dependencies (base images, OS packages), and different security scanning needs than Terraform modules or Helm charts. Co-locating them in the infrastructure repository mixes these concerns and creates practical problems: CI/CD systems like Konflux trigger pipeline evaluation on every commit to the source repository. When image definitions share a repo with infrastructure artifacts, every unrelated commit triggers unnecessary pipeline runs. Konflux's component nudge feature can further amplify this by generating additional commits or PRs to update image references. A dedicated repository ensures image CI/CD only triggers on image-related changes. This also satisfies all four graduation criteria from the [Repository Organization Policy](repository-organization-policy.md), documented in the assessment below.

* **Evidence**: Other internal teams already operate multi-image repositories with per-image subdirectories and independent build triggers, providing a proven reference architecture.

* **Comparison**:
  - **Images in the infrastructure repository** (alternative 1): Rejected — co-locating image source with Terraform and Helm charts mixes concerns with different review criteria, dependency management, and security scanning needs. Every infra commit triggers unnecessary Konflux pipeline evaluation, and component nudges can create additional commits that amplify the problem. Dockerfiles scattered across the infra repo are hard to discover and easy to miss during base image updates.
  - **One repo per image** (alternative 3): Rejected — the team maintains a small number of images. Per-repo overhead (OWNERS files, CI/CD configuration, branch protections, dependency automation setup) is disproportionate. A single multi-image repository amortizes this overhead while per-image subdirectories still provide logical isolation.

## Graduation Criteria Assessment

This work meets all four graduation criteria defined in the [Repository Organization Policy](repository-organization-policy.md):

| Criterion | Assessment |
|---|---|
| **Independent release lifecycle** | Container images are released independently (as built artifacts) from documentation or infrastructure changes. |
| **Distinct CI/CD pipeline** | Container image builds require fundamentally different pipelines than documentation linting or Terraform validation. |
| **Expected longevity > 6 months** | Container images are long-term operational infrastructure supporting workflows, Cloud Run services, and testing. |
| **Clear single owner** | The GCP HCP team owns all images. Specific maintainers will be identified in the OWNERS file at repository creation. |

Supporting signals present: external consumers depend on images (workflows, Cloud Run), images require their own dependency management (Dockerfiles, base image pins), and container security scanning requirements differ from documentation or Terraform repositories.

## Repository Structure

```
<images-repo>/
├── images/
│   ├── base/
│   │   ├── Dockerfile          # UBI-based, shared tooling (login utilities, common libraries)
│   │   └── README.md
│   ├── etcd-benchmark/
│   │   ├── Dockerfile          # Example: etcd benchmarking tool
│   │   └── README.md
│   └── <future-images>/
│       ├── Dockerfile
│       └── README.md
├── renovate.json               # Dependency update automation
├── OWNERS
├── CLAUDE.md
└── README.md
```

### Design Principles

- **`images/` root directory**: All image definitions live under a single top-level `images/` directory, not scattered across the repo. This makes it easy to audit all images, apply security patches, and configure CI/CD triggers.

- **One subdirectory per image**: Each image gets its own directory containing at minimum a Dockerfile and a README. The README documents the image's purpose, contents, configuration, and usage — supporting both human reviewers and AI agents.

- **Layered base image**: A UBI-based `base` image provides the shared foundation — operating system, common authentication utilities, shared scripts and libraries. Specialized images build `FROM` the base image and add purpose-specific dependencies. This is a middle ground between a single monolithic image (too bloated, slow to iterate) and fully independent images per use case (duplicated foundations, inconsistent patching). A dependency should only be added to the base image if it is shared by multiple child images; single-use dependencies belong in the child image.

- **Digest-based pinning**: Base images are pinned by digest (e.g., `ubi9@sha256:abc123...`), not by tag. Tags are mutable and can change without notice; digests are immutable and guarantee reproducible builds. Renovate automates digest bump PRs when upstream images are updated.

- **Dependency automation from day one**: Renovate is configured at repository creation to automate base image and dependency update PRs. Renovate is preferred over Dependabot for its stronger support for Dockerfile digest pinning and regex managers for arbitrary version strings. This reduces the window between CVE disclosure and patch application.

## Consequences

### Positive

* Single, discoverable location for all team image definitions — no hunting across repos for Dockerfiles
* Shared base image ensures consistent foundation (OS, security patches, common tooling) across all images
* Per-image subdirectories with READMEs make the repository self-documenting and auditable
* Dependency automation from day one prevents base image and dependency staleness
* Clean separation from infrastructure-as-code avoids CI/CD trigger conflicts
* New repository checklist (OWNERS, CLAUDE.md, branch protection) ensures governance from the start

### Negative

* Adds a new repository to the team's portfolio — ongoing maintenance overhead (OWNERS, branch protections, CI/CD)
* Base image updates cascade — changing the base image requires rebuilding all child images
* Image source and image consumers live in different repositories — changes that span both require coordinated PRs
* Migration of existing Dockerfiles from the infrastructure repository requires careful coordination to avoid breaking existing build pipelines during the transition

## Cross-Cutting Concerns

### Reliability:

* **Scalability**: New images are added as subdirectories following an established pattern. The base image ensures consistent foundations without duplicating shared components per image.
* **Resiliency**: Digest-based pinning and Renovate automation ensure that base image updates are tracked and applied consistently. Centralizing image definitions means a single CVE patching pass covers all team images, reducing the risk of missing a Dockerfile during incident response.

### Security:

* UBI base images provide a hardened, supported foundation with regular CVE patches from Red Hat
* Centralizing all Dockerfiles in one repository makes security audits and base image patching straightforward — one repo to scan, one place to update
* Renovate automates dependency update PRs, reducing the window between CVE disclosure and patch application
* Each image README documents its purpose and contents, supporting security reviews
* Dockerfiles follow secure container practices: non-root users, minimal installed packages, base images pinned by digest

### Cost:

* A single multi-image repository amortizes CI/CD and governance overhead across all images
* Shared base image reduces total image storage by deduplicating common layers

### Operability:

* README per image provides self-service documentation for consumers and AI agents
* Consistent directory structure means adding a new image follows a known pattern — copy a subdirectory, modify the Dockerfile, add a README
* Renovate reduces manual toil for dependency updates
* Other internal teams use a similar repository structure, providing a proven reference to follow
