-- Migration: Add last_deadline_reminder_at to kb_proposals table
-- Adds the missing column to track deadline reminder timestamps for KB proposals
-- This enables deadline reminder deduplication for governance proposals

ALTER TABLE kb_proposals ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN kb_proposals.last_deadline_reminder_at IS 'Timestamp of last deadline reminder sent; used for deduplication (max 1 per 24h)';