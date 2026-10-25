# Local packaging and release policy

The packaging follows the local snapshot pattern reviewed in
[cpanato/tado-bridge](https://github.com/cpanato/tado-bridge). Merging code builds
and tests local snapshots without publishing. The separate
[release workflow](../.github/workflows/release.yml) publishes a GitHub release
and GHCR image, signs them and records build provenance when an operator pushes
a `v*` version tag. This is the only release trigger. Merges and
ordinary branch pushes do not publish. No environment approval is required.
Deployment and Helm chart publication remain outside this workflow.

## Local tools

CI uses the upstream GoReleaser action and Azure's Helm setup action, pinned to
reviewed commit SHAs. GoReleaser always selects `latest`; other tool selections
are defined in the [workflows](../.github/workflows/). There is no repository-managed
tool installer. For local work, install tools on PATH through their official
[GoReleaser](https://goreleaser.com/install/), [ko](https://ko.build/install/)
and [Helm](https://helm.sh/docs/intro/install/) instructions or your existing tool
manager. Make variables `GORELEASER`, `KO` and `HELM` accept executable overrides.
No build tool is added to the runtime Go module.

## Snapshots and images

```sh
make release-snapshot
make image
make image-multiarch
make helm-check helm-package
```

GoReleaser builds Linux/macOS/Windows amd64/arm64 binaries, archives and checksums
under ignored `dist/`. Windows archives use ZIP; other platforms use tar.gz.
`--snapshot --skip=publish,sign` keeps ordinary builds unsigned and unpublished.
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
Keep both configurations synchronized when changing the selected base.
`org.opencontainers.image.created` records build time in UTC; standalone targets
accept an explicit `BUILD_DATE` for reproducibility. GoReleaser also generates
local SPDX image SBOMs under `dist/sbom/`, retained as CI artifacts.

ko images use entrypoint **`/ko-app/berlin-transit`**. The Helm chart targets these
images. The existing Dockerfile/Compose path remains a separate local option with
entrypoint `/berlin-transit`; its Compose healthcheck must not be used unchanged
with a ko image. See [Compose operations](deployment.md).

CI uses one GoReleaser snapshot for all six binary targets and both Linux image
variants, then verifies and exercises the daemon-local image with networking disabled.
The Helm checks render manifests, verify security/secret/egress policies and package
locally; they do not install a chart or access a cluster. No credentials are needed
for these tests. Actual installation, CNI enforcement, scheduling and arm64 runtime
behavior still require operator acceptance in an explicitly authorized environment.

`release-snapshot --clean` removes previous `dist/` outputs; package Helm and TRMNL
after the snapshot if you want all local artifacts together.

## Signing and build provenance

The release job uses cosign with GitHub OIDC, following the signing configuration
reviewed in [tado-bridge](https://github.com/cpanato/tado-bridge). There is no stored
signing key. The cosign installer uses its upstream defaults; GoReleaser uses
`latest`. Tool/action selections live in the workflow, not a second version list.

GoReleaser signs application binaries before archiving and signs `checksums.txt`,
publishing Sigstore verification bundles with the release assets. Archive hashes
are covered by that signed checksum list. The multi-platform image is signed at
the immutable digest produced by ko, with its signature stored in GHCR. Stable
releases receive the exact tag and `latest`; prereleases do not update `latest`.
Snapshots keep using only the local daemon through `local_domain: ko.local`.

The official GitHub attestation action adds signed SLSA build provenance for
archives, raw binaries, checksums, SBOM files and the image digest. The image digest
comes from GoReleaser's build metadata, rather than resolving a mutable registry
tag. Artifact and image provenance bundles are attached to the release; image
provenance is also stored in GHCR. A signature establishes integrity and signer
identity; build provenance additionally records the workflow and source commit.
This workflow makes no claim of a higher SLSA assurance level.

Ordinary CI has no OIDC, registry-write or attestation-write permission. It skips
signing/publishing and tests release guards, image tag selection and digest
extraction with local fixtures. Real OIDC signing, attestation publication and
registry verification require an authorized release; snapshot tests do not prove
those external operations have succeeded.

See the official [cosign integration](https://goreleaser.com/customization/sign/sign/#signing-with-cosign)
and [GitHub build provenance documentation](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations).

## Publish a release

Create and push a gitsign-signed SemVer tag for a merged commit with passing CI.
Pushing a `v*` tag is the operator's publication instruction: the workflow runs
without an additional approval. It validates the tag format, requires the tag to
match the event's source commit and checks that the commit belongs to main.
Only the publication job receives write/OIDC permissions after validation.

The workflow builds the event's immutable SHA so provenance describes the source
actually compiled. It never creates or moves tags and does not require a GitHub
environment.

The configured destinations are this repository's GitHub releases and
`ghcr.io/cpanato/berlin-transit`. Signing/attestation records publish signer,
artifact digest and build metadata to Sigstore/GitHub/GHCR. Re-running a partial
release can update release assets or image tags; review its state before retrying.
The chart still defaults to a local image and assumes no published release exists.

## Verify downloaded artifacts

Use the expected commit and source ref for the release, not values supplied only
by the downloaded artifact. Releases use the version tag as their source ref.
The initial `v0.1.0` release predates this policy and used `refs/heads/main`:

```sh
repo=cpanato/berlin-transit
commit='<approved-commit-sha>'
release_tag='<release-tag>'
source_ref="refs/tags/$release_tag"
# For the initial v0.1.0 release, use: source_ref=refs/heads/main
identity="https://github.com/$repo/.github/workflows/release.yml@$source_ref"
issuer=https://token.actions.githubusercontent.com

cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "$identity" --certificate-oidc-issuer "$issuer" \
  checksums.txt
# With GNU coreutils, check the archives you downloaded against the signed list.
sha256sum --check --ignore-missing checksums.txt

gh attestation verify '<downloaded-archive>' --repo "$repo" \
  --signer-workflow "$repo/.github/workflows/release.yml" \
  --source-ref "$source_ref" --source-digest "$commit"

image=ghcr.io/cpanato/berlin-transit
digest='sha256:<published-manifest-digest>'
cosign verify --certificate-identity "$identity" \
  --certificate-oidc-issuer "$issuer" "$image@$digest"
gh attestation verify "oci://$image@$digest" --repo "$repo" \
  --signer-workflow "$repo/.github/workflows/release.yml" \
  --source-ref "$source_ref" --source-digest "$commit"
```

Extracted binaries can also be verified with their corresponding binary Sigstore
bundle using `cosign verify-blob`. Git commit signing remains separate: gitsign's
certificate identity does not change the Git author/committer metadata.
