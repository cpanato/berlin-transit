# Private API

Start explicitly with `./dist/berlin-transit serve`. The default listener is
`127.0.0.1:8080`. Only literal loopback IPv4/IPv6 addresses are accepted by default;
the explicit container-only listener exception is described below. There is no
authentication, CORS or public proxy. By default, requests use the listener's exact Host
(or `localhost` with the same port). Cross-origin browser requests are rejected.
The outbound VBB HTTPS request is necessary for uncached queries and is separate
from publishing or exposing this service.

## Endpoints

All routes accept `GET` only and no request body. Unknown routes return 404;
other methods return 405 with `Allow: GET`. JSON responses use stable field names,
`Content-Type: application/json`, `Cache-Control: no-store`, `nosniff`, and a
server-generated `X-Request-ID`. No incoming request ID is trusted or logged.

| Route | Parameters / response |
| --- | --- |
| `/api/v1/stations` | Required `query`, optional `limit` (default 10). Returns `stations`, `source`, `fetched_at`, `stale`, and stale metadata when applicable. |
| `/api/v1/stations/{id}/departures` | Optional `mode` (default `suburban`), `limit` (10), `duration` (30 minutes), `direction` (provider stop ID). Returns a normalized departures group. |
| `/api/v1/departures` | Required comma-separated `stations` (1–5 distinct IDs); the same options as single-station departures, applied to every station. Returns ordered per-station results; see below. |
| `/healthz` | Liveness: `{"status":"ok"}`, independent of VBB. |
| `/readyz` | Initialized: `{"status":"ready"}`. Does not fail for an upstream outage; becomes not ready during shutdown. |
| `/metrics` | Disabled by default (404). When enabled, local Prometheus text counters/duration, without station/query labels. |

There is no debug/pprof route. Query bounds: search text 1–200 bytes after trimming, opaque IDs 1–256
bytes, results 1–50, duration 1–120 minutes. Modes are `suburban`, `subway`, `tram`,
`bus`, `ferry`, `express`, `regional`. Direction is a stop ID, not destination
text. Control characters, unsafe path segments and whitespace in IDs are rejected.
Duplicate, empty, unknown or malformed query parameters are rejected before
upstream access. Explicit `limit=0` / `duration=0` are invalid, not defaults.
The HTTP query is limited to 2048 bytes, decoded path to 1024 bytes and request
target to 3072 bytes. Server headers, body read, write and idle time are bounded.

Example fresh departures response:

```json
{
  "station": {"id": "900100003", "name": "S+U Alexanderplatz"},
  "departures": [{
    "line": "S7", "mode": "suburban", "direction": "Potsdam Hbf",
    "planned_at": "2026-10-19T12:00:00+02:00",
    "predicted_at": "2026-10-19T12:03:00+02:00",
    "delay_seconds": 180, "platform": "2", "planned_platform": "2",
    "cancelled": false, "provenance": null
  }],
  "source": "vbb", "fetched_at": "2026-10-19T11:59:00+02:00",
  "realtime_updated_at": null, "realtime": true, "stale": false
}
```

Times use RFC3339 and Europe/Berlin offsets, including DST. Unknown fields stay
null, including unknown vs known zero delay. Predictions require evidence of a
known delay and are absent for cancellations. `realtime` means at least one
returned departure has a prediction, and is always false for stale responses.

## Multiple stations

```sh
curl 'http://127.0.0.1:8080/api/v1/departures?stations=900100003,900003201&mode=suburban&limit=5'
```

`stations` contains 1–5 distinct, nonempty IDs separated by commas, without
whitespace. Commas are separators, including URL-encoded commas; IDs containing
a comma must use the single-station route. Duplicates and invalid IDs/options
reject the entire request with 400 before any collector call. `limit` applies
per station, not to the combined response (at most 250 departures).

The response preserves input order. Each item has `station_id`, `status`, and
exactly one of `data` (the complete single-station departures response) or
`error` (the same sanitized code/message used by single-station errors).
Retryable errors also include bounded `retry_after_seconds` (1–86400).

