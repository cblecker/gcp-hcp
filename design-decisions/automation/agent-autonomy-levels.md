# Define Agent Autonomy Levels for Remediation: Read-Only, Interactive, and Automated

***Scope***: GCP-HCP

**Date**: 2026-03-23

## Decision

Adopt a three-stage approach for agent-driven remediation, with increasing autonomy and decreasing human involvement:

1. **Read-only diagnosis agent** (current) — triggered by users or alerts, no destructive access, no customer data access
2. **Interactive recovery sessions** — conversational agent proposes destructive actions, CLI executes under the user's own PAM-granted identity
3. **Predefined automated remediation** — alert-triggered, dedicated SA executes well-known recovery procedures encoded in workflows, potentially out of PAM

Each stage can be implemented incrementally. All three coexist in production.

## Context

- **Problem Statement**: The diagnosis agent (`diagnose-agent`) investigates GKE cluster issues using read-only Cloud Workflows and produces root cause analysis in 2-5 minutes. However, the SRE must then manually translate the agent's recommendations into `gcloud workflows run` commands — losing the investigation context that informed *what* to fix with *which* parameters. We want the agent to drive remediation, not just diagnose. The question is: how much autonomy does the agent get, and how do we gate destructive actions?

- **Constraints**:
  - PAM (Privileged Access Manager) gates destructive workflows for human operators via IAM conditions + time-bounded grants with approval
  - PAM grants are per-principal and time-bounded (min 30 min). No single-use grants, no concurrency limits
  - Cloud Run cannot impersonate its caller — the service runs as its own SA, making per-user workflow execution impossible from the server side
  - Data sovereignty: production cluster data must stay within GCP (Vertex AI)
  - Prompt injection is a real threat when an LLM processes untrusted data (K8s logs, events, resource annotations) and has access to destructive tools

- **Assumptions**:
  - Trust in the AI agent's remediation judgment will increase over time as we observe its proposals
  - Well-known recovery procedures (etcd disk pressure, pod CrashLoopBackOff) can be encoded as deterministic or semi-deterministic workflows
  - SREs already have PAM entitlements for `roles/workflows.invoker` and use them for manual workflow invocation

## Alternatives Considered

1. **Incremental three-stage strategy (chosen)**: Match autonomy levels to trust levels. Stage 1 is the existing read-only agent. Stage 2 adds interactive recovery where the agent proposes and the CLI executes under user identity. Stage 3 adds automated remediation for well-known procedures via dedicated workflows. All three coexist.

2. **Agent-side PAM (agent SA requests PAM grants)**: The agent's shared SA requests PAM grants to execute destructive workflows server-side. Simpler architecture — no CLI changes needed.

3. **Dual-layer PAM (Cloud Run invocation + workflow PAM)**: A dedicated recovery Cloud Run service gated by PAM, which then invokes PAM-gated workflows. Two approval layers for defense-in-depth.

4. **Autonomous AI agent from day one**: Grant the agent SA direct destructive access with LLM-controlled safety gates (system prompts, tool restrictions). Skip the incremental trust-building stages.

5. **Manual-only remediation**: Keep the agent read-only permanently. SREs manually translate agent recommendations into workflow invocations using existing CLI tooling.

## Decision Rationale

* **Justification**: The three-stage approach matches increasing trust levels to increasing autonomy. Stage 1 is already deployed. Stage 2 reuses existing PAM infrastructure with zero new services. Stage 3 is architecturally separate from the AI agent, avoiding the "AI with destructive access" trust problem entirely by encoding well-known procedures as deterministic workflows.

