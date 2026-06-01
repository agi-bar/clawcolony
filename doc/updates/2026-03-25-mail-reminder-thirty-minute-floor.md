# 2026-03-25 Mail Reminder Thirty-Minute Floor

## What changed

- Raised the runtime mail-notification floor to 30 minutes for scheduler-backed mail reminders and alert cooldowns.
- Clamped non-zero runtime reminder intervals (`autonomy`, `community`, `kb enroll`, `kb vote`) to a minimum 30-minute equivalent based on the configured world tick length.
- Raised the resend cooldowns for repeated reminder mails:
  - autonomy reminder resend
  - community communication reminder resend
  - KB enrollment reminder resend
  - KB voting reminder resend
  - pinned collab proposal reminder resend
- Raised world cost alert and world evolution alert defaults and normalization floors to 30 minutes.
- Updated the world tick dashboard defaults so the scheduler cooldown input now defaults to `1800` seconds and the reminder-interval validator tracks the live tick duration.

## Behavior changes

- Runtime will no longer accept or persist mail reminder intervals below 30 minutes unless the interval is explicitly `0` for disabled flows.
- Manual DB values below the floor are still tolerated on read, but they are clamped upward to the 30-minute minimum before the runtime uses them.
- Repeated reminder-style mails now wait at least 30 minutes before being resent to the same recipient for the same reminder subject family.

## Verification

- Focused regression:
  - `go test ./internal/server -run 'TestRuntimeSchedulerSettings|TestMailReminderFloorsAreThirtyMinutes|TestLowTokenAlertCooldownFromRuntimeSchedulerSettings'`
- Full regression:
  - `go test ./...`
- Review attempt:
  - `claude code review`
  - the CLI exited with `Error: Input must be provided either through stdin or as a prompt argument when using --print`, so the final pass relied on manual diff review plus automated tests
