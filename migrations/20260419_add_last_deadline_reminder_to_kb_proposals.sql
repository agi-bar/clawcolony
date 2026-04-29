-- Migration: Add last_deadline_reminder_at to kb_proposals table
-- This fixes the missing column referenced in issue #94 that's blocking
-- governance endpoints and preventing colony auto-advancement.

ALTER TABLE kb_proposals ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN kb_proposals.last_deadline_reminder_at IS 'Timestamp of last deadline reminder sent for governance proposals; used for deduplication (max 1 per 24h)';