* **Evidence**: During the architecture analysis, we evaluated 6 architecture options for agent-side PAM. The key findings were:
  - Granting PAM to a shared agent SA fails per-user isolation (PAM grants are per-principal — one user's grant benefits all concurrent users of the same SA)
  - Cloud Run cannot impersonate its caller, making server-side per-user execution impossible
  - The "agent proposes, CLI executes" model (Stage 2) is the only architecture that provides per-user isolation, user-identity audit trails, AND prompt injection defense simultaneously
  - Pre-approved playbooks (Stage 3) are a legitimate alternative to AI-autonomous execution, addressing a different use case with a simpler security model

* **Comparison**:
  - **Agent-side PAM** (alternative 2): Rejected — shared SA means one user's grant benefits all concurrent sessions. Also requires the SA to have a path to destructive access, which conflicts with keeping the agent SA read-only.
  - **Dual-layer PAM** (alternative 3): Viable for Stage 2 evolution but requires new infrastructure (Cloud Run service, PAM entitlement). Weaker audit trail (SA identity on workflows, not user identity). Double approval overhead during outages leads to approver fatigue and rubber-stamping.
  - **Autonomous AI agent** (alternative 4): Premature — no established trust basis for unconstrained LLM execution decisions. Prompt injection risk is unmitigated when the agent processes untrusted K8s data and has destructive access.
  - **Manual-only** (alternative 5): Safe but loses the investigation-to-remediation context that motivated this decision. SREs waste time re-deriving parameters the agent already identified.

## Consequences

### Positive

* Stage 2 reuses the existing PAM entitlement, approval group, and CLI — zero new GCP infrastructure
* Stage 2 provides the best audit trail of any option: workflow executions show the actual user identity
* Stage 2's "agent proposes, CLI executes" model defends against prompt injection at the architectural level (agent literally cannot execute destructive workflows)
* Stage 3 sidesteps the AI trust problem for well-known procedures — no unconstrained LLM in the execution path
* All three stages can be implemented and deployed incrementally without blocking each other
* The architecture supports evolution: Stage 2's confirmation gate is a mode flag that can be relaxed when trust is established

### Negative

* Stage 2 adds latency to multi-step recoveries (user confirms each action) — may cause approval fatigue for 5+ destructive actions per session
* Stage 2 requires CLI implementation work (conversational loop, PAM auto-withdrawal) or MCP server development
* Stage 2 depends on a human at a terminal — not suitable for fully automated off-hours remediation (Stage 3 addresses this)
* Stage 3 requires encoding recovery procedures in workflows — upfront effort per procedure, ongoing maintenance
* Stage 3's branching logic complexity is TBD — the boundary between "deterministic" and "needs AI" is unclear until we build the first procedures
* PAM approver availability at 3 AM affects Stage 2 (SRE waiting at terminal for PAM approval)

## Cross-Cutting Concerns

### Security:

* **Data access boundary**: The agent must not access customer data (etcd content, secrets, customer workload internals). Enforced via workflow-level namespace allow-lists, resource type restrictions, and agent-side data redaction.
* **Prompt injection**: Stage 1 has zero execution risk. Stage 2 has low risk (agent proposes, cannot execute). Stage 3 has no risk for deterministic branches, variable risk if AI-assisted classification is introduced.
* **Per-user isolation**: Stage 2 achieves this via user credentials (not shared SA). Stage 3 uses a dedicated SA — per-user isolation is not applicable (it's automated).

### Cost:

* Stage 2 multi-turn conversations replay Gemini context per turn. A 10-turn session costs ~5-10x a single diagnosis. Acceptable for an infrequent operational tool.
* Stage 2 via MCP + Gemini CLI: Vertex AI API costs per session. No Cloud Run hosting cost for the reasoning (handled by CLI).
* Stage 3 workflow executions: standard Cloud Workflows pricing, negligible.

### Operability:

* Stage 1: No operational change.
* Stage 2: SREs learn a new CLI subcommand (`wf-cli recover`) or use Gemini CLI with an MCP server. Same PAM flow they already use.
* Stage 3: New operational procedures for managing predefined recovery workflows. Requires testing, versioning, and runbook alignment.

---

**Related documents**:
- [PAM Workflow Gating design decision](pam-workflow-gating.md)
- [Cloud Workflows Automation Platform design decision](cloud-workflows-automation-platform.md)
- [GCP-320: Automated Remediation Platform Scaffolding](https://redhat.atlassian.net/browse/GCP-320)
- [GCP-511: Interactive Recovery Sessions](https://redhat.atlassian.net/browse/GCP-511)
- [GCP-481: Agent-Based PAM Grant Requests](https://redhat.atlassian.net/browse/GCP-481)
