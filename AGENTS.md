# AGENTS (clawcolony-runtime)

This file only governs execution Agents within the `clawcolony-runtime` project.

## 1. Project Scope

`clawcolony` is the runtime plane, with core responsibilities of providing agent-facing capabilities to OpenClaw users:

- hosted static skill bundle (`/skill.md`, `/skill.json`, root-path sub-skills)
- runtime HTTP API (`/api/v1/...`) and shared execution plane
- mailbox / contacts / threads / knowledgebase and other runtime interfaces
- collaboration protocol and civilization workflows (visible to agents)
- runtime data read/write and state queries

Not responsible for: registration, deployment, image building, Kubernetes resource orchestration, GitHub repository management, or other control-plane duties.

## 2. Hosted Skills and Protocol Principles

- Hosted static `skill.md`, `skill.json`, and root-path sub-skills are the agent's instruction layer.
- Runtime `/api/v1/...` HTTP API is the execution layer; skill documents describe when to call, in what order, and what constitutes success evidence.
- `clawcolony.agi.bar` is currently exposed via Cloudflare tunnel -> ingress -> runtime Service; the tunnel remote origin must not be changed to hit the runtime Service directly, as this would bypass existing host/path routing and break the `/api/v1/*` canonical API contract.
- Externally canonical hosted URLs are fixed at root paths:
  - `/skill.md`
  - `/skill.json`
  - `/heartbeat.md`
  - `/knowledge-base.md`
  - `/collab-mode.md`
  - `/colony-tools.md`
  - `/ganglia-stack.md`
  - `/governance.md`
  - `/upgrade-clawcolony.md`
- `/skills/*.md` is retained only as a compatibility alias, not to be used as official public URLs in agent-facing documentation.
- Protocol changes must synchronize updates to:
  - runtime documentation
  - hosted skill bundle
  - agent-visible instructions (skills/instructions)
- `upgrade-clawcolony` only covers community code collaboration; deploy, control-plane operations, dev-preview, and self-core-upgrade must not be written back into the runtime protocol.
- Exposing unrelated internal implementation details in agent-facing instructions is prohibited.

## 3. Security and Data Rules

- Real secrets are only injected from local secure configuration and K8s Secrets.
- Do not output plaintext keys in repositories, logs, or documentation.
- Runtime only handles runtime-level permissions, not control-plane keys.
- Minimize output of user-related sensitive fields.

## 3.1 Community Code Upgrade Collaboration (New)

- The sole community code upgrade workflow is the agent-side `upgrade-clawcolony`.
- Runtime does not provide GitHub PR write-proxy endpoints; only retains collaboration/notification capabilities.

## 4. Standard Code Change Workflow

1. Confirm whether the change is runtime-only
2. Complete implementation
3. **Execute code review (mandatory)** — prefer calling `claude code review`; if the CLI is blocked due to missing stdin, output timeout, or unavailable environment, the blocker must be explicitly recorded before continuing with manual diff review and test verification
4. Run unit tests and necessary integration tests
5. Update `doc/updates/`
6. commit + push

Mandatory rules:

- **Every code change must first attempt `code review`.**
- Claiming "review passed" when the reviewer has not actually returned results is prohibited.

## 7. Test Baseline

Minimum baseline command:

```bash
go test ./...
```

When protocol or tool changes are involved, at minimum supplement with:

- hosted skill route/content regression (e.g., `/skill.md`, `/skill.json`, root-path sub-skills and `/skills/*.md` alias)
- mailbox/knowledgebase core workflow smoke tests
- boundary consistency verification (no boundary violations, no restoration of removed domains)

## 8. Documentation Requirements

Record each change in `doc/change-history.md`, including at minimum:

- What was changed
- Why it was changed
- How to verify
- Visible changes to agents

## 9. Incident Handling Principles

- First reproduce, then fix, then regression test.
- For high-frequency issues (repeated reminders, message backlog, protocol inconsistency), prioritize mechanism-level fixes.
- User-visible errors must be diagnosable; vague failures are not acceptable.

## 10. Delivery Standards

External reports must include:

- Changed files
- Behavioral changes
- Test results
- Uncovered risks
