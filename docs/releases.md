# Local packaging and release policy

The packaging follows the local snapshot pattern reviewed in
[cpanato/tado-bridge](https://github.com/cpanato/tado-bridge). Merging code builds
and tests it; no workflow creates tags, GitHub releases, registry pushes or
Kubernetes deployments. There is no publishing workflow or write permission.

## Verified toolchain

Versions checked against official sources on 2026-10-23:

| Tool | Version | Official source |
| --- | --- | --- |
| Go | 1.27.3 | [Download feed](https://go.dev/dl/?mode=json) |
| GoReleaser | 2.19.0 | [Release](https://github.com/goreleaser/goreleaser/releases/tag/v2.19.0) |
| ko | 0.19.1 | [Release](https://github.com/ko-build/ko/releases/tag/v0.19.1) |
| Helm | 4.3.1 | [Release](https://github.com/helm/helm/releases/tag/v4.3.1) |
| Trivy | 0.76.1 | [Release](https://github.com/aquasecurity/trivy/releases/tag/v0.76.1) |

Install the reviewed binaries into ignored `.tools/bin/`:

```sh
sh scripts/install-tool.sh goreleaser
sh scripts/install-tool.sh ko
sh scripts/install-tool.sh helm
```

The installer verifies committed SHA-256 values from official release assets
before extracting a single executable. It supports Linux amd64 and macOS arm64.
No build tool is added to the runtime Go module. Review versions and checksums
explicitly when upgrading; do not replace pins with `latest` downloads.

## Snapshots and images

```sh
make release-snapshot
make image
make image-multiarch
make helm-check helm-package
```

GoReleaser builds Linux/macOS/Windows amd64/arm64 binaries, archives and checksums
under ignored `dist/`. Windows archives use ZIP; other platforms use tar.gz.
`release.disable: true` and `--snapshot --skip=publish,sign` keep releases disabled.
Its built-in [ko integration](https://goreleaser.com/customization/package/ko/)
builds both Linux image variants and imports **`ko.local:snapshot`** into the local
Docker daemon. Daemon import selects a platform variant; CI exercises amd64.
The standalone ko target `make image` imports **`ko.local/berlin-transit:local`**
for the host architecture (`PLATFORM=linux/amd64` or `linux/arm64` overrides it).
`make image-multiarch` uses `--push=false`, an `example.invalid` destination and
writes both platforms to the local OCI layout `dist/oci` without a registry push.

Docker must be running for daemon imports. Building may fetch the public base
image; no runtime VBB/TRMNL request is part of these targets. ko packages only the
compiled binary and optional main-package `kodata` (none exists here), rather
than copying the repository or operator `.env` into layers. Builds disable CGO,
trim paths and run as 65532:65532. Both `.ko.yaml` and `.goreleaser.yaml` use
`cgr.dev/chainguard/static:latest` with CA certificates, following the operator's
explicit choice to use a floating base tag. This is an exception to the design's
digest-pinning default: rebuilding the same commit can resolve a different base.
Keep both configurations synchronized and scan the resulting image on every build.
`org.opencontainers.image.created` records build time in UTC; standalone targets
accept an explicit `BUILD_DATE` for reproducibility. GoReleaser also generates a
local SPDX image SBOM; Trivy retains its CycloneDX report as a CI artifact.

ko images use entrypoint **`/ko-app/berlin-transit`**. The Helm chart targets these
images. The existing Dockerfile/Compose path remains a separate local option with
entrypoint `/berlin-transit`; its Compose healthcheck must not be used unchanged
with a ko image. See [Compose operations](deployment.md).

CI uses one GoReleaser snapshot for all six binary targets and both Linux image
variants, then scans and exercises the daemon-local image with networking disabled.
The Helm checks render manifests, verify security/secret/egress policies and package
locally; they do not install a chart or access a cluster. No credentials are needed
for these tests. Actual installation, CNI enforcement, scheduling and arm64 runtime
behavior still require operator acceptance in an explicitly authorized environment.

`release-snapshot --clean` removes previous `dist/` outputs; package Helm and TRMNL
after the snapshot if you want all local artifacts together.

## Future publishing

A Git tag, release, image/chart publication or deployment requires a separate
explicit instruction naming the action and target. Before adding that path,
review the version, intended commit, destination, signing/provenance, SBOM and
passing checks. No registry location or released image is assumed by the chart.
