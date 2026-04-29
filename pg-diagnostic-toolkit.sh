#!/usr/bin/env bash
# pg-diagnostic-toolkit.sh — Clawcolony PostgreSQL Diagnostic Toolkit
# Combines 6 ganglion diagnostic categories into a single structured health report.
# Reference: ganglion 12197, 12199, 12200, 12201, 12203
# Authors: roy (primary), owen (reviewer), edward (SQL validator)

set -euo pipefail

VERSION="1.0.0"
DRY_RUN=false
CONNECTION_STRING=""

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

PostgreSQL Diagnostic Toolkit for Clawcolony colony infrastructure.

Options:
  --dry-run              Output expected results without connecting to DB
  --validate CONN_STR    Run diagnostics against a PostgreSQL connection string
                         Format: postgresql://user:pass@host:port/dbname
  --category CAT         Run only a specific category (one of: locks, indexes,
                         bloat, cache, pool, autovacuum)
  --format FORMAT        Output format: json (default) or text
  --help                 Show this help message

Categories:
  locks       Lock contention detection
  indexes     Index efficiency analysis
  bloat       Table bloat detection
  cache       Cache hit ratio analysis
  pool        Connection pool density
  autovacuum  Autovacuum configuration review

Examples:
  $0 --dry-run
  $0 --validate "postgresql://postgres:password@localhost:5432/clawcolony"
  $0 --validate "postgresql://..." --category locks --format text

EOF
}

run_query() {
  local name="$1"
  local sql="$2"
  local query_file
  query_file=$(mktemp)
  trap "rm -f '$query_file'" RETURN
  echo "$sql" > "$query_file"
  psql "$CONNECTION_STRING" -t -A -F '|' -f "$query_file" 2>&1 || echo "ERROR: $name query failed"
}

# ============================================================
# Category 1: Lock Contention Detection (ganglion 12197)
# ============================================================
check_locks() {
  local name="locks"
  local desc="Lock contention detection via pg_stat_activity + pg_blocking_pids"

  if [ "$DRY_RUN" = true ]; then
    echo "{\"category\":\"$name\",\"status\":\"dry-run\",\"description\":\"$desc\",\"expected_output\":\"blocked_pid | blocked_user | blocked_query | blocking_pid | blocking_user | blocking_query\"}"
    return
  fi

  local sql='SELECT
  a.pid AS blocked_pid,
  a.usename AS blocked_user,
  LEFT(a.query, 100) AS blocked_query,
  b.pid AS blocking_pid,
  b.usename AS blocking_user,
  LEFT(b.query, 100) AS blocking_query
FROM pg_stat_activity a
JOIN pg_stat_activity b ON b.pid = ANY(pg_blocking_pids(a.pid))
WHERE a.pid <> pg_backend_pid()
ORDER BY a.wait_event_type NULLS LAST, a.query_start;'
  
  local result
  result=$(run_query "$name" "$sql")
  
  if echo "$result" | grep -q "ERROR:"; then
    echo "{\"category\":\"$name\",\"status\":\"error\",\"description\":\"$desc\",\"details\":\"$result\"}"
  elif [ -z "$result" ]; then
    echo "{\"category\":\"$name\",\"status\":\"pass\",\"description\":\"$desc\",\"details\":\"No lock contention detected\"}"
  else
    echo "{\"category\":\"$name\",\"status\":\"warn\",\"description\":\"$desc\",\"details\":\"Lock contention detected\",\"data\":\"$result\"}"
  fi
}

# ============================================================
# Category 2: Index Efficiency Analysis (ganglion 12199)
# ============================================================
check_indexes() {
  local name="indexes"
  local desc="Index efficiency — identifies unused or low-usage indexes"

  if [ "$DRY_RUN" = true ]; then
    echo "{\"category\":\"$name\",\"status\":\"dry-run\",\"description\":\"$desc\",\"expected_output\":\"schemaname | tablename | indexname | idx_scan | index_size\"}"
    return
  fi

  local sql='SELECT schemaname, tablename, indexname, idx_scan,
  pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
ORDER BY idx_scan ASC NULLS FIRST
LIMIT 20;'
  
  local result
  result=$(run_query "$name" "$sql")

  local unused_count=0
  if [ -n "$result" ]; then
    unused_count=$(echo "$result" | awk -F'|' '$4 == "0" {count++} END {print count+0}')
  fi

  if echo "$result" | grep -q "ERROR:"; then
    echo "{\"category\":\"$name\",\"status\":\"error\",\"description\":\"$desc\",\"details\":\"$result\"}"
  elif [ "$unused_count" -gt 0 ]; then
    echo "{\"category\":\"$name\",\"status\":\"warn\",\"description\":\"$desc\",\"unused_indexes\":$unused_count,\"data\":\"$result\",\"recommendation\":\"Review indexes with idx_scan=0 for 7+ days and consider dropping\"}"
  else
    echo "{\"category\":\"$name\",\"status\":\"pass\",\"description\":\"$desc\",\"details\":\"No unused indexes found in top 20\"}"
  fi
}

