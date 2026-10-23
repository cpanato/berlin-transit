# CI and security checks

`ci.yml` and `security.yml` run for pull requests targeting `main` and pushes to
`main`. Security checks also support manual dispatch and run weekly on Monday at 06:23 UTC. Both use
read-only repository access, disabled checkout credentials, job timeouts,
redundant-run cancellation, no secrets and no telemetry export. They execute
no live VBB or TRMNL queries. Dependency/tool downloads and vulnerability database reads
require outbound HTTPS; source analysis happens on the runner.

CI checks formatting, vet, tests, race tests, fuzzing, module integrity/tidiness,
golangci-lint configuration and analysis, and actionlint. Core executable
packages under `internal/` must each reach 80% statement coverage, including
shared domain validation. The CLI is
measured but excluded from that gate because `main` exits the process. Cross
builds cover Linux, macOS and Windows on amd64 and arm64 with CGO disabled.
Fuzz targets exercise domain input validation, upstream JSON and HTTP query parsing.
CI also builds the flat private-plugin import ZIP with `make trmnl-package`; this
bundle is only local to the runner and is not uploaded or sent to TRMNL.

Security checks run govulncheck, gosec, Gitleaks over full Git history, and
Trivy filesystem vulnerability, misconfiguration, secret and license checks.
Trivy blocks HIGH/CRITICAL findings, including vulnerabilities without fixes;
there are no blanket scan exclusions or ignored scanner failures. It also
scans the local image and generates a CycloneDX SBOM. Coverage and SBOM are
retained as workflow artifacts for seven days, with no registry uploads,
release creation, deployments or public services. There is no release workflow.

## Verified versions and sources

Versions were checked against the official release/tag APIs on 2026-10-18.
Actions tags were resolved to immutable commit SHAs through each repository's
Git references API. Go 1.27.2 was rechecked against the
[official Go feed](https://go.dev/dl/?mode=json).

| Component | Version | Official source |
| --- | --- | --- |
| checkout | v7.0.1 | [release](https://github.com/actions/checkout/releases/tag/v7.0.1) |
| setup-go | v7.0.0 | [release](https://github.com/actions/setup-go/releases/tag/v7.0.0) |
| upload-artifact | v7.0.2 | [release](https://github.com/actions/upload-artifact/releases/tag/v7.0.2) |
| golangci-lint | v2.15.0 | [release](https://github.com/golangci/golangci-lint/releases/tag/v2.15.0) |
| actionlint | v1.7.12 | [release](https://github.com/rhysd/actionlint/releases/tag/v1.7.12) |
| govulncheck | v1.1.4 | [release](https://github.com/golang/vuln/releases/tag/v1.1.4) |
| gosec | v2.29.0 | [release](https://github.com/securego/gosec/releases/tag/v2.29.0) |
| golang.org/x/tools | v0.52.0 | [tag](https://github.com/golang/tools/tree/v0.52.0) |
| gRPC (scanner dependency) | v1.84.0 | [release](https://github.com/grpc/grpc-go/releases/tag/v1.84.0) |
| Trivy | v0.76.1 | [release](https://github.com/aquasecurity/trivy/releases/tag/v0.76.1) |
| Gitleaks | v8.30.1 | [release](https://github.com/gitleaks/gitleaks/releases/tag/v8.30.1) |

`scripts/install-tool.sh` supports Linux/amd64 CI and macOS/arm64 local checks.
It downloads the selected official release archive and verifies a SHA256 pinned
in `scripts/tool-checksums.txt`, copied from the official release checksums. Only
the named executable is extracted. For updates, compare official release notes,
Go compatibility and checksums, update both files, then rerun the checks.
Dependabot proposes grouped weekly updates for Actions, both Go modules and the
Docker base. No automatic merging is configured. Release archive tool pins
must be reviewed and updated manually.

Packaging tools and Trivy were rechecked on 2026-10-23; current GoReleaser,
ko and Helm pins are recorded in [packaging policy](releases.md).

The collector module still has no external runtime dependencies. `tools/` is a
separate development-only module with its own lockfile. It builds gosec and
govulncheck with Go 1.27.2. The official gosec v2.29.0 archive and its unchanged
source dependencies fail to read Go 1.27 export data (version 5). The isolated
module explicitly pins the official stable `golang.org/x/tools` v0.52.0 to
provide the compatible importer, without changing collector dependencies or
ignoring scanner errors. Go module downloads use the public checksum database.
The tools module also upgrades transitive gRPC to the verified stable v1.84.0:
Trivy detected CVE-2026-84445 in the scanner's original v1.83.1 dependency.
The update fixes that finding without suppressing it.

To reproduce the source checks locally:

```sh
make check
make coverage
go mod verify
go -C tools mod verify
sh scripts/install-tool.sh golangci-lint
sh scripts/install-tool.sh actionlint
sh scripts/install-tool.sh gitleaks
mkdir -p .tools/bin
go -C tools build -o ../.tools/bin/gosec github.com/securego/gosec/v2/cmd/gosec
go -C tools build -o ../.tools/bin/govulncheck golang.org/x/vuln/cmd/govulncheck
.tools/bin/golangci-lint config verify
.tools/bin/golangci-lint run
.tools/bin/actionlint
.tools/bin/gosec ./...
.tools/bin/govulncheck ./...
.tools/bin/gitleaks git --redact --no-banner .
```

## Container build validation

The optional Compose Dockerfile builds from the official `golang:1.27.3-bookworm` image pinned to
the manifest digest verified with `docker buildx imagetools inspect`. The runtime
uses Docker's built-in empty `scratch` filesystem, carrying only the static CLI
and CA trust bundle, and runs as UID/GID 65532. Timezone data is embedded in Go.
There are no exposed ports or shell. The Docker context allows only module and
collector source files, excluding Git metadata, tools, tests and local artifacts.

CI tests execution with `--network none --read-only --cap-drop ALL`,
`--security-opt no-new-privileges:true`, Docker's default seccomp profile and
memory/CPU/PID limits. It intentionally supplies invalid configuration and
verifies the expected startup error, so no upstream request occurs. Image
metadata is checked for the non-root user and absence of exposed ports.
Runtime restrictions belong in `docker run` or the deployment runtime and cannot
be enforced by an image alone. CI exercises the ko snapshot image described below;
the Dockerfile remains an alternative local Compose build. No container is deployed or kept running.

The container job also runs `python3 scripts/check-compose.py`, using only a
copy of `.env.example` in a temporary directory. It resolves Compose and checks
loopback-only publication, Host-related environment overrides, disabled workers,
non-root/read-only/capability restrictions, health command, bounded resources,
NAT bridge settings and local logging. It never reads an operator `.env`, starts
Compose, publishes a port or sends to VBB/TRMNL. Python uses only its standard
library. Installed Docker Engine 29.8.2 and Compose 5.6.0 were checked against
official Docker release notes on 2026-10-20. Go 1.27.3 and the builder image's
immutable digest were verified from the official Go feed and Docker Hub tag API.

## Local packaging checks

The security/container job installs checksum-pinned GoReleaser and Helm. One
GoReleaser snapshot replaces the separate binary build matrix, covers all six
Linux/macOS/Windows amd64/arm64 targets and builds both Linux ko variants. It
reuses the daemon-local snapshot for network-disabled container checks and Trivy.
The chart is linted, rendered against positive/negative Go policy fixtures and
packaged locally. CI never runs Helm install, creates a cluster, creates a GitHub
release or pushes a registry image. See [packaging](releases.md).
