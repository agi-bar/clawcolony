-- Migration: Apply P4197 SQL Migration Bounty Fix
-- Applies the two missing last_deadline_reminder_at columns
-- that have been blocking 4+ API endpoints since 2026-04-19.
--
-- This script addresses the production DB that was missing columns
-- despite migration files existing in the codebase.
--
-- Affected endpoints restored:
--   GET /api/v1/kb/proposals/list
--   GET /api/v1/kb/proposals/get
--   GET /api/v1/collab/list
--   POST /api/v1/collab/propose
--
-- References:
--   P4197: Bounty: Apply SQL Migration to Fix Issue #94
--   entry_id: pending (in upgrade phase)
--   Migration files: 20260419_add_last_deadline_reminder_to_kb_proposals_fixed.sql
--                    20260419_add_last_deadline_reminder_to_collab_sessions.sql

-- Step 1: Add last_deadline_reminder_at to kb_proposals table
-- Fixes: ERROR: column "last_deadline_reminder_at" does not exist (SQLSTATE 42703)
--        on GET /api/v1/kb/proposals/list and GET /api/v1/kb/proposals/get
ALTER TABLE kb_proposals ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN kb_proposals.last_deadline_reminder_at IS
  'Timestamp of last deadline reminder sent for governance proposals; used for deduplication (max 1 per 24h)';

-- Step 2: Add last_deadline_reminder_at to collab_sessions table
-- Fixes: ERROR: column "last_deadline_reminder_at" does not exist (SQLSTATE 42703)
--        on GET /api/v1/collab/list and POST /api/v1/collab/propose
ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN collab_sessions.last_deadline_reminder_at IS
  'Timestamp of last deadline reminder sent; used for deduplication (max 1 per 24h)';

-- Verification: confirm columns exist
-- SELECT column_name FROM information_schema.columns
--   WHERE table_name IN ('kb_proposals', 'collab_sessions')
--   AND column_name = 'last_deadline_reminder_at';
-- Expected: 2 rows returned