```json
{
  "results": [
    {"station_id": "900100003", "status": 200, "data": {
      "station": {"id": "900100003", "name": null}, "departures": [],
      "source": "vbb", "fetched_at": "2026-10-19T12:00:00+02:00",
      "realtime_updated_at": null, "realtime": false, "stale": false
    }},
    {"station_id": "900003201", "status": 429,
     "error": {"code": "rate_limited", "message": "Too Many Requests"},
     "retry_after_seconds": 2}
  ],
  "partial": true
}
```

HTTP status is 200 if any station succeeds, including an eligible stale result.
`partial` is true only when successes and errors coexist; inspect every result.
If all stations fail, the response still contains `results` and `partial=false`:
a shared per-station status becomes the HTTP status; differing statuses give
503. An all-failed response includes `Retry-After` when any item supplies it,
using the largest per-item delay. Partial success carries retry delays on the
individual errors rather than the HTTP header.

At most five station reads run concurrently per admitted HTTP request, under
the existing handler and service concurrency bounds. Reads share the same
single-station cache, request coalescing, cancellations, timeout and upstream
budget; there is no separate batch cache, expanded burst or automatic polling.
An individual failure does not discard other stations. Freshness is per station,
including `stale`, `realtime`, `last_success_at` and `stale_age_seconds`.

With the default burst-one budget, several cold stations usually cannot all be
fetched in one call. Which uncached station gets the available slot is not
guaranteed. Warm individual caches or retry failed stations after their stated
delay. Retrying a whole batch reuses successful cached entries while they remain
fresh. A batch creates no delayed queue or additional retry policy.

## Cache and resilience

The process shares a bounded in-memory LRU across station search and departures;
it has no cross-process consistency. Defaults: 256 entries, 16 MiB of payload/key
bytes, 24h search TTL, 30s departures TTL. Heap overhead is additional but bounded
by entry/concurrency limits. Each normalized JSON snapshot is capped at 2 MiB,
separately from the raw upstream limit, including JSON escaping expansion.
Every result-affecting option is included in the key.
Equal concurrent requests share one upstream fetch; individual cancellations do
not cancel other callers. All-caller cancellation cancels that fetch.

Upstream `max-age` minus `Age` can shorten the configured TTL, never extend it.
`no-store` prevents storage and deletes an older snapshot; `no-cache` requires
revalidation for each access and forbids stale fallback; `must-revalidate` also
forbids stale fallback. ETag/If-None-Match and 304 reuse validated snapshots.
A 304 without a sent validator or available snapshot fails closed.

Snapshots may stay retained for their effective TTL plus `MAX_STALE` to support
conditional validation. Retention alone never permits a stale response:
`MAX_STALE` is an absolute age since the last successful fetch/revalidation,
not additional time after expiry. With the default 5m, snapshots older than 5m
cannot be served stale, including search snapshots with a 24h fresh TTL.
Set `MAX_STALE=0s` to disable stale fallback entirely.

Stale fallback is allowed only for transient upstream/network/timeouts or local
rate-budget exhaustion, never for malformed payloads or not-found responses.
It includes `stale=true`, `realtime=false`, `last_success_at` and
`stale_age_seconds` (age since last success). `fetched_at` remains that last
successful fetch time. A successful 304 updates that time while preserving the
upstream realtime-update timestamp. No success means no stale fallback.

The process allows 30 upstream requests/minute by default with burst one
(2-second spacing), at most 16 distinct fetches and 128 waiting callers. Fresh
cache hits bypass the upstream budget. New misses beyond the budget return 429
with `Retry-After`, or eligible stale data. Retries share the same budget; only
transient errors receive at most two retries with exponential backoff/jitter,
all within `HTTP_TIMEOUT`. Upstream Retry-After sets a shared cooldown and returns
promptly; it is parsed as seconds or HTTP date and bounded to 24h.

## Errors and operations

Errors use `{"error":{"code":"...","message":"..."}}`. Messages are fixed
HTTP descriptions, with no input, raw upstream body, URL or stack trace.

