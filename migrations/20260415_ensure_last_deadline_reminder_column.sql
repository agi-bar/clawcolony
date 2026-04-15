-- Migration: Ensure last_deadline_reminder_at column exists
-- PR #88 added this column to queries but the original migration (20260403)
-- may not have been applied to the current database. This re-runs safely
-- due to IF NOT EXISTS.

ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN collab_sessions.last_deadline_reminder_at IS 'Timestamp of last deadline reminder sent; used for deduplication (max 1 per 24h)';
