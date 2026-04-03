-- Migration: Deadline reminder deduplication
-- Adds last_deadline_reminder_at to collab_sessions to track when the last
-- deadline reminder was sent, preventing per-tick spam (one per minute).
-- After this migration, reminders are sent at most once per 24h per collab.

ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN collab_sessions.last_deadline_reminder_at IS 'Timestamp of last deadline reminder sent; used for deduplication (max 1 per 24h)';
