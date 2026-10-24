# berlin-transit — Technical Design Document

Status: Design reference

## 1. Summary

Build a lightweight, provider-extensible Go service that discovers public transport stations and serves normalized departure information. The initial provider is VBB (Berlin and Brandenburg), and the initial transport mode is S-Bahn. Any station supported by the provider must be queryable; no station is hardcoded. The service is independent of any display, dashboard, or automation consumer.

**Non-negotiable defaults:** private network access only; no automatic public exposure, deployment, image push, release, telemetry export, or external notification. Any publishing capability requires explicit operator opt-in. Calling the configured VBB upstream is necessary for functionality and is documented separately from publishing the service.

## 2. Goals

- Search stations by text, with provider IDs preserved as opaque strings.
- Fetch real-time departures for any valid station, including destination, line, planned and predicted time, delay, platform, cancellation, and provenance when available.
- Support multiple stations, transport-mode filters, direction, and bounded result counts.
- Normalize data in a stable, versioned REST JSON API with correct Europe/Berlin timezone handling.
- In-memory bounded cache, upstream rate limiting, request coalescing, and stale-on-error behavior with explicit stale metadata.
- Configurable HTTP listener bound to 127.0.0.1 by default; operational endpoints; secure, reproducible builds.
- Extensible provider interface without premature plugin frameworks or database.

## 3. Non-goals for MVP

- No database, authentication to VBB, UI, mandatory display-specific rendering, MQTT, route planning, ticketing, or user accounts. Optional TRMNL push is deferred to a later milestone.
- No public listener, reverse proxy, Kubernetes Ingress, hosted service, scheduled publishing, container registry push, or release automation by default.
- No guarantee of realtime accuracy when upstream data is missing; expose planned vs predicted values clearly.

## 4. Upstream and constraints

Initial upstream: https://v6.vbb.transport.rest/ (unofficial REST wrapper for VBB). Document that this is a third-party service, its availability and terms can change, and attribution may be required. Its documentation describes a 100 requests/minute limit, HTTP ETag and Cache-Control, and station search and departures endpoints. Implement a conservative configurable limit substantially below the advertised maximum. Prefer conditional requests when supported. Avoid coupling normalized models to upstream JSON.

Relevant endpoints (verify live contract during implementation):
- GET /stations?query=...&limit=...
- GET /stops/:id/departures?duration=...&results=...&suburban=true&subway=false&tram=false&bus=false&ferry=false&express=false&regional=false

Validate station IDs and URL-escape path segments. Prefer upstream stop IDs as opaque strings. Normalize upstream `delay` in seconds and timestamps with timezone offsets; distinguish null/unknown from zero delay. Capture `realtimeDataUpdatedAt` when supplied.

## 5. Architecture

```
VBB API -> provider/vbb -> normalizer -> service -> bounded in-memory cache -> HTTP JSON API
                                              |                |
                                         rate limiter      health/metrics
```

Interfaces should be small and defined where consumed:
- `Provider.SearchStations(ctx, query, limit)`
- `Provider.Departures(ctx, stationID, options)`
- `Clock` injectable for deterministic tests.

One instance uses an in-memory cache; cache consistency across replicas is not guaranteed. Keep core domain models independent of HTTP and VBB specifics. Start with a single binary and a single provider implementation.

## 6. REST API contract (v1)

- `GET /api/v1/stations?query=Alexanderplatz&limit=10`
- `GET /api/v1/stations/{id}/departures?mode=suburban&limit=10&duration=30`
- `GET /api/v1/departures?stations=<id1>,<id2>&mode=suburban&limit=10` (bounded station count; per-station grouping)
- `GET /healthz` for liveness (no upstream dependency)
- `GET /readyz` for readiness (server initialized; do not fail solely for transient VBB outage)
- `GET /metrics` only if explicitly enabled; still bound to private listener

Use RFC3339 timestamps and stable JSON field names. Example (illustrative only):

```json
{
  "station": {"id":"900100003", "name":"S+U Alexanderplatz"},
  "departures": [
    {
      "line":"S7", "mode":"suburban", "direction":"Potsdam Hbf",
      "planned_at":"2026-10-18T18:35:00+02:00",
      "predicted_at":"2026-10-18T18:38:00+02:00",
      "delay_seconds":180, "platform":"2", "cancelled":false
    }
  ],
  "source":"vbb", "fetched_at":"2026-10-18T18:30:00+02:00",
  "realtime":true, "stale":false
}
```