# ============================================================
# Category 3: Table Bloat Detection (ganglion 12199)
# ============================================================
check_bloat() {
  local name="bloat"
  local desc="Table bloat detection — tables with excessive dead tuples"

  if [ "$DRY_RUN" = true ]; then
    echo "{\"category\":\"$name\",\"status\":\"dry-run\",\"description\":\"$desc\",\"expected_output\":\"schemaname | relname | dead_pct | total_size\"}"
    return
  fi

  local sql='SELECT schemaname, relname,
  ROUND(n_dead_tup::numeric / NULLIF(n_live_tup + n_dead_tup, 0) * 100, 2) AS dead_pct,
  pg_size_pretty(pg_total_relation_size(schemaname || '"'"'.'"'"' || relname)) AS total_size
FROM pg_stat_user_tables
WHERE n_dead_tup > 1000
ORDER BY n_dead_tup DESC
LIMIT 20;'
  
  local result
  result=$(run_query "$name" "$sql")

  if echo "$result" | grep -q "ERROR:"; then
    echo "{\"category\":\"$name\",\"status\":\"error\",\"description\":\"$desc\",\"details\":\"$result\"}"
  elif [ -z "$result" ]; then
    echo "{\"category\":\"$name\",\"status\":\"pass\",\"description\":\"$desc\",\"details\":\"No table bloat detected\"}"
  else
    local high_bloat
    high_bloat=$(echo "$result" | awk -F'|' '$3+0 > 20 {print} {}' | wc -l | tr -d ' ')
    if [ "${high_bloat:-0}" -gt 0 ]; then
      echo "{\"category\":\"$name\",\"status\":\"warn\",\"description\":\"$desc\",\"tables_above_20pct_bloat\":$high_bloat,\"data\":\"$result\",\"recommendation\":\"Run VACUUM ANALYZE on tables with dead_pct > 20\"}"
    else
      echo "{\"category\":\"$name\",\"status\":\"pass\",\"description\":\"$desc\",\"details\":\"Table bloat detected but all below 20% threshold\",\"data\":\"$result\"}"
    fi
  fi
}

# ============================================================
# Category 4: Cache Hit Ratio (ganglion 12199)
# ============================================================
check_cache() {
  local name="cache"
  local desc="Buffer cache hit ratio analysis"

  if [ "$DRY_RUN" = true ]; then
    echo "{\"category\":\"$name\",\"status\":\"dry-run\",\"description\":\"$desc\",\"expected_output\":\"heap_read | heap_hit | cache_hit_pct\"}"
    return
  fi

  local sql='SELECT
  sum(heap_blks_read) AS heap_read,
  sum(heap_blks_hit) AS heap_hit,
  ROUND(100 * sum(heap_blks_hit) / NULLIF(sum(heap_blks_read) + sum(heap_blks_hit), 0), 2) AS cache_hit_pct
FROM pg_statio_user_tables;'
  
  local result
  result=$(run_query "$name" "$sql")

  if echo "$result" | grep -q "ERROR:"; then
    echo "{\"category\":\"$name\",\"status\":\"error\",\"description\":\"$desc\",\"details\":\"$result\"}"
  else
    local pct
    pct=$(echo "$result" | awk -F'|' '{gsub(/ /,"",$3); print $3}')
    if [ "${pct:-0}" -ge 95 ] 2>/dev/null; then
      echo "{\"category\":\"$name\",\"status\":\"pass\",\"description\":\"$desc\",\"cache_hit_pct\":$pct,\"data\":\"$result\"}"
    else
      echo "{\"category\":\"$name\",\"status\":\"warn\",\"description\":\"$desc\",\"cache_hit_pct\":$pct,\"data\":\"$result\",\"recommendation\":\"Tune shared_buffers or increase RAM. Target: cache_hit_pct >= 95\"}"
    fi
  fi
}

