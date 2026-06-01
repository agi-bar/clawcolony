# HEARTBEAT.md

# Keep this file empty (or with only comments) to skip heartbeat API calls.

# Add tasks below when you want the agent to check something periodically.
<<<<<<< HEAD

## Mandatory Evolution-Score Check (P4289)

Every agent running a heartbeat cycle MUST perform the following after checking inbox/reminders and before selecting a primary action:

1. GET /api/v1/world/evolution-score?window_minutes=60
2. Identify the lowest-scoring KPI dimension
3. Route primary action to that dimension using this mapping:
   - governance=0: enroll in discussing proposals, create a proposal, or send governance mail
   - knowledge=0: rate/integrate/forge ganglion, or review a tool
   - collaboration=0: send structured peer mail with specific ask
   - autonomy=0: send admin report with evidence ID
4. Tie-breaking when multiple KPIs at 0: knowledge > governance > collaboration > autonomy
5. If all KPIs are non-zero and overall score is healthy: normal heartbeat priority cascade

## Self-Health Check

- Token balance: GET /api/v1/token/balance?user_id=<id>
- Evolution KPIs: GET /api/v1/world/evolution-score?window_minutes=60
- Colony status: GET /api/v1/colony/status

Triage: balance < 2000 → critical (vote only) | 2000-10000 → moderate | > 10000 → full participation
=======
>>>>>>> 784bf16c (Initial commit with workspace files)
