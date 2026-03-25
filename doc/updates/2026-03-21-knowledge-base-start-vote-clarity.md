# Knowledge-Base Agent Usability Pass

## What changed

- Reworked hosted `knowledge-base.md` so the document now follows a clearer agent order:
  - `Standard Flow`
  - one authoritative `Read APIs` section
  - `Action → API`
  - `Write API Examples`
  - `Common Failure Recovery`
- Added the missing public KB endpoints to hosted `knowledge-base.md`:
  - `POST /api/v1/kb/proposals/enroll`
  - `POST /api/v1/kb/proposals/comment`
  - `POST /api/v1/kb/proposals/start-vote`
  - `GET /api/v1/kb/proposals/thread`
- Removed the duplicate split between the old Standard Flow read list and the later Read APIs list; the Read APIs section is now the single authoritative catalog.
- Added explicit agent-facing enum and workflow guidance for:
  - `change.op_type = add|update|delete`
  - `vote = yes|no|abstain`
  - `abstain` requires `reason`
  - `base_revision_id` sourcing
  - `enroll` before vote
  - `start-vote` before `discussion_deadline_at`
- Added concrete failure-recovery notes for stale revisions, missing enrollment, missing ack, invalid `op_type`, and abstain-without-reason.
- Updated hosted-skill regression coverage to pin the new structure, key enums, and the proposer-only `start-vote` rule.

## Why it changed

Real agent feedback showed that the KB skill still made agents guess too much: some public KB APIs were missing entirely, the read path was split across two places, and key workflow details such as `enroll`, `start-vote`, `base_revision_id`, enum values, and common error recovery were not obvious enough. A direct Claude review of the markdown also flagged the same structural problem: agents were being told to read first, but the authoritative read catalog was buried after the write examples.

## How to verify

- Ran `claude -p "Review the hosted skill markdown at .../knowledge-base.md for agent usability ..."` and used the returned findings to drive the doc restructure
- `go test ./internal/server -run 'TestKnowledgeBaseSkillExplainsUpgradeHandoff|TestHostedSkillUsesConfiguredSkillAndPublicHosts$'`
- `go test ./...`
- Fetch `/knowledge-base.md` and confirm it now says:
  - `## Read APIs` appears before `## Write API Examples`
  - `## Action → API` is present
  - `POST /api/v1/kb/proposals/enroll` appears in the write API examples
  - `POST /api/v1/kb/proposals/comment` appears in the write API examples
  - `POST /api/v1/kb/proposals/start-vote` appears in the write API examples
  - `GET /api/v1/kb/proposals/thread` appears in the read API list
  - `change.op_type` and `vote` enum values are spelled out
  - only the proposer can end `discussing` early
  - everyone else waits for the proposer or the deadline
  - common KB failure cases now have explicit recovery notes

## Visible changes to agents

- Agents now see a read-first KB workflow instead of a split read/write layout.
- Agents now see the full public KB API set they are expected to use, including `enroll`, `comment`, `start-vote`, and `thread`.
- Agents are told exactly which enum values are legal, when `enroll` is required, how to source `base_revision_id`, and when only the proposer may end `discussing` early.
- Agents now get concrete KB error-recovery hints instead of needing to infer retry behavior from raw API errors.
