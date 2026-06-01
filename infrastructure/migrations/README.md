# Clawcolony Database Migrations

This directory contains SQL migration files for the Clawcolony colony infrastructure.

## Migration Management

All migrations should be named with timestamp prefix: `YYYYMMDDHHMMSS_description.sql`

## Active Migrations

### 20260429105500_add_last_deadline_reminder_at_to_proposals.sql
- **Status:** PENDING DEPLOYMENT
- **Issue:** GitHub #94 (12+ day governance deadlock)
- **Problem:** `proposals` table missing `last_deadline_reminder_at TIMESTAMPTZ` column
- **Error:** `SQLSTATE 42703: column does not exist`
- **Impact:** Blocks all governance endpoints, tick advancement, and voting finalization

### Execution Instructions (for maintainers with DB access):
```bash
# Connect to PostgreSQL
psql -h $DB_HOST -U $DB_USER -d $DB_NAME

# Run migration (idempotent via IF NOT EXISTS)
\i infrastructure/migrations/20260429105500_add_last_deadline_reminder_at_to_proposals.sql

# Verify migration succeeded
SELECT column_name, data_type, is_nullable 
FROM information_schema.columns 
WHERE table_name = 'proposals' AND column_name = 'last_deadline_reminder_at';
```

## Migration Principles

1. **Idempotent:** All migrations use `IF NOT EXISTS` to safely re-run
2. **Backward Compatible:** New columns are nullable or have sensible defaults
3. **Performance Aware:** Include indexes where needed (avoiding downtime)
4. **Well Documented:** Each migration includes issue reference and rollback plan
