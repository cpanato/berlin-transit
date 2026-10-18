# berlin-transit

A small, display-independent public transport collector in Go. M1 searches VBB
stations and fetches normalized departures for one station, as JSON on stdout.
S-Bahn (`suburban`) is the default mode. Station IDs are opaque strings; no station
is hardcoded. No server, telemetry, background polling, or publishing runs.

Requires Go **1.27.2**, verified against the [official stable release feed](https://go.dev/dl/?mode=json)
on 2026-10-18. The module uses only the Go standard library and bundled timezone
data, with no external Go runtime dependencies. Consequently
there is no `go.sum`. The Go directive selects the verified patched minimum;
use the latest patched stable Go for development.

```sh
make check
make build
./dist/berlin-transit stations -query Alexanderplatz -limit 5
# Copy an ID from search into the next command:
./dist/berlin-transit departures -station 900100003 -limit 10 -duration 30
```

These explicit commands call the configured third-party VBB HTTPS upstream. They
open no listening socket or public endpoint. Results stay on stdout unless the
operator redirects them. Automated tests use local mock servers and need no VBB
access. GitHub Actions test and build the collector and a local container image;
they never publish images, create releases, or deploy anything. See
[CI and security checks](docs/ci.md) for tools, coverage and artifact retention.

Departures accept `-mode` (`suburban`, `subway`, `tram`, `bus`, `ferry`, `express`,
`regional`) and `-direction` (a provider stop ID, not destination text). Search
text is limited to 200 bytes, IDs to 256 bytes, results to 1–50 and duration to
1–120 minutes. Missing required inputs fail before any upstream request.

`BERLIN_TRANSIT_HTTP_TIMEOUT` defaults to `10s` and must be positive and at most
`1m`. `BERLIN_TRANSIT_VBB_BASE_URL` defaults to `https://v6.vbb.transport.rest`;
only that exact HTTPS origin is accepted (an optional trailing slash is allowed).
There is no production development override. Environment variables configure
transport; command flags configure queries, so their settings do not overlap.
The client disables proxies and redirects, rejects private DNS answers and dials
validated IPs directly. Responses are capped at 2 MiB with bounded connections.
Errors omit search text, URLs, and upstream payloads. Interrupt/termination
signals cancel the request. There are no credentials.

JSON uses RFC3339 times in `Europe/Berlin`, including DST offsets. Unknown
times, delay, platform, line, direction, station name and provenance are `null`.
Known zero delay is preserved. `predicted_at` requires both `when` and a known
delay and is absent for cancellations; a timetable `when` alone does not prove
realtime. `realtime` means at least one returned departure has a prediction.
The upstream update timestamp is exposed separately as `realtime_updated_at`.
`fetched_at` records completion of the fetch. M1 has no cache: `stale` is always
false. An empty departures result keeps the requested ID with a null name.

The [VBB REST wrapper](https://v6.vbb.transport.rest/) is an unofficial third-party
service. Availability, terms and attribution requirements can change; it is not
a guarantee of realtime accuracy. Consult its [API documentation](https://v6.vbb.transport.rest/api.html).
Avoid polling aggressively; the documented upstream limit is 100 requests/minute.
M1 makes one request per command, with no retries or automatic polling. The
shared request budget, caching and stale-on-error handling belong to M2.

See [architecture and verification notes](docs/architecture.md) and the
[design](docs/design/berlin-transit-design.md). HTTP API/health endpoints,
cache, rate limiting and service lifecycle remain M2; the requested quality
gates from M3 are included ahead of M2. Multi-station and optional private
deployment remain M4. No publication or deployment is authorized.
