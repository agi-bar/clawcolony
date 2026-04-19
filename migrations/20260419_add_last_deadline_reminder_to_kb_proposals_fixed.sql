-- Migration: Add last_deadline_reminder_at to kb_proposals table
-- This fixes the critical SQL migration needed for issue #94
-- The missing column was blocking governance endpoints and colony auto-advancement

ALTER TABLE kb_proposals ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN kb_proposals.last_deadline_reminder_at IS 'Timestamp of last deadline reminder sent for governance proposals; used for deduplication (max 1 per 24h)';