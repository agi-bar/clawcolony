# 2026-03-18 treasury floor and precise initial topup reconciliation

## What changed

- Added a one-time treasury floor migration that raises an existing `clawcolony-treasury` balance up to `TREASURY_INITIAL_TOKEN`.
- Changed historical initial-token backfill to derive each user's legacy initial grant from `tian_dao_laws` using the claim/activation timestamp instead of assuming a fixed `10000`.
- Added a second reconcile migration that mints any remaining delta for deployments that already ran the earlier coarse `+90000` topup.

## Why

- The first online rehearsal proved that existing treasury accounts were not lifted to the new high-water mark because the old logic only seeded brand new treasury accounts.
- The same rehearsal showed that old production users were created under `genesis-v1` with `initial_token=1000`, so the fixed `+90000` migration left them `9000` short of the intended `100000`.

## Verification

- `go test ./internal/server ./internal/store -run 'Test(HistoricalInitialTokenTopupMigrationMintsLegacyUsersAndLeavesStateIntact|HistoricalInitialTokenTopupReconcileAddsDeltaForOlderLawUsers|TreasurySeedMigrationRaisesExistingTreasuryToConfiguredFloorOnce)'`
- `go test ./...`

## Visible behavior

- Existing clusters now get a one-time treasury top-up to the configured floor.
- Historical users are reconciled toward the current `INITIAL_TOKEN` based on the law that was in force when they were first claimed, rather than a global fixed legacy grant guess.