| HTTP | Typical code |
| --- | --- |
| 400 | `invalid_request` |
| 403 | `forbidden` (Host/Origin protection) |
| 404 | `station_not_found` or `not_found` |
| 405 | `method_not_allowed` |
| 429 | `rate_limited` or `upstream_rate_limited` (Retry-After supplied) |
| 502 | `bad_gateway` |
| 503 | `upstream_unavailable`, `upstream_timeout`, `unavailable`, `overloaded`, `request_cancelled`, `not_ready` |

Logs contain request ID, fixed route category, status, duration and sanitized
error category. They omit full URLs, queries, station IDs, sensitive headers and
payloads at all log levels. Metrics are opt-in local counters without location
labels. Ctrl+C/SIGTERM stops admission, cancels upstream workers and allows up to
five seconds for HTTP shutdown. There are no analytics or outbound telemetry.

## Configuration

Environment configures transport/service; CLI flags configure one-shot queries.
There are no overlapping settings or HTTP upstream-URL override. Configuration
is validated at startup; errors name the variable without echoing its value.

| Variable (`BERLIN_TRANSIT_` prefix) | Default | Bounds |
| --- | --- | --- |
| `LISTEN_ADDR` | `127.0.0.1:8080` | Literal loopback IP, port 1–65535; e.g. `[::1]:8080`. Exact `0.0.0.0` requires container opt-in. |
| `ALLOW_CONTAINER_LISTEN` | `false` | Explicitly permits IPv4 `0.0.0.0` inside an isolated container network; use only with loopback host publication. |
| `VBB_BASE_URL` | `https://v6.vbb.transport.rest` | Exact allowlisted HTTPS origin; optional trailing slash |
| `HTTP_TIMEOUT` | `10s` | 1ms–1m overall fetch timeout |
| `DEPARTURES_TTL` | `30s` | 1ms–5m |
| `STATIONS_TTL` | `24h` | 1ms–7d (Go duration syntax, e.g. `168h`) |
| `MAX_STALE` | `5m` | 0–24h absolute snapshot age for stale fallback |
| `CACHE_ENTRIES` | `256` | 1–4096 |
| `CACHE_BYTES` | `16777216` | 1–64 MiB |
| `REQUESTS_PER_MINUTE` | `30` | 1–60, shared across retries/search/departures |
| `METRICS_ENABLED` | `false` | Boolean |
| `LOG_LEVEL` | `info` | Standard slog levels (`debug`, `info`, `warn`, `error`) |

The VBB client disables redirects and proxies, validates DNS answers and dials
only validated public IPs with TLS certificate verification. Responses are capped
at 2 MiB, headers at 64 KiB and upstream connections at four. The API server uses
5s read-header, 10s read, `HTTP_TIMEOUT+5s` write and 60s idle timeouts, and a
16 KiB header limit. Optional scheduled collection and TRMNL output are disabled
by default; see the [adapter guide](../integrations/trmnl/README.md) for opt-in
configuration and virtual-device testing. The optional [private Compose example](deployment.md) prepares loopback host
publication with an isolated container listener. Starting it is an explicit
operator action; there is no automatic deployment or image/release publication.

## Container listener and health probe

`BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN=true` additionally permits only literal
IPv4 `0.0.0.0:<port>` (1–65535), using a `tcp4` listener inside an isolated
container network. LAN/public literal addresses, arbitrary hostnames, empty-host
wildcards and IPv6 unspecified addresses remain invalid even with opt-in. The
flag alone keeps the existing default loopback address. Running this exception
on a host or mapping it to a public host interface is outside the private setup.

For the container listener, HTTP Host/Origin checks accept only
`127.0.0.1:<container-port>` or `localhost:<container-port>`. Wildcard/container
IPs, service names and forwarded Host headers are not trusted. The provided
Compose file fixes the same container/host port 8080 and pins host publication to
127.0.0.1. These checks do not replace network isolation or authentication.

`berlin-transit healthcheck` checks the existing `/readyz` over loopback with a
2s timeout, no proxy/redirects and a 1 KiB JSON body bound. It validates
`{"status":"ready"}` and returns zero on success, or a fixed error/nonzero exit.
It converts the explicit container wildcard to 127.0.0.1 and never creates a
VBB client or invokes background workers. No shell/curl is needed in scratch.
