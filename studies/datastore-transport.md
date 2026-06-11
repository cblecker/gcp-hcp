# Datastore Transport: Replacing Maestro with a Database-Backed Transport Layer

**Date**: 2026-06-05
**Status**: Complete
**Authors**: Patrick Martin

## Problem Statement

The Cluster Lifecycle Manager (CLM) on region clusters currently uses Maestro as a transport layer to deliver Kubernetes resource specs (e.g., HostedCluster, NodePool) to management clusters and receive status updates back. Maestro acts as an intermediary: CLM writes specs to the Maestro API, Maestro stores them in a database and publishes messages on Google Pub/Sub, and a Maestro agent on each management cluster (MC) subscribes to its own topic, syncs resources, and reports status back via Pub/Sub.

Other teams using Maestro have encountered operational issues and replaced it with a simpler database-backed transport. Notably, the ARO-HCP team on Azure replaced Maestro with CosmosDB containers — one container per management cluster — where the CLM equivalent writes specs and reads status, and a per-MC agent (kube-applier) reads specs, applies them, and writes status back.

This study evaluates GCP-native datastore options for an equivalent replacement.

## Requirements

### Hard Requirements

1. **Per-MC access segregation**: Each MC agent must only read/write its own data. A compromised or misconfigured MC agent must not access another MC's resources. Isolation must be IAM-enforced, not application-enforced only.
2. **Regional data residency**: All data must remain within a single GCP region. If the region fails, access is lost (acceptable), but data must be recoverable when the region comes back.
3. **Cross-project access via Workload Identity only**: Region clusters (project A) and management clusters (project B, C, ...) run in separate GCP projects. The datastore must support cross-project IAM via Workload Identity Federation. **No static credentials, service account keys, or secrets may be created or distributed.** All authentication must flow through GKE Workload Identity → GCP IAM bindings. This eliminates any datastore option that requires distributing database passwords, connection strings with embedded credentials, or API keys.
4. **Bidirectional data flow**: CLM writes specs and reads status. MC agents read specs and write status. Ideally, read/write scoping can be enforced per direction (e.g., MC agent reads specs but cannot modify them).
5. **Recovery via CLM resync**: In case of data loss (e.g., MC project deletion/rebuild), CLM must be able to fully resync all resource specs to a new datastore. CLM is the source of truth for desired state — the datastore is a transport, not a primary store. The adapter's regular sync model inherently supports this: on startup against an empty datastore, it will write all current specs.

### Desirable Properties

- **Change notification**: Agents should be notified of spec changes rather than polling.
- **Low operational complexity**: Fully managed, minimal infrastructure to operate.
- **Cost efficiency**: The data volume is small (K8s YAML specs, typically 5-50 KB per resource).
- **ARO kube-applier compatibility**: If the datastore is document-oriented (like CosmosDB), sharing a common Go interface with the ARO kube-applier would reduce duplication.
- **Scalability**: Support hundreds of MCs with thousands of resources each.

## Transport Store Requirements

