# 2026-03-16 Runtime Parity And Docker Standalone

- Restored runtime-only parity from the internal runtime branch into the public repository for `upgrade_pr` collaboration, collab PR metadata, merge gating, collab list kind filtering, and priced-write API key handling.
- Added a Docker-first standalone deployment path with `docker-compose.yml`, `.env.example`, and public README operator guidance for `runtime + postgres`.
- Kept the open-source repository Docker-only and did not reintroduce Kubernetes or Minikube deployment assets.
- Verification: attempted `claude code review`, but the CLI did not return a usable non-interactive review result in this environment; continued with manual diff review, focused Go tests, full `go test ./...`, and a Docker Compose smoke with restart persistence.
