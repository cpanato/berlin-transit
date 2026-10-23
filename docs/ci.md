# CI and security checks

`ci.yml` and `security.yml` run for pull requests targeting `main` and pushes to
`main`. Security checks also support manual dispatch and run weekly on Monday
at 06:23 UTC. Jobs use read-only repository permissions, disabled checkout
credentials, timeouts and redundant-run cancellation. They perform no live VBB
or TRMNL queries, registry pushes or deployments.

CI checks formatting, vet, unit/integration tests, race tests, fuzzing, module
integrity/tidiness and workflow syntax. Coverage is reported through Go's native
coverage profile and retained as an artifact, with no custom percentage gate.
Helm/Compose policy tests use
Go and the external tools' own parsers, with no YAML dependency in the runtime.
The flat TRMNL import ZIP is built locally but never sent to TRMNL.

## Maintained actions and tools

Prefer upstream actions over repository-managed installers. Actions are pinned
to immutable full commit SHAs, verified against their official tag APIs on
2026-10-23. Dependabot proposes weekly Actions/module/Docker updates; it does
not merge automatically. There is no custom tool-download script or checksum
catalog, and local tools are resolved from PATH (Make variables can override).

| Purpose | Action / tool | Version | Official source |
| --- | --- | --- | --- |
| Checkout | actions/checkout | v7.0.1 | [Release](https://github.com/actions/checkout/releases/tag/v7.0.1) |
| Go | actions/setup-go | v7.0.0 | [Release](https://github.com/actions/setup-go/releases/tag/v7.0.0) |
| Artifacts | actions/upload-artifact | v7.0.2 | [Release](https://github.com/actions/upload-artifact/releases/tag/v7.0.2) |
| Go lint | golangci/golangci-lint-action | v9.3.0 / tool v2.15 | [Release](https://github.com/golangci/golangci-lint-action/releases/tag/v9.3.0) |
| Vulnerabilities | golang/govulncheck-action | v1.1.0 | [Release](https://github.com/golang/govulncheck-action/releases/tag/v1.1.0) |
| Snapshots | goreleaser/goreleaser-action | v7.3.0 / tool v2.19.0 | [Release](https://github.com/goreleaser/goreleaser-action/releases/tag/v7.3.0) |
| Helm | azure/setup-helm | v5.0.1 / tool v4.3.1 | [Release](https://github.com/Azure/setup-helm/releases/tag/v5.0.1) |
| Workflow lint | actionlint via `go run` | v1.7.12 | [Release](https://github.com/rhysd/actionlint/releases/tag/v1.7.12) |
| Static security | gosec via tools module | v2.29.0 | [Release](https://github.com/securego/gosec/releases/tag/v2.29.0) |

The official govulncheck action installs its current tool release and scans
`./...`; checkout is explicitly disabled inside it to preserve the existing
credential-free checkout. Go is selected through the stable channel with latest
patch checks and caching disabled. Workflow lint has no upstream action in the
selected actionlint release, so it runs as a versioned Go command instead.

The upstream gosec action/container does not support Go 1.27 export data. We
build gosec with the compatible, reviewed `golang.org/x/tools` v0.52.0 importer
in the separate `tools/` module. Its transitive gRPC remains pinned to v1.84.0
to address CVE-2026-84445. That module has its own lockfile and uses the Go public
checksum database; the application module has no external runtime dependencies.

Secret scanning and push protection are provided by the repository's native
GitHub settings (both confirmed enabled). The zizmor workflow separately reviews
Actions security. No duplicate secret scanner or container scanner is configured.

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
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
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
alongside the separate coverage artifact. No release workflow is configured.

`make compose-check` copies only `compose.yaml` and `.env.example` into a temporary
directory and calls `docker compose config --format json`. Go assertions verify
loopback publication, safe environment overrides, raw literal handling, restricted
container settings, bounded resources/logs, healthcheck and disabled workers. The
check never reads an operator `.env`, starts a container or needs a running Docker
daemon. Compose 2.30+ is needed for raw environment parsing.

Helm lint and Go render tests cover positive/negative fixtures, network boundaries,
existing Secret references, disabled workers and rejected public service options.
The chart is packaged locally and never installed by CI. The separate Dockerfile
remains an optional Compose path with a digest-pinned Go builder and scratch
runtime. See [packaging policy](releases.md) and [deployment](deployment.md).
