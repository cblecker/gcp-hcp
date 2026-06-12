# Implementation Plan: Firestore-Backed Database Transport

**Status**: Draft
**Jira**: [GCP-813](https://redhat.atlassian.net/browse/GCP-813)
**Last Updated**: 2026-06-05

---

## Context

CLM currently uses Maestro as transport between region clusters and management clusters. Maestro introduces operational complexity (API server, DB, Pub/Sub plumbing, agent per MC). ARO-HCP replaced Maestro with CosmosDB + kube-applier on Azure. This plan implements the GCP equivalent using Cloud Firestore — one database per MC, hosted in the MC's GCP project.

Design decision: [`design-decisions/datastore-transport.md`](../design-decisions/datastore-transport.md). Study: [`studies/datastore-transport.md`](../studies/datastore-transport.md). Epic defined with story breakdown and acceptance criteria.

## Architecture Overview

```text
CLM (region cluster)                    MC (management cluster)
┌─────────────────────┐                 ┌──────────────────────┐
│ CLM                 │                 │ Agent (cmd/agent)    │
│   ↓ Pub/Sub (specs) │                 │   ↑ specs-db         │
│ Adapter             │                 │   │ listener          │
│   ├─ write specs-db ─┼── Firestore ──→ │   │                  │
│   │                  │   (per-MC)     │   ↓ K8s SSA          │
│   └─ read status-db ←┼── Firestore ──← │   write status-db   │
│   ↓ Pub/Sub (status) │                 └──────────────────────┘
│ CLM                  │
└──────────────────────┘
```

**Key patterns:**
- Adapter remains event-driven: CLM events trigger processing, adapter reads status from Firestore on-demand during event execution (same pattern as Maestro gRPC reads today)
- Agent uses Firestore real-time listener on `specs-db` for low-latency spec delivery
- Two Firestore databases per MC: `specs` (adapter writes, agent reads) and `status` (agent writes, adapter reads) — IAM-enforced directional isolation
- Shared Go interfaces compatible with ARO kube-applier's `KubeApplierDBClient` for eventual convergence

## Package Layout

All code in CLM adapter repo ([hyperfleet-adapter](https://github.com/openshift/hyperfleet-adapter)), well-separated for future extraction:

```text
pkg/
└── dbclient/                        # Shared interfaces (future standalone module)
    ├── interfaces.go                # DB client interfaces (KubeApplierDBClient-compatible)
    ├── types.go                     # Desire types, document model
    └── firestore/                   # Firestore implementation
        ├── client.go                # FirestoreDBClient factory + connection
        ├── crud.go                  # CRUD operations (Get, Create, Replace, Delete)
        ├── list.go                  # List/query operations (GlobalLister)
        ├── listener.go              # Real-time snapshot listener (for agent)
        └── client_test.go           # Emulator-based integration tests

internal/
├── firestoretransport/              # Adapter-side: TransportClient wrapper
│   ├── client.go                    # Implements transportclient.TransportClient
│   ├── context.go                   # FirestoreTransportContext type
│   └── client_test.go
│
├── agent/                           # Agent-side internals
│   ├── controller/                  # Main controller loop (informer-style)
│   │   └── controller.go
│   ├── specwatcher/                 # Firestore listener → work queue
│   │   └── watcher.go
│   ├── applier/                     # K8s server-side apply + delete + read
│   │   └── applier.go
│   └── statuswriter/                # Write status docs to Firestore
│       └── writer.go

cmd/
├── adapter/main.go                  # Extend: add Firestore client wiring
└── agent/main.go                    # NEW: agent entry point
```

## Document Model

Aligned with ARO kube-applier desire types. Reusing the same schema enables shared Go interfaces and future code convergence with ARO-HCP.

**Document IDs**: Deterministic UUID v5 assigned by the CLM adapter. The UUID is derived from a fixed namespace UUID and the string `{taskKey}/{group}/{version}/{resource}/{namespace}/{name}`, where `taskKey` is a stable identifier from the adapter task config (exact field name TBD — must be immutable across restarts/redeployments). This gives:
- **Natural idempotency**: crash-and-retry computes the same ID, so create returns conflict if already exists
- **Multiple desires per K8s object**: different `taskKey` values (e.g., different field managers) produce different UUIDs for the same resource
- **Deterministic**: same as ARO's approach (`uuid.NewSHA1(namespaceUUID, []byte(input))`)

### Database Layout

Each MC has **two Firestore databases** in its GCP project, providing IAM-enforced directional isolation:

| Database | CLM Adapter | Agent |
|----------|-------------|-------|
| `specs` | `datastore.user` (read/write) | `datastore.viewer` (read only) |
| `status` | `datastore.viewer` (read only) | `datastore.user` (read/write) |

This ensures the agent cannot write specs and the adapter cannot write status — enforced by IAM, not application code.

> **Simplification option**: A single database per MC with application-enforced directional isolation is possible. This halves the number of databases and connections but loses IAM-enforced direction. Both the adapter and agent would have `datastore.user` on the single database, and write direction would be enforced in code only.

**Adapter side**: manages 2N Firestore connections for N MCs (one `specs` + one `status` per MC, cross-project). The `DBClient` factory returns a `(specsDB, statusDB)` pair per MC.

**Agent side**: 2 local database connections in its own project (simple, always the same pair).

### Spec Documents (`specs` database)

Each desire document contains a typed `Spec` following ARO's structure:

```json
{
  "spec": {
    "desireType": "apply|delete|read",
    "targetItem": {
      "group": "hypershift.openshift.io",
      "version": "v1beta1",
      "resource": "hostedclusters",
      "namespace": "clusters",
      "name": "my-cluster"
    },
    "kubeContent": "<raw K8s resource JSON (RawExtension)>",
    "generation": 42
  },
  "updatedAt": "2026-06-05T..."
}
```

For cluster-scoped resources, `namespace` is empty.

### Status Documents (`status` database)

Status contains `[]metav1.Condition` reporting operation outcome. ApplyDesire and DeleteDesire status reports whether the operation succeeded (`Successful`, `Degraded`) — it does NOT include the full K8s object status. To retrieve the actual K8s resource (e.g., HostedCluster `.status`), the adapter creates a ReadDesire and reads `kubeContent` from the ReadDesire status.

**ApplyDesire / DeleteDesire status:**
```json
{
  "status": {
    "conditions": [
      {"type": "Successful", "status": "True", "reason": "Applied", ...},
      {"type": "Degraded", "status": "False", ...}
    ]
  },
  "lastAppliedAt": "2026-06-05T...",
  "lastObservedAt": "2026-06-05T..."
}
```

**ReadDesire status** — includes the full K8s object:
```json
{
  "status": {
    "conditions": [
      {"type": "Successful", "status": "True", ...}
    ],
    "kubeContent": "<full K8s resource JSON including .status (RawExtension)>"
  },
  "lastObservedAt": "2026-06-05T..."
}
```

---

## Phase 1: Shared DB Client Library (`pkg/dbclient/`)

**Goal**: Foundation layer — interfaces and Firestore implementation that both adapter and agent build on.

### Deliverables

1. **Go interfaces** (`pkg/dbclient/interfaces.go`):
   - `MCDatabasePair` — per-MC handle wrapping a `specsDB` and `statusDB` (factory: project ID → pair)
   - `DesireCRUD[T Desire]` — typed Get/Create/Replace/Delete for desire documents
   - `DesireLister[T Desire]` — paginated list with continuation cursor
   - `StatusWriter` — optimistic read-mutate-replace for status documents
   - `SpecListener` — real-time listener interface (Start/Stop, delivers change events)
   - Interface signatures compatible with ARO's `KubeApplierDBClient` stack

2. **Desire types** (`pkg/dbclient/types.go`):
   - `ApplyDesire`, `DeleteDesire`, `ReadDesire` structs with typed `Spec` containing `TargetItem` (ResourceReference), `KubeContent` (`*RawExtension`), matching ARO's schema
   - `DesireStatus` struct with `[]metav1.Condition` for operation outcome
   - `ChangeEvent` struct for listener callbacks (type: added/modified/removed, document)

3. **Firestore implementation** (`pkg/dbclient/firestore/`):
   - `NewMCDatabasePair(ctx, projectID)` — creates clients for `specs` and `status` databases using Application Default Credentials (Workload Identity)
   - CRUD: map to Firestore `Set`/`Get`/`Delete` with `precondition: {updateTime}` for optimistic concurrency
   - List: `Collection.Documents()` with `StartAfter` cursor for pagination
   - Listener: `Collection.Snapshots.Listen()` wrapper with reconnection handling
   - `Close()` for cleanup of both database connections

4. **Integration tests** with Firestore emulator:
   - CRUD round-trip
   - Optimistic concurrency conflict
   - Listener receives changes
   - Pagination

### Dependencies
- `cloud.google.com/go/firestore` Go SDK
- Firestore emulator for tests (testcontainers or CI-managed)

### Acceptance Criteria
- [ ] Interfaces compile without importing ARO code but are structurally compatible
- [ ] Firestore CRUD works against emulator
- [ ] Optimistic concurrency rejects stale writes
- [ ] Listener delivers add/modify/remove events (target: p99 < 1s, internal goal, not SLA)
- [ ] `go doc` shows clean, documented API surface

---

## Phase 2: CLM Adapter — Firestore TransportClient

**Goal**: Enable adapter to write specs to Firestore and read status from Firestore, replacing Maestro gRPC calls.

### Deliverables

1. **TransportClient implementation** (`internal/firestoretransport/client.go`):
   - `ApplyResource(manifest, opts, target)`: decode manifest → extract GVR/namespace/name/generation → compute UUID v5 → write `ApplyDesire` to `specs-db` (or `DeleteDesire` if lifecycle.delete triggered)
   - `GetResource(gvk, namespace, name, target)`: write `ReadDesire` to `specs-db`, read `ReadDesireStatus.kubeContent` from `status-db`, return as `*unstructured.Unstructured`
   - `GetApplyStatus(gvk, namespace, name, target)`: read operator conditions from `status-db` (Successful/Degraded — does NOT include K8s object status)
   - `DiscoverResources(gvk, discovery, target)`: query `status-db`, filter by discovery criteria, return as `UnstructuredList`
   - `DeleteResource(gvk, namespace, name, opts, target)`: write `DeleteDesire` to `specs-db`

2. **Transport context** (`internal/firestoretransport/context.go`):
   - `FirestoreTransportContext` struct: `ProjectID` (equivalent of Maestro's `ConsumerName`; database names are fixed: `specs`, `status`)
   - Resolved from task config params (e.g., `target_project: '{{ .mcProjectId }}'`)

3. **Config integration** (`internal/configloader/`):
   - Add `FirestoreConfig` to `ClientsConfig`: minimal — Firestore uses ADC, so no credentials config
   - `transport.client: firestore` in task config resource blocks

4. **Client wiring** (`cmd/adapter/main.go`):
   - `createFirestoreClient()` factory
   - Update `createTransportClient()` dispatch

5. **Example task config** (`charts/examples/firestore/adapter-task-config.yaml`):
   - Equivalent to current Maestro example, using Firestore transport

### Key Design Decisions

- **Status semantics by desire type**: ApplyDesire/DeleteDesire status contains only operator conditions (`Successful`, `Degraded`) — it answers "did the operation succeed?", not "what does the K8s object look like?". ReadDesire status includes the full K8s resource in `kubeContent`. The adapter's post-action CEL expressions (`resources.{name}.status.conditions[...]`) evaluate against the operator conditions for apply/delete, or against the full object status via ReadDesire when needed.

- **Generation handling**: Reuse `manifest.CompareGenerations()` — extract generation from rendered manifest, compare with generation stored in Firestore spec document

### Acceptance Criteria
- [ ] Adapter writes spec document to Firestore emulator during event processing
- [ ] Adapter reads status document from Firestore and post-action CEL expressions evaluate correctly
- [ ] Generation comparison skips apply when generation unchanged
- [ ] Delete lifecycle works (writes DeleteDesire or deletes spec doc)
- [ ] Existing Maestro and K8s transport paths unaffected

---

## Phase 3: Agent (`cmd/agent/`)

**Goal**: Lightweight component on each MC that watches Firestore specs, applies to K8s, writes status back.

### Deliverables

1. **Agent binary** (`cmd/agent/main.go`):
   - Cobra CLI: `agent serve` (main loop), `agent version`
   - Config: Firestore project/database (from env or config file), K8s in-cluster config
   - Graceful shutdown, health/readiness probes
   - OpenTelemetry tracing (optional, matching adapter pattern)

2. **Spec watcher** (`internal/agent/specwatcher/`):
   - Uses `pkg/dbclient.SpecListener` on `/specs` collection
   - Converts change events to work queue items
   - Handles reconnection (Firestore listener auto-reconnects)

3. **K8s applier** (`internal/agent/applier/`):
   - `ApplyDesire` → server-side apply (K8s SSA) using the raw manifest
   - `DeleteDesire` → delete resource, poll until absent (matches kube-applier pattern)
   - `ReadDesire` → read resource, return full status
   - Error handling: transient errors → requeue with backoff, permanent errors → write error status

4. **Status writer** (`internal/agent/statuswriter/`):
   - After apply/delete/read: write status document to `/status/{key}`
   - Include: conditions, full `.status` from K8s resource, generation, timestamps
   - Optimistic concurrency via `precondition: {updateTime}`

5. **Controller loop** (`internal/agent/controller/`):
   - Informer-style: work queue + worker goroutines
   - On spec change: dequeue → apply → write status
   - Periodic resync: list all specs, ensure all applied (catch missed events)
   - Rate limiting + exponential backoff for retries

6. **Helm chart** (`charts/agent/` or extend `charts/hyperfleet-adapter/`):
   - Deployment on MC
   - ServiceAccount with Workload Identity annotation
   - RBAC: cluster-admin or scoped roles for SSA
   - Health/readiness probes

### Key Design Decisions

- **Work queue vs. direct processing**: Use client-go's `workqueue.RateLimitingInterface` for deduplication, rate limiting, and retry. Listener events enqueue keys; workers dequeue and process. Standard K8s controller pattern.

- **Leader election**: Required for HA. Running multiple agent replicas without leader election causes SSA field manager conflicts. Use `client-go/tools/leaderelection` with a Lease object. Only the leader processes the work queue; standby replicas maintain Firestore listener connections for fast failover.

- **Cooldown gates**: Firestore listeners fire events for all writes — including the agent's own status writes. Without a cooldown gate, this creates a hot loop: write status → listener fires → re-reconcile → write same status → repeat. Port the cooldown gate pattern from ARO's `controllerutils/cooldown.go`: after writing status, suppress reconciliation for that key for a short window (e.g., 5s).

- **Resync interval**: Default 5 minutes. Full spec list + diff against applied state. Catches: missed listener events, agent restarts, Firestore reconnections.

- **Concurrency**: Start with configurable worker count, default 3. Work queue provides deduplication and rate limiting. If specific ordering constraints emerge for related resources, reduce to 1 via configuration.

### Acceptance Criteria
- [ ] Agent applies K8s resource within 5s of spec write to Firestore
- [ ] Agent writes status back to Firestore after successful apply
- [ ] Agent handles ApplyDesire, DeleteDesire, ReadDesire
- [ ] Agent reconnects after Firestore listener disconnection
- [ ] Periodic resync catches specs missed during disconnection
- [ ] Agent cannot access another MC's Firestore database (IAM test)
- [ ] Health probe reports unhealthy when Firestore connection lost
- [ ] Leader election prevents concurrent SSA from multiple replicas
- [ ] Cooldown gate prevents hot loop from agent's own status writes

---

## Phase 4: Integration & E2E Testing

**Goal**: Validate full flow end-to-end.

### Test Scenarios

1. **Adapter + Firestore emulator** (no agent):
   - CLM event → adapter writes spec → verify Firestore document
   - Pre-populate status doc → adapter reads it → post-action reports correctly

2. **Agent + Firestore emulator + envtest** (no adapter):
   - Write spec doc → agent applies to envtest K8s → verify status doc

3. **Full E2E** (adapter + agent + Firestore emulator + envtest):
   - CLM event → adapter writes spec → agent applies → agent writes status → adapter reads status → reports status to CLM via Pub/Sub
   - Latency: spec write to status availability < 60s (acceptance criterion from epic)

4. **CLM resync**: adapter writes all specs to empty Firestore DB on startup equivalent (regular sync model)

5. **IAM isolation** (requires real Firestore, not emulator):
   - Agent A's service account cannot read/write Agent B's database

### Test Infrastructure
- Firestore emulator via testcontainers (matches existing integration test pattern with `make test-integration`)
- K8s envtest for agent's K8s operations
- Mock CLM API for adapter post-action verification

---

## Phase 5: Documentation

- Runbook: DB provisioning, IAM troubleshooting, agent recovery, backup/restore
- Architecture diagram: full flow with Firestore
- Configuration reference: adapter Firestore config, agent config
- Example task configs for common CLM resource types

---

## Resolved Questions

1. **Status notification to CLM**: The Maestro client does NOT register for gRPC event streams or status notifications. It uses on-demand reads only (Get/List) during CLM event processing. No background watchers, no streams, no subscriptions. **For Firestore: same pattern — adapter reads status from Firestore `/status` on-demand during event processing. No Firestore listener needed on the adapter side. No Cloud Function bridge needed.**

2. **Document bundling**: ARO kube-applier uses a **flat model — one desire per K8s resource**. Each `ApplyDesire`/`DeleteDesire`/`ReadDesire` contains exactly one resource's spec + GVR identifier. **For Firestore: one spec doc and one status doc per K8s resource. No bundling.** This replaces the Maestro `nested_discoveries` pattern — multi-resource status aggregation uses collection queries with label/selector filtering instead.

3. **Document ID format**: Deterministic UUID v5 assigned by CLM adapter: `uuid.NewSHA1(namespaceUUID, "{taskKey}/{GVR}/{namespace}/{name}")`. **Deterministic IDs give natural idempotency** (crash-and-retry computes same ID) and **allow multiple desires per K8s object** (different `taskKey` → different UUID). Matches ARO's approach. GVR/namespace/name also stored as document fields for queries. Cluster-scoped resources use empty `namespace`.

4. **Large document handling**: Firestore does not compress documents automatically. Typical K8s manifests (5-50KB) are well under the 1 MiB limit — **store raw JSON directly, no compression needed**. For occasional large manifests (200-500KB), gzip into a `bytes` field with indexing disabled. GCS reference pattern is overkill at this scale. Validate manifest size before write; log warning for >500KB; reject >900KB.

## Open Questions

1. **Firestore emulator in CI**: Current CI uses Konflux/Tekton. Need testcontainers image for Firestore emulator, or sidecar container in pipeline. Check if `google/cloud-sdk` image includes emulator.

3. **Unified applier vision**: At what point do we propose sharing code with ARO kube-applier? Options:
   - (a) After Phase 1 — share `pkg/dbclient/` interfaces, propose Firestore backend to kube-applier
   - (b) After Phase 3 — share agent controller logic, propose unified applier
   - (c) Never merge code, just maintain interface compatibility
   - Recommend **(a)** — share interfaces early, validate compatibility, defer code merging

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Firestore emulator not available in Konflux CI | Blocks integration tests | Use testcontainers (existing pattern), verify emulator image availability early |
| ARO kube-applier interfaces change | Breaks compatibility | Pin to specific ARO commit for interface reference, don't import directly |
| Firestore IAM cache lag (5 min) | Agent can't access DB immediately after provisioning | Retry with backoff on auth errors during agent startup |
| 1 MiB document size limit | Large K8s resources fail | Validate manifest size before write, log warning for >500KB, reject >900KB. Gzip compress into `bytes` field if needed. |

---

## Phase Dependencies

```text
Phase 1 (pkg/dbclient)
  ├──→ Phase 2 (Adapter TransportClient)
  └──→ Phase 3 (Agent)
            └──→ Phase 4 (Integration & E2E)
                      └──→ Phase 5 (Documentation)
```

Phases 2 and 3 can run in parallel after Phase 1 completes.
