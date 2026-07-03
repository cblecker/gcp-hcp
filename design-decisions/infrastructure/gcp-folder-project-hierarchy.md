# GCP Folder and Project Hierarchy

**Scope**: GCP-HCP

**Date**: 2025-10-08

## Decision

Implement an environment-first GCP folder hierarchy as illustrated here: [Miro board: HCP GCP Project Overview](https://miro.com/app/board/uXjVJywcZeM=/) 

## Context

**Problem Statement**: Need organizational structure for multiple environments, regions, and cluster types with clear isolation, policy inheritance, and cost tracking.

**Constraints**:

- Strict environment isolation required
- Must scale to multiple regions per environment
- Multiple management clusters per region
- Enable folder-level IAM and org policies

**Assumptions**:

- Different regions may have different compliance requirements
- Development environment for free-form experimentation

## Alternatives Considered

1. **Flat Project Structure**: All projects at organization root
   - No policy inheritance or environment isolation

2. **Region-First Hierarchy**: Top folders by region, then environments
   - Breaks environment isolation
   - Complicates promotion workflow

3. **Environment-First Hierarchy** (Chosen): Top folders by environment, then regions
   - Natural promotion path (integration → stage → production)
   - Clear environment boundaries

## Decision Rationale

- **Justification**: Environment-first aligns with deployment workflow and provides natural isolation boundaries. Regional folders enable region-specific policies while maintaining environment cohesion.

- **Evidence**: GCP folder hierarchy supports IAM and Organization Policy inheritance. Google Cloud best practices recommend organizing by environment for isolation and compliance.

- **Comparison**: Flat structure lacks isolation. Region-first complicates promotion. Environment-first matches our deployment model.

## Consequences

### Positive

- Clear environment isolation with hierarchical policy inheritance
- Natural cost attribution (environment → region → project)
- Matches deployment promotion workflow
- Development isolated from production path

### Negative

- Folder structure must exist before projects
- Policy inheritance can be complex to debug

## Cross-Cutting Concerns

### Security

- Folder-level IAM enforces strict boundaries:
  - Workloads in one environment cannot access other environments
  - Workloads in one region cannot access other regions
- Service perimeters per environment
- Development separate from production

### Cost

- Labels: environment/region/cluster-type
- Billing reports by folder hierarchy

### Operability

- Bootstrap folders before projects
- Terraform state mirrors hierarchy

## Implementation Notes

### Naming Conventions

**Folders**: `GCP HCP <Environment>/`, `<region>/`

**Projects**:

- Global: `<env>-global` (e.g., `int-global`)
- Regional: `<env>-<region>` (e.g., `int-us-central1`)
- Management: `<env>-<region>-mgmt-<random>`

### Regional Project

Merges infrastructure and regional cluster into single project. May split in future if operational needs require separation.

### Labels

```yaml
environment: integration|stage|production|development
region: us-central1|europe-west1|...
cluster-type: global|regional|management
managed-by: terraform
```
