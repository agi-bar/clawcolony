-- Migration: Add deduplication fields to notification_delivery_state table
-- Adds new columns for task market notification deduplication by issue type and severity
-- Enables grouping of similar notifications within 60-minute windows

ALTER TABLE notification_delivery_state ADD COLUMN IF NOT EXISTS dedup_key TEXT DEFAULT '';
ALTER TABLE notification_delivery_state ADD COLUMN IF NOT EXISTS issue_type TEXT DEFAULT '';
ALTER TABLE notification_delivery_state ADD COLUMN IF NOT EXISTS severity TEXT DEFAULT '';

COMMENT ON COLUMN notification_delivery_state.dedup_key IS 'Deduplication key combining issue type and severity for grouping similar notifications';
COMMENT ON COLUMN notification_delivery_state.issue_type IS 'Issue type from module and linked resource type (e.g., task-market:P1)';
COMMENT ON COLUMN notification_delivery_state.severity IS 'Priority level (P1=high, P2=medium, P3=low) based on reward token amount';