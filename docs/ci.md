# CI and security checks

`ci.yml` and `security.yml` run for pull requests targeting `main` and pushes to
`main`. Security checks also support manual dispatch and run weekly on Monday
at 06:23 UTC. Jobs use read-only repository permissions, disabled checkout
credentials, timeouts and redundant-run cancellation. They perform no live VBB
or TRMNL queries, registry pushes or deployments.

`ci.yml` owns tests, lint and packaging: GoReleaser/ko snapshots, Helm/Compose
validation, restricted container checks and SBOM retention. `security.yml` owns
govulncheck and gosec, including the weekly scheduled analysis.

CI checks formatting, vet, unit/integration tests, race tests, fuzzing, module
integrity/tidiness. Coverage is reported through Go's native
coverage profile and retained as an artifact, with no custom percentage gate.
Helm/Compose policy tests use
Go and the external tools' own parsers, with no YAML dependency in the runtime.
The flat TRMNL import ZIP is built locally but never sent to TRMNL.


Tool versions and action references are defined in the [workflows](../.github/workflows/)
and [tools module](../tools/go.mod), rather than duplicated here.

## Reproduce locally

Install Go, golangci-lint, GoReleaser, ko and Helm through their official
installation instructions or your existing tool manager. Then run:

```sh
make check coverage
make helm-check helm-package compose-check
go mod verify
go -C tools mod verify
golangci-lint config verify
golangci-lint run
mkdir -p .tools/bin
go -C tools build -o ../.tools/bin/gosec github.com/securego/gosec/v2/cmd/gosec
go -C tools build -o ../.tools/bin/govulncheck golang.org/x/vuln/cmd/govulncheck
.tools/bin/gosec ./...
.tools/bin/govulncheck ./...
```

## Packaging checks

One GoReleaser snapshot covers Linux/macOS/Windows amd64/arm64 archives and both
Linux ko image variants. CI reuses the daemon-local snapshot for metadata and
network-disabled execution checks, verifies the non-root UID, absence of exposed
ports and expected sanitized errors. Restrictions include a read-only filesystem,
dropped capabilities, no-new-privileges and memory/CPU/PID limits. ko generates
SPDX SBOMs under `dist/sbom/`, retained as workflow artifacts for seven days,
alongside the separate coverage artifact. A [separate manual release workflow](releases.md)
handles approved signing/publication; ordinary CI never requests a signing identity.

`make compose-check` copies only `compose.yaml` and `.env.example` into a temporary
directory and calls `docker compose config --format json`. Go assertions verify
loopback publication, safe environment overrides, raw literal handling, restricted
container settings, bounded resources/logs, healthcheck and disabled workers. The
check never reads an operator `.env`, starts a container or needs a running Docker
daemon. See [deployment requirements](deployment.md#requirements-and-network-boundary)
for the Compose features this example requires.

Helm lint and Go render tests cover positive/negative fixtures, network boundaries,
existing Secret references, disabled workers and rejected public service options.
The chart is packaged locally and never installed by CI. The separate Dockerfile
remains an optional Compose path with a digest-pinned Go builder and scratch
runtime. See [packaging policy](releases.md) and [deployment](deployment.md).
