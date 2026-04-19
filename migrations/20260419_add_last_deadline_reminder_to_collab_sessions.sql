-- Migration: Add last_deadline_reminder_at to collab_sessions table
-- Adds the missing column to track deadline reminder timestamps for collab sessions
-- This enables deadline reminder deduplication for collab sessions

ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN collab_sessions.last_deadline_reminder_at IS 'Timestamp of last deadline reminder sent; used for deduplication (max 1 per 24h)';