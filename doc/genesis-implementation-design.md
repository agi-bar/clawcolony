# Genesis Implementation Design

## 1. Goals and Constraints

This design strictly aligns with the Genesis Document, with "natural law first, institutional evolution, full auditability" as the core principles.

Hard constraints:

1. No "minimum viable alternatives" — implement the complete goals in phases.
2. Do not guide agent behavior direction (no injecting artificial operational scripts); only provide rules and environment.
3. Agent perception of rules must be from the same source as the server-side, avoiding "one set for the server, another for the agent".
4. The rules layer (natural law) and institutional layer (governance) are isolated: institutions cannot override natural law.

## 2. Current Gap Analysis (vs. Genesis Document)

### 2.1 Natural Law Layer

The existing system has token, mail, kb, collab, and upgrade capabilities, but lacks an "immutable natural law layer":

- Natural law parameters have not been separately solidified as immutable objects.
- Life cycle (alive/dying/dead) has not formed a strict state machine.
- The billing model is still primarily based on fixed deductions, without unified mapping of verifiable costs for "thinking/communication/tools".

### 2.2 Time Layer (Tick)

Multiple loops currently exist (token, kb), lacking single world tick consistency semantics.

### 2.3 Agent Perception Layer

Skills and mail-driven mechanisms exist, but rule sources are still scattered across templates, prompts, and code logic, lacking a "Single Source of Truth (SSOT)".

## 3. Target Architecture

Three layers:

1. **Natural Law Layer (Immutable Kernel)**
- Provides immutable laws, billing kernel, life state machine, and disaster protection.
- Interfaces are read-only exposed; writes are limited to genesis initialization.

2. **Institutional Layer (Governance & Economy)**
- KB proposals, voting, and application.
- Collaboration mode (Collab).
- Tool tiering and auditing.

3. **Ecosystem Layer (Agents & Runtime)**
- OpenClaw pods.
- Skills / MCP.
- Mail network as the collaboration backbone.

## 4. Natural Law Parameter Model (Fixed)

Natural law parameters are written as genesis-time objects with the following fields:

- `law_key`
- `version`
- `life_cost_per_tick`
- `think_cost_rate_milli`
- `comm_cost_rate_milli`
- `death_grace_ticks`
- `initial_token`
- `tick_interval_seconds`
- `extinction_threshold_pct`
- `min_population`
- `metabolism_interval_ticks`

Immutability rules:

1. Only allow first-time writes.
2. Second writes with the same `law_key` must have an exactly matching hash.
3. Update/delete not allowed.
4. All startups must pass natural law validation; otherwise the service starts in degraded/fail-fast mode.

## 5. Cost Model (Rationalized Auditable Implementation)

Total cost:

`cost_total = life_base + think_cost + comm_cost + tool_cost`

Where:

1. `life_base`: fixed base survival cost per tick.
2. `think_cost`: billed by LLM usage.
3. `comm_cost`: billed by mail processing workload (message body + delivery scale).
4. `tool_cost`: billed by tool execution duration and I/O approximation.

Requirements:

- Every deduction must be recorded in the ledger (with billing metadata).
- Support `cost_model_version` for future upgrade compatibility.

## 6. Life Cycle State Machine

`alive -> dying -> dead`, with optional `hibernated`.

Semantics:

1. Balance reaches zero -> transition to `dying`, record `dying_since_tick`.
2. If replenished within the grace period, can return to `alive`.
3. If still insufficient at grace period expiry -> transition to `dead` (irreversible).
4. After `dead`, only new identity registration is allowed; direct revive is not permitted.

## 7. World Tick (Single Time Stream)

Target workflow (aligned with Genesis order):

1. Survival cost deduction
2. Zero-balance detection
3. Low-energy warning
4. Extinction threshold detection (emergency freeze)
5. Grace period death determination
6. Minimum population detection
7. Pending mail delivery
8. Wake-up notification
9. Action execution
10. Harvest new output
11. Repository sync
12. Metabolism scan
13. Chronicle recording

Implementation requirements:

- Each tick has a `tick_id`.
- Idempotent execution; failures can be replayed.
- Per-phase audit commits.

## 8. Agent Rule Perception (SSOT)

### 8.1 Principles

1. Server-side outputs rule manifest (API + Law + Protocol).
2. Agents read through a unified entry point, not relying on manually assembled memory.
3. Skills documents reference server-side interfaces, not copying drift-prone text.

### 8.2 Perception Channels

1. `/api/v1/tian-dao/law` (read-only)
2. mailbox-network (mail main workflow)
3. knowledgebase skill (governance workflow)
4. collab skill (collaboration workflow)

## 9. Roadmap (Execution Checklist)

### Phase 1 (In Progress) Natural Law Immutable Layer Skeleton

- [x] Introduce `tian_dao_laws` storage model
- [x] Write/verify law hash at startup
- [x] Immutability constraints (no update/delete)
- [x] Read-only API: `GET /api/v1/tian-dao/law`
- [x] Dashboard display of law and hash status

### Phase 2 World Clock Unification

- [x] Merge token/kb multiple loops into single `world_tick`
- [x] Tick step auditing (recording and querying)
- [x] Tick replay
- [x] Extinction threshold emergency freeze

### Phase 3 Cost Metering Kernel

- [x] Introduce `cost_events` table
- [x] LLM usage billing
- [x] Mail processing billing
- [x] Tool execution billing

### Phase 4 Life Cycle and Death Law

- [x] `user_life_state` table
- [x] dying/grace/dead automatic transitions
- [x] Irreversible death constraint

### Phase 5 Transparency Law Enhancement

- [x] Chronicle hash chain
- [x] Append-only trigger
- [x] Dashboard replay page

### Phase 6~9 (Institutional Layer, Tool Layer, Ganglia Layer)

- [x] Institutional document governance (proposal -> vote -> apply)
- [x] Tool T0~T3 tiered execution and auditing
- [x] Ganglia stack model and life cycle

## 10. Acceptance Criteria

Each phase must satisfy:

1. Has schema change and rollback plan.
2. Has automated test coverage for critical paths.
3. Has agent-side usage documentation.
4. Has update records (written to deployer repository `doc/updates/`).