Preserve null for unavailable fields. A `stale` response must also include `last_success_at` and `stale_age_seconds`. Do not silently claim fresh data. Error responses use a stable shape: `{ "error": {"code":"...", "message":"..."} }`, with appropriate 400/404/429/502/503 statuses. Never echo raw upstream error bodies to clients.

## 7. Caching and resilience

- Bounded, mutex-protected in-memory TTL cache; separate station-search and departures TTLs (suggested initial defaults 24h and 30s, respectively, configurable).
- Honor upstream Cache-Control when appropriate without extending beyond configured policy. Support ETag/If-None-Match when feasible.
- Cache key includes provider, station, mode, direction, duration, limit, and all result-affecting parameters; canonicalize input.
- Deduplicate concurrent requests for the same key (singleflight pattern or small in-house equivalent).
- Conservative upstream request budget, configurable timeout, bounded retries only for transient errors, exponential backoff with jitter, and respect Retry-After.
- Stale-if-error only within configured max age (e.g. 5 minutes), explicitly flagged. No indefinite stale responses.
- Hard limits on query lengths, number of stations (e.g. 5), result counts, upstream response body size, connection pool, and HTTP timeouts.
- Return controlled 429/503 when overloaded. Graceful shutdown and context cancellation.

## 8. Configuration and privacy

Environment variables or CLI flags (document precedence):
- `BERLIN_TRANSIT_LISTEN_ADDR=127.0.0.1:8080`
- `BERLIN_TRANSIT_VBB_BASE_URL=https://v6.vbb.transport.rest`
- `BERLIN_TRANSIT_HTTP_TIMEOUT=10s`
- `BERLIN_TRANSIT_DEPARTURES_TTL=30s`
- `BERLIN_TRANSIT_STATIONS_TTL=24h`
- `BERLIN_TRANSIT_MAX_STALE=5m`
- `BERLIN_TRANSIT_METRICS_ENABLED=false`
- `BERLIN_TRANSIT_LOG_LEVEL=info`

Reject invalid configuration at startup. Restrict configurable upstream URL: default allowlisted HTTPS origin, no redirects to private IPs, no arbitrary user-supplied upstream URL via HTTP endpoints; explicit development/test override only. This prevents SSRF. No secrets are required for VBB. Do not log full query strings, station search terms, personal location data, or raw upstream payloads by default. Structured logs with request IDs, durations, sanitized error categories, and no sensitive headers. No analytics or outbound telemetry.

**Network posture:** localhost bind by default. Docker Compose example maps `127.0.0.1:8080:8080` only after configuring the container process to listen on `0.0.0.0:8080` inside its isolated network; do not publish host port on all interfaces. Kubernetes example uses ClusterIP only, no Ingress/LoadBalancer/NodePort, no public DNS, no public metrics. NetworkPolicies restrict inbound traffic where CNI enforcement exists and egress to required DNS and HTTPS destinations; explain that NetworkPolicy support depends on CNI. External network access is an explicit deployment decision. For remote LAN access, require a documented opt-in and network-level controls (VPN/reverse proxy auth/firewall), never imply that binding 0.0.0.0 alone is safe.

## 9. Security requirements

- Use Go standard library by default; introduce dependencies only with documented rationale.
- Use the latest patched stable Go release, verified against the official Go release feed. Keep go.mod/toolchain current; documentation should reference those files rather than duplicate tool versions.
- Validate and cap all inputs; `net/http` read header, read, write, idle timeouts; cap upstream body size; avoid redirect and DNS rebinding pitfalls.
- Secure container: multi-stage build, minimal runtime image pinned by digest, non-root UID, read-only root filesystem, no added Linux capabilities, seccomp default, no privilege escalation, resource limits, drop unnecessary network permissions.
- Never add debug/pprof listener or metrics public by default.
- Keep errors sanitized; no stack traces or upstream response bodies to users.
- No credentials or tokens in repo, logs, fixtures, CI artifacts, or image layers.
- Security policy and disclosure instructions in SECURITY.md.

## 10. Repository layout

