# M1 architecture and verification

The CLI consumes a small provider interface. `internal/domain` contains only
provider-independent data; `internal/provider/vbb` owns transport, upstream DTOs,
validation and normalization. Time is injected into the client for deterministic
tests. No empty packages, plugin registry, database, UI or dashboard adapter are
needed. M2 can consume the same provider methods from a service layer.

## Verified upstream contract (2026-10-18)

Official sources: [Go release feed](https://go.dev/dl/?mode=json),
[VBB API documentation](https://v6.vbb.transport.rest/api.html).
Go 1.27.2 is the latest stable patch in the feed and the installed toolchain.
The only development tools used are Go's bundled build, fmt, vet, test, race,
coverage and fuzz tools. No external dependency/tool versions are selected.

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
Non-200 responses, including 304 and 429, are sanitized errors in M1. Conditional
requests, Retry-After scheduling, retries and cache policy are M2 work.

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

The collector has no HTTP listener, cache, deployment or publishing. Requested
CI/security gates are documented separately in [ci.md](ci.md); they advance the
quality checks from M3 without implementing M2 or deployment functionality.
There is no full-service API acceptance claim in M1.
