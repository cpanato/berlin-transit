# Architecture and verification

The CLI consumes a small provider interface. `internal/domain` contains only
provider-independent data; `internal/provider/vbb` owns transport, upstream DTOs,
validation and normalization. Time is injected into the client for deterministic
tests. No empty packages, plugin registry, database, UI or dashboard adapter are
needed. The service layer consumes a two-method provider interface. Retrieval
results carry cache policy/validators separately in `internal/provider`, keeping
HTTP-specific metadata out of domain models. The original CLI output is preserved.

## Verified upstream contract (2026-10-18)

Official sources: [Go release feed](https://go.dev/dl/?mode=json),
[VBB API documentation](https://v6.vbb.transport.rest/api.html).
Go 1.27.2 is the latest stable patch in the feed and the installed toolchain.
The runtime uses only the standard library; separate CI tools and their official
sources are documented in [ci.md](ci.md). The Go release feed and VBB documentation
were rechecked on 2026-10-19; Go 1.27.2 remains the latest patched stable release.

Conservative live GET checks revealed an incompatibility in the proposed design:
`/stations?query=Alexanderplatz&limit=1` returned an object of IFOPT station IDs
such as `de:11000:900100003` (and more than the requested limit).
`/stops/de:11000:900100003/departures` returned HTTP 500.
The documented `/locations?query=Alexanderplatz&results=1&stops=true&addresses=false&poi=false`
returned a JSON array with stop ID `900100003`, accepted by the departures route.
Search therefore uses `/locations`, preserving its returned ID verbatim. No ID
conversion or station-specific fallback is used.

Departures use `/stops/{escapedID}/departures` with `results`, `duration`, optional
`direction` and all seven product booleans set explicitly. Normalized mode comes
from `line.product` (e.g. `suburban`), not `line.mode` (`train`). Unknown JSON
fields are ignored for forward compatibility, while invalid timestamps, missing
departure collections, malformed/trailing JSON and oversized bodies fail closed.
The provider now supports conditional retrieval: validated 304 responses reuse
the normalized snapshot, while other non-200 responses become sanitized typed
errors. No raw upstream body or query URL enters service errors or logs.

## Repository reference

The exact reference URL is provided in section 18 of the design:
https://github.com/cpanato/pr-inbox . Its current Makefile and SECURITY.md were
read on 2026-10-18. M1 follows its simple `cmd/<binary>` layout, local `dist/`
build target, standard Go test target and private vulnerability reporting
guidance. Before adding the requested CI, its current `ci.yaml`, `zizmor.yaml`
and `release.yaml` workflows were also reviewed on 2026-10-18. CI follows its
pinned Actions, disabled checkout credentials and cross-build matrix. Frontend
and release automation are inapplicable; no release workflow is copied.

## Verification scope

Tests are offline and use `httptest.Server` through an unexported constructor;
production construction accepts only the allowlisted origin. DNS validation is
tested separately without resolving or dialing external hosts. No unsafe
upstream override is exposed in the CLI or production constructor.

M1 verification passed on Go 1.27.2: formatting, `go vet ./...`,
`go test -coverprofile=coverage.out ./...`, `go test -race ./...`, `go mod tidy`,
and `go build -trimpath`. Statement coverage is 87.4% for the VBB client and
89.8% for the command package (100% for collection/configuration paths; `main`
is not invoked in unit tests). Both validation and departure JSON fuzz targets
passed five-second runs. The built CLI also successfully performed one live
search and one single-result departures request using the restricted production
transport. These live checks are manual, separate from the offline test suite.

## M2 service

`serve` adds a loopback-only HTTP API, `internal/service`, `internal/cache`,
validated environment configuration and local observability. There is no database
or framework dependency. The CLI still makes an uncached on-demand request.

The cache is a mutex-protected LRU bounded by entry count and payload/key bytes.
Snapshots are JSON bytes cloned on insertion/retrieval; consumers decode their
own domain objects, so nullable field pointers cannot mutate other responses.
Keys hash provider, operation and every canonical result-affecting parameter.
TTL differs for search/departures; upstream restrictions can only shorten it.
Expired snapshots may remain bounded for validators, but stale responses require
a transient failure and an age since last success below the configured maximum.

There are at most 16 active distinct fetches and 128 waiting callers. Shared
fetches have their own timeout, independent of any one caller; when all callers
cancel, the fetch is canceled. Shutdown cancels workers and waits for completion.
The shared rate budget has burst one, limits requests to 30/minute by default,
and applies to retries. At most two retries use exponential backoff/jitter within
the overall timeout. Retry-After establishes a shared cooldown, without a queue
or a long sleeping worker. Cache policy, cache age and retry time are bounded.

HTTP requests have strict query/body/length limits, fixed operational routes,
Host/Origin checks and independent admission control. Logs use generated request
IDs, route categories, duration and sanitized error categories; searches, station
IDs, query strings, headers and payloads are omitted even at debug level.
Optional metrics contain only process counters/duration with no location labels.

Tests cover expiry/eviction, immutable snapshots, all cache key dimensions,
coalescing, individual/all-caller cancellation, limits, stale boundaries,
Retry-After, cache restrictions, 304 validation and HTTP lifecycle. The end-to-end
test runs HTTP -> service -> VBB against a local mock upstream. Fake clocks test
time-dependent service behavior without wall-clock TTL sleeps; no live VBB
access is required. A query-parser fuzz target is added to CI.

M2 includes no scheduled collection, TRMNL adapter, multi-station endpoint,
deployment or publication. Those remain separate follow-ups.

## M4: multiple stations

The HTTP adapter composes up to five concurrent calls to the existing small
collector interface, after validating the entire batch. It retains request order
and reports per-station data/errors. There is no domain/provider API expansion,
new runtime dependency, batch cache, polling loop or display-specific model.
Single-station and batch callers share cache keys, coalescing, the global
burst-one budget and stale policy. Parallel reads keep the request within the
existing fetch timeout instead of multiplying it by the station count; each
worker uses the request context and is joined before the handler returns.
Per-worker panic recovery retains sanitized HTTP error handling.

Tests cover list validation before collection, five-station bounds, ordering,
shared options/defaults, mixed successes/errors, freshness preservation,
all-failed statuses, retry metadata, cancellation and panic sanitization. An
offline HTTP-to-service-to-mock-VBB test verifies shared single/batch cache use,
result-affecting keys and rate-budget enforcement. The existing race, coverage,
lint and security checks apply. Go 1.27.2 was reverified against the official
release feed on 2026-10-19; existing verified tool pins remain unchanged.
Optional deployment examples are deferred. No services are exposed publicly,
and no image publication, release or deployment is introduced.

## M5: opt-in collection and TRMNL output

`internal/collector` rotates 1–5 configured stations through the existing service,
using the shared cache, coalescing, timeout and upstream budget. Its minimum
rotation interval is validated against the budget; requests remain spaced and
retries cannot create another burst. Each worker is synchronous, with bounded
state and cancellation-aware sleeps. Both workers are attached to the `serve`
lifecycle and joined on shutdown, including unexpected HTTP server termination.
Neither runs for one-shot CLI queries or by default.

`Service.CachedDepartures` exposes immutable normalized cache reads without a
provider call or freshness extension. The isolated `internal/integrations/trmnl`
adapter accepts only this cache-reader interface, projects selected fields into
bounded merge variables, and POSTs replacement snapshots only with explicit
opt-in. Eligibility respects cache lifetime, no-store/no-cache/must-revalidate
and maximum stale age; unavailable stations are reported, with no publication
if none has usable data. Freshness remains per station.

`internal/netguard` holds the existing public-IP validation and validated-literal
dial behavior shared by VBB and TRMNL. Each client keeps a fixed hostname,
disables proxies and redirects, and verifies TLS normally. The adapter accepts
only the official `trmnl.com/api/custom_plugins/<token>` HTTPS webhook pattern;
errors/logs/configuration diagnostics redact its secret URL. No arbitrary host
or test override exists in production.

The TRMNL worker has one in-flight request, latest-content replacement,
content/freshness deduplication, bounded retries on later ticks and Retry-After
cooldown. Its minimum interval includes retries and conservatively targets the
standard account quota; restart loses in-memory budget/dedup state. Generation
time alone does not force sends. No adapter-specific names enter domain models,
HTTP routes, provider interfaces or collector configuration.

The four private-plugin Liquid layouts and import settings live under
`integrations/trmnl`; `make trmnl-package` creates a local-only flat ZIP. The
[adapter guide](../integrations/trmnl/README.md) documents the payload, explicit
outbound consent, virtual-device preview and live operator smoke test. Official
TRMNL webhook, private-plugin, template and import docs and Go 1.27.2 were
rechecked on 2026-10-19. No new Go dependencies or CI tools are introduced.