```
berlin-transit/
  cmd/berlin-transit/main.go
  internal/config/
  internal/domain/
  internal/provider/vbb/
  internal/service/
  internal/cache/
  internal/httpapi/
  internal/observability/
  .github/workflows/ci.yml
  .github/workflows/security.yml
  .github/workflows/release.yml   # operator-pushed version tags; manual fallback
  .github/dependabot.yml
  docs/architecture.md
  docs/api.md
  docs/deployment.md
  README.md
  SECURITY.md
  CONTRIBUTING.md
  LICENSE
  Dockerfile
  .dockerignore
  Makefile
  go.mod
  go.sum
```

## 11. GitHub Actions and supply-chain security

Study the user's intended `pr-inbox` repository before copying patterns; the repository owner/link was not verified. If unavailable, state that explicitly and proceed with best-practice defaults rather than adopting another repo's patterns.

CI triggers: pull_request, push to main, workflow_dispatch. Security scans also run on a weekly schedule. Workflow-wide `permissions: contents: read`, override only where strictly necessary. Use current stable GitHub Actions versions **pinned to full immutable commit SHA** with comments identifying the version; validate current versions at implementation time. Checkout with `persist-credentials: false`. No untrusted `pull_request_target` code execution, no privileged workflow_run, no secrets for PR tests, no arbitrary shell interpolation of untrusted PR metadata. Concurrency cancellation for redundant PR runs.

Required checks:
- `gofmt` / `go vet` / `go test ./...` / `go test -race ./...` / native Go coverage report (informational)
- `go mod tidy` diff check
- golangci-lint (compatible with Go version; config validated; pin binary/action version)
- `govulncheck ./...`
- `gosec` as defense in depth, with reviewed suppressions only
- actionlint for workflow files
- container build validation (no push) and ko SPDX SBOM generation as CI artifacts (not registry uploads)
- dependency analysis and native GitHub secret scanning / push protection
- Dependabot for gomod, github-actions, and docker, weekly grouped PRs, no automatic merge.

**Release policy:** ordinary branch/PR CI never publishes. An operator-pushed `v*` tag authorizes the separate release workflow to publish the GitHub release, GHCR image, signatures and provenance without an extra approval. Manual dispatch remains available with explicit confirmation. Validate tag/source identity and main ancestry; use least-privilege permissions. The workflow never creates tags or deploys services. Restrict publication permissions to the release job.

## 12. Testing strategy

- Unit tests for normalization, nullable delay/platform, cancellation, timestamp DST changes, validation, cache expiry, concurrency, and stale behavior.
- `httptest.Server` fake upstream for station search, departures, errors, timeouts, 304, 429 Retry-After, malformed/oversized payloads, and redirects.
- Race detector and fuzz tests for query parsing and upstream JSON parsing.
- Integration tests must not require live VBB access by default. Optional manual smoke test uses live API conservatively.
- Tests for default localhost binding, disabled metrics, rejected unsafe upstream URLs, and no public container port publication.

## 13. Milestones and Codex implementation order

**M1 — Minimal functional collector:** repository scaffold, Go module, domain models, VBB client, search and single-station departures, normalization, mock-backed tests, README. No deployment or publishing.

**M2 — Safe service:** HTTP endpoints, validation, bounded cache, rate limiting, timeouts, stale flags, health endpoints, structured logging, graceful shutdown, security tests.

**M3 — Quality gates:** GitHub Actions CI/security, Dependabot, lint, vuln scans, race tests, container build with no push, security policy and docs.

**M4 — Multi-station and optional deployment:** multi-station endpoint, Docker Compose localhost-only, optional private K3s ClusterIP manifests, documented opt-in for network exposure. No release automation unless explicitly requested.

## 14. Acceptance criteria

1. Any VBB-supported station can be searched and queried without source edits.
2. Default mode is suburban/S-Bahn, with clear extensibility for other modes.
3. REST API provides stable, documented normalized responses and honest realtime/stale metadata.
4. Cache is bounded, concurrent-safe, tested, and never hides stale data.
5. By default the service binds only to loopback and exposes no public endpoint.
6. CI tests, lint, security, race checks and dependency updates are configured.
7. No workflow publishes images, releases, deployments, or data without explicit opt-in.
8. All integration tests work offline with deterministic fixtures.
9. No TRMNL-specific naming or code in core.
10. README includes secure local quick start and clear limitations of the upstream API.
11. Optional TRMNL push integration is isolated, disabled by default, and never required for the REST API.

