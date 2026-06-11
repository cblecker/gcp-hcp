# Studies

This directory contains research and analysis documents that explore technical approaches, evaluate alternatives, and provide foundational knowledge for architectural decisions.

## Purpose

**Studies are pre-decision research documents** that:
- Investigate multiple solution approaches objectively
- Analyze trade-offs and constraints
- Document rejected alternatives with rationale
- Provide technical depth for team discussion
- Serve as basis for design decisions

## Relationship to Other Directories

```
studies/              ← Research phase: "What are our options?"
  │
  │ Team Discussion
  ▼
design-decisions/     ← Decision phase: "What did we choose and why?"
  │
  │ Implementation
  ▼
docs/                 ← Documentation phase: "How does it work?"
```

## Workflow

1. **Research**: Create study document analyzing a technical problem
2. **Discussion**: Team reviews study, asks questions, evaluates options
3. **Decision**: Team makes architectural decision based on study findings
4. **Document**: Create design decision document referencing the study
5. **Implement**: Build according to design decision
6. **Document**: Update user/operator docs with implementation details

## Study Document Structure

Studies should include:

- **Problem statement**: What problem are we solving?
- **Requirements**: What constraints must be satisfied?
- **Options**: What approaches are available? (objective analysis)
- **Trade-offs**: Pros and cons for each option
- **Rejected alternatives**: What didn't we choose and why?
- **Open questions**: What needs further investigation?
- **References**: Sources, related work, code references

**Studies should NOT**:
- Prescribe a single solution (present options objectively)
- Include implementation details (keep conceptual)
- Make decisions (that's for design-decisions/)

## Active Studies

### Datastore Transport: Replacing Maestro
**File**: `datastore-transport.md`
**Status**: Complete
**Topic**: Evaluating GCP-native datastore options (Firestore, Spanner, Cloud SQL, Bigtable, AlloyDB, GCS) to replace Maestro as transport between region and management clusters

### Federated Authentication from OpenShift/ROSA to GCP
**File**: `rosa-to-gcp-wif.md`
**Status**: Validated with working example
**Topic**: Setting up GCP Workload Identity Federation to trust an OpenShift/ROSA cluster's OIDC issuer, enabling pods to authenticate to GCP without static credentials

### WIF Service Account Key Management
**File**: `wif-sa-key-management.md`
**Status**: Research complete, ready for team discussion
**Topic**: Secure management of service account signing keys for Workload Identity Federation

## Naming Conventions

- Use kebab-case: `my-study-topic.md`
- Be descriptive: document should be discoverable by name
- Keep focused: one study per technical problem

## Related Directories

- `design-decisions/`: Final architectural decisions with rationale
- `docs/`: User and operator documentation
- `implementation-plans/`: Detailed implementation plans for features
- `PROJECTS/`: Jira story tracking and progress (different purpose than studies)