Requirements derived from the CLM adapter codebase in [hyperfleet-adapter](https://github.com/openshift/hyperfleet-adapter) (`internal/transportclient/`, `internal/executor/`, `internal/manifest/`) and the ARO kube-applier interface study.

### CLM Adapter Operations

| Operation | Pattern | Details |
|---|---|---|
| **Write spec** | Single document by key | One resource at a time. Key = GVR + namespace + name. Content = full rendered K8s manifest JSON + generation annotation. Optimistic concurrency via generation comparison (read-compare-write). |
| **Read status** | Single document by key | Exact lookup by GVR + namespace + name. Returns full K8s resource structure including `.status.conditions[]`. Adapter filters via CEL expressions client-side. |
| **Discover resources** | List all + client-side filter | Store lists all documents; adapter filters client-side via `manifest.MatchesDiscoveryCriteria()` (exact name match or K8s label selector). No server-side query capability needed. Select resource with highest generation from results. |
| **Delete spec** | Single document by key | Idempotent — NotFound is success. |

### MC Agent Operations

| Operation | Pattern | Details |
|---|---|---|
| **Watch specs** | Real-time change listener | Push-based notification of add/modify/remove events. Sub-second latency desired. Automatic reconnection on disconnection. |
| **Read spec** | Single document by key | Fetch one spec at a time after listener notification. |
| **List all specs** | Full collection scan | Periodic resync (every ~5 minutes). No filtering — agent processes all specs in its database. |
| **Write status** | Single document by key | One status doc per K8s resource. Optimistic concurrency (reject stale writes). Contains: conditions, full `.status` JSON, generation, timestamps. |

### What Is NOT Required

- **Field-based queries** (e.g., "find resources where generation > 5") — adapter reads by exact key or lists all, then filters client-side with CEL
- **Cross-collection joins or aggregations** — each resource is independent
- **Transactions spanning multiple documents** — each write is atomic and independent
- **Partial document reads (projections)** — full document is always fetched
- **Server-side filtering or queries** — discovery uses label selectors but filtering is done client-side after listing all documents (verified in Maestro client: `ListManifestWorks` with empty selector, then `MatchesDiscoveryCriteria` locally)

### Minimum Viable Feature Set

1. Single-document write/read/delete by key
2. List all documents (no server-side filtering needed — client filters after fetch)
3. Optimistic concurrency (generation or ETag comparison)
4. Real-time change listener (for agent spec watching)
5. IAM-enforced per-MC isolation
6. Workload Identity Federation (no static credentials)
7. Regional persistence with automatic zone failover

## Options Evaluated

### Option 1: Cloud Firestore (Native Mode) — Per-MC Database

Firestore supports multiple named databases per project (GA since 2023). Each MC gets a dedicated Firestore database with IAM scoped to that database.

**Segregation model**: One Firestore database per MC, hosted in the MC's own GCP project. The MC agent accesses Firestore locally (no cross-project IAM needed for the agent). CLM on the region cluster is granted cross-project access to each MC's Firestore database via Workload Identity.

**DB hosting rationale**: Hosting the Firestore database in the MC project rather than the region project has several advantages:
- DB lifecycle is naturally tied to MC lifecycle — created during MC provisioning, deleted on MC teardown. No orphaned databases in the region project.
- MC agent needs no cross-project IAM — it accesses Firestore in its own project. Only CLM needs cross-project bindings (one per MC).
- Billing is naturally attributed to the MC project.
- The 100-database-per-project limit becomes irrelevant — each MC project holds exactly one Firestore database.
- In case of disaster (MC project deletion/rebuild), CLM resyncs all specs to the new database. CLM is the source of truth for desired state; status data is transient (mirrors live K8s state) and will be re-reported by the agent once it starts.

**Change notification**: Native real-time listeners via the Go SDK (`snapshots.Listen`). Push-based, automatic reconnection, sub-second latency. No additional infrastructure required.

**Regional behavior**: Regional Firestore pins data to a single region. 99.99% SLA. Transparent zone failover. Automatic recovery after regional outage with no data loss.

**Cross-project IAM**: CLM's Workload Identity GSA on the region cluster is granted `roles/datastore.user` on each MC's Firestore database. No credentials to create, rotate, or distribute. Authentication is fully handled by GKE Workload Identity → IAM.

**Cost**: Per-operation pricing ($0.06/100k reads, $0.18/100k writes, $0.18/GB/month storage). With real-time listeners, reads are billed only on changes. Estimated $10-50/month for hundreds of MCs.

**Operational complexity**: Fully managed, serverless, no proxies or connection pooling. Database lifecycle (create/delete per MC) is a single `gcloud` or API call, integrated into MC provisioning.

**ARO compatibility**: Firestore is a document store like CosmosDB. The ARO kube-applier uses well-abstracted Go interfaces (`KubeApplierDBClient`, `GlobalLister[T]`, `Fetcher/Replacer`) that map naturally to Firestore operations.

**Limitations**:
- **IAM granularity stops at database level**. Within a single database, IAM cannot restrict access to specific collections or document paths — Security Rules only apply to mobile/web SDKs, not server-side Go SDK. This means spec vs. status read/write separation within one DB is application-enforced only.
- **5-minute IAM cache lag** for role changes.

### Option 2: Cloud Firestore — Single Shared Database

All MCs share one Firestore database with collection paths like `/{mc-id}/specs/{resource}` and `/{mc-id}/status/{resource}`.

**Segregation model**: Application-enforced only. The Go Admin SDK bypasses Firestore Security Rules entirely. IAM Conditions cannot scope to document paths — `resource.name` only resolves to `projects/P/databases/D`, not to document or collection paths. There is no `firestore.googleapis.com/Document` resource type in GCP IAM.

**Verdict**: Does not meet the hard requirement for IAM-enforced isolation. A misconfigured component can read/write any MC's data.

### Option 3: Cloud Spanner (Regional) — Single Database with FGAC

Spanner's Fine-Grained Access Control supports table and column-level grants via database roles. Row-level read isolation can be achieved using definer's-rights views with embedded `WHERE mc_id = ?` filters.

**Segregation model**: One database role per MC, IAM-bound to the MC's service account. Definer's-rights views enforce read isolation. However, Spanner views are read-only — writes (INSERT/UPDATE/DELETE) must go to base tables, where FGAC only enforces table-level grants, not row-level. Write isolation is application-enforced.

**Change notification**: Native change streams (GA). Near-real-time gRPC pull-based. Requires a streaming reader (sidecar or controller loop).

**Regional behavior**: 3-zone synchronous replication within one region. 99.99% SLA. Zero data loss on zone or region failure.

**Cost**: Node-based pricing. Minimum ~$65/month (100 PUs). Typical workload: $200-600/month. More expensive than Firestore for this data volume.

**Limitations**:
- **100 database roles per database** (hard limit). Hundreds of MCs exceed this ceiling.
- **Write isolation is partial** — reads are IAM-enforced via views, but writes require application-level enforcement.
- Not a document store — breaks ARO kube-applier interface compatibility.

### Option 4: Cloud SQL PostgreSQL (Enterprise Plus) — Schema-per-MC

Each MC gets a PostgreSQL schema within a single Cloud SQL instance. PostgreSQL GRANT enforces structural isolation — a role with no `USAGE ON SCHEMA mc_43` cannot query it.

**Segregation model**: Per-schema GRANT + IAM Database Authentication. Each MC's GCP service account maps to a PostgreSQL role with access only to its own schema. This is structurally enforced by the database engine — the strongest single-instance isolation of all options evaluated.

**Change notification**: PostgreSQL `LISTEN`/`NOTIFY` is unreliable (at-most-once, no persistence for missed messages). WAL-based CDC is complex. Practical pattern is hybrid: notify-as-hint + polling.

**Regional behavior**: Enterprise Plus HA: synchronous replication to a second zone. 99.99% SLA. Automatic failover ~60 seconds.

**Cross-project IAM**: Cloud SQL Auth Proxy in each MC pod, authenticating via Workload Identity. The proxy itself uses Workload Identity (no static credentials), but it requires a sidecar container in every agent pod. IAM Database Authentication maps the GCP service account to a PostgreSQL role — no database passwords are created or stored.

**Cost**: Instance-based. $100-300/month for Enterprise Plus HA with adequate sizing.

**Operational complexity**: Auth Proxy sidecars in every MC agent pod, connection pooling (PgBouncer or Cloud SQL Managed Connection Pooling), schema migrations applied to N schemas.

**Limitations**:
- Auth Proxy sidecar required in every pod — adds operational overhead even though no credentials are managed
- Schema migrations × N schemas — significant operational burden at scale
- No native per-schema backup — requires `pg_dump` per schema
- Connection pooling mandatory for hundreds of MCs
- Not a document store — breaks ARO kube-applier interface compatibility

### Option 5: Cloud Bigtable

**Segregation model**: Authorized views with row-prefix IAM. Per-MC isolation via row key prefix `mc-{id}/`.

**Change notification**: None natively. Requires building a CDC pipeline (Cloud Functions + Pub/Sub).

**Cost**: Node-based, minimum ~$470/month per node. 3-node HA cluster = ~$1400/month. Severely over-provisioned for this workload.

**Verdict**: Wrong tool. Highest cost, no change notification, most operational complexity. Designed for massive throughput that this use case does not need.

### Option 6: AlloyDB for PostgreSQL

Similar to Cloud SQL PostgreSQL but with higher entry cost (~$250/month minimum, no small tier) and more complex cross-project networking (Private Service Connect required, must be configured at cluster creation time).

**Verdict**: No advantages over Cloud SQL for this workload. Higher cost floor and operational complexity.

### Option 7: Cloud Storage (GCS) — Single Bucket in Region Project

A single regional GCS bucket in the region cluster's GCP project serves all MCs. Per-MC isolation is achieved via GCS Managed Folders — each MC gets a managed folder (`{mc-id}/`) with its own IAM policy. Change notification uses a shared Pub/Sub topic with per-MC subscription filters.

**Segregation model**: One GCS bucket in the region project with a Managed Folder per MC. GCS Managed Folders are first-class IAM resources — granting `roles/storage.objectUser` on managed folder `mc-123/` restricts a service account to objects under that prefix only. Each MC agent's GSA gets IAM access to its own managed folder; CLM's GSA gets access to all managed folders (or the bucket itself). Enforcement is server-side at the GCS API layer — no application-level trust required.

Unlike the Firestore per-DB model (where DB lifecycle is tied to MC project), the GCS bucket lives in the region project. MC data does not cascade on MC project deletion — CLM must explicitly clean up the MC's prefix. However, CLM is the source of truth for desired state and already tracks MC lifecycle, so this is a coordination concern, not a data loss risk.

**Change notification**: GCS has no native real-time listener API. Change notification uses GCS Pub/Sub notifications:

- One **notification configuration** on the bucket (not per MC), publishing `OBJECT_FINALIZE` and `OBJECT_DELETE` events to a shared Pub/Sub topic in the region project.
- One **Pub/Sub subscription per MC** with server-side attribute filter: `hasPrefix(attributes.objectId, "mc-123/")`. Each MC agent only receives messages for its own prefix.

This architecture uses 1 notification config (GCS limits to 10 per event type per bucket) and scales horizontally via subscriptions. Notification latency is typically seconds (no SLA). Delivery is at-least-once — duplicates possible, ordering not guaranteed. The notification payload contains object metadata only (name, size, generation) — the agent must perform a follow-up GET to read content.

Compared to Firestore native listeners: Firestore delivers document content inline with sub-second latency, requires zero additional infrastructure, and handles reconnection automatically. GCS notifications require a shared Pub/Sub topic + per-MC subscription, deliver metadata only (requiring follow-up reads), and offer no latency SLA.

**Regional behavior**: Regional GCS bucket pins data to a single region with synchronous zone replication. Monthly uptime SLA is 99.9% for single-region Standard storage (lower than Firestore's 99.99%). Automatic zone failover is transparent.

**Cross-project IAM**: CLM accesses the bucket in its own project (no cross-project IAM needed for CLM). Each MC agent's GSA is granted cross-project access to its managed folder. No credentials to create, rotate, or distribute — Workload Identity + ADC throughout.

**Cost**: Lowest of all options evaluated. Calculation for 200 MCs, 50 resources each, ~10 writes/day:

| Component | Calculation | Monthly Cost |
|---|---|---|
| **Storage** | 200 MCs x 50 resources x 25 KB avg x 2 (spec + status) = ~500 MB | $0.01 |
| **Write operations** (Class A) | 200 x 50 x 10 = 100k writes/day x 30 = 3M/month | $15.00 |
| **Read operations** (Class B) | ~2x writes (agent reads + CLM reads) = 6M/month | $2.40 |
| **Pub/Sub** | 3M notifications/month ingestion + delivery | $0.24 |
| **Pub/Sub follow-up reads** | 3M additional Class B ops | $1.20 |
| **Total** | | **~$19/month** |

**Operational complexity**: Medium. Simpler than bucket-per-MC, but Pub/Sub infrastructure adds overhead vs. Firestore.

Per-MC provisioning:
1. Create GCS Managed Folder (`mc-id/`) in the shared bucket.
2. Grant MC agent's GSA `roles/storage.objectUser` on the managed folder.
3. Create Pub/Sub subscription with `hasPrefix(attributes.objectId, "mc-id/")` filter.

Per-MC teardown:
1. Delete all objects under `mc-id/` prefix (`gcloud storage rm -r gs://bucket/mc-id/`).
2. Delete the Managed Folder.
3. Delete the Pub/Sub subscription.

**3 steps** per MC lifecycle event (vs. 2 for Firestore, 6+ for bucket-per-MC). One-time setup: create the shared bucket, Pub/Sub topic, and notification configuration.

**Object model**: K8s resources stored as individual JSON objects:

```
{mc-id}/specs/{resource-key}.json
{mc-id}/status/{resource-key}.json
```

The actual store requirements are simple (see Transport Store Requirements above): write/read/delete by key, list all under a prefix, client-side filtering. GCS handles all of these natively — no server-side query capability is needed since the adapter lists all documents and filters client-side.

**Optimistic concurrency**: GCS provides `ifGenerationMatch` preconditions on writes. The `generation` number changes on every overwrite. Write with `ifGenerationMatch` set to the previously read generation; stale writes fail with HTTP `412 Precondition Failed`. Functionally equivalent to Firestore `precondition: {updateTime}` and CosmosDB ETags.

**Limitations**:
- **No native real-time listeners**: Requires Pub/Sub infrastructure (shared topic + per-MC subscriptions). Adds one service to provision and monitor vs. Firestore's zero-dependency listeners.
- **Notification is metadata-only**: Agent must perform follow-up GET after each notification, doubling read operations and adding latency vs. Firestore (which delivers content inline).
- **MC data lifecycle not tied to MC project**: Bucket lives in region project; MC teardown requires explicit prefix cleanup by CLM. Firestore per-DB in MC project cascades naturally with MC project deletion.
- **Lower SLA**: 99.9% (vs. Firestore 99.99%).
- **Not a document store**: Different abstraction level from ARO kube-applier interfaces. `KubeApplierDBClient` expects typed CRUD on documents; GCS provides raw object GET/PUT. A GCS backend implementation would need an adaptation layer mapping object operations to the CRUD interface.
- **Shared bucket blast radius**: A misconfigured bucket-level IAM grant (e.g., accidental project-level `storage.objectViewer`) would expose all MCs' data. Firestore per-DB isolation has no equivalent risk since each DB is a separate resource.

**Verdict**: GCS is a viable alternative to Firestore. The actual store requirements (key-based CRUD, list-all, optimistic concurrency) are simple enough that GCS handles them natively. The single-bucket + Managed Folders + shared Pub/Sub architecture achieves reasonable operational overhead (3 steps per MC). Main trade-offs vs. Firestore: Pub/Sub dependency for change notification (1 extra service to manage), metadata-only notifications requiring follow-up reads, lower SLA, and weaker isolation guarantees (shared bucket). Recommended as fallback if Firestore is ruled out for other reasons.

## ARO kube-applier Compatibility Analysis

The ARO-HCP kube-applier agent was studied for interface compatibility. Key findings:

**Architecture**: Three desire types — `ApplyDesire` (server-side apply), `DeleteDesire` (delete + poll until absent), `ReadDesire` (watch + mirror status). One CosmosDB container per MC. Polling-based (30s relist, no change feed used). ETag-based optimistic concurrency for status writes.

**Interface stack**: Well-abstracted behind Go interfaces:
- `KubeApplierDBClient` — per-MC handle
- `KubeApplierApplyDesireCRUD` — typed Get/Create/Replace/Delete
- `GlobalLister[T]` — paginated list with continuation
- `desirestatuswriter.Fetcher + Replacer` — optimistic read-mutate-replace

**CosmosDB → Firestore mapping**:

| CosmosDB Concept | Firestore Equivalent | Bridging Effort |
|---|---|---|
| Container per MC | Database per MC | Direct mapping |
| Document ID (ARM path) | Document ID (string) | None |
| `_etag` + If-Match | `precondition: { updateTime }` | Low |
| SQL cross-partition query | Collection group query | Low |
| TransactionalBatch | Firestore transaction | Medium |
| Continuation token | `startAfter` cursor | Low |

**Verdict**: High compatibility. A `NewFirestoreKubeApplierDBClient(...)` factory could implement the same interfaces alongside the existing Cosmos implementation. Controller code would not change.

## Comparison Summary

| Criterion | Firestore (per-MC DB) | Spanner (FGAC) | Cloud SQL (schema-per-MC) | Bigtable | AlloyDB | GCS (single bucket) |
|---|---|---|---|---|---|---|
| IAM-enforced isolation | Yes (per-DB) | Reads only | Yes (structural) | Yes (authorized views) | Yes (structural) | Yes (Managed Folders) |
| Write isolation | Per-DB IAM | App-enforced | Structural GRANT | App-enforced | Structural GRANT | Per-folder IAM |
| Change notification | Native push listeners | Change streams (pull) | LISTEN/NOTIFY (unreliable) | None | LISTEN/NOTIFY (unreliable) | Via shared Pub/Sub topic + per-MC subscriptions |
| Per-MC ceiling | None (1 DB per MC project) | 100 roles/DB | No hard limit | No hard limit | No hard limit | No hard limit |
| Cost (estimated/month) | $10-50 | $200-600 | $100-300 | $1400+ | $500-800 | ~$19 (incl. Pub/Sub) |
| Ops complexity | Low (serverless) | Medium | Medium-High | High | High | Medium (Managed Folders + shared Pub/Sub) |
| Workload Identity (no credentials) | Native — direct SDK + ADC | Native — direct SDK + ADC | Via Auth Proxy sidecar | Native — direct SDK + ADC | Via Auth Proxy + PSC | Native — direct SDK + ADC |
| ARO interface compat | Yes (CosmosDB analog) | No | No | No | No | Adaptation layer needed (actual ops are simple CRUD) |
| SLA (regional) | 99.99% | 99.99% | 99.99% | 99.99% (multi-cluster) | 99.99% | 99.9% |
| Backup granularity | Per-DB native | Per-DB; per-MC via PITR | Per-schema pg_dump | Per-table | Per-schema pg_dump | Per-object versioning |
| MC lifecycle coupling | DB in MC project — cascades | Shared DB | Shared instance | Shared cluster | Shared instance | Bucket in region project — explicit cleanup |

## Operational Overhead Comparison

The primary selection criterion is **minimal operational overhead** — the total burden of provisioning, operating, and decommissioning per-MC transport infrastructure across the fleet lifecycle.

### Summary Ranking

| Rank | Option | Overhead | Key Driver |
|---|---|---|---|
| 1 | **Firestore (per-MC DB)** | **Lowest** | Serverless, 2-step MC lifecycle, zero sidecars |
| 2 | **GCS (single bucket)** | **Low-Medium** | 3-step MC lifecycle, but Pub/Sub dependency + metadata-only notifications |
| 3 | **Spanner (FGAC)** | **Medium** | Role provisioning hits hard ceiling at 100 MCs |
| 4 | **Bigtable** | **Medium-High** | Node management, no change notification requires custom CDC |
| 5 | **Cloud SQL (schema-per-MC)** | **High** | Auth Proxy sidecars, connection pooling, schema migrations x N |
| 6 | **AlloyDB** | **Highest** | Cloud SQL overhead + PSC networking + higher cost floor |

### Detailed Assessment

#### 1. Firestore (per-MC DB) — Lowest Overhead

| Dimension | Assessment |
|---|---|
| **Per-MC provisioning** | Create one Firestore database in MC project (`gcloud firestore databases create`). Grant CLM's GSA `roles/datastore.user` on the database. **2 steps.** |
| **Per-MC teardown** | Delete the Firestore database (cascades all documents). Revoke IAM binding. **2 steps.** |
| **Infrastructure dependencies** | None. No sidecars, no proxies, no Pub/Sub topics, no Cloud Functions. Direct SDK calls over HTTPS with ADC. |
| **Ongoing maintenance** | None. Serverless — no patching, no upgrades, no capacity planning, no connection pools. Google manages everything. |
| **Credential management** | Workload Identity + ADC only. No database passwords, no connection strings, no API keys, no Auth Proxy. |
| **Day-2 operations** | Backup: native per-DB PITR and scheduled exports (built-in). Monitoring: Cloud Monitoring integration with pre-built Firestore dashboards. Alerting: standard GCP alerting on Firestore metrics. All included, nothing to build. |
| **Failure recovery** | Database is fully managed with automatic zone failover. If MC project is destroyed, CLM resyncs all specs to a new database on startup — the adapter's regular sync model handles this natively. No manual intervention. |
| **Scaling wall** | None within the architecture. Each MC has its own database in its own project. No shared resource limits, no connection pool exhaustion, no role count ceilings. 1000 MCs = 1000 independent databases, each in its own project. |

#### 2. GCS (single bucket) — Low-Medium Overhead

| Dimension | Assessment |
|---|---|
| **Per-MC provisioning** | Create Managed Folder in shared bucket, grant MC agent's GSA IAM on the folder, create Pub/Sub subscription with prefix filter. **3 steps.** One-time setup: create bucket + Pub/Sub topic + notification config. |
| **Per-MC teardown** | Delete objects under MC prefix, delete Managed Folder, delete Pub/Sub subscription. **3 steps.** MC data does not cascade with MC project deletion — explicit cleanup required by CLM. |
| **Infrastructure dependencies** | Shared Pub/Sub topic + one subscription per MC. Agent runs a Pub/Sub subscriber (pull or streaming pull) + follow-up GET per notification. No sidecar proxy needed. |
| **Ongoing maintenance** | Low for storage. Pub/Sub subscriptions require monitoring for backlog buildup and expiration (inactive subscriptions expire after 31 days by default). Notification config health: GCS auto-deletes after 7 days of delivery failures — must monitor. |
| **Credential management** | Workload Identity + ADC only. No static credentials. |
| **Day-2 operations** | Backup: object versioning (built-in). Monitoring: two services (GCS + Pub/Sub) — no unified dashboard. Alerting: configure separately for GCS errors and Pub/Sub delivery failures. |
| **Failure recovery** | GCS operations are simple. If notification config is lost, all MC agents lose change notifications simultaneously until re-created. Shared bucket = shared failure domain for notification config. |
| **Scaling wall** | Pub/Sub subscriptions scale horizontally (no hard limit). Managed Folders scale well. Main concern: shared Pub/Sub topic throughput at very high MC counts, and monitoring hundreds of subscriptions for health. |

#### 3. Spanner (FGAC) — Medium Overhead

| Dimension | Assessment |
|---|---|
| **Per-MC provisioning** | Create database role, create definer's-rights view, grant role to MC's GSA via IAM. **3 steps**, but requires SQL DDL execution. |
| **Per-MC teardown** | Drop view, drop role, revoke IAM binding. **3 steps.** |
| **Infrastructure dependencies** | Change stream reader (sidecar or controller loop) for near-real-time notifications. |
| **Ongoing maintenance** | Processing unit capacity monitoring and scaling. Change stream reader health monitoring. Schema migrations applied once (shared tables). |
| **Credential management** | Workload Identity + ADC only. No static credentials. |
| **Day-2 operations** | Backup: native per-DB PITR (built-in). Monitoring: Cloud Monitoring with Spanner dashboards. Well-instrumented. |
| **Failure recovery** | Fully managed, automatic zone failover, zero data loss. Robust. |
| **Scaling wall** | **Hard ceiling at 100 database roles per database.** This is a non-negotiable Spanner limit. At 100 MCs, must shard to a second database — introducing cross-database coordination, routing logic, and doubled operational burden. |

#### 4. Bigtable — Medium-High Overhead

| Dimension | Assessment |
|---|---|
| **Per-MC provisioning** | Create authorized view with row-prefix filter, grant IAM on the view. **2 steps**, but Bigtable authorized views are a newer feature with less operational maturity. |
| **Per-MC teardown** | Delete authorized view, revoke IAM. **2 steps.** |
| **Infrastructure dependencies** | No native change notification. Must build a CDC pipeline: Cloud Functions or Dataflow job to detect changes + Pub/Sub to notify agents. Significant custom infrastructure. |
| **Ongoing maintenance** | Node count management (minimum 1 node, HA requires 3+). Node scaling based on storage and throughput. CDC pipeline maintenance. |
| **Credential management** | Workload Identity + ADC only. No static credentials. |
| **Day-2 operations** | Backup: managed backups (built-in). Monitoring: Cloud Monitoring with Bigtable dashboards. CDC pipeline monitoring is entirely custom-built. |
| **Failure recovery** | Bigtable itself is robust (multi-zone replication). CDC pipeline failures require manual diagnosis and restart. |
| **Scaling wall** | Bigtable scales well for data, but the custom CDC pipeline becomes a single point of failure and operational bottleneck. Node cost ($470+/month per node) grows linearly regardless of actual load. |

#### 5. Cloud SQL (schema-per-MC) — High Overhead

| Dimension | Assessment |
|---|---|
| **Per-MC provisioning** | Create PostgreSQL schema, create PostgreSQL role, grant schema permissions, bind IAM DB authentication to GCP service account, configure Auth Proxy sidecar in agent pod. **5 steps**, requiring both SQL DDL and Kubernetes manifest changes. |
| **Per-MC teardown** | Drop schema (cascade), drop role, remove IAM DB auth binding, remove Auth Proxy sidecar. **4 steps.** Schema drop cascades objects, but role and IAM cleanup must be explicit. |
| **Infrastructure dependencies** | **Auth Proxy sidecar** in every MC agent pod (mandatory for Workload Identity IAM DB authentication). **Connection pooler** (PgBouncer or Cloud SQL Managed Connection Pooling) required at scale. LISTEN/NOTIFY is unreliable — practical change notification requires a hybrid notify-hint + polling pattern or WAL-based CDC. |
| **Ongoing maintenance** | PostgreSQL version upgrades (managed but require maintenance windows). Connection pool tuning as MC count grows. Schema migrations must be applied to N schemas — a migration affecting 500 schemas must execute 500 DDL statements, with partial failure handling. Auth Proxy version upgrades across all MC agent pods. |
| **Credential management** | No database passwords (IAM DB authentication), but Auth Proxy sidecar adds an authentication hop that must be configured correctly in every pod. Misconfigured sidecar = silent connectivity failure. |
| **Day-2 operations** | Backup: instance-level automated backups (built-in), but no per-schema granularity — restoring one MC's data requires `pg_dump`/`pg_restore` per schema. Monitoring: Cloud SQL dashboards + custom per-schema monitoring. Alerting: must build custom alerts for connection pool saturation, per-schema size, replication lag. |
| **Failure recovery** | Automatic HA failover (~60 seconds). However, connection pool drain/reconnect after failover requires careful tuning. Auth Proxy pods may need restart. Schema-level corruption requires per-schema restore from `pg_dump`. |
| **Scaling wall** | At 200+ MCs: connection pool saturation (each MC agent + CLM = 2+ connections per schema, 400+ active connections). Auth Proxy resource consumption across 200+ pods. Schema migration execution time grows linearly. At 500+ MCs: likely need to shard to multiple Cloud SQL instances, doubling all operational overhead. |

#### 6. AlloyDB — Highest Overhead

| Dimension | Assessment |
|---|---|
| **Per-MC provisioning** | Same as Cloud SQL (schema + role + IAM + Auth Proxy), plus Private Service Connect must be pre-configured at cluster creation time for cross-project access. **5+ steps.** |
| **Per-MC teardown** | Same as Cloud SQL. **4+ steps.** |
| **Infrastructure dependencies** | Everything Cloud SQL requires (Auth Proxy, connection pooler, hybrid change notification), plus **Private Service Connect** networking between projects. PSC endpoints must be provisioned per consumer project — adds network infrastructure per MC project. |
| **Ongoing maintenance** | All Cloud SQL maintenance, plus AlloyDB-specific capacity management (vCPU-based pricing, read pool sizing). PSC endpoint health monitoring. |
| **Credential management** | Same as Cloud SQL — Auth Proxy sidecar required. |
| **Day-2 operations** | Same as Cloud SQL, with higher cost floor (~$250/month minimum, no small tier). Monitoring requires familiarity with AlloyDB-specific metrics (different from Cloud SQL). |
| **Failure recovery** | Same class as Cloud SQL, plus PSC networking layer as additional failure domain. |
| **Scaling wall** | Same as Cloud SQL, plus PSC endpoints per MC project add network infrastructure scaling concern. Higher cost floor makes multi-instance sharding more expensive. |

### Key Takeaway

Firestore per-MC DB has the **lowest operational overhead**: 2-step MC lifecycle, zero infrastructure dependencies (no sidecars, no proxies, no Pub/Sub topics), and native real-time listeners that deliver content inline.

GCS single-bucket is a **viable alternative** with 3-step MC lifecycle and simple actual requirements (the store only needs key-based CRUD + list-all + optimistic concurrency — no server-side queries). The main trade-off is the Pub/Sub dependency for change notification: one extra service to provision/monitor, metadata-only notifications requiring follow-up reads, and a shared notification config as a single failure domain.

All other options introduce significantly more operational overhead — sidecars, connection poolers, CDC pipelines, or hard scaling ceilings — that compound at fleet scale.

## Open Questions

1. **Spec/status write separation within one Firestore DB**: Since IAM cannot scope to collection paths, should we use two databases per MC (one for specs, one for status) or accept application-enforced directional isolation?
2. **Real-time listeners vs. polling**: kube-applier currently polls at 30s intervals. Should the GCP equivalent use Firestore's native listeners for lower latency, or maintain polling for consistency with the ARO pattern?
3. **Firestore document size limits**: Firestore documents are capped at 1 MiB. Are any K8s resources (e.g., large ConfigMaps referenced by HostedCluster) likely to exceed this?

## Resolved Questions

1. **~~Firestore 100 DB/project limit~~**: Resolved by hosting the Firestore database in each MC's own project (1 DB per MC project). The per-project limit is no longer a concern.
2. **DB hosting location**: Resolved — Firestore DB lives in the MC project, not the region project. MC agent accesses locally; CLM uses cross-project IAM. Recovery from MC project loss is handled by CLM resync (adapter's regular sync model writes all specs on startup against an empty datastore).

## Related Documents

- Design decision: [`design-decisions/datastore-transport.md`](../design-decisions/datastore-transport.md)
- Implementation plan: [`implementation-plans/gcp-813-datastore-transport.md`](../implementation-plans/gcp-813-datastore-transport.md)
- Superseded decision: [`design-decisions/rc-mc-transport-layer.md`](../design-decisions/rc-mc-transport-layer.md)

## References

- [ARO-HCP kube-applier](https://github.com/Azure/ARO-HCP/tree/main/kube-applier)
- [Firestore IAM documentation](https://cloud.google.com/firestore/native/docs/security/iam)
- [Firestore real-time listeners](https://cloud.google.com/firestore/native/docs/query-data/listen)
- [Firestore multiple databases](https://cloud.google.com/blog/products/databases/firestore-multiple-databases-is-now-generally-available)
- [Spanner FGAC overview](https://cloud.google.com/spanner/docs/fgac-about)
- [Cloud SQL IAM authentication](https://cloud.google.com/sql/docs/postgres/iam-authentication)
- [GCP IAM Conditions resource attributes](https://cloud.google.com/iam/docs/conditions-resource-attributes)
- [GCS Managed Folders](https://cloud.google.com/storage/docs/managed-folders)
- [GCS Pub/Sub Notifications](https://cloud.google.com/storage/docs/pubsub-notifications)
- [GCS Quotas and Limits](https://cloud.google.com/storage/quotas)
- [Pub/Sub subscription message filters](https://cloud.google.com/pubsub/docs/subscription-message-filter)