Operator choices: prefer maintained upstream Actions over custom
tool installers; use Go or Bash for repository tooling; use native GitHub secret
scanning/push protection without a duplicate scanner; omit container scanning.
The ko base intentionally follows `cgr.dev/chainguard/static:latest` without a
digest. These choices supersede the corresponding original defaults above.

## 15. Codex handoff prompt

Implement this design incrementally. Start with M1 only, producing a working, well-tested Go repository. Before choosing versions, verify the latest patched stable Go version and compatible current dependencies/actions from authoritative release sources. Do not assume an unverified `pr-inbox` repository is the user's intended reference; ask for its URL if needed. Prefer standard library and avoid unnecessary frameworks. Never expose a network endpoint publicly, publish an image, deploy, tag, or create a release without explicit authorization. Provide a concise implementation summary, tests executed, and unresolved design decisions after each milestone.

## 16. References

- https://go.dev/doc/devel/release
- https://v6.vbb.transport.rest/
- https://v6.vbb.transport.rest/api.html
- https://docs.github.com/en/code-security/concepts/supply-chain-security/dependabot-version-updates
- https://docs.github.com/en/actions/reference/security/securely-using-pull_request_target

## 17. Optional TRMNL output adapter (M5)

The `berlin-transit` repository remains provider- and display-neutral. The TRMNL integration is an opt-in output adapter, following the same conventions as `tado-bridge`; the repositories MUST NOT depend on each other. The adapter reads the normalized cached departures only, never triggers additional upstream calls.

**Push (recommended):** operator creates a TRMNL private plugin and provides a webhook URL through `TRMNL_WEBHOOK_URL` secret environment variable. With `TRMNL_ENABLED=true`, a background worker sends bounded JSON via HTTPS on a configurable interval (example 5m). The worker is disabled by default; no inbound network exposure, image publishing, or TRMNL-specific API route is required. Validate webhook destinations to prevent SSRF: require HTTPS, prohibit userinfo and unsafe redirects, reject loopback/private/link-local/reserved IPs after DNS resolution and on connection, or implement a narrowly scoped verified allowlist for official destinations. Do not log the secret URL. Implement timeout, limited retries, jitter, `429`/Retry-After, request deduplication, bounded queues, and graceful shutdown.

**Data shape:** `generated_at`, `collected_at`, `stale`, `station` (public ID/name), `departures` (line, destination, scheduled/predicted time, delay seconds or null, cancelled, platform or null), and `source`. Include explicit timezone offsets and display freshness. A snapshot must never be labeled realtime if stale; skip publishing when no successful fetch exists. Station list and maximum departures are configurable; avoid sending unrelated stations. Cache freshness and publisher cadence are independent; the 5m example is not a promise of realtime accuracy.

**Pull (later):** existing generic JSON API can be consumed by a user-managed authenticated gateway. Never enable public binding, Ingress, or port forwarding automatically.

**Files:** `internal/integrations/trmnl/{config,client,payload}.go` and tests; `integrations/trmnl/{README.md,settings.yml,full.liquid,half_horizontal.liquid,half_vertical.liquid,quadrant.liquid}`. Verify current official TRMNL webhook schema and Liquid template conventions before writing. Add mock-server tests for HTTP errors, stale data, payload bounds, secrets redaction, and disabled-by-default behavior.

```yaml
integrations:
  trmnl:
    enabled: false
    mode: push
    webhook_url_env: TRMNL_WEBHOOK_URL
    interval: 5m
    stations: [] # operator-selected VBB station IDs
    max_departures: 8
```

**M5 acceptance:** user can configure a private plugin and see departures on TRMNL without exposing the local service; the API remains fully functional with the integration off or disconnected. External transmission occurs only with explicit operator opt-in.

## 18. Reference repository and implementation guidance

Review https://github.com/cpanato/pr-inbox for repo organization, Makefile, security documentation, CI and release conventions; inspect actual current files before reusing anything. Prefer current patched stable Go and up-to-date verified tool versions. Workflows: PR/push validation, Go tests, race tests, vet, lint, govulncheck, Dependabot for Go and Actions, pinned action SHAs, least-privilege permissions, no `pull_request_target` for untrusted code. CI may build containers but MUST NOT push images, publish releases, deploy or tag automatically. No public listeners or ingress by default. TRMNL is M5, after core M1–M4.