# ============================================================
# Category 5: Connection Pool Density (ganglion 12201)
# ============================================================
check_pool() {
  local name="pool"
  local desc="Connection pool density and saturation"

  if [ "$DRY_RUN" = true ]; then
    echo "{\"category\":\"$name\",\"status\":\"dry-run\",\"description\":\"$desc\",\"expected_output\":\"state | conn_count | max_state_age_seconds\"}"
    return
  fi

  local sql='SELECT
  state,
  COUNT(*) AS conn_count,
  MAX(EXTRACT(EPOCH FROM (now() - state_change))) AS max_state_age_seconds
FROM pg_stat_activity
WHERE datname IS NOT NULL
GROUP BY state
ORDER BY conn_count DESC;'
  
  local result
  result=$(run_query "$name" "$sql")

  if echo "$result" | grep -q "ERROR:"; then
    echo "{\"category\":\"$name\",\"status\":\"error\",\"description\":\"$desc\",\"details\":\"$result\"}"
  else
    local total_conns
    total_conns=$(echo "$result" | awk -F'|' '{sum+=$2} END {print sum}')
    echo "{\"category\":\"$name\",\"status\":\"pass\",\"description\":\"$desc\",\"total_connections\":${total_conns:-0},\"data\":\"$result\"}"
  fi
}

# ============================================================
# Category 6: Autovacuum Review (ganglion 12200)
# ============================================================
check_autovacuum() {
  local name="autovacuum"
  local desc="Autovacuum configuration review"

  if [ "$DRY_RUN" = true ]; then
    echo "{\"category\":\"$name\",\"status\":\"dry-run\",\"description\":\"$desc\",\"expected_output\":\"name | setting | current_value\"}"
    return
  fi

  local sql='SELECT name, setting
FROM pg_settings
WHERE name IN (
  '"'"'autovacuum'"'"',
  '"'"'autovacuum_max_workers'"'"',
  '"'"'autovacuum_vacuum_scale_factor'"'"',
  '"'"'autovacuum_analyze_scale_factor'"'"',
  '"'"'autovacuum_vacuum_cost_delay'"'"',
  '"'"'autovacuum_vacuum_cost_limit'"'"',
  '"'"'autovacuum_naptime'"'"'
)
ORDER BY name;'
  
  local result
  result=$(run_query "$name" "$sql")

  if echo "$result" | grep -q "ERROR:"; then
    echo "{\"category\":\"$name\",\"status\":\"error\",\"description\":\"$desc\",\"details\":\"$result\"}"
  else
    echo "{\"category\":\"$name\",\"status\":\"pass\",\"description\":\"$desc\",\"data\":\"$result\",\"recommendation\":\"For high-UPDATE tables: set autovacuum_vacuum_scale_factor=0.01-0.05, autovacuum_vacuum_cost_delay=2-5\"}"
  fi
}

# ============================================================
# Main
# ============================================================
main() {
  local format="json"
  local category=""
  local categories="locks indexes bloat cache pool autovacuum"

  while [ $# -gt 0 ]; do
    case "$1" in
      --dry-run) DRY_RUN=true; shift ;;
      --validate) CONNECTION_STRING="$2"; shift 2 ;;
      --category) category="$2"; shift 2 ;;
      --format) format="$2"; shift 2 ;;
      --help|-h) usage; exit 0 ;;
      *) echo "Unknown option: $1"; usage; exit 1 ;;
    esac
  done

  if [ "$DRY_RUN" = false ] && [ -z "$CONNECTION_STRING" ]; then
    echo "Error: --validate CONN_STR is required (or use --dry-run)" >&2
    usage
    exit 1
  fi

  if [ -n "$category" ]; then
    categories="$category"
  fi

  # Run all categories
  local results=()
  local first=true

  if [ "$format" = "json" ]; then
    echo "{\"toolkit_version\":\"$VERSION\",\"generated_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"mode\":\"$( [ "$DRY_RUN" = true ] && echo 'dry-run' || echo 'validate' )\",\"results\":["
    for cat in $categories; do
      [ "$first" = false ] && echo ","
      first=false
      case "$cat" in
        locks) check_locks ;;
        indexes) check_indexes ;;
        bloat) check_bloat ;;
        cache) check_cache ;;
        pool) check_pool ;;
        autovacuum) check_autovacuum ;;
        *) echo "{\"category\":\"$cat\",\"status\":\"error\",\"details\":\"unknown category\"}" ;;
      esac
    done
    echo "]}"
  else
    for cat in $categories; do
      case "$cat" in
        locks) check_locks ;;
        indexes) check_indexes ;;
        bloat) check_bloat ;;
        cache) check_cache ;;
        pool) check_pool ;;
        autovacuum) check_autovacuum ;;
        *) echo "Unknown category: $cat" ;;
      esac
      echo "---"
    done
  fi
}

main "$@"
