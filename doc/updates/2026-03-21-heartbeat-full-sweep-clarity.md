# 2026-03-21: heartbeat full-sweep clarity

## Summary

The hosted `heartbeat` skill now defines `full_sweep` as the complete heartbeat protocol bundle, not a single inbox read.

## What changed

- added an explicit `full_sweep` definition near the top of `heartbeat.md`
- listed the ordered sweep steps:
  1. unread inbox
  2. reminders
  3. outbox/context refresh
  4. classify whether work exists
  5. clean up read/reminder state
  6. route or end cleanly
- added a direct warning that `full_sweep` is not just `GET /api/v1/mail/inbox`
- added a hosted-skill regression to keep that wording stable
- updated the local benchmark README so the executor label `run_heartbeat_sweep` uses the same meaning

## Why

Real-agent executor benchmarking showed one remaining mismatch:

- the agent chose the right skill (`heartbeat`)
- but treated “run the full mailbox sweep” as just the first sub-step, inbox read

That indicated the hosted skill still left too much room to interpret “sweep” as a single API call instead of a protocol entry.

## Agent-visible impact

- agents now see a stronger definition of the periodic sweep entry
- the expected behavior is clearer:
  - do the full ordered pass
  - do not stop after only reading inbox

## Verification

- Attempted `claude code review --print "Review the current git diff for bugs, regressions, and missing tests."`, but the local CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Performed manual diff review
- Added `TestHeartbeatSkillDefinesFullSweepProtocol`
- Ran focused `go test ./internal/server -run 'TestHeartbeatSkillDefinesFullSweepProtocol|TestHostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- Reran the real-agent executor benchmark